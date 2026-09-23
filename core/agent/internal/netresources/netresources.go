// SPDX-License-Identifier: Apache-2.0























































package netresources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/nodeface"
)




const catalogueFormat = "baton-network-resources/1"


type Syncer struct {


	WorkspaceRoot string
	MasterURL     string
	Client        *http.Client
	Logger        *slog.Logger
}








type entry struct {
	ResourceID string `json:"resource_id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Publisher  string `json:"publisher,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}




type pullView struct {
	Items []struct {
		ResourceID string `json:"resource_id"`
		Type       string `json:"type"`
		Name       string `json:"name"`
		Version    string `json:"version"`
		Visibility string `json:"visibility"`
		Hash       string `json:"hash"`
		Publisher  string `json:"publisher"`
		UpdatedAt  string `json:"updated_at"`
	} `json:"items"`
}




func (s *Syncer) Sync(ctx context.Context) error {
	if s.WorkspaceRoot == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.MasterURL+nodeface.APIBase+"/agent/network-resources", nil)
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {


		_ = s.write(stateUnavailable, nil)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = s.write(stateUnavailable, nil)
		return fmt.Errorf("network-resources: control plane answered %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var view pullView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		_ = s.write(stateUnavailable, nil)
		return err
	}

	rows := make([]entry, 0, len(view.Items))
	for _, it := range view.Items {
		rows = append(rows, entry{
			ResourceID: it.ResourceID,
			Type:       it.Type,
			Name:       it.Name,
			Version:    it.Version,
			Scope:      it.Visibility,
			Hash:       it.Hash,
			Publisher:  it.Publisher,
			UpdatedAt:  it.UpdatedAt,
		})
	}
	return s.write(stateFresh, rows)
}




const (
	stateFresh       = "fresh"
	stateUnavailable = "unavailable"
)








func (s *Syncer) MarkUnavailable() error {
	if s.WorkspaceRoot == "" {
		return nil
	}
	return s.write(stateUnavailable, nil)
}


func (s *Syncer) write(state string, rows []entry) error {
	dir := filepath.Join(s.WorkspaceRoot, ".baton", "local", "net")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if rows == nil {
		rows = []entry{}
	}
	doc := map[string]any{
		"format": catalogueFormat,


		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"source":     "master",
		"state":      state,



		"state_means": map[string]string{
			"fresh":       "this is what the company has right now; an empty list means it has nothing",
			"unavailable": "the catalogue could not be fetched — nobody could tell you. An empty list here is NOT an answer about the company",
		},
		"resources": rows,
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".resources.json.tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "resources.json"))
}
