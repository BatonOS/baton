// SPDX-License-Identifier: Apache-2.0












package contacts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/grantproof"
	"github.com/batonos/baton/core/agent/internal/nodeface"
)

const (
	membersFormat = "baton-contacts-members/1"
	heldFormat    = "baton-held-grant/1"
)


type Syncer struct {


	WorkspaceRoot string
	MasterURL     string
	Client        *http.Client
	Logger        *slog.Logger
}

type memberView struct {
	Identity string `json:"identity"`
	NodeID   string `json:"node_id,omitempty"`
}

type heldView struct {
	GrantID   string `json:"grant_id"`
	RecordB64 string `json:"record_b64"`
	ProofB64  string `json:"proof_b64"`




}

type pullView struct {
	Format          string       `json:"format"`
	AuthorityKeyPEM string       `json:"authority_key_pem"`
	Members         []memberView `json:"members"`
	Held            []heldView   `json:"held"`
}




type heldFile struct {
	Format     string `json:"format"`
	Scope      string `json:"scope"`
	Action     string `json:"action"`
	Grantor    string `json:"grantor"`
	Grantee    string `json:"grantee"`
	ReceivedAt string `json:"received_at"`
	RecordB64  string `json:"record_b64"`
	ProofB64   string `json:"proof_b64"`
}


func (s *Syncer) Sync(ctx context.Context) (int, error) {
	if s.WorkspaceRoot == "" {
		return 0, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.MasterURL+nodeface.APIBase+"/agent/contacts", nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("contacts: control plane answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var view pullView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return 0, err
	}
	if view.Format != "baton-contacts/1" {
		return 0, fmt.Errorf("contacts: control plane speaks %q, this build reads baton-contacts/1", view.Format)
	}

	if err := s.writeMembers(view.Members, view.AuthorityKeyPEM); err != nil {
		return 0, err
	}
	return s.syncHeld(view.Held)
}




func (s *Syncer) writeMembers(members []memberView, authorityKeyPEM string) error {
	dir := filepath.Join(s.WorkspaceRoot, ".baton", "local", "net")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	doc := map[string]any{
		"format":     membersFormat,
		"rebuilt_at": time.Now().UTC().Format(time.RFC3339),
		"source":     "master",



		"authority_key_pem": authorityKeyPEM,
		"members":           members,
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".members.json.tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "members.json"))
}




func (s *Syncer) syncHeld(entries []heldView) (int, error) {
	dir := filepath.Join(s.WorkspaceRoot, ".baton", "portable", "self", "held")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}

	changed := 0
	want := map[string]bool{}
	for _, e := range entries {
		canonical, err := base64.StdEncoding.DecodeString(e.RecordB64)
		if err != nil {
			s.Logger.Warn("contacts: held entry with undecodable canonical refused", "grant_id", e.GrantID, "error", err)
			continue
		}
		f, err := grantproof.Parse(canonical)
		if err != nil {
			s.Logger.Warn("contacts: held entry refused", "grant_id", e.GrantID, "error", err)
			continue
		}
		if f.GrantID != e.GrantID {


			s.Logger.Warn("contacts: held entry refused — envelope grant_id disagrees with the signed bytes",
				"envelope", e.GrantID, "signed", f.GrantID)
			continue
		}
		want[f.GrantID] = true
		file := heldFile{
			Format: heldFormat,
			Scope:  f.Scope, Action: f.Action, Grantor: f.Grantor, Grantee: f.Grantee,
			ReceivedAt: time.Now().UTC().Format(time.RFC3339),
			RecordB64:  e.RecordB64, ProofB64: e.ProofB64,
		}
		path := filepath.Join(dir, f.GrantID+".json")
		if same, _ := heldUnchanged(path, e.RecordB64, e.ProofB64); same {
			continue
		}
		raw, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			return changed, err
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			return changed, err
		}
		changed++
	}




	dirents, err := os.ReadDir(dir)
	if err != nil {
		return changed, err
	}
	for _, d := range dirents {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(d.Name(), ".json")
		if want[id] {
			continue
		}
		var existing heldFile
		raw, rerr := os.ReadFile(filepath.Join(dir, d.Name()))
		if rerr != nil || json.Unmarshal(raw, &existing) != nil {
			continue
		}
		if !strings.HasPrefix(existing.Scope, "network") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, d.Name())); err == nil {
			s.Logger.Info("contacts: held grant withdrawn by the authority", "grant_id", id)
			changed++
		}
	}
	return changed, nil
}


func heldUnchanged(path, recordB64, proofB64 string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var f heldFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return false, err
	}
	return f.RecordB64 == recordB64 && f.ProofB64 == proofB64, nil
}
