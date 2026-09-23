// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/store/sqlite"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)







var KnownScopes = map[string]bool{
	"skill.install":    true,
	"resource.install": true,
	"resource.update":  true,



	"resource.read": true,
}

type integrationView struct {
	IntegrationID string `json:"integration_id"`
	Provider      string `json:"provider"`



	PublicKey string `json:"public_key"`



	Fingerprint   string `json:"fingerprint"`
	Scopes        []string `json:"scopes"`
	TargetNetwork string `json:"target_network"`
	Enabled       bool     `json:"enabled"`
	CreatedAt     string   `json:"created_at"`
	ConfirmedAt   string   `json:"confirmed_at,omitempty"`
}

func toIntegrationView(in spi.Integration) integrationView {
	v := integrationView{
		IntegrationID: in.IntegrationID,
		Provider:      in.Provider,
		PublicKey:     in.PublicKeyPEM,
		Fingerprint:   in.Fingerprint,
		Scopes:        in.Scopes,
		TargetNetwork: in.TargetNetwork,
		Enabled:       in.Enabled,
		CreatedAt:     in.CreatedAt.UTC().Format(time.RFC3339),
	}
	if v.Scopes == nil {
		v.Scopes = []string{}
	}
	if !in.ConfirmedAt.IsZero() {
		v.ConfirmedAt = in.ConfirmedAt.UTC().Format(time.RFC3339)
	}
	return v
}

func (a *API) handleIntegrationList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	items, err := a.Store.Integrations().List(r.Context(), spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]integrationView, 0, len(items))
	for _, in := range items {
		out = append(out, toIntegrationView(in))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": "baton.mailloop.dev/v1alpha1",
		"kind":       "IntegrationList",
		"items":      out,



		"next_cursor": "",
	})
}

func (a *API) handleIntegrationCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req struct {
		Provider      string   `json:"provider"`
		PublicKey     string   `json:"public_key"`
		Scopes        []string `json:"scopes"`
		TargetNetwork string   `json:"target_network"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "the body is not JSON",
			"Send {provider, public_key, scopes, target_network}.", nil)
		return
	}
	if req.Provider == "" || strings.TrimSpace(req.PublicKey) == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"provider and public_key are required",
			"The public key is the half a provider published — never a private key.", nil)
		return
	}


	if strings.Contains(req.PublicKey, "PRIVATE KEY") {
		a.fail(w, r, http.StatusBadRequest, "PRIVATE_KEY_SUBMITTED",
			"that is a private key, and it must not be shared with anyone",
			"Paste the PUBLIC half. Treat the key you just pasted as compromised and have "+
				"the provider issue a new pair.", nil)
		return
	}
	for _, sc := range req.Scopes {
		if !KnownScopes[sc] {
			a.fail(w, r, http.StatusBadRequest, "UNKNOWN_SCOPE",
				"this build does not know the scope "+sc,
				"Refused rather than ignored: a scope nobody checks is a permission nobody granted.",
				map[string]any{"known": scopeNames()})
			return
		}
	}

	net, err := a.Store.Networks().Get(r.Context(), spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	if req.TargetNetwork != "" && req.TargetNetwork != net.NetworkID {
		a.fail(w, r, http.StatusConflict, "WRONG_TARGET_NETWORK",
			"this integration names a different network than the one it is being added to",
			"It looks like it was copied from another network's configuration. This network is "+
				net.NetworkID+".", nil)
		return
	}














	req.PublicKey = strings.TrimSpace(req.PublicKey)
	fp := sqlite.Fingerprint(req.PublicKey)




	if fp == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"public_key is not a PEM public key",
			"Paste the key including its BEGIN/END lines.", nil)
		return
	}
	if existing, err := a.Store.Integrations().ByFingerprint(r.Context(), spi.DefaultTenant, fp); err == nil && existing != nil {
		a.fail(w, r, http.StatusConflict, "INTEGRATION_EXISTS",
			"an integration with this key is already configured",
			"The same public key was added before. Reuse it, remove it (baton integrations remove), "+
				"or enable it — not add it twice.",
			map[string]any{"integration_id": existing.IntegrationID, "enabled": existing.Enabled})
		return
	}

	created, err := a.Store.Integrations().Create(r.Context(), spi.Integration{
		TenantID:      spi.DefaultTenant,
		Provider:      req.Provider,
		PublicKeyPEM:  req.PublicKey,
		Fingerprint:   fp,
		Scopes:        req.Scopes,
		TargetNetwork: net.NetworkID,
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "integration.created", Actor: p.Subject, ActorType: "user",
		Action: "create", Target: created.IntegrationID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, toIntegrationView(*created))
}

func (a *API) handleIntegrationSetEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if err := a.Store.Integrations().SetEnabled(r.Context(), spi.DefaultTenant, id, enabled); err != nil {
			a.failStore(w, r, err)
			return
		}
		event := "integration.disabled"
		if enabled {
			event = "integration.enabled"
		}
		if _, err := a.Log.Append(r.Context(), audit.Record{
			Event: event, Actor: p.Subject, ActorType: "user",
			Action: event, Target: id, Result: "ok",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		}); err != nil {
			a.failInternal(w, r, err)
			return
		}
		got, err := a.Store.Integrations().Get(r.Context(), spi.DefaultTenant, id)
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		a.writeJSON(w, http.StatusOK, toIntegrationView(*got))
	}
}



func (a *API) handleIntegrationDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := a.Store.Integrations().Delete(r.Context(), spi.DefaultTenant, id); err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "integration.revoked", Actor: p.Subject, ActorType: "user",
		Action: "revoke", Target: id, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func scopeNames() []string {
	out := make([]string, 0, len(KnownScopes))
	for k := range KnownScopes {
		out = append(out, k)
	}
	return out
}
