// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)


































const ManageDirName = "manage"








const DisabledDirName = "disabled"




const (
	VerbAdopt   = "adopt"
	VerbEnable  = "enable"
	VerbDisable = "disable"
	VerbRemove  = "remove"
)








var ManageVerbs = []string{VerbAdopt, VerbEnable, VerbDisable, VerbRemove}


type ManageRequest struct {
	Verb   string `json:"verb"`
	Plugin string `json:"plugin"`
}


type ManageDone struct {
	Verb   string `json:"verb"`
	Plugin string `json:"plugin"`




	Done bool `json:"done"`





	Refused string `json:"refused,omitempty"`


	Found bool `json:"found"`




	Disabled bool `json:"disabled"`



	Summary   string   `json:"summary,omitempty"`
	Withdrawn []string `json:"withdrawn,omitempty"`
	At        string   `json:"at,omitempty"`
}


func ManageDir(stateDir string) string { return filepath.Join(stateDir, ManageDirName) }


func DisabledDir(stateDir string) string { return filepath.Join(stateDir, DisabledDirName) }






func IsDisabled(stateDir, id string) bool {
	_, err := os.Stat(filepath.Join(DisabledDir(stateDir), id))
	return err == nil
}


func DisabledIDs(stateDir string) []string {
	entries, err := os.ReadDir(DisabledDir(stateDir))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}





func SetDisabled(stateDir, id string, off bool) error {
	dir := DisabledDir(stateDir)
	path := filepath.Join(dir, id)
	if !off {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}



	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}


func PendingManageRequests(dir string) ([]string, error) {
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
	sort.Strings(out)
	return out, nil
}


func ReadManageRequest(dir, nonce string) (ManageRequest, error) {
	var r ManageRequest
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
	if !knownVerb(r.Verb) {
		return r, fmt.Errorf("%s.request asks for %q, which this build does not perform (%s)",
			nonce, r.Verb, strings.Join(ManageVerbs, ", "))
	}
	return r, nil
}

func knownVerb(v string) bool {
	for _, k := range ManageVerbs {
		if k == v {
			return true
		}
	}
	return false
}



func WriteManageDone(dir, nonce string, d ManageDone) error {
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



func SubmitManageRequest(stateDir, verb, plugin string, wait time.Duration) (ManageDone, error) {
	if !knownVerb(verb) {
		return ManageDone{}, fmt.Errorf("%s is not one of %s", verb, strings.Join(ManageVerbs, ", "))
	}
	dir := ManageDir(stateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ManageDone{}, err
	}
	nonce := fmt.Sprintf("manage-%d", time.Now().UTC().UnixNano())
	raw, _ := json.Marshal(ManageRequest{Verb: verb, Plugin: plugin})
	tmp := filepath.Join(dir, nonce+".request.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return ManageDone{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, nonce+".request")); err != nil {
		return ManageDone{}, err
	}
	deadline := time.Now().Add(wait)
	donePath := filepath.Join(dir, nonce+".done")
	for {
		if raw, err := os.ReadFile(donePath); err == nil {
			var d ManageDone
			if err := json.Unmarshal(raw, &d); err != nil {
				return ManageDone{}, fmt.Errorf("%s: %w", donePath, err)
			}
			_ = os.Remove(donePath)
			return d, nil
		}
		if time.Now().After(deadline) {



			_ = os.Remove(filepath.Join(dir, nonce+".request"))
			return ManageDone{}, fmt.Errorf("no answer from the node daemon within %s — is it running? (the request was withdrawn)", wait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
