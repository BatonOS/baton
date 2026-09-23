// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"net/http"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)














type inboxPolicyView struct {
	Identity string `json:"identity"`
	ActOn    string `json:"act_on"`




	AllowSenders  []string `json:"allow_senders"`
	AllowNetworks []string `json:"allow_networks"`






	Channels  string `json:"channels"`
	PolicySet bool   `json:"policy_set"`
}

func inboxPolicyOf(i *spi.Identity) inboxPolicyView {
	senders := i.InboxPolicy.AllowSenders
	if senders == nil {
		senders = []string{}
	}
	networks := i.InboxPolicy.AllowNetworks
	if networks == nil {
		networks = []string{}
	}
	return inboxPolicyView{
		Identity: i.Name, ActOn: string(i.InboxPolicy.ActOn),
		AllowSenders: senders, AllowNetworks: networks,
		Channels:  string(i.InboxPolicy.Channels),
		PolicySet: i.InboxPolicy.Set,
	}
}








func (a *API) handleInboxPolicyGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ident, err := a.Store.Identities().Resolve(r.Context(), spi.DefaultTenant, r.PathValue("identity"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, inboxPolicyOf(ident))
}

type inboxPolicyRequest struct {
	ActOn         string   `json:"act_on"`
	AllowSenders  []string `json:"allow_senders"`
	AllowNetworks []string `json:"allow_networks"`


	Channels string `json:"channels"`
}








func (a *API) handleInboxPolicySet(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	var req inboxPolicyRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	switch spi.InboxActOn(req.ActOn) {
	case spi.InboxActOnEveryone, spi.InboxActOnAllowlist, spi.InboxActOnNobody:
	default:




		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"act_on must be everyone, allowlist or nobody",
			"Pick one of the three. There is no fourth value, and an unset act_on is not one of them.", nil)
		return
	}
	channels := spi.InboxChannels(req.Channels)
	switch channels {
	case "":
		channels = spi.InboxChannelsAllow
	case spi.InboxChannelsAllow, spi.InboxChannelsHold:
	default:
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"channels must be allow or hold",
			"`hold` keeps mail a facility's service relayed in (envelope `via`) received and not delivered; `allow` acts on it like any other.", nil)
		return
	}
	name := r.PathValue("identity")
	ident, err := a.Store.Identities().SetInboxPolicy(r.Context(), spi.DefaultTenant, name, spi.InboxPolicy{
		ActOn:         spi.InboxActOn(req.ActOn),
		AllowSenders:  req.AllowSenders,
		AllowNetworks: req.AllowNetworks,
		Channels:      channels,
		Set:           true,
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "inbox.policy_set", Actor: p.Subject, ActorType: "user",
		Action: "set", Target: name, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r),



		Detail: map[string]any{
			"act_on":         req.ActOn,
			"channels":       string(channels),
			"allow_senders":  len(req.AllowSenders),
			"allow_networks": len(req.AllowNetworks),
		},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}





	if ident.NodeID != "" {
		go a.drainInbox(context.WithoutCancel(r.Context()), ident.NodeID)
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"policy": inboxPolicyOf(ident),









		"redelivery": "triggered",
	})
}
