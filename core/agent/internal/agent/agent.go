// SPDX-License-Identifier: Apache-2.0








package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/batonos/baton/core/agent/internal/capability"
	"github.com/batonos/baton/core/agent/internal/identity"
	"github.com/batonos/baton/core/agent/internal/netresolve"
	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/contacts"
	"github.com/batonos/baton/core/agent/internal/netresources"
	"github.com/batonos/baton/core/agent/internal/skills"
	"github.com/batonos/baton/core/agent/internal/protocol"
	"github.com/batonos/baton/core/agent/internal/runtimespec"
	"github.com/batonos/baton/core/agent/internal/supervisor"
)






type State string

const (
	StateInit       State = "INIT"




	StateWaitingToJoin State = "WAITING_TO_JOIN"
	StateEnrolling  State = "ENROLLING"
	StateConnecting State = "CONNECTING"
	StateReady      State = "READY"
	StateDegraded   State = "DEGRADED"
	StatePaused     State = "PAUSED"
	StateDraining   State = "DRAINING"
	StateRevoked    State = "REVOKED"
)


const (
	backoffMin = 1 * time.Second
	backoffMax = 60 * time.Second



	backoffJitter = 0.2
)


type Config struct {
	DataDir      string
	MasterURL    string
	Name         string
	Version      string





	Labels       map[string]string
	ManifestPath string



	RuntimeSpecPath string
	Logger          *slog.Logger
}


type Agent struct {
	cfg     Config
	logger  *slog.Logger
	ids     *identity.Store
	caps    *capability.Registry
	started time.Time





	dial string





	nets interface {
		Resolve(ctx context.Context, ref string) (netresolve.Resolution, error)
	}




	fenced bool


	sup *supervisor.Supervisor

	state atomic.Value
	seq   atomic.Int64



	outboxMu      sync.Mutex
	outboxSending map[string]*inbox.Box




	restart chan struct{}



	ptyMu sync.Mutex
	ptys  map[string]*ptySession
}


func New(cfg Config) *Agent {
	a := &Agent{
		cfg:     cfg,
		logger:  cfg.Logger,
		ids:     identity.NewStore(cfg.DataDir),
		caps:    capability.NewRegistry(),
		started: time.Now(),
		restart: make(chan struct{}, 1),
	}
	a.state.Store(StateInit)
	capability.RegisterBuiltins(a.caps, a.started)
	return a
}


func (a *Agent) State() State { return a.state.Load().(State) }














func refusalOf(res *http.Response, err error) error {
	if res == nil || res.Body == nil {
		return err
	}
	defer res.Body.Close()
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`






		Remediation string `json:"remediation"`
	}



	if json.NewDecoder(io.LimitReader(res.Body, 8<<10)).Decode(&body) != nil || body.Message == "" {
		return err
	}
	if body.Code != "" {
		if body.Remediation != "" {
			return fmt.Errorf("%s: %s (%s)", body.Code, body.Message, body.Remediation)
		}
		return fmt.Errorf("%s: %s", body.Code, body.Message)
	}
	return errors.New(body.Message)
}

func (a *Agent) setState(s State) {
	previous := a.State()
	if previous == s {
		return
	}
	a.state.Store(s)
	a.logger.Info("state changed", "from", string(previous), "to", string(s))
}









func (a *Agent) LoadManifest() error {
	if a.cfg.ManifestPath == "" {
		return nil
	}
	m, err := capability.LoadManifest(a.cfg.ManifestPath)
	if err != nil {
		return err
	}

	var unservable []string
	for _, c := range m.Capabilities {
		if !a.caps.Has(c.Name) {
			unservable = append(unservable, c.Name)
		}
	}
	if len(unservable) > 0 {
		a.logger.Warn("manifest declares capabilities this build cannot serve; they are not advertised",
			"path", a.cfg.ManifestPath, "capabilities", strings.Join(unservable, ", "))
	}
	return nil
}






func (a *Agent) LoadRuntime() error {
	if a.cfg.RuntimeSpecPath == "" {
		return nil
	}

	spec, err := runtimespec.Load(a.cfg.RuntimeSpecPath, a.cfg.Name)
	if errors.Is(err, runtimespec.ErrNotFound) {
		a.logger.Info("no runtime spec; this node supervises nothing",
			"path", a.cfg.RuntimeSpecPath)
		return nil
	}
	if err != nil {
		return err
	}

	sup, err := supervisor.New(spec, supervisor.Options{
		Logger: a.logger,






		HasPendingMail: a.mailWaiting,



		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		NodeName: a.cfg.Name,




		FetchFacility: a.fetchFacility,
	})
	if err != nil {
		return err
	}
	a.sup = sup

	a.logger.Info("runtime spec loaded",
		"runtime", spec.Metadata.Name, "image", spec.Package.Image,
		"tty", spec.Adapter.Terminal.TTY)








	if err := sup.EnsureWorkspace(); err != nil {
		return err
	}
	return nil
}


func (a *Agent) runtimeDecl() *protocol.WorkspaceDecl {
	if a.sup == nil {
		return nil
	}
	spec := a.sup.Spec()
	return &protocol.WorkspaceDecl{
		Name:      spec.Metadata.Name,
		Image:     spec.Package.Image,
		Type:      spec.Metadata.Labels["runtime"],
		Template:  spec.Metadata.Labels["template"],
		Session:   supervisor.SessionWord(spec.Adapter.Terminal.TTY),
		Enterable: spec.Adapter.Enterable(),




		SkillsMountPath: spec.Adapter.Skills.MountPath,
	}
}


func (a *Agent) runtimeState() *protocol.WorkspaceState {
	if a.sup == nil {
		return nil
	}
	st := a.sup.Status()
	out := &protocol.WorkspaceState{
		State:         string(st.State),
		RuntimeStatus: string(st.RuntimeStatus),
		Probe:        string(st.Probe),
		Memory:       st.Memory,
		RestartCount: st.RestartCount,
		LastExitCode: st.LastExitCode,
		OOMKilled:    st.OOMKilled,
		LastError:    st.LastError,
	}
	if !st.StartedAt.IsZero() {
		started := st.StartedAt
		out.StartedAt = &started
	}
	return out
}


func (a *Agent) Enroll(ctx context.Context, token string, roles []string, labels map[string]string) error {
	if a.ids.Revoked() {
		a.setState(StateRevoked)
		return errors.New("agent: this node has been revoked and will not re-enroll")
	}
	if a.ids.Enrolled() {
		return nil
	}

	a.setState(StateEnrolling)





	a.logger.Info("enrolling", "master", a.cfg.MasterURL,
		"note", "first contact is trust-on-first-use until the cluster CA is received")

	client := firstContactClient()

	id, err := a.ids.Enroll(client, identity.EnrollRequest{
		MasterURL:    a.cfg.MasterURL,
		Token:        token,
		DisplayName:  a.cfg.Name,
		Roles:        roles,
		AgentVersion: a.cfg.Version,
		Labels:       labels,
	})
	if err != nil {
		return err
	}

	a.logger.Info("enrolled",
		"node_id", id.NodeID, "cluster_id", id.ClusterID,
		"cert_not_after", id.CertNotAfter.Format(time.RFC3339))
	return nil
}


func (a *Agent) client() (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(a.ids.CertPath(), a.ids.KeyPath())
	if err != nil {
		return nil, fmt.Errorf("agent: load node certificate: %w", err)
	}
	caPEM, err := os.ReadFile(a.ids.CAPath())
	if err != nil {
		return nil, fmt.Errorf("agent: read cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("agent: cluster CA bundle is not valid PEM")
	}

	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      pool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}, nil
}









func (a *Agent) Run(ctx context.Context) error {









	if !a.ids.Enrolled() {
		if err := a.awaitEnrollment(ctx); err != nil {
			return err
		}
	}






	if a.cfg.MasterURL == "" && a.ids.Enrolled() {
		if id, err := a.ids.Load(); err == nil {




			if ref := id.Network; ref != "" {
				a.cfg.MasterURL = ref
				a.logger.Info("recovered the network reference from the enrolled identity", "network", ref)
			} else if id.EntryPoint != "" {
				a.cfg.MasterURL = id.EntryPoint
				a.logger.Info("recovered master URL from enrolled identity", "master", a.cfg.MasterURL,
					"note", "this identity predates the network reference; it holds an address and cannot re-ask")
			}
		}
	}





	supervised := make(chan struct{})
	if a.sup != nil {
		go func() {
			defer close(supervised)
			if err := a.sup.Run(ctx); err != nil {
				a.logger.Error("supervisor stopped", "error", err)
			}
		}()
	} else {
		close(supervised)
	}
	defer a.awaitRuntimeStop(supervised)

	backoff := backoffMin
	for {
		if a.ids.Revoked() {
			a.setState(StateRevoked)



			a.logger.Warn("node is revoked; not reconnecting")
			return nil
		}

		err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errRevoked) {
			continue
		}
		if errors.Is(err, errEndpointChanged) {


			a.logger.Info("reconnecting to the new master", "endpoint", a.cfg.MasterURL)
			backoff = backoffMin
			continue
		}












		if errors.Is(err, errFenced) {
			a.logger.Warn("superseded: this node's master was replaced, and this node cannot find the new one",
				"detail", err,
				"why", "it was enrolled against a direct address, which has nothing to re-resolve: recovery re-asks the network's declared resolver, and a direct entry-point address has none",
				"next", "re-point this node at the new master (BATON_MASTER_URL), or enrol it by network name (DNS TXT or registry), which the node re-resolves when it reconnects, once that exists")
		}

		a.setState(StateDegraded)










		a.markNetResourcesUnavailable()
		wait := jitter(backoff)
		a.logger.Warn("channel lost; reconnecting",
			"error", err, "retry_in", wait.Round(time.Millisecond).String())

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

var errRevoked = errors.New("agent: revoked by the control plane")



var errEndpointChanged = errors.New("agent: master endpoint changed")











var errFenced = errors.New("agent: master presented a stale leader epoch")







func (a *Agent) awaitRuntimeStop(supervised <-chan struct{}) {
	if a.sup == nil {
		return
	}
	limit := a.sup.Spec().Adapter.Lifecycle.StopGrace() + 15*time.Second
	select {
	case <-supervised:
	case <-time.After(limit):
		a.logger.Warn("the runtime did not stop in time; exiting anyway",
			"waited", limit.String(), "state", string(a.sup.Status().State))
	}
}


func (a *Agent) session(ctx context.Context) error {
	a.setState(StateConnecting)

	client, err := a.client()
	if err != nil {
		return err
	}













	if err := a.resolveDial(ctx); err != nil {
		return err
	}
	if !strings.HasPrefix(a.dial, "https") {




		return fmt.Errorf("agent: no https endpoint to dial (reference %q resolved to %q); node is enrolled but has no reachable entry point", a.cfg.MasterURL, a.dial)
	}
	wsURL := "wss" + a.dial[len("https"):] + "/api/v1alpha1/agent/channel"
	ws, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, refusalOf(res, err))
	}
	ws.SetReadLimit(protocol.MaxFrameBytes)
	defer ws.CloseNow()

	conn := protocol.NewConn(ws)

	id, err := a.ids.Load()
	if err != nil {
		return err
	}

	if err := a.sendHello(ctx, conn, id); err != nil {
		return err
	}

	ack, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("await hello_ack: %w", err)
	}
	if ack.Type != protocol.TypeHelloAck {
		return fmt.Errorf("expected hello_ack, got %s", ack.Type)
	}
	var helloAck protocol.HelloAck
	_ = ack.Decode(&helloAck)




	if helloAck.Identities != nil {
		if serr := inbox.New(a.cfg.DataDir).SaveBound(helloAck.Identities); serr != nil {
			a.logger.Warn("could not record this node's identities", "error", serr)
		}
	}




	if id, lerr := a.ids.Load(); lerr == nil {
		if helloAck.LeaderEpoch < id.LeaderEpoch {
			a.logger.Warn("master presents a stale leader epoch; refusing (fenced)",
				"presented", helloAck.LeaderEpoch, "highest_seen", id.LeaderEpoch)




			a.fenced = true
			return fmt.Errorf("%w: %d is behind the %d already seen — refusing a stale primary: an epoch lower than one already seen means a replaced primary",
				errFenced, helloAck.LeaderEpoch, id.LeaderEpoch)
		}
		if helloAck.LeaderEpoch > id.LeaderEpoch {
			id.LeaderEpoch = helloAck.LeaderEpoch
			if serr := a.ids.SaveMeta(id); serr != nil {
				a.logger.Warn("could not persist leader epoch", "error", serr)
			}
		}
	}
	for _, r := range helloAck.Rejected {


		a.logger.Warn("capability rejected by control plane",
			"capability", r.Name, "reason", r.Reason)
	}

	interval := time.Duration(helloAck.HeartbeatIntervalSec) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}

	a.setState(StateReady)
	a.logger.Info("connected",
		"node_id", id.NodeID, "accepted_capabilities", len(helloAck.AcceptedCapabilities),
		"heartbeat_interval", interval.String())







	go a.syncSkills(ctx)
	go a.syncContacts(ctx)








	go a.syncNetResources(ctx)

	return a.serve(ctx, conn, id, interval)
}






func (a *Agent) syncSkills(ctx context.Context) {
	if a.sup == nil {
		return
	}
	client, err := a.client()
	if err != nil {
		a.logger.Error("skill sync: client", "error", err)
		return
	}
	sy := &skills.Syncer{
		MountPath: a.sup.Spec().Adapter.Skills.MountPath,
		StateDir:  filepath.Join(a.cfg.DataDir, "skills"),
		MasterURL: a.cfg.MasterURL,
		Client:    client,
		Logger:    a.logger,
	}
	changed, err := sy.Sync(ctx)
	if errors.Is(err, skills.ErrNoMountPath) {



		a.logger.Warn("skills are assigned to this node but its spec declares no "+
			"adapter.skills.mountPath; nothing was unpacked", "error", err)
		return
	}
	if err != nil {
		a.logger.Error("skill sync", "error", err)
		return
	}
	if changed > 0 {
		a.logger.Info("skills reconciled", "changed", changed)
	}













	a.sup.ReconcilePlugins(ctx)
}




func (a *Agent) syncContacts(ctx context.Context) {
	if a.sup == nil {
		return
	}
	root := a.sup.Spec().Adapter.Workspace.MountPath
	if root == "" {
		return
	}
	client, err := a.client()
	if err != nil {
		a.logger.Error("contacts sync: client", "error", err)
		return
	}
	sy := &contacts.Syncer{
		WorkspaceRoot: root,
		MasterURL:     a.cfg.MasterURL,
		Client:        client,
		Logger:        a.logger,
	}
	changed, err := sy.Sync(ctx)
	if err != nil {
		a.logger.Error("contacts sync", "error", err)
		return
	}
	if changed > 0 {
		a.logger.Info("contacts reconciled", "changed", changed)
	}
}




func (a *Agent) markNetResourcesUnavailable() {
	if a.sup == nil {
		return
	}
	root := a.sup.Spec().Adapter.Workspace.MountPath
	if root == "" {
		return
	}
	sy := &netresources.Syncer{WorkspaceRoot: root, Logger: a.logger}
	if err := sy.MarkUnavailable(); err != nil {
		a.logger.Warn("could not mark the resource catalogue unavailable", "error", err)
	}
}






func (a *Agent) syncNetResources(ctx context.Context) {
	if a.sup == nil {
		return
	}
	root := a.sup.Spec().Adapter.Workspace.MountPath
	if root == "" {
		return
	}
	client, err := a.client()
	if err != nil {
		a.logger.Error("network-resources sync: client", "error", err)
		return
	}
	sy := &netresources.Syncer{
		WorkspaceRoot: root,
		MasterURL:     a.cfg.MasterURL,
		Client:        client,
		Logger:        a.logger,
	}
	if err := sy.Sync(ctx); err != nil {
		a.logger.Error("network-resources sync", "error", err)
		return
	}
	a.logger.Info("network resource catalogue projected", "path", ".baton/local/net/resources.json")
}

func (a *Agent) sendHello(ctx context.Context, conn *protocol.Conn, id *identity.Identity) error {
	decls := make([]protocol.CapabilityDecl, 0, len(a.caps.List()))
	for _, c := range a.caps.List() {
		decls = append(decls, protocol.CapabilityDecl{
			Name: c.Name, Version: c.Version, Risk: c.Risk,
			InputSchema: c.Inputs, OutputSchema: c.Outputs, AllowFrom: c.AllowFrom,
		})
	}

	return conn.Send(ctx, protocol.TypeHello, a.nextSeq(), protocol.Hello{
		AgentVersion: a.cfg.Version,
		Platform:     runtime.GOOS,
		Arch:         runtime.GOARCH,


		ResumeFromSeq: a.seq.Load(),




		LeaderEpoch:  id.LeaderEpoch,
		Capabilities: decls,
		RemoteShell:   RemoteShellAllowed(a.cfg.DataDir),
		Workspace:     a.runtimeDecl(),
	})
}

func (a *Agent) resolver() interface {
	Resolve(ctx context.Context, ref string) (netresolve.Resolution, error)
} {
	if a.nets != nil {
		return a.nets
	}
	return &netresolve.Resolver{}
}






















func (a *Agent) resolveDial(ctx context.Context) error {
	ref := a.cfg.MasterURL
	if netresolve.ChannelOf(ref) == netresolve.ChannelEntryPoint {
		a.dial = strings.TrimRight(ref, "/")
		return nil
	}
	if a.dial != "" && !a.fenced {
		return nil
	}
	res, err := a.resolver().Resolve(ctx, ref)
	if err != nil {



		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	if len(res.Candidates) == 0 {
		return fmt.Errorf("resolve %s: named the network but no endpoint", ref)
	}




	a.dial = res.Candidates[0]
	a.fenced = false
	a.logger.Info("resolved the network's entry point",
		"reference", ref, "channel", string(res.Channel), "endpoint", a.dial,
		"pinned", res.Pin != "")
	return nil
}


func (a *Agent) serve(ctx context.Context, conn *protocol.Conn, id *identity.Identity, interval time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frames := make(chan protocol.Frame, 8)
	readErr := make(chan error, 1)

	go func() {
		for {
			f, err := conn.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()




	outTicker := time.NewTicker(time.Second)
	defer outTicker.Stop()

	for {
		select {
		case <-ctx.Done():


			_ = conn.Send(context.WithoutCancel(ctx), protocol.TypeGoodbye, a.nextSeq(),
				protocol.Goodbye{Reason: "agent stopping"})
			a.setState(StateDraining)
			return nil

		case <-a.restart:
			a.logger.Info("restarting the session on console request")
			return errors.New("restart requested")

		case err := <-readErr:
			return err

		case <-ticker.C:
			if err := a.sendHeartbeat(ctx, conn, id); err != nil {
				return err
			}

		case <-outTicker.C:



			a.drainOutbox(ctx, conn)

		case f := <-frames:
			if err := a.handleFrame(ctx, conn, id, f); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) sendHeartbeat(ctx context.Context, conn *protocol.Conn, id *identity.Identity) error {
	return conn.Send(ctx, protocol.TypeHeartbeat, a.nextSeq(), protocol.Heartbeat{
		UptimeSec:     int64(time.Since(a.started).Seconds()),
		InflightCalls: a.caps.Inflight(),


		CertNotAfter: id.CertNotAfter,
		RemoteShell:  RemoteShellAllowed(a.cfg.DataDir),
		Workspace:    a.runtimeState(),
		Inbox:        a.inboxState(),




		InstructionBlocks: a.instructionBlocks(),
	})
}



func (a *Agent) instructionBlocks() []protocol.InstructionBlock {
	if a.sup == nil {
		return nil
	}
	reps := a.sup.InstructionBlocks()
	if len(reps) == 0 {
		return nil
	}
	out := make([]protocol.InstructionBlock, 0, len(reps))
	for _, r := range reps {
		out = append(out, protocol.InstructionBlock{Plugin: r.ID, Path: r.InstructionsPath, SHA256: r.InstructionsSHA256})
	}
	return out
}

func (a *Agent) handleFrame(ctx context.Context, conn *protocol.Conn, id *identity.Identity, f protocol.Frame) error {
	switch f.Type {
	case protocol.TypeHeartbeatAck:
		return nil

	case protocol.TypeCallRequest:
		var req protocol.CallRequest
		if err := f.Decode(&req); err != nil {
			return nil
		}


		go a.runCall(ctx, conn, f.MsgID, req)
		return nil

	case protocol.TypeConsoleRequest:
		var req protocol.ConsoleRequest
		if err := f.Decode(&req); err != nil {
			return nil
		}
		return a.runConsole(ctx, conn, f.MsgID, req)

	case protocol.TypePtyOpen:
		var req protocol.PtyOpen
		if err := f.Decode(&req); err != nil {
			return nil
		}
		a.handlePtyOpen(ctx, conn, req)
		return nil
	case protocol.TypePtyData:
		var req protocol.PtyData
		if err := f.Decode(&req); err != nil {
			return nil
		}
		a.handlePtyData(req)
		return nil
	case protocol.TypePtyResize:
		var req protocol.PtyResize
		if err := f.Decode(&req); err != nil {
			return nil
		}
		a.handlePtyResize(req)
		return nil
	case protocol.TypePtyClose:
		var req protocol.PtyClose
		if err := f.Decode(&req); err != nil {
			return nil
		}
		a.closePty(ctx, conn, req.Session, "closed by operator")
		return nil

	case protocol.TypeMessageSendAck:
		var ack protocol.MessageSendAck
		if err := f.Decode(&ack); err != nil {
			a.logger.Error("decode message_send_ack", "error", err)
			return nil
		}
		a.onMessageSendAck(f.MsgID, ack)
		return nil

	case protocol.TypeMessageDeliver:
		var m protocol.MessageDeliver
		if err := f.Decode(&m); err != nil {



			a.logger.Warn("undecodable message frame")
			return nil
		}
		return a.deliverMessage(ctx, conn, f.MsgID, m)

	case protocol.TypeChanged:



		var ch protocol.Changed
		if err := f.Decode(&ch); err != nil {
			a.logger.Warn("changed: undecodable frame; ignoring", "error", err)
			return nil
		}
		switch ch.Kind {
		case "skills":
			go a.syncSkills(ctx)
			return nil
		case "contacts":
			go a.syncContacts(ctx)
			return nil
		case "endpoint":
			if !strings.HasPrefix(ch.Endpoint, "https://") {
				a.logger.Warn("changed(endpoint): endpoint is not https; ignoring", "endpoint", ch.Endpoint)
				return nil
			}
			a.logger.Info("master moved; following to the new endpoint", "endpoint", ch.Endpoint)




			if id, err := a.ids.Load(); err == nil {
				id.EntryPoint = ch.Endpoint
				if serr := a.ids.SaveMeta(id); serr != nil {
					a.logger.Warn("could not persist the new entry point", "error", serr)
				}
			}
			a.cfg.MasterURL = ch.Endpoint
			return errEndpointChanged
		default:



			a.logger.Warn("changed: unknown kind refused", "kind", ch.Kind)
			return nil
		}

	case protocol.TypeCertRotateRequired:
		go a.rotate()
		return nil

	case protocol.TypeRevoked:
		var rev protocol.Revoked
		_ = f.Decode(&rev)
		a.logger.Warn("revoked by control plane", "reason", rev.Reason)
		if err := a.ids.MarkRevoked(rev.Reason); err != nil {
			a.logger.Error("record revocation", "error", err)
		}
		a.setState(StateRevoked)
		return errRevoked

	default:


		a.logger.Debug("ignoring unknown frame", "type", f.Type)
		return nil
	}
}

func (a *Agent) runCall(ctx context.Context, conn *protocol.Conn, msgID string, req protocol.CallRequest) {
	started := time.Now()
	out, err := a.caps.Invoke(ctx, req.CallID, req.Capability, req.Input)

	result := protocol.CallResult{
		CallID:     req.CallID,
		Status:     "succeeded",
		Output:     out,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		result.Status = "failed"
		result.ErrorCode = errorCode(err)
		result.ErrorMessage = err.Error()
	}

	if sendErr := conn.Reply(ctx, protocol.TypeCallResult, msgID, result); sendErr != nil {
		a.logger.Warn("send call result", "call_id", req.CallID, "error", sendErr)
	}
}



func errorCode(err error) string {
	switch {
	case errors.Is(err, capability.ErrNotFound):
		return protocol.ErrCapabilityNotFound
	case errors.Is(err, capability.ErrPaused):
		return protocol.ErrCapabilityPaused
	case errors.Is(err, capability.ErrTooLarge):
		return protocol.ErrOutputTooLarge
	case errors.Is(err, capability.ErrInputInvalid):
		return protocol.ErrInputInvalid
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.ErrCapabilityTimeout
	default:
		return protocol.ErrExecutionFailed
	}
}

func (a *Agent) runConsole(ctx context.Context, conn *protocol.Conn, msgID string, req protocol.ConsoleRequest) error {
	resp := protocol.ConsoleResponse{RequestID: req.RequestID, Result: "ok"}

	switch req.Command {
	case "status":




		data := map[string]any{
			"state":          string(a.State()),
			"uptime_sec":     int64(time.Since(a.started).Seconds()),
			"inflight_calls": a.caps.Inflight(),
			"paused":         a.caps.Paused(),
			"capabilities":   len(a.caps.List()),
		}
		out := fmt.Sprintf("%s, up %s, %d inflight",
			a.State(), time.Since(a.started).Round(time.Second), a.caps.Inflight())

		if a.sup != nil {
			st := a.sup.Status()
			data["runtime"] = st
			out += fmt.Sprintf("; runtime %s %s/%s", st.Name, st.State, st.RuntimeStatus)
			if st.RestartCount > 0 {
				out += fmt.Sprintf(" (%d restarts)", st.RestartCount)
			}
		} else {



			data["runtime"] = nil
			out += "; no runtime supervised"
		}

		resp.Data = data
		resp.Output = out

	case "capabilities":
		names := make([]string, 0)
		for _, c := range a.caps.List() {
			names = append(names, c.Name+"@"+c.Version)
		}
		resp.Data = map[string]any{"capabilities": names}
		resp.Output = fmt.Sprintf("%d registered", len(names))

	case "tasks":
		resp.Data = map[string]any{"inflight": a.caps.Inflight()}
		resp.Output = fmt.Sprintf("%d inflight", a.caps.Inflight())




	case "pause":
		a.caps.Pause()
		a.setState(StatePaused)
		resp.Output = "node paused: new capability calls refused, still heartbeating. " +
			"The runtime is unaffected — use runtime.pause for that."

	case "resume":
		a.caps.Resume()
		a.setState(StateReady)
		resp.Output = "node resumed: accepting capability calls"

	case "restart":
		resp.Output = "reconnecting the control-plane channel. " +
			"Neither the container nor the runtime restarts — use runtime.restart for that."



		select {
		case a.restart <- struct{}{}:
		default:
		}


	case "runtime.pause":
		resp.Result, resp.Output = a.runtimeAction("pause", func() error { return a.sup.Pause() },
			"runtime paused (SIGSTOP): it holds its memory, files, and connections but is not "+
				"executing. Anything waiting on it will see a hang.")

	case "runtime.resume":
		resp.Result, resp.Output = a.runtimeAction("resume", func() error { return a.sup.Resume() },
			"runtime resumed")

	case "runtime.restart":
		resp.Result, resp.Output = a.runtimeAction("restart",
			func() error { return a.sup.Restart(ctx) },
			"runtime stopping; the supervisor will start it again")









	case "skills.show":
		name, _ := req.Args["skill"].(string)
		if name == "" {
			resp.Result = "denied"
			resp.Output = "name the skill: skills.show takes skill=<name>"
			break
		}
		if a.sup == nil {
			resp.Result = "denied"
			resp.Output = "this node supervises no runtime, so it holds no skills"
			break
		}
		obs, err := skills.Observe(a.sup.Spec().Adapter.Skills.MountPath, name)
		if err != nil {


			resp.Result = "error"
			resp.Output = err.Error()
			break
		}
		resp.Data = map[string]any{
			"skill":          obs.Name,
			"content_digest": obs.ContentDigest,



			"code_id": obs.CodeID,
		}
		resp.Output = obs.Name + " " + obs.ContentDigest

	default:
		resp.Result = "denied"
		resp.Output = "unknown console command " + req.Command
	}

	return conn.Reply(ctx, protocol.TypeConsoleResponse, msgID, resp)
}







func (a *Agent) runtimeAction(verb string, do func() error, success string) (string, string) {
	if a.sup == nil {
		return "denied", "this node supervises no runtime, so there is nothing to " + verb
	}
	if err := do(); err != nil {
		return "error", err.Error()
	}
	return "ok", success
}


func (a *Agent) rotate() {
	client, err := a.client()
	if err != nil {
		a.logger.Error("certificate rotation", "error", err)
		return
	}
	notAfter, err := a.ids.Renew(client, a.cfg.MasterURL)
	if err != nil {
		a.logger.Error("certificate rotation", "error", err)
		return
	}
	a.logger.Info("certificate rotated", "not_after", notAfter.Format(time.RFC3339))
}

func (a *Agent) nextSeq() int64 { return a.seq.Add(1) }


func jitter(d time.Duration) time.Duration {
	delta := float64(d) * backoffJitter
	return time.Duration(float64(d) - delta + rand.Float64()*2*delta)
}















func (a *Agent) awaitEnrollment(ctx context.Context) error {
	a.setState(StateWaitingToJoin)
	a.logger.Info("this node is ready but not on a network",
		"next", "baton setup master  —  or  —  baton agent join --name <agent> --network <network>")

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if a.ids.Enrolled() {
				a.logger.Info("joined a network")
				return nil
			}
			if err := a.pollApplication(ctx); err != nil {
				a.logger.Warn("application", "error", err)
			}
		}
	}
}


func (a *Agent) pollApplication(ctx context.Context) error {
	app, err := a.ids.LoadApplication()
	if err != nil {
		return nil
	}
	client := firstContactClient()
	state, err := a.ids.Poll(client, app)
	if err != nil {
		return err
	}
	switch state {
	case identity.ApplicationPending:
		return nil
	case identity.ApplicationAdmitted:
		token, err := a.ids.Collect(client, app)
		if err != nil {
			return err
		}
		a.logger.Info("admitted", "network", app.EntryPoint, "request_id", app.RequestID)


		a.cfg.MasterURL = app.EntryPoint
		if err := a.Enroll(ctx, token, []string{"agent"}, a.cfg.Labels); err != nil {
			return fmt.Errorf("enrol after admission: %w", err)
		}
		return a.ids.ClearApplication()
	case identity.ApplicationCollected:



		a.logger.Warn("application was collected but this node is not enrolled; apply again",
			"request_id", app.RequestID)
		return a.ids.ClearApplication()
	case identity.ApplicationDenied, identity.ApplicationExpired:
		a.logger.Warn("application "+string(state), "network", app.EntryPoint, "request_id", app.RequestID)
		return a.ids.ClearApplication()
	default:
		return fmt.Errorf("application in unknown state %q", state)
	}
}


func (a *Agent) SetLabels(labels map[string]string) { a.cfg.Labels = labels }




func (a *Agent) Apply(entryPoint, pin string) (*identity.Application, error) {
	if a.ids.Revoked() {
		return nil, errors.New("agent: this node has been revoked and will not apply")
	}
	if a.ids.Enrolled() {
		return nil, errors.New("agent: this node is already in a network")
	}
	a.logger.Info("applying", "network", entryPoint,
		"note", "first contact is trust-on-first-use; the network's key is learned from its answer")
	return a.ids.Apply(firstContactClient(), entryPoint, a.cfg.Name, pin)
}



func firstContactClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}













func (a *Agent) fetchFacility(ctx context.Context, ref string) ([]byte, string, string, error) {
	client, err := a.client()
	if err != nil {
		return nil, "", "", err
	}



	dl := &http.Client{Timeout: 5 * time.Minute, Transport: client.Transport}

	base := strings.TrimRight(a.cfg.MasterURL, "/") + "/api/v1alpha1/agent/network-resources"
	get := func(url string) ([]byte, int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		resp, err := dl.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxFacilityArchive+1))
		if err != nil {
			return nil, resp.StatusCode, err
		}
		if int64(len(b)) > maxFacilityArchive {
			return nil, resp.StatusCode, fmt.Errorf("the archive is past %d bytes", maxFacilityArchive)
		}
		return b, resp.StatusCode, nil
	}



















	if rest, ok := strings.CutPrefix(ref, "cloud-public/"); ok {
		net, res, ok := strings.Cut(rest, "/")
		if !ok || net == "" || res == "" {
			return nil, "", "", fmt.Errorf(
				"%s does not name a facility in the open directory — "+
					"write `cloud-public/<publisher-network>/<resource-id>`", ref)
		}
		return a.fetchCloudFacility(ctx, get, net, res)
	}



	id := ref
	if typ, name, ok := strings.Cut(ref, "/"); ok {
		raw, code, err := get(base + "?type=" + url.QueryEscape(typ))
		if err != nil || code != http.StatusOK {
			return nil, "", "", fmt.Errorf("the company could not be asked for %s (%d): %v", ref, code, err)
		}
		var list struct {
			Items []struct {
				ResourceID string  `json:"resource_id"`
				NetworkID  *string `json:"network_id"`
				Name       string  `json:"name"`
				Version    string  `json:"version"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, "", "", err
		}



































		distinct := map[string]string{}
		for _, it := range list.Items {
			if it.Name != name {
				continue
			}
			net := ""
			if it.NetworkID != nil {
				net = *it.NetworkID
			}
			distinct[net+"\x00"+it.ResourceID] = it.ResourceID
		}
		switch len(distinct) {
		case 1:
			for _, v := range distinct {
				id = v
			}
		case 0:
			id = ""
		default:
			keys := make([]string, 0, len(distinct))
			for k := range distinct {
				net, res, _ := strings.Cut(k, "\x00")
				if net == "" {
					net = "this network"
				}
				keys = append(keys, net+"/"+res)
			}
			sort.Strings(keys)
			return nil, "", "", fmt.Errorf(
				"%d different things are called %q here — say which by id: %s",
				len(distinct), name, strings.Join(keys, ", "))
		}
		if id == "" {


			return nil, "", "", fmt.Errorf("the company shares no %s called %q on this network", typ, name)
		}
	}

	raw, code, err := get(base + "/" + url.PathEscape(id))
	if err != nil || code != http.StatusOK {
		return nil, "", "", fmt.Errorf("no facility %s is shared with this node (%d): %v", ref, code, err)
	}
	var rec struct {
		ResourceID string `json:"resource_id"`
		Type       string `json:"type"`
		Name       string `json:"name"`
		Hash       string `json:"hash"`
		Detail     struct {
			PluginID string `json:"plugin_id"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, "", "", err
	}
	if rec.Type != "plugin" {


		return nil, "", "", fmt.Errorf("%s is a %s, and only a plugin is a facility this office can be fitted with", ref, rec.Type)
	}
	if rec.Hash == "" {
		return nil, "", "", fmt.Errorf("%s names no archive — the company has its record and not its bytes", ref)
	}

	arch, code, err := get(base + "/" + url.PathEscape(rec.ResourceID) + "/archive")
	if err != nil || code != http.StatusOK {
		return nil, "", "", fmt.Errorf("the archive for %s could not be fetched (%d): %v", ref, code, err)
	}



	pid := rec.Detail.PluginID
	if pid == "" {
		pid = rec.Name
	}
	return arch, rec.Hash, pid, nil
}















func (a *Agent) fetchCloudFacility(
	ctx context.Context,
	get func(string) ([]byte, int, error),
	network, resource string,
) ([]byte, string, string, error) {
	base := strings.TrimRight(a.cfg.MasterURL, "/") + "/api/v1alpha1/agent"
	raw, code, err := get(base + "/cloud-resources/" + url.PathEscape(network) + "/" + url.PathEscape(resource))
	if err != nil {
		return nil, "", "", fmt.Errorf("the open directory could not be reached through this network: %v", err)
	}
	if code != http.StatusOK {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)


		switch e.Code {
		case "NO_SUCH_RESOURCE":
			return nil, "", "", fmt.Errorf("the open directory does not list %s/%s", network, resource)
		case "RESOURCE_WITHDRAWN":



			return nil, "", "", fmt.Errorf(
				"%s/%s was withdrawn from the open directory — it was published and then taken down, "+
					"so this is not a wrong id and another copy is not the answer",
				network, resource)
		case "ARTIFACT_MISSING":
			return nil, "", "", fmt.Errorf(
				"%s/%s is listed in the open directory and its archive is not there — "+
					"it exists, and it cannot be fetched; the publisher has to publish it again",
				network, resource)
		case "CLOUD_NOT_CONNECTED":
			return nil, "", "", fmt.Errorf(
				"this network is not connected to the open directory, so nothing can be taken from it " +
					"(that connection is what authorises this path)")
		}
		msg := e.Message
		if msg == "" {
			msg = fmt.Sprintf("status %d", code)
		}
		return nil, "", "", fmt.Errorf("the open directory leg failed: %s", msg)
	}
	var rec struct {
		ResourceID string `json:"resource_id"`
		Hash       string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, "", "", err
	}
	if rec.Hash == "" {
		return nil, "", "", fmt.Errorf("%s/%s names no archive", network, resource)
	}
	arch, code, err := get(base + "/archives/" + url.PathEscape(rec.Hash))
	if err != nil || code != http.StatusOK {
		return nil, "", "", fmt.Errorf("the archive for %s/%s could not be collected (%d): %v",
			network, resource, code, err)
	}






	return arch, rec.Hash, rec.ResourceID, nil
}




const maxFacilityArchive int64 = 64 << 20
