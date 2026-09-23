// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"strings"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


func (a *API) handleAgentChannel(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r)
	if !ok || !p.IsNode() {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"the control channel requires a node certificate",
			"Nodes connect here after enrolling; operators use the admin API.", nil)
		return
	}

	ctx := r.Context()
	node, err := a.Store.Nodes().Get(ctx, p.Identity.Name)
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if err := a.Store.Nodes().SetHostAddress(ctx, node.NodeID, clientIP(r)); err != nil {
		a.Logger.Warn("record host address", "node_id", node.NodeID, "error", err)
	}
	if node.Status == spi.NodeStatusRevoked {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this node has been revoked",
			"Re-enroll with a fresh token if it should rejoin.", nil)
		return
	}






	if node.SuspendedAt != nil {
		a.fail(w, r, http.StatusForbidden, CodeNodeSuspended,
			"this node is suspended",
			"An operator paused it. `baton node resume "+node.DisplayName+"` lets it back in; "+
				"its certificate and its membership are untouched.", nil)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{



		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		a.Logger.Warn("websocket upgrade failed", "node_id", node.NodeID, "error", err)
		return
	}

	conn := channel.NewConn(ws, node.NodeID, rolesLabel(node.Roles), a.Hub)
	a.Hub.Register(conn)
	defer func() {
		a.Hub.Unregister(conn)


		if err := a.Store.Nodes().SetStatus(context.WithoutCancel(ctx),
			node.NodeID, spi.NodeStatusOffline, ""); err != nil &&
			!errors.Is(err, spi.ErrNotFound) {
			a.Logger.Warn("mark offline", "node_id", node.NodeID, "error", err)
		}
		a.Log.System(context.WithoutCancel(ctx), "channel.closed",
			map[string]any{"node_id": node.NodeID})
	}()

	a.serveChannel(ctx, conn, node)
}

func (a *API) serveChannel(ctx context.Context, conn *channel.Conn, node *spi.Node) {



	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	first, err := conn.Read(readCtx)
	cancel()
	if err != nil {
		conn.Close(channel.CloseProtocolViolation, "no hello frame")
		return
	}
	if first.Type != channel.TypeHello {
		conn.Close(channel.CloseProtocolViolation, "expected hello, got "+first.Type)
		return
	}

	var hello channel.Hello
	if err := first.Decode(&hello); err != nil {
		conn.Close(channel.CloseProtocolViolation, "malformed hello")
		return
	}






	a.noticeReportedEpoch(ctx, node.NodeID, hello.LeaderEpoch)



	if hello.ResumeFromSeq > 0 {
		conn.SetLastSeq(hello.ResumeFromSeq)
	} else {
		conn.SetLastSeq(first.Seq)
	}

	accepted, rejected := a.registerCapabilities(ctx, node, hello.Capabilities)




	if hello.Workspace != nil {
		rt := spi.RuntimeInfo{
			Name:      hello.Workspace.Name,
			Type:      hello.Workspace.Type,
			Image:     hello.Workspace.Image,
			Template:  hello.Workspace.Template,
			Enterable: hello.Workspace.Enterable,


			SkillsMountPath: hello.Workspace.SkillsMountPath,
		}
		if err := a.Store.Nodes().SetRuntimeDecl(ctx, node.NodeID, rt); err != nil {
			a.Logger.Warn("record runtime declaration", "node_id", node.NodeID, "error", err)
		}
	}



	if err := a.Store.Nodes().UpdateHeartbeat(ctx, node.NodeID, spi.Heartbeat{
		Seq: first.Seq, At: time.Now(), RemoteShell: hello.RemoteShell,
	}); err != nil {
		a.Logger.Warn("heartbeat on hello", "node_id", node.NodeID, "error", err)
	}










	var bound []string
	if ids, ierr := a.Store.Identities().List(ctx, spi.DefaultTenant); ierr == nil {
		for _, id := range ids {
			if id.NodeID == node.NodeID {
				bound = append(bound, id.Name)
			}
		}
		sort.Strings(bound)
	} else {
		a.Logger.Warn("could not read this node's identities for hello_ack",
			"node_id", node.NodeID, "error", ierr)
	}

	ack, _ := channel.NewFrame(channel.TypeHelloAck, first.MsgID, channel.HelloAck{
		HeartbeatIntervalSec: int(channel.HeartbeatInterval.Seconds()),
		LeaderEpoch:          a.epochNow(),
		AcceptedCapabilities: accepted,
		Rejected:             rejected,
		Identities:           bound,
	})
	if err := conn.Send(ctx, ack); err != nil {
		conn.Close(channel.CloseInternalError, "hello_ack failed")
		return
	}

	a.Log.System(ctx, "channel.connected", map[string]any{
		"node_id": node.NodeID, "agent_version": hello.AgentVersion,
		"capabilities": len(accepted),
	})









	go a.drainInbox(context.WithoutCancel(ctx), node.NodeID)

	for {


		readCtx, cancel := context.WithTimeout(ctx,
			channel.HeartbeatInterval*time.Duration(channel.MissedHeartbeatsBeforeOffline))
		frame, err := conn.Read(readCtx)
		cancel()

		if errors.Is(err, channel.ErrSeqViolation) {
			a.Log.System(ctx, "channel.seq_violation",
				map[string]any{"node_id": node.NodeID, "detail": err.Error()})
			conn.Close(channel.ClosePolicyViolation, "sequence violation")
			return
		}
		if err != nil {
			return
		}

		switch frame.Type {
		case channel.TypeHeartbeat:
			a.onHeartbeat(ctx, conn, node, frame)
		case channel.TypeGoodbye:
			a.Log.System(ctx, "channel.goodbye", map[string]any{"node_id": node.NodeID})
			conn.Close(channel.CloseNormal, "node draining")
			return
		case channel.TypeMessageSend:








			go func(frame channel.Frame) {
				var req channel.MessageSend
				ack := channel.MessageSendAck{Error: "INVALID_ARGUMENT", Reason: "could not decode the frame"}
				if err := frame.Decode(&req); err == nil {
					_, ack = a.acceptFromNode(ctx, node, req)
				}
				if reply, err := channel.NewFrame(channel.TypeMessageSendAck, frame.MsgID, ack); err == nil {
					_ = conn.Send(ctx, reply)
				}
			}(frame)

		case channel.TypeCallResult, channel.TypeConsoleResponse:

		case channel.TypePtyData, channel.TypePtyClose:


			var env struct {
				Session string `json:"session"`
			}
			if frame.Decode(&env) == nil {
				conn.RoutePty(env.Session, frame)
			}
		default:



			a.Logger.Debug("ignoring unknown frame", "type", frame.Type, "node_id", node.NodeID)
		}
	}
}



















func (a *API) auditInstructionBlocks(ctx context.Context, node *spi.Node, blocks []channel.InstructionBlock) {
	for _, b := range blocks {
		if b.Plugin == "" || b.Path == "" || b.SHA256 == "" {


			a.Logger.Warn("instruction block: incomplete report",
				"node_id", node.NodeID, "plugin", b.Plugin, "path", b.Path)
			continue
		}
		first, err := a.Store.Nodes().NoteInstructionBlock(ctx, node.NodeID, b.Plugin, b.Path, b.SHA256)
		if err != nil {
			a.Logger.Warn("instruction block: note", "node_id", node.NodeID, "plugin", b.Plugin, "error", err)
			continue
		}
		if !first {
			continue
		}
		if _, err := a.Log.Append(ctx, audit.Record{
			Event: "plugin.instructions.placed", Actor: node.NodeID, ActorType: "node",
			Action: "place", Target: b.Plugin, Result: "ok", NodeID: node.NodeID, At: time.Now().UTC(),
			Detail: map[string]any{"path": b.Path, "sha256": b.SHA256},
		}); err != nil {



			if ferr := a.Store.Nodes().ForgetInstructionBlock(ctx, node.NodeID, b.Plugin, b.Path, b.SHA256); ferr != nil {
				a.Logger.Error("instruction block: audit failed AND the sighting could not be taken back — this block will not be audited again",
					"node_id", node.NodeID, "plugin", b.Plugin, "audit_error", err, "forget_error", ferr)
				continue
			}
			a.Logger.Warn("instruction block: audit write failed, sighting taken back for retry",
				"node_id", node.NodeID, "plugin", b.Plugin, "error", err)
		}
	}
}

func (a *API) onHeartbeat(ctx context.Context, conn *channel.Conn, node *spi.Node, frame channel.Frame) {
	var hb channel.Heartbeat
	_ = frame.Decode(&hb)

	beat := spi.Heartbeat{
		Seq: frame.Seq, At: time.Now(), UptimeSec: hb.UptimeSec,
		InflightCalls: hb.InflightCalls, CertNotAfter: hb.CertNotAfter,
		RemoteShell: hb.RemoteShell,
	}




	if hb.Inbox != nil {
		info := spi.InboxInfo{Waiting: hb.Inbox.Waiting}
		if t, err := time.Parse(time.RFC3339, hb.Inbox.OldestWaitingCreatedAt); err == nil {
			info.OldestWaitingAt = t
		}
		beat.Inbox = &info
	}

	if hb.Workspace != nil {
		status := spi.RuntimeStatus(hb.Workspace.RuntimeStatus)
		if !status.Valid() {













			a.Logger.Warn("node reported a runtime_status outside the vocabulary; recording unknown",
				"node_id", node.NodeID, "reported", hb.Workspace.RuntimeStatus)
			status = spi.StatusUnknown
		}
		beat.Runtime = &spi.RuntimeInfo{
			State:         hb.Workspace.State,
			RuntimeStatus: status,
			RestartCount: hb.Workspace.RestartCount,
			StartedAt:    hb.Workspace.StartedAt,
			LastExitCode: hb.Workspace.LastExitCode,
			OOMKilled:    hb.Workspace.OOMKilled,
			LastError:    hb.Workspace.LastError,
			Probe:        probeOf(hb.Workspace.Probe),
			Memory:       hb.Workspace.Memory,
		}
	}

	if err := a.Store.Nodes().UpdateHeartbeat(ctx, node.NodeID, beat); err != nil {
		a.Logger.Warn("heartbeat", "node_id", node.NodeID, "error", err)
	}

	a.auditInstructionBlocks(ctx, node, hb.InstructionBlocks)

	ack, _ := channel.NewFrame(channel.TypeHeartbeatAck, frame.MsgID, channel.HeartbeatAck{
		ServerTS:        time.Now().UTC(),
		NextExpectedSeq: conn.LastSeq() + 1,
	})
	_ = conn.Send(ctx, ack)




	if !hb.CertNotAfter.IsZero() {
		remaining := time.Until(hb.CertNotAfter)
		if remaining > 0 && remaining < 8*time.Hour {
			rotate, _ := channel.NewFrame(channel.TypeCertRotateRequired, "",
				channel.CertRotateRequired{Reason: "expiring", GraceSec: int(remaining.Seconds())})
			_ = conn.Send(ctx, rotate)
		}
	}
}


func (a *API) registerCapabilities(ctx context.Context, node *spi.Node,
	decls []channel.CapabilityDecl) (accepted []string, rejected []channel.RejectedCapability) {

	existing, err := a.Store.Capabilities().ListByNode(ctx, node.NodeID)
	if err != nil {
		a.Logger.Warn("list capabilities", "node_id", node.NodeID, "error", err)
	}
	byKey := make(map[string]spi.Capability, len(existing))
	for _, c := range existing {
		byKey[c.Name+"@"+c.Version] = c
	}

	for _, d := range decls {
		risk := spi.Risk(d.Risk)
		switch risk {
		case spi.RiskLow, spi.RiskConfirm, spi.RiskBlocked:
		case "":



			risk = spi.RiskConfirm
		default:
			rejected = append(rejected, channel.RejectedCapability{
				Name:   d.Name,
				Reason: "unknown risk level " + d.Risk,
			})
			continue
		}

		key := d.Name + "@" + d.Version
		id := "cap_" + uuid.NewString()
		if prior, found := byKey[key]; found {






			id = prior.ID
		}

		if err := a.Store.Capabilities().Upsert(ctx, &spi.Capability{
			ID: id, NodeID: node.NodeID, Name: d.Name, Version: d.Version,
			Risk: risk, InputSchema: []byte(orEmptyJSON(d.InputSchema)),
			OutputSchema: []byte(orEmptyJSON(d.OutputSchema)),
			AllowFrom:    d.AllowFrom, Health: "ok",
			RegisteredAt: time.Now(),
		}); err != nil {
			rejected = append(rejected, channel.RejectedCapability{
				Name: d.Name, Reason: "registry write failed",
			})
			a.Logger.Warn("register capability", "node_id", node.NodeID,
				"capability", d.Name, "error", err)
			continue
		}
		accepted = append(accepted, key)
	}

	a.Log.System(ctx, "capability.registered", map[string]any{
		"node_id": node.NodeID, "accepted": len(accepted), "rejected": len(rejected),
	})
	return accepted, rejected
}

func orEmptyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}




func rolesLabel(rs spi.Roles) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, "+")
}













func probeOf(s string) spi.ProbeResult {
	r := spi.ProbeResult(s)
	if !r.Valid() {
		return spi.ProbeUnreported
	}
	return r
}








































func (a *API) noticeReportedEpoch(ctx context.Context, nodeID string, reported int64) {


	if reported <= 0 || reported <= a.epochNow() {
		return
	}
	if a.Demote == nil {

		a.Logger.Warn("a node reports a higher leader epoch, and this build cannot step down",
			"node_id", nodeID, "reported", reported, "ours", a.epochNow())
		return
	}



	if a.TransferOffered == nil {
		a.Logger.Warn("a node reports a higher leader epoch; cannot check whether this master offered its seat",
			"node_id", nodeID, "reported", reported, "ours", a.epochNow())
		return
	}
	offered, err := a.TransferOffered(ctx)
	if err != nil {
		a.Logger.Warn("a node reports a higher leader epoch; could not read this master's own transfer record",
			"node_id", nodeID, "reported", reported, "ours", a.epochNow(), "error", err)
		return
	}
	if !offered {



		a.Logger.Error("a node reports a leader epoch above this master's, but this master never signed a transfer offer",
			"node_id", nodeID, "reported", reported, "ours", a.epochNow())
		a.Log.System(ctx, "cluster.epoch_ahead_unexplained", map[string]any{
			"node_id": nodeID, "reported": reported, "ours": a.epochNow(),
		})
		return
	}




	a.Log.System(ctx, "cluster.superseded", map[string]any{
		"node_id": nodeID, "reported": reported, "ours": a.epochNow(),
		"detail": "a node has seen a higher epoch and this master signed a transfer offer; stepping down to read-only",
	})
	a.Logger.Warn("superseded: stepping down to a read-only mirror",
		"node_id", nodeID, "reported", reported, "ours", a.epochNow())
	if err := a.Demote(ctx, ""); err != nil {
		a.Logger.Error("could not step down after learning this master was superseded",
			"node_id", nodeID, "error", err)
	}
}
