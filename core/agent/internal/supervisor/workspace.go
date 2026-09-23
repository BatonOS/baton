// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/batonos/baton/core/agent/internal/wscontract"
)


















const (




	WorkspaceContract = "baton-workspace/3"




	TravelsPosition = "portable"
)




var WorkspacePositions = []string{"portable", "local"}







var WorkspaceZones = map[string][]string{
	"portable": {"self", "library", "ext"},
	"local":    {"mail", "net", "cache", "ext"},
}









var MailBoxes = []string{"inbox", "read", "outbox", "sent"}













var NeverPortable = []string{"mail", "inbox", "outbox", "pki", "revoked", "enrollment"}


var RuntimeWritable = struct{ Position, Zone, Path string }{
	Position: "local", Zone: "mail", Path: "outbox",
}













type WorkspaceVerdict struct {

	Built []string

	Version string


	Refusals []string




	Compared int
}


var ErrWorkspaceContract = errors.New("supervisor: workspace contract")














func ensureWorkspace(log *slog.Logger, root string) (WorkspaceVerdict, error) {
	v := WorkspaceVerdict{}
	if strings.TrimSpace(root) == "" {


		v.Version = "no workspace"
		return v, nil
	}

	base := filepath.Join(root, wscontract.BatonDir)




	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if e.Name() != "portable" && e.Name() != "local" {
				v.Refusals = append(v.Refusals, fmt.Sprintf(
					"%s/%s is a third position — a workspace has two, and a thing belongs to one of them",
					wscontract.BatonDir, e.Name()))
			}
		}
	} else if !os.IsNotExist(err) {
		return v, fmt.Errorf("supervisor: read %s: %w", base, err)
	}


	for _, position := range WorkspacePositions {
		declared := WorkspaceZones[position]
		dir := filepath.Join(base, position)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return v, fmt.Errorf("supervisor: read %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			v.Compared++





			if position == TravelsPosition && contains(NeverPortable, name) {
				v.Refusals = append(v.Refusals, fmt.Sprintf(
					"%s/%s/%s: %q is on the never-portable list (identity material, enrollment tokens, "+
						"inbox, outbox, revocation marker) — it may never be carried: "+
						"a restored node is a different identity, and an unsent message would go out "+
						"signed by whoever restored it",
					wscontract.BatonDir, position, name, name))
				continue
			}
			if !contains(declared, name) {
				if other := positionOf(name); other != "" {
					v.Refusals = append(v.Refusals, fmt.Sprintf(
						"%s/%s/%s: %q is declared under %q, not %q — a zone in the wrong position "+
							"travels when it must not, or is left behind when it must not be",
						wscontract.BatonDir, position, name, name, other, position))
				} else {
					v.Refusals = append(v.Refusals, fmt.Sprintf(
						"%s/%s/%s: no such zone is declared at that position",
						wscontract.BatonDir, position, name))
				}
			}
		}
	}


	ver, err := readContractVersion(base)
	switch {
	case err != nil:
		v.Version = "unreadable"
		v.Refusals = append(v.Refusals, fmt.Sprintf(
			"%s/contract.json could not be read as a contract document (%v) — "+
				"rewriting it would discard whatever wrote it", wscontract.BatonDir, err))
	case ver == "":
		v.Version = "written"
	case ver == WorkspaceContract:
		v.Version = "current"
	default:
		cmp, cerr := compareContract(ver, WorkspaceContract)
		switch {
		case cerr != nil:
			v.Version = "unreadable"
			v.Refusals = append(v.Refusals, fmt.Sprintf(
				"%s/contract.json says %q, which this build cannot compare with %q",
				wscontract.BatonDir, ver, WorkspaceContract))
		case cmp > 0:


			v.Version = "newer"
			v.Refusals = append(v.Refusals, fmt.Sprintf(
				"%s/contract.json says %q and this build is %q — this workspace came from a build "+
					"that knows more than this one, and regenerating it would silently downgrade it",
				wscontract.BatonDir, ver, WorkspaceContract))
		default:
			v.Version = "stale"
		}
	}









	for _, norm := range wscontract.RootNorms {
		p := filepath.Join(root, norm)
		fi, serr := os.Stat(p)
		if serr != nil {
			continue
		}
		if !fi.IsDir() {
			v.Refusals = append(v.Refusals, fmt.Sprintf(
				"%s is a file, and %s/ is a root directory this contract requires because a party "+
					"other than the agent must find it by name — "+
					"rename your file, then restart; deleting it for you is not this gate's to do", norm, norm))
			continue
		}
		if v.Version == "stale" || v.Version == "written" {
			if entries, rerr := os.ReadDir(p); rerr == nil && len(entries) > 0 {
				v.Refusals = append(v.Refusals, fmt.Sprintf(
					"%s/ already holds this workspace's own content from before the sharing "+
						"boundary existed — adopting it would mark it \"willing to give out\" without "+
						"anyone saying so. Rename your %s, then restart", norm, norm))
			}
		}
	}



	if len(v.Refusals) > 0 {
		return v, fmt.Errorf("%w: %s", ErrWorkspaceContract, strings.Join(v.Refusals, "; "))
	}


	for _, position := range WorkspacePositions {
		for _, zone := range WorkspaceZones[position] {
			dir := filepath.Join(base, position, zone)
			if _, err := os.Stat(dir); err == nil {
				continue
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return v, fmt.Errorf("supervisor: create %s: %w", dir, err)
			}
			v.Built = append(v.Built, filepath.Join(wscontract.BatonDir, position, zone))
		}
	}



	for _, norm := range wscontract.RootNorms {
		dir := filepath.Join(root, norm)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return v, fmt.Errorf("supervisor: create %s: %w", dir, err)
		}
		v.Built = append(v.Built, norm)
	}
	mailDir := filepath.Join(base, RuntimeWritable.Position, RuntimeWritable.Zone)
	for _, box := range MailBoxes {
		dir := filepath.Join(mailDir, box)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return v, fmt.Errorf("supervisor: create %s: %w", dir, err)
		}
		v.Built = append(v.Built, filepath.Join(wscontract.BatonDir, RuntimeWritable.Position, RuntimeWritable.Zone, box))
	}

	if v.Version == "written" || v.Version == "stale" {
		if err := writeContract(base); err != nil {
			return v, err
		}
	}

	log.Info("workspace contract",
		"version", v.Version, "built", len(v.Built), "zones_compared", v.Compared)
	return v, nil
}


func runtimeWritablePath(root string) string {
	return filepath.Join(root, wscontract.BatonDir, RuntimeWritable.Position, RuntimeWritable.Zone, RuntimeWritable.Path)
}




func positionOf(zone string) string {
	for _, position := range WorkspacePositions {
		if contains(WorkspaceZones[position], zone) {
			return position
		}
	}
	return ""
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}


func readContractVersion(base string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(base, "contract.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var doc struct {
		Contract string `json:"contract"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Contract) == "" {
		return "", errors.New("no contract field")
	}
	return doc.Contract, nil
}



func compareContract(a, b string) (int, error) {
	an, av, ok := strings.Cut(a, "/")
	bn, bv, ok2 := strings.Cut(b, "/")
	if !ok || !ok2 || an != bn {
		return 0, fmt.Errorf("%q and %q are not the same contract", a, b)
	}
	ai, err := strconv.Atoi(av)
	if err != nil {
		return 0, err
	}
	bi, err := strconv.Atoi(bv)
	if err != nil {
		return 0, err
	}
	switch {
	case ai > bi:
		return 1, nil
	case ai < bi:
		return -1, nil
	}
	return 0, nil
}







func writeContract(base string) error {
	doc := map[string]any{
		"contract": WorkspaceContract,
		"root": map[string]any{
			"public": map[string]any{
				"holds": "what this agent is willing to give out — not in here = not taken",















				"read_by": []string{"baton-resource publish"},
				"readers": "one reader exists: `baton-resource publish` takes what you hand to the company from here, and refuses any path outside it. Putting something here is consent, not delivery — nothing leaves until you run that verb.",
			},
		},
		"portable": map[string]any{
			"self":    map[string]any{"holds": "agent identity, network membership, held attestations"},
			"library": map[string]any{"holds": "references and provenance for adopted resources — kept here because they cannot be fetched again"},
			"ext":     map[string]any{"holds": "provider namespaces that travel — parcels, reports, and what a provider built here; a provider's machine-bound state goes in local/ext/<provider>/"},
		},
		"local": map[string]any{
			"mail": map[string]any{
				"why":         "identity-addressed; mail is never carried in a snapshot or move — a restored node is a different identity",
				"writable_by": map[string]any{RuntimeWritable.Path: "runtime"},





				"delivered_from": nil,
				"delivery_reads": "the node's data directory, not this workspace — nothing is delivered from here today; mail is sent with `baton-inbox send`, and its directory comes from `baton-inbox contract`",
				"rebuilt_from":   nil,
			},
			"net": map[string]any{
				"why":         "a projection; it goes stale",
				"rebuilt_from": "master",






				"projects": map[string]any{
					"members.json":   "who is on this network",
					"resources.json": "what the network shares — the catalogue, not the bodies",
				},
				"absent_means": "a projection named here that is not on disk at all means this daemon never ran. A projection that could not be REBUILT says so in band — see resources.json state.",
			},
			"cache": map[string]any{"why": "re-fetchable by hash", "rebuilt_from": "content-address"},
			"ext":   map[string]any{"why": "machine-bound provider state — device bindings and provider caches", "rebuilt_from": "provider"},
		},
	}

	delete(doc["local"].(map[string]any)["mail"].(map[string]any), "rebuilt_from")

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "contract.json"), append(raw, '\n'), 0o600)
}
