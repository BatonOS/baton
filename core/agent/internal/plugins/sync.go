// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)






















const SyncDirName = "sync"


type SyncRequest struct {
	Plugin string `json:"plugin"`
}


type SyncDone struct {
	Plugin string `json:"plugin"`


	Found bool `json:"found"`



	Skills []SkillReport `json:"skills"`

	Linked  int `json:"linked"`
	Refused int `json:"refused"`
	Unknown int `json:"unknown"`




	Trust        string `json:"trust"`
	TrustReason  string `json:"trust_reason"`
	Hook         string `json:"hook"`
	Instructions string `json:"instructions"`




	Withdrawn []string `json:"withdrawn,omitempty"`

	Summary      string    `json:"summary"`
	ReconciledAt time.Time `json:"reconciled_at"`

	Error string `json:"error,omitempty"`
}


func SyncDir(stateDir string) string { return filepath.Join(stateDir, SyncDirName) }


func PendingSyncRequests(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".request") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".request"))
	}
	return out, nil
}


func ReadSyncRequest(dir, nonce string) (SyncRequest, error) {
	var r SyncRequest
	raw, err := os.ReadFile(filepath.Join(dir, nonce+".request"))
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("%s.request: %w", nonce, err)
	}
	if r.Plugin == "" {
		return r, errors.New(nonce + ".request names no plugin")
	}
	return r, nil
}


func DoneFor(rep *Report, plugin string, at time.Time) SyncDone {
	d := SyncDone{Plugin: plugin, ReconciledAt: at}
	if rep == nil {
		return d
	}
	d.Summary = rep.Summary.Line()
	d.Withdrawn = append([]string{}, rep.Withdrawn...)
	seen := map[string]bool{}
	for _, w := range rep.Withdrawn {
		seen[w] = true
	}
	for _, w := range rep.Retired[plugin] {
		if !seen[w] {
			d.Withdrawn = append(d.Withdrawn, w)
		}
	}
	for _, p := range rep.Plugins {
		if p.ID != plugin {
			continue
		}
		d.Found = true
		d.Skills = p.Skills
		d.Trust, d.TrustReason, d.Hook, d.Instructions = string(p.Trust), p.TrustReason, p.Hook, p.Instructions
		for _, sk := range p.Skills {
			switch sk.State {
			case ExposureSymlink, ExposureExternalDirs:
				d.Linked++
			case ExposureRefused:
				d.Refused++
			default:
				d.Unknown++
			}
		}
	}
	return d
}



func WriteSyncDone(dir, nonce string, d SyncDone) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, nonce+".done.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, nonce+".done")); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, nonce+".request"))
}





func SubmitSyncRequest(stateDir, plugin string, wait time.Duration) (SyncDone, error) {
	dir := SyncDir(stateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SyncDone{}, err
	}
	nonce := fmt.Sprintf("sync-%d", time.Now().UTC().UnixNano())
	raw, _ := json.Marshal(SyncRequest{Plugin: plugin})
	tmp := filepath.Join(dir, nonce+".request.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return SyncDone{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, nonce+".request")); err != nil {
		return SyncDone{}, err
	}
	deadline := time.Now().Add(wait)
	donePath := filepath.Join(dir, nonce+".done")
	for {
		if raw, err := os.ReadFile(donePath); err == nil {
			var d SyncDone
			if err := json.Unmarshal(raw, &d); err != nil {
				return SyncDone{}, fmt.Errorf("%s: %w", donePath, err)
			}
			_ = os.Remove(donePath)
			return d, nil
		}
		if time.Now().After(deadline) {
			_ = os.Remove(filepath.Join(dir, nonce+".request"))
			return SyncDone{}, fmt.Errorf("no answer from the node daemon within %s — is it running? (the request was withdrawn)", wait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
