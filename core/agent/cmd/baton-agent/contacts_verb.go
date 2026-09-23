// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/batonos/baton/core/agent/internal/grantproof"
)













type contactsMembersDoc struct {
	Format          string `json:"format"`
	RebuiltAt       string `json:"rebuilt_at"`
	Source          string `json:"source"`
	AuthorityKeyPEM string `json:"authority_key_pem"`
	Members         []struct {
		Identity string `json:"identity"`
		NodeID   string `json:"node_id"`
	} `json:"members"`
}

type contactsHeldDoc struct {
	Format    string `json:"format"`
	Scope     string `json:"scope"`
	RecordB64 string `json:"record_b64"`
	ProofB64  string `json:"proof_b64"`
}

type contactGrantRef struct {
	GrantID      string `json:"grant_id"`
	Action       string `json:"action"`
	Scope        string `json:"scope"`
	Effect       string `json:"effect"`
	Grantor      string `json:"grantor"`
	Grantee      string `json:"grantee"`
	Verification string `json:"verification"`
}

func runInboxContacts(asJSON bool) int {
	root := os.Getenv("BATON_WORKSPACE")
	if root == "" {
		fmt.Fprintln(os.Stderr, "baton-inbox contacts: this runtime has no workspace (BATON_WORKSPACE is not set) — "+
			"the address book lives in the workspace, so there is nothing to read")
		return exitConfigError
	}

	var doc contactsMembersDoc
	membersPath := filepath.Join(root, ".baton", "local", "net", "members.json")
	raw, err := os.ReadFile(membersPath)
	switch {
	case os.IsNotExist(err):


		fmt.Fprintln(os.Stderr, "baton-inbox contacts: no member projection yet — the node has not synced contacts "+
			"(not in a network, or the daemon has not connected since this workspace was built)")
		return exitEmpty
	case err != nil:
		fmt.Fprintf(os.Stderr, "baton-inbox contacts: %v\n", err)
		return exitInternal
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "baton-inbox contacts: members.json is not readable as %s: %v\n", "baton-contacts-members/1", err)
		return exitInternal
	}

	var authority ed25519.PublicKey
	if block, _ := pem.Decode([]byte(doc.AuthorityKeyPEM)); block != nil {
		if pub, perr := x509.ParsePKIXPublicKey(block.Bytes); perr == nil {
			if ed, ok := pub.(ed25519.PublicKey); ok {
				authority = ed
			}
		}
	}

	held := readHeld(filepath.Join(root, ".baton", "portable", "self", "held"), authority)

	type entry struct {
		Identity  string            `json:"identity"`
		NodeID    string            `json:"node_id,omitempty"`
		GrantRefs []contactGrantRef `json:"grant_refs"`


		NoGrantReference bool `json:"no_grant_reference"`
	}
	entries := make([]entry, 0, len(doc.Members))
	for _, m := range doc.Members {
		refs := []contactGrantRef{}
		for _, h := range held {
			if h.Grantor == m.Identity || h.Grantee == m.Identity {
				refs = append(refs, h)
			}
		}
		entries = append(entries, entry{Identity: m.Identity, NodeID: m.NodeID,
			GrantRefs: refs, NoGrantReference: len(refs) == 0})
	}

	if asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"format":     "baton-contacts-view/1",
			"rebuilt_at": doc.RebuiltAt,
			"source":     doc.Source,
			"contacts":   entries,
			"held":       held,
		}, "", "  ")
		fmt.Println(string(out))
		return exitOK
	}

	fmt.Printf("contacts (projection rebuilt %s from %s)\n", doc.RebuiltAt, doc.Source)
	if len(entries) == 0 {
		fmt.Println("  no members in the projection")
	}
	for _, e := range entries {
		suffix := ""
		if e.NodeID != "" {
			suffix = "  @" + e.NodeID
		}
		fmt.Printf("  %s%s\n", e.Identity, suffix)
		if e.NoGrantReference {
			fmt.Println("      no_grant_reference — no grant names this identity; that is a state, not an error")
			continue
		}
		for _, g := range e.GrantRefs {
			fmt.Printf("      %s %s %s → %s  [%s]  verification: %s\n",
				g.Effect, g.Action, g.Grantor, g.Grantee, g.GrantID, g.Verification)
		}
	}
	return exitOK
}




func readHeld(dir string, authority ed25519.PublicKey) []contactGrantRef {
	out := []contactGrantRef{}
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, d := range dirents {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, d.Name()))
		if rerr != nil {
			continue
		}
		var f contactsHeldDoc
		if json.Unmarshal(raw, &f) != nil || f.Format != "baton-held-grant/1" {
			fmt.Fprintf(os.Stderr, "contacts: %s refused — not a baton-held-grant/1 file\n", d.Name())
			continue
		}
		canonical, cerr := base64.StdEncoding.DecodeString(f.RecordB64)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "contacts: %s refused — undecodable canonical\n", d.Name())
			continue
		}
		fields, perr := grantproof.Parse(canonical)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "contacts: %s refused — %v\n", d.Name(), perr)
			continue
		}



		if f.Scope != fields.Scope {
			fmt.Fprintf(os.Stderr, "contacts: %s refused — its scope field disagrees with the signed bytes\n", d.Name())
			continue
		}

		verification := "unknown"
		if authority != nil {
			proof, derr := base64.StdEncoding.DecodeString(f.ProofB64)
			if derr == nil && grantproof.Verify(authority, canonical, proof) {
				verification = "verified"
			} else {
				verification = "signature-invalid"
			}
		}
		out = append(out, contactGrantRef{
			GrantID: fields.GrantID, Action: fields.Action, Scope: fields.Scope,
			Effect: fields.Effect, Grantor: fields.Grantor, Grantee: fields.Grantee,
			Verification: verification,
		})
	}
	return out
}
