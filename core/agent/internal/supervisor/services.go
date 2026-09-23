// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/batonos/baton/core/agent/internal/plugins"
	"github.com/batonos/baton/core/agent/internal/runtimespec"
)











type service struct {
	id   string
	decl *plugins.ServiceDecl
	spec *runtimespec.Spec
	dir  string


	workspace string


	secretFiles []string

	cancel context.CancelFunc
	done   chan struct{}




	stopFailed bool

	mu     sync.Mutex
	report plugins.ServiceReport
}

const servicesStateDir = "services"


func ServicesRoot(dataDir string) string {
	return filepath.Join(dataDir, pluginStateDir, servicesStateDir)
}




func ServiceInbox(dataDir, id string) string {
	return filepath.Join(ServicesRoot(dataDir), id, "inbox")
}



func (s *Supervisor) ServiceStatus(id string) (string, bool) {
	s.servicesMu.Lock()
	defer s.servicesMu.Unlock()
	svc, ok := s.services[id]
	if !ok {
		return "", false
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.report.Status, true
}



func ServiceOutbox(dataDir, id string) string {
	return filepath.Join(ServicesRoot(dataDir), id, "outbox")
}





func (s *Supervisor) reconcileServices(ctx context.Context, m *plugins.Manager, rep *plugins.Report) {
	s.servicesMu.Lock()
	defer s.servicesMu.Unlock()
	if s.services == nil {
		s.services = map[string]*service{}
	}
	for id, svc := range s.services {
		if _, want := rep.Services[id]; want {
			continue
		}
		s.log.Info("service: plugin gone, stopping", "plugin", id)
		svc.cancel()


		go func(id string, svc *service) {
			<-svc.done
			svc.mu.Lock()
			failed := svc.stopFailed
			svc.mu.Unlock()
			if failed {
				s.log.Error("service: process could not be stopped; leaving its directory in place", "plugin", id, "path", svc.dir)
				return
			}
			if err := os.RemoveAll(svc.dir); err != nil {
				s.log.Warn("service: remove state dir", "plugin", id, "path", svc.dir, "error", err)
			}
		}(id, svc)
		delete(s.services, id)
	}
	ids := make([]string, 0, len(rep.Services))
	for id := range rep.Services {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if svc, running := s.services[id]; running {







			svc.mu.Lock()
			rep := svc.report
			svc.mu.Unlock()
			if err := m.UpdateService(id, rep); err != nil {
				s.log.Warn("service: restore status after reconcile", "plugin", id, "error", err)
			}
			continue
		}
		svc := s.newService(id, rep.Services[id])
		s.services[id] = svc
		sctx, cancel := context.WithCancel(ctx)
		svc.cancel = cancel
		go s.runService(sctx, m, svc)
	}
}

func (s *Supervisor) newService(id string, decl *plugins.ServiceDecl) *service {
	dataDir := envOr("BATON_DATA_DIR", DefaultDataDir)
	svc := &service{
		id:        id,
		decl:      decl,
		dir:       filepath.Join(ServicesRoot(dataDir), id),
		workspace: s.spec.Adapter.Workspace.MountPath,
		done:      make(chan struct{}),
		report: plugins.ServiceReport{
			Status: string(StatePending), User: decl.User,
			Outbox: ServiceOutbox(dataDir, id), Isolation: plugins.ServiceUnknown,
		},
	}




	svc.spec = &runtimespec.Spec{
		Package:   runtimespec.Package{Command: decl.Command, WorkingDir: decl.WorkingDir},
		Execution: runtimespec.Execution{User: decl.User, Secrets: decl.Secrets},
		Adapter: runtimespec.Adapter{
			Health:    decl.Health,
			Lifecycle: runtimespec.Lifecycle{Restart: decl.Restart, StopGraceSec: decl.StopGraceSec},
		},
	}
	if svc.spec.Adapter.Lifecycle.Restart == "" {
		svc.spec.Adapter.Lifecycle.Restart = runtimespec.RestartOnFailure
	}
	return svc
}



func (s *Supervisor) stopServices() {
	s.servicesMu.Lock()
	all := make([]*service, 0, len(s.services))
	for _, svc := range s.services {
		all = append(all, svc)
	}
	s.services = nil
	s.servicesMu.Unlock()
	for _, svc := range all {
		svc.cancel()
	}
	for _, svc := range all {
		<-svc.done
	}
}




func (s *Supervisor) ServiceIsolation() map[string]string {
	s.servicesMu.Lock()
	defer s.servicesMu.Unlock()
	out := make(map[string]string, len(s.services))
	for id, svc := range s.services {
		svc.mu.Lock()
		out[id] = svc.report.Isolation
		svc.mu.Unlock()
	}
	return out
}


func (s *Supervisor) Services() []plugins.ServiceReport {
	s.servicesMu.Lock()
	defer s.servicesMu.Unlock()
	out := make([]plugins.ServiceReport, 0, len(s.services))
	for _, svc := range s.services {
		svc.mu.Lock()
		out = append(out, svc.report)
		svc.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Outbox < out[j].Outbox })
	return out
}


func (s *Supervisor) set(m *plugins.Manager, svc *service, status, reason string) {
	svc.mu.Lock()
	svc.report.Status = status
	svc.report.Reason = reason
	rep := svc.report
	svc.mu.Unlock()
	s.reconcileMu.Lock()
	err := m.UpdateService(svc.id, rep)
	s.reconcileMu.Unlock()
	if err != nil {
		s.log.Warn("service: write status", "plugin", svc.id, "error", err)
	}
}





func (s *Supervisor) runService(ctx context.Context, m *plugins.Manager, svc *service) {
	defer close(svc.done)
	if err := s.prepareService(svc); err != nil {
		s.set(m, svc, string(StateFailed), err.Error())
		s.log.Error("service: prepare", "plugin", svc.id, "error", err)
		return
	}
	backoff := restartBackoffMin
	for {
		runner := newProcessRunner(svc.spec, Options{Logger: s.log.With("service", svc.id), Stdout: os.Stdout, Stderr: os.Stderr})
		runner.extraEnv = svc.env()
		s.set(m, svc, string(StateStarting), "")
		if err := runner.Start(ctx); err != nil {
			s.set(m, svc, string(StateFailed), err.Error())
			s.log.Error("service: failed to start", "plugin", svc.id, "error", err)
			if svc.spec.Adapter.Lifecycle.Restart == runtimespec.RestartNever || !s.waitServiceRestart(ctx, &backoff) {
				return
			}
			continue
		}
		svc.mu.Lock()
		svc.report.PID = runner.pid()
		svc.mu.Unlock()
		s.set(m, svc, string(StateRunning), "")
		probeDone := make(chan struct{})
		go s.probeService(ctx, m, svc, probeDone)
		exit, waitErr := runner.Wait(ctx)
		close(probeDone)
		svc.mu.Lock()
		svc.report.PID = 0
		svc.mu.Unlock()
		if ctx.Err() != nil {

			stopCtx, cancel := context.WithTimeout(context.Background(), svc.spec.Adapter.Lifecycle.StopGrace()+10*time.Second)
			err := runner.Stop(stopCtx, svc.spec.Adapter.Lifecycle.StopGrace())
			cancel()
			if err != nil {


				svc.mu.Lock()
				svc.stopFailed = true
				svc.report.PID = runner.pid()
				svc.mu.Unlock()
				s.set(m, svc, plugins.ServiceUnknown, "could not stop: "+err.Error())
				return
			}
			s.set(m, svc, string(StateStopped), "")
			return
		}
		if waitErr != nil {
			s.set(m, svc, plugins.ServiceUnknown, "wait: "+waitErr.Error())
		} else if exit.Code == 0 {
			s.set(m, svc, string(StateExited), "")
		} else {
			s.set(m, svc, string(StateFailed), exitLine(exit))
		}
		s.log.Warn("service exited", "plugin", svc.id, "code", exit.Code, "signal", exit.Signal, "oom_killed", exit.OOMKilled)
		restart := false
		switch svc.spec.Adapter.Lifecycle.Restart {
		case runtimespec.RestartAlways:
			restart = true
		case runtimespec.RestartNever:
			restart = false
		default:
			restart = exit.Code != 0
		}
		if !restart {
			return
		}
		s.set(m, svc, string(StateRestarting), "")
		if !s.waitServiceRestart(ctx, &backoff) {
			s.set(m, svc, string(StateStopped), "")
			return
		}
	}
}

func exitLine(e Exit) string {
	if e.Signal != "" {
		return "exit " + strconv.Itoa(e.Code) + " (" + e.Signal + ")"
	}
	return "exit " + strconv.Itoa(e.Code)
}

func (s *Supervisor) waitServiceRestart(ctx context.Context, backoff *time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(jitter(*backoff)):
	}
	*backoff *= 2
	if *backoff > restartBackoffMax {
		*backoff = restartBackoffMax
	}
	return true
}





func (s *Supervisor) prepareService(svc *service) error {
	uid, gid, err := uidOf(svc.decl.User)
	if err != nil {
		return fmt.Errorf("service.user %q: %w", svc.decl.User, err)
	}
	runtimeUID, _, err := uidOf(s.spec.Execution.User)
	if err != nil {
		return fmt.Errorf("execution.user %q: %w", s.spec.Execution.User, err)
	}




	for _, d := range []string{ServicesRoot(envOr("BATON_DATA_DIR", DefaultDataDir)), svc.dir} {
		if err := os.MkdirAll(d, 0o711); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o711); err != nil {
			return err
		}
	}
	for _, d := range []string{filepath.Join(svc.dir, "home"), filepath.Join(svc.dir, "outbox"), filepath.Join(svc.dir, "inbox")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(d, uid, gid); err != nil {
				return fmt.Errorf("chown %s: %w", d, err)
			}
		}
	}









	svc.secretFiles = svc.secretFiles[:0]
	if os.Geteuid() == 0 && len(svc.decl.Secrets) > 0 {
		sdir := filepath.Join(svc.dir, "secrets")
		if err := os.MkdirAll(sdir, 0o700); err != nil {
			return err
		}
		if err := os.Chown(sdir, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", sdir, err)
		}
		for _, sec := range svc.decl.Secrets {
			src := sec.Path()
			raw, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("secret %s is not mounted at %s inside this container: %w", sec.Name, src, err)
			}
			dst := filepath.Join(sdir, sec.Name)
			if err := os.WriteFile(dst, raw, 0o400); err != nil {
				return err
			}
			if err := os.Chown(dst, uid, gid); err != nil {
				return fmt.Errorf("chown %s: %w", dst, err)
			}
			svc.secretFiles = append(svc.secretFiles, sec.Name+"_FILE="+dst)
		}
	}
	isolation := "isolated"
	if uid == runtimeUID {
		isolation = "shared"
		s.log.Info("service: via is a label on this node — the service and the runtime share a uid, so a letter in the service's outbox proves nothing about who wrote it",
			"plugin", svc.id, "uid", uid)
	}
	svc.mu.Lock()
	svc.report.Isolation = isolation
	svc.mu.Unlock()
	return nil
}



func uidOf(user string) (uid, gid int, err error) {
	if user == "" {
		return os.Geteuid(), os.Getegid(), nil
	}
	cred, err := credentialFor(user)
	if err != nil {
		return 0, 0, err
	}
	return int(cred.Uid), int(cred.Gid), nil
}





func (svc *service) env() []string {
	out := []string{
		"HOME=" + filepath.Join(svc.dir, "home"),
		"BATON_OUTBOX_DIR=" + filepath.Join(svc.dir, "outbox"),
		"BATON_INBOX_DIR=" + filepath.Join(svc.dir, "inbox"),
		"BATON_SERVICE=" + svc.id,
	}
	if svc.workspace != "" {
		out = append(out, "BATON_WORKSPACE="+svc.workspace)
	}
	out = append(out, svc.secretFiles...)
	for _, e := range svc.decl.Env {
		out = append(out, e.Name+"="+e.Value)
	}
	return out
}





func (s *Supervisor) probeService(ctx context.Context, m *plugins.Manager, svc *service, done <-chan struct{}) {
	h := svc.spec.Adapter.Health
	if h.Probe != runtimespec.ProbeTCP && h.Probe != runtimespec.ProbeExec {
		return
	}
	threshold := h.FailureThreshold
	if threshold < 1 {
		threshold = 1
	}
	tick := time.NewTicker(h.Interval())
	defer tick.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
		}
		ok, err := probeOnce(ctx, h.Probe, h)
		if ok {
			if fails >= threshold {
				s.set(m, svc, string(StateRunning), "")
			}
			fails = 0
			continue
		}
		fails++
		if fails == threshold {
			msg := "health probe failed"
			if err != nil {
				msg += ": " + err.Error()
			}
			s.set(m, svc, plugins.ServiceUnknown, msg)
			s.log.Warn("service: health probe failed", "plugin", svc.id, "probe", string(h.Probe), "consecutive", fails, "error", err)
		}
	}
}
