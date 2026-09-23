// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)













type receivedGrantImport struct {

	Grant struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
		PublicKey string `json:"public_key"`
	} `json:"grant"`



	Connection struct {
		Endpoint string `json:"endpoint"`




		Resolver string `json:"resolver"`



		Domain string `json:"domain"`
		CA       string `json:"ca"`
	} `json:"connection"`
}

type receivedGrantView struct {
	GrantID       string `json:"grant_id"`
	Issuer        string `json:"issuer_network_id"`
	Subject       string `json:"subject_network_id"`
	Scope         string `json:"scope"`
	IssuedAt      string `json:"issued_at"`
	ExpiryAt      string `json:"expiry_at"`
	Endpoint      string `json:"endpoint"`











	Resolver      string `json:"resolver"`
	Domain        string `json:"domain"`
	CAFingerprint string `json:"ca_fingerprint"`
	ImportedAt    string `json:"imported_at"`


	IssuerKey string `json:"issuer_key,omitempty"`
	CA        string `json:"ca,omitempty"`
	Payload   string `json:"payload,omitempty"`
	Signature string `json:"signature,omitempty"`
}

func parseRFC3339(str string) time.Time { t, _ := time.Parse(time.RFC3339, str); return t }

func fmtRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}












func caFingerprint(pemText string) string {
	der := rootCertDER(pemText)
	if der == nil {
		return ""
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return "SHA256:" + strings.Join(parts, ":")
}





func rootCertDER(pemText string) []byte {
	rest := []byte(pemText)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			continue
		}
		if bytes.Equal(cert.RawSubject, cert.RawIssuer) && cert.CheckSignatureFrom(cert) == nil {
			return blk.Bytes
		}
	}
}

func (a *API) handleReceivedGrantImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var req receivedGrantImport
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	self, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	canon, err := base64.StdEncoding.DecodeString(req.Grant.Payload)
	sig, err2 := base64.StdEncoding.DecodeString(req.Grant.Signature)
	if err != nil || err2 != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the grant payload and signature must be base64", "Import the grant the issuer handed you, unmodified.", nil)
		return
	}




	issuerPub, ok := ed25519FromPEM(req.Grant.PublicKey)
	if !ok {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the issuer public_key is not a valid ed25519 PKIX PEM", "Import the grant unmodified.", nil)
		return
	}
	if !ed25519.Verify(issuerPub, canon, sig) {
		a.fail(w, r, http.StatusConflict, "GRANT_SIGNATURE_INVALID",
			"the grant is not signed by the key it carries",
			"The bytes and signature disagree — the grant is corrupt or altered.", nil)
		return
	}














	fields := strings.Split(string(canon), "\n")
	if len(fields) != 8 || !importableGrantPurpose(fields[0]) {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the grant is not an 8-line access-grant or delivery-grant",
			"Import a grant from `access grant issue` or `delivery grant issue`.", nil)
		return
	}
	grantID, issuer, subject, scope, issued, expiry := fields[1], fields[2], fields[3], fields[5], fields[6], fields[7]





	switch req.Connection.Resolver {
	case "", "dns", "cloud-registry":
	default:
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"resolver must be dns or cloud-registry",
			"It records which resolver the peer declared for itself; it is not a list to try in order. "+
				"Each network declares one resolver; recovery re-asks it and never tries another.", nil)
		return
	}
	if fields[0] == purposeDeliveryGrant && req.Connection.Resolver == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"a delivery grant must record which resolver the peer declared",
			"Send connection.resolver = dns or cloud-registry. Without it the send path would have to "+
				"guess a channel on the destination's behalf, and guessing means trying — which is the "+
				"downgrade that is forbidden, because an attacker who breaks one resolver could then choose which one is used.", nil)
		return
	}
	if subject != self.NetworkID {
		a.fail(w, r, http.StatusConflict, "NOT_THE_SUBJECT",
			"this grant is for a different network",
			"It names "+subject+" as subject; this network is "+self.NetworkID+".", nil)
		return
	}

	rg := &spi.ReceivedGrant{
		TenantID: spi.DefaultTenant, GrantID: grantID,
		Issuer: issuer, IssuerKey: req.Grant.PublicKey, Subject: subject, Scope: scope,
		IssuedAt: parseRFC3339(issued), ExpiryAt: parseRFC3339(expiry),
		Endpoint: req.Connection.Endpoint, PeerResolver: req.Connection.Resolver,
		PeerDomain: req.Connection.Domain,
		CAPEM: req.Connection.CA, CAFingerprint: caFingerprint(req.Connection.CA),
		Payload: req.Grant.Payload, Signature: req.Grant.Signature,
	}
	if err := a.Store.ReceivedGrants().Import(ctx, rg); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, receivedGrantViewFull(rg))
}

func (a *API) handleReceivedGrantList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	grants, err := a.Store.ReceivedGrants().List(r.Context(), spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]receivedGrantView, 0, len(grants))
	for i := range grants {
		out = append(out, receivedGrantViewSummary(&grants[i]))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"received_grants": out})
}

func (a *API) handleReceivedGrantGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	g, err := a.Store.ReceivedGrants().Get(r.Context(), spi.DefaultTenant, r.PathValue("grant_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, receivedGrantViewFull(g))
}

func (a *API) handleReceivedGrantDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	if err := a.Store.ReceivedGrants().Delete(r.Context(), spi.DefaultTenant, r.PathValue("grant_id")); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"grant_id": r.PathValue("grant_id"), "removed": true})
}

func receivedGrantViewSummary(g *spi.ReceivedGrant) receivedGrantView {
	return receivedGrantView{
		GrantID: g.GrantID, Issuer: g.Issuer, Subject: g.Subject, Scope: g.Scope,
		IssuedAt: fmtRFC3339(g.IssuedAt), ExpiryAt: fmtRFC3339(g.ExpiryAt),
		Endpoint: g.Endpoint, Resolver: g.PeerResolver, Domain: g.PeerDomain,
		CAFingerprint: g.CAFingerprint, ImportedAt: fmtRFC3339(g.ImportedAt),
	}
}

func receivedGrantViewFull(g *spi.ReceivedGrant) receivedGrantView {
	v := receivedGrantViewSummary(g)
	v.IssuerKey, v.CA, v.Payload, v.Signature = g.IssuerKey, g.CAPEM, g.Payload, g.Signature
	return v
}









func importableGrantPurpose(p string) bool {
	return p == purposeAccessGrant || p == purposeDeliveryGrant
}







func receivedGrantPurpose(g spi.ReceivedGrant) string {
	canon, err := base64.StdEncoding.DecodeString(g.Payload)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(canon), "\n")
	if !importableGrantPurpose(first) {
		return ""
	}
	return first
}






















func (a *API) deliveryGrantFor(ctx context.Context, network, recipient string, now time.Time) (*spi.ReceivedGrant, error) {
	grants, err := a.Store.ReceivedGrants().List(ctx, spi.DefaultTenant)
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		if g.Issuer != network || receivedGrantPurpose(g) != purposeDeliveryGrant {
			continue
		}
		if !g.ExpiryAt.IsZero() && !now.Before(g.ExpiryAt) {
			continue
		}
		if recipient != "" {
			scope, err := parseDeliveryScope(g.Scope)
			if err != nil || !deliveryScopeAllows(scope, recipient) {
				continue
			}
		}
		held := g
		return &held, nil
	}
	return nil, spi.ErrNotFound
}










func (a *API) peerByDomain(ctx context.Context, domain, recipient string, now time.Time) (*spi.ReceivedGrant, error) {
	grants, err := a.Store.ReceivedGrants().List(ctx, spi.DefaultTenant)
	if err != nil {
		return nil, err
	}
	var known []string
	for _, g := range grants {
		if receivedGrantPurpose(g) != purposeDeliveryGrant {
			continue
		}
		if g.PeerDomain != "" {
			known = append(known, g.PeerDomain)
		}
		if !strings.EqualFold(g.PeerDomain, domain) {
			continue
		}
		if !g.ExpiryAt.IsZero() && !now.Before(g.ExpiryAt) {
			continue
		}
		if recipient != "" {
			scope, perr := parseDeliveryScope(g.Scope)
			if perr != nil || !deliveryScopeAllows(scope, recipient) {
				continue
			}
		}
		held := g
		return &held, nil
	}
	_ = known
	return nil, spi.ErrNotFound
}
