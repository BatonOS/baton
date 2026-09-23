// SPDX-License-Identifier: Apache-2.0

package httpapi















import (
	"context"
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

type deliveryGrantIssueRequest struct {
	SubjectNetworkID string `json:"subject_network_id"`
	SubjectKey       string `json:"subject_key"`
	Scope            string `json:"scope"`
	ExpiryAt         string `json:"expiry_at"`
























	Endpoint string `json:"endpoint,omitempty"`


	CA string `json:"ca,omitempty"`

	Domain string `json:"domain,omitempty"`
}

type deliveryGrantView struct {
	GrantID   string `json:"grant_id"`
	Subject   string `json:"subject_network_id"`
	Scope     string `json:"scope"`
	IssuedAt  string `json:"issued_at"`
	ExpiryAt  string `json:"expiry_at"`
	Payload   string `json:"payload,omitempty"`
	Signature string `json:"signature,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`



	Endpoint string `json:"endpoint,omitempty"`
	Domain   string `json:"domain,omitempty"`
















	ReplyRoute string `json:"reply_route"`
}








func replyRouteState(c map[string]string) string {
	ep, ca := c["endpoint"] != "", c["ca"] != ""
	switch {
	case ep && ca:
		return "recorded"
	case ep || ca:
		return "incomplete"
	default:
		return "absent"
	}
}

func (a *API) handleDeliveryGrantIssue(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req deliveryGrantIssueRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SubjectNetworkID) == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"a delivery grant needs subject_network_id", "Name the network you are endorsing.", nil)
		return
	}
	if _, ok := ed25519FromB64DER(req.SubjectKey); !ok {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"subject_key is not a single-line base64(DER) ed25519 public key",
			"Paste the sending network's public key — the base64 body of its PEM, one line.", nil)
		return
	}
	if _, err := parseDeliveryScope(req.Scope); err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"scope is not canonical: "+err.Error(),
			"Comma-separated recipient names, ascending and unique, or `*`.", nil)
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

	grantID := "dgrant-" + uuid.NewString()
	issued := time.Now().UTC()
	canonical := deliveryGrantCanonical(grantID, net.NetworkID, req.SubjectNetworkID,
		req.SubjectKey, req.Scope, issued.Format(time.RFC3339), expiry.UTC().Format(time.RFC3339))
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(canonical))
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if bPub, ok := ed25519FromPEM(net.PublicKeyPEM); !ok || !ed25519.Verify(bPub, []byte(canonical), sig) {
		a.failInternal(w, r, errors.New("just-signed delivery-grant does not verify against this network's own key"))
		return
	}





	constraints := "subject_key:" + req.SubjectKey
	if req.Endpoint != "" {
		constraints += "\nendpoint:" + req.Endpoint
	}
	if req.Domain != "" {
		constraints += "\ndomain:" + req.Domain
	}
	if req.CA != "" {
		constraints += "\nca:" + base64.StdEncoding.EncodeToString([]byte(req.CA))
	}
	if _, err := a.Store.Grants().Issue(ctx, spi.Grant{
		TenantID: spi.DefaultTenant, GrantID: grantID,
		Grantor: net.NetworkID, Grantee: req.SubjectNetworkID,
		Action: spi.ActionMessageDeliver, Scope: req.Scope,
		Effect: spi.GrantAllow, Constraints: constraints,
		ValidFrom: issued, ValidUntil: expiry.UTC(),
		Proof: sig, CreatedAt: issued,
	}); err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "delivery-grant.issued", Actor: p.Subject, ActorType: "operator",
		Action: req.Scope, Target: req.SubjectNetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: issued,
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, deliveryGrantView{
		GrantID:  grantID,
		Subject:  req.SubjectNetworkID,
		Scope:    req.Scope,
		IssuedAt: issued.Format(time.RFC3339),
		ExpiryAt: expiry.UTC().Format(time.RFC3339),
		Endpoint: req.Endpoint,
		Domain:   req.Domain,





		ReplyRoute: replyRouteState(grantConstraints(constraints)),
		Payload:   base64.StdEncoding.EncodeToString([]byte(canonical)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		PublicKey: net.PublicKeyPEM,
	})
}

















func (a *API) handleDeliveryGrantList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ctx := r.Context()
	grants, err := a.Store.Grants().List(ctx, spi.DefaultTenant, "", spi.ActionMessageDeliver)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]deliveryGrantView, 0, len(grants))
	for _, g := range grants {
		c := grantConstraints(g.Constraints)
		v := deliveryGrantView{
			GrantID:  g.GrantID,
			Subject:  g.Grantee,
			Scope:    g.Scope,
			IssuedAt: g.ValidFrom.UTC().Format(time.RFC3339),
			ExpiryAt: g.ValidUntil.UTC().Format(time.RFC3339),




			Endpoint:   c["endpoint"],
			Domain:     c["domain"],
			ReplyRoute: replyRouteState(c),
		}
		if g.RevokedAt != nil {
			v.RevokedAt = g.RevokedAt.UTC().Format(time.RFC3339)
		}





		out = append(out, v)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"grants": out})
}



















func (a *API) handleDeliveryGrantRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	grantID := r.PathValue("grant_id")
	ctx := r.Context()




	g, err := a.Store.Grants().Get(ctx, spi.DefaultTenant, grantID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if g.Action != spi.ActionMessageDeliver {
		a.fail(w, r, http.StatusNotFound, CodeNotFound,
			"no delivery grant with that id",
			"This face revokes delivery endorsements; other grant families have their own.", nil)
		return
	}
	if err := a.Store.Grants().Revoke(ctx, spi.DefaultTenant, grantID, p.Subject); err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "delivery-grant.revoked", Actor: p.Subject, ActorType: "operator",
		ActingFor: actingForFrom(r),
		Action:    "revoke", Target: g.Grantee, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"grant_id": grantID, "subject_network_id": g.Grantee, "revoked": true,
		"note": "no new delivery tokens will be minted for this grant; " +
			"a token already minted stays valid until it expires (300s)",
	})
}











func (a *API) replyRoute(ctx context.Context, peerNetwork string, now time.Time) (endpoint, caPEM string) {
	grants, err := a.Store.Grants().List(ctx, spi.DefaultTenant, peerNetwork, spi.ActionMessageDeliver)
	if err != nil {
		return "", ""
	}
	for _, g := range grants {
		if g.RevokedAt != nil {
			continue
		}
		if !g.ValidUntil.IsZero() && !now.Before(g.ValidUntil) {
			continue
		}
		c := grantConstraints(g.Constraints)
		ep := c["endpoint"]
		ca := ""
		if raw, e := base64.StdEncoding.DecodeString(c["ca"]); e == nil {
			ca = string(raw)
		}
		if ep != "" && ca != "" {
			return ep, ca
		}
	}
	return "", ""
}








func grantConstraints(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && k != "" {
			out[k] = v
		}
	}
	return out
}












func (a *API) peerName(ctx context.Context, peerNetwork string, now time.Time) string {
	grants, err := a.Store.Grants().List(ctx, spi.DefaultTenant, peerNetwork, spi.ActionMessageDeliver)
	if err != nil {
		return ""
	}
	for _, g := range grants {
		if g.RevokedAt != nil {
			continue
		}
		if !g.ValidUntil.IsZero() && !now.Before(g.ValidUntil) {
			continue
		}
		if d := grantConstraints(g.Constraints)["domain"]; d != "" {
			return d
		}
	}
	return ""
}
