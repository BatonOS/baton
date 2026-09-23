// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/plugins"
)







const DefaultPluginsRoot = "/opt/baton/plugins"




const pluginStateDir = "plugins"




func (s *Supervisor) pluginManager() (*plugins.Manager, error) {
	env, err := buildEnvironment(s.spec)
	if err != nil {
		return nil, err
	}
	dataDir := envOr("BATON_DATA_DIR", DefaultDataDir)
	sk := s.spec.Adapter.Skills
	return &plugins.Manager{
		Root:          envOr("BATON_PLUGINS_ROOT", DefaultPluginsRoot),


		InventoryPath: filepath.Join(envOr("BATON_PLUGINS_ROOT", DefaultPluginsRoot), plugins.InventoryFile),
		Workspace:     s.spec.Adapter.Workspace.MountPath,
		MountPath:     sk.MountPath,
		Discovery:     sk.Discovery,
		RuntimePath:   expandHome(sk.RuntimePath, env),
		StateDir:      filepath.Join(dataDir, pluginStateDir),
		Env:           env,


		Supplier: s.spec.Metadata.Labels["supplier"],
		Log:           s.log,
	}, nil
}









func (s *Supervisor) reconcilePlugins(ctx context.Context) *plugins.Report {
	m, err := s.pluginManager()
	if err != nil {
		s.log.Error("plugins: environment", "error", err)
		return nil
	}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	rep, err := m.Reconcile(ctx)
	if err != nil {
		s.log.Error("plugins: reconcile", "error", err)
		return nil
	}


	s.log.Info("plugins reconciled", "summary", rep.Summary.Line())
	s.reconcileServices(ctx, m, &rep)
	for _, r := range rep.Refused {
		s.log.Warn("plugins: refused", "reason", r)
	}
	for _, p := range rep.Plugins {
		if strings.HasPrefix(p.Hook, "refused") || strings.HasPrefix(p.Hook, "failed") {
			s.log.Warn("plugins: hook", "plugin", p.ID, "outcome", p.Hook)
		}
		for _, sk := range p.Skills {
			if sk.State == plugins.ExposureRefused {
				s.log.Warn("plugins: skill not exposed", "plugin", p.ID, "skill", sk.Name, "why", sk.Why)
			}
		}
	}
	return &rep
}











func (s *Supervisor) serveSkillSync(ctx context.Context) {
	dir := plugins.SyncDir(filepath.Join(envOr("BATON_DATA_DIR", DefaultDataDir), pluginStateDir))
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		nonces, err := plugins.PendingSyncRequests(dir)
		if err != nil {
			s.log.Warn("plugins: sync dir", "error", err)
			continue
		}
		for _, nonce := range nonces {
			req, err := plugins.ReadSyncRequest(dir, nonce)
			if err != nil {


				_ = plugins.WriteSyncDone(dir, nonce, plugins.SyncDone{Error: err.Error(), ReconciledAt: time.Now().UTC()})
				continue
			}
			rep := s.reconcilePlugins(ctx)
			done := plugins.DoneFor(rep, req.Plugin, time.Now().UTC())
			if rep == nil {
				done.Error = "reconcile did not run — see the daemon log"
			}
			if err := plugins.WriteSyncDone(dir, nonce, done); err != nil {
				s.log.Warn("plugins: sync answer", "nonce", nonce, "error", err)
			}
			s.log.Info("plugins: skills sync", "plugin", req.Plugin, "found", done.Found,
				"linked", done.Linked, "refused", done.Refused, "unknown", done.Unknown)
		}
	}
}







func (s *Supervisor) ReconcilePlugins(ctx context.Context) { s.reconcilePlugins(ctx) }








func (s *Supervisor) InstructionBlocks() []plugins.PluginReport {
	return plugins.InstructionBlocksAt(filepath.Join(envOr("BATON_DATA_DIR", DefaultDataDir), pluginStateDir))
}









func expandHome(p string, env []string) string {
	if p == "" {
		return ""
	}
	home := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	return os.Expand(p, func(k string) string {
		if k == "HOME" {
			return home
		}



		return "$" + k
	})
}
