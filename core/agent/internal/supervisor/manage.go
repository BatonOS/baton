// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/batonos/baton/core/agent/internal/plugins"
	"github.com/batonos/baton/core/agent/internal/skills"
)











func (s *Supervisor) serveManage(ctx context.Context) {
	stateDir := filepath.Join(envOr("BATON_DATA_DIR", DefaultDataDir), pluginStateDir)
	dir := plugins.ManageDir(stateDir)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		nonces, err := plugins.PendingManageRequests(dir)
		if err != nil {
			s.log.Warn("plugins: manage dir", "error", err)
			continue
		}
		for _, nonce := range nonces {
			req, err := plugins.ReadManageRequest(dir, nonce)
			if err != nil {




				_ = plugins.WriteManageDone(dir, nonce, plugins.ManageDone{
					Refused: err.Error(), At: time.Now().UTC().Format(time.RFC3339),
				})
				continue
			}
			done := s.performManage(ctx, stateDir, req)
			if err := plugins.WriteManageDone(dir, nonce, done); err != nil {
				s.log.Warn("plugins: manage answer", "nonce", nonce, "error", err)
			}
			s.log.Info("plugins: manage", "verb", req.Verb, "plugin", req.Plugin,
				"done", done.Done, "refused", done.Refused)
		}
	}
}





func (s *Supervisor) performManage(ctx context.Context, stateDir string, req plugins.ManageRequest) plugins.ManageDone {
	d := plugins.ManageDone{Verb: req.Verb, Plugin: req.Plugin, At: time.Now().UTC().Format(time.RFC3339)}

	root := envOr("BATON_PLUGINS_ROOT", DefaultPluginsRoot)
	dir := filepath.Join(root, req.Plugin)





	if req.Verb == plugins.VerbAdopt {
		return s.performAdopt(ctx, stateDir, root, req)
	}

	man, class, err := readFacility(root, req.Plugin)
	if err != nil {



		d.Refused = fmt.Sprintf("%s is not a facility on this node — %v", req.Plugin, err)
		return d
	}
	d.Found = true
	d.Disabled = plugins.IsDisabled(stateDir, req.Plugin)
























	if req.Verb == plugins.VerbRemove {
		if man.HasRuntime() {






























			d.Refused = req.Plugin + " supplies this node's runtime and is not removed from it" +
				" — a node is one runtime, chosen at create from its template, and that is not changed from inside it"
			return d
		}
		if class == plugins.ClassSystem {
			d.Refused = req.Plugin + " came with this build (door: seeded) and is not removed" +
				" — it is what makes this office work, and taking it out from inside is not a way to fix anything"
			return d
		}
	}

	switch req.Verb {
	case plugins.VerbDisable, plugins.VerbEnable:
		want := req.Verb == plugins.VerbDisable
		if d.Disabled == want {



			d.Refused = fmt.Sprintf("%s is already %s", req.Plugin, map[bool]string{true: "disabled", false: "enabled"}[want])
			return d
		}
		if err := plugins.SetDisabled(stateDir, req.Plugin, want); err != nil {
			d.Refused = fmt.Sprintf("could not record the switch: %v", err)
			return d
		}
		d.Disabled = want
		d.Done = true

	case plugins.VerbRemove:





		if err := os.RemoveAll(dir); err != nil {
			d.Refused = fmt.Sprintf("could not remove %s: %v", dir, err)
			return d
		}


		_ = plugins.SetDisabled(stateDir, req.Plugin, false)
		d.Disabled = false
		d.Done = true
	}




	if rep := s.reconcilePlugins(ctx); rep != nil {
		d.Summary = rep.Summary.Line()
		d.Withdrawn = append([]string{}, rep.Withdrawn...)
		for _, w := range rep.Retired[req.Plugin] {
			d.Withdrawn = append(d.Withdrawn, w)
		}
	} else {
		d.Refused = "the change was made and the reconcile did not run — see the daemon log"
	}
	return d
}






func readFacility(root, id string) (*plugins.Manifest, plugins.Class, error) {
	dir := filepath.Join(root, id)
	raw, err := os.ReadFile(filepath.Join(dir, plugins.ManifestFile))
	if err != nil {
		return nil, plugins.ClassUnknown, err
	}
	man, err := plugins.Parse(raw, filepath.Join(id, plugins.ManifestFile))
	if err != nil {
		return nil, plugins.ClassUnknown, err
	}
	return man, plugins.ClassifyAt(root, id), nil
}















func (s *Supervisor) performAdopt(ctx context.Context, stateDir, root string, req plugins.ManageRequest) plugins.ManageDone {
	d := plugins.ManageDone{Verb: req.Verb, Plugin: req.Plugin, At: time.Now().UTC().Format(time.RFC3339)}

	if s.fetchFacility == nil {



		d.Refused = "this node has no way to reach its network's catalogue, so it cannot adopt anything"
		return d
	}

	archive, digest, id, err := s.fetchFacility(ctx, req.Plugin)
	if err != nil {



		d.Refused = err.Error()
		return d
	}
	if id == "" {
		id = req.Plugin
	}
	d.Plugin = id







	if got := plugins.ArchiveDigest(archive); got != digest {
		d.Refused = fmt.Sprintf("what arrived is not what the catalogue named: it says %s, these bytes are %s", digest, got)
		return d
	}





	stage, err := os.MkdirTemp(root, ".adopt-"+id+"-")
	if err != nil {
		d.Refused = fmt.Sprintf("could not stage the archive: %v", err)
		return d
	}
	defer func() { _ = os.RemoveAll(stage) }()





	if _, err := skills.Extract(archive, stage); err != nil {
		d.Refused = fmt.Sprintf("the archive did not unpack: %v", err)
		return d
	}
	if _, err := os.Stat(filepath.Join(stage, plugins.ManifestFile)); err != nil {
		d.Refused = fmt.Sprintf("the archive has no %s at its root — that is what makes a directory a facility", plugins.ManifestFile)
		return d
	}

	dest := filepath.Join(root, id)
	if err := os.RemoveAll(dest); err != nil {
		d.Refused = fmt.Sprintf("could not clear %s: %v", dest, err)
		return d
	}
	if err := os.Rename(stage, dest); err != nil {
		d.Refused = fmt.Sprintf("could not put %s in place: %v", id, err)
		return d
	}

	if err := plugins.WriteAdoptedLedger(root, id); err != nil {




		d.Refused = fmt.Sprintf("the facility is on disk and its ledger lines are not (%v) — "+
			"it will read as untrusted until this is repaired; ask again", err)
		return d
	}




	_ = plugins.SetDisabled(stateDir, id, false)
	d.Found, d.Done = true, true

	if rep := s.reconcilePlugins(ctx); rep != nil {
		d.Summary = rep.Summary.Line()
		d.Withdrawn = append([]string{}, rep.Withdrawn...)
	} else {
		d.Refused = "it is on disk and the reconcile did not run — see the daemon log"
	}
	return d
}
