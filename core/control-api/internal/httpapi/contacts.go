// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/grantproof"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


















type contactsMemberView struct {
	Identity string `json:"identity"`
	NodeID   string `json:"node_id,omitempty"`
}

type contactsHeldView struct {
	GrantID   string `json:"grant_id"`
	Scope     string `json:"scope"`
	Action    string `json:"action"`
	Grantor   string `json:"grantor"`
	Grantee   string `json:"grantee"`
	RecordB64 string `json:"record_b64"`
	ProofB64  string `json:"proof_b64"`
}

func (a *API) handleAgentContacts(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	net, err := a.Store.Networks().Get(ctx, node.TenantID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	ids, err := a.Store.Identities().List(ctx, node.TenantID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	members := make([]contactsMemberView, 0, len(ids))
	bound := []string{}
	for _, id := range ids {
		members = append(members, contactsMemberView{Identity: id.Name, NodeID: id.NodeID})
		if id.NodeID == node.NodeID {
			bound = append(bound, id.Name)
		}
	}

	now := time.Now()
	held := []contactsHeldView{}
	for _, name := range bound {
		grants, gerr := a.Store.Grants().List(ctx, node.TenantID, name, "")
		if gerr != nil {
			a.failStore(w, r, gerr)
			return
		}
		for _, g := range grants {




			if g.StatusAt(now) != spi.GrantActive {
				continue
			}
			if g.Action == spi.ActionResourceFetch {
				continue
			}
			held = append(held, contactsHeldView{
				GrantID: g.GrantID, Scope: g.Scope, Action: g.Action,
				Grantor: g.Grantor, Grantee: g.Grantee,
				RecordB64: base64.StdEncoding.EncodeToString(grantproof.Canonical(g)),
				ProofB64:  base64.StdEncoding.EncodeToString(g.Proof),
			})
		}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"format": "baton-contacts/1",





		"authority_key_pem": net.PublicKeyPEM,
		"members":           members,
		"held":              held,
	})
}
