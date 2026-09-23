// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)














type accessGrantIssueRequest struct {
	SubjectNetworkID string `json:"subject_network_id"`
	SubjectKey       string `json:"subject_key"`
	Scope            string `json:"scope"`
	ExpiryAt         string `json:"expiry_at"`
}

type accessGrantView struct {
	GrantID   string  `json:"grant_id"`
	Issuer    string  `json:"issuer_network_id"`
	Subject   string  `json:"subject_network_id"`
	Scope     string  `json:"scope"`
	IssuedAt  string  `json:"issued_at"`
	ExpiryAt  string  `json:"expiry_at"`
	RevokedAt *string `json:"revoked_at"`



	Payload   string `json:"payload,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

func (a *API) handleAccessGrantIssue(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req accessGrantIssueRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SubjectNetworkID) == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"a grant needs subject_network_id", "Name the network you are endorsing.", nil)
		return
	}



	if _, ok := ed25519FromB64DER(req.SubjectKey); !ok {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"subject_key is not a single-line base64(DER) ed25519 public key",
			"Paste B's network public key — the base64 body of its PEM, one line.", nil)
		return
	}
	if _, err := parseScope(req.Scope); err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"scope is not canonical: "+err.Error(),
			"Use verb:type/pattern entries, comma-separated, ascending and unique.", nil)
		return
	}
	expiry, err := time.Parse(time.RFC3339, req.ExpiryAt)
	if err != nil || !time.Now().Before(expiry) {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"expiry_at must be an RFC3339 time in the future",
			"A grant that is already expired endorses nothing.", nil)
		return
	}

	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	grantID := "grant-" + uuid.NewString()
	issued := time.Now().UTC()
	canonical := accessGrantCanonical(grantID, net.NetworkID, req.SubjectNetworkID,
		req.SubjectKey, req.Scope, issued.Format(time.RFC3339), expiry.UTC().Format(time.RFC3339))
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(canonical))
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	if aPub, ok := ed25519FromPEM(net.PublicKeyPEM); !ok || !ed25519.Verify(aPub, []byte(canonical), sig) {
		a.failInternal(w, r, errors.New("just-signed access-grant does not verify against this network's own key"))
		return
	}







	if _, err := a.Store.Grants().Issue(ctx, spi.Grant{
		TenantID: spi.DefaultTenant, GrantID: grantID,
		Grantor: net.NetworkID, Grantee: req.SubjectNetworkID,
		Action: spi.ActionResourceFetch, Scope: req.Scope,
		Effect: spi.GrantAllow, Constraints: "subject_key:" + req.SubjectKey,
		ValidFrom: issued, ValidUntil: expiry.UTC(),
		Proof: sig, CreatedAt: issued,
	}); err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "access-grant.issued", Actor: p.Subject, ActorType: "user",
		ActingFor: actingForFrom(r),
		Action:    req.Scope, Target: req.SubjectNetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, accessGrantView{
		GrantID:   grantID,
		Issuer:    net.NetworkID,
		Subject:   req.SubjectNetworkID,
		Scope:     req.Scope,
		IssuedAt:  issued.Format(time.RFC3339),
		ExpiryAt:  expiry.UTC().Format(time.RFC3339),
		Payload:   base64.StdEncoding.EncodeToString([]byte(canonical)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		PublicKey: net.PublicKeyPEM,
	})
}

func (a *API) handleAccessGrantList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	grants, err := a.Store.Grants().List(ctx, spi.DefaultTenant, "", spi.ActionResourceFetch)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]accessGrantView, 0, len(grants))
	for _, g := range grants {
		var revoked *string
		if g.RevokedAt != nil {
			s := g.RevokedAt.UTC().Format(time.RFC3339)
			revoked = &s
		}
		out = append(out, accessGrantView{
			GrantID:   g.GrantID,
			Issuer:    net.NetworkID,
			Subject:   g.Grantee,
			Scope:     g.Scope,
			IssuedAt:  g.ValidFrom.UTC().Format(time.RFC3339),
			ExpiryAt:  g.ValidUntil.UTC().Format(time.RFC3339),
			RevokedAt: revoked,
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"grants": out})
}

func (a *API) handleAccessGrantRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	grantID := r.PathValue("grant_id")
	ctx := r.Context()


	if err := a.Store.Grants().Revoke(ctx, spi.DefaultTenant, grantID, p.Subject); err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "access-grant.revoked", Actor: p.Subject, ActorType: "user",
		ActingFor: actingForFrom(r),
		Action:    "revoke", Target: grantID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}


	a.writeJSON(w, http.StatusOK, map[string]any{
		"grant_id": grantID, "revoked": true,
		"note": "no new tokens will be minted; tokens already issued expire within their short TTL",
	})
}
