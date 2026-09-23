// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

















type invitationView struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	Invitee     string `json:"invitee"`
	FromNetwork string `json:"from_network"`
	ToNetwork   string `json:"to_network"`


	ToNetworkKey         string `json:"to_network_key"`
	ToNetworkFingerprint string `json:"to_network_fingerprint"`

	Token     string `json:"token"`
	TokenHash string `json:"token_hash"`
	ExpiresAt string `json:"expires_at"`



	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	Nonce     string `json:"nonce"`
	Timestamp string `json:"timestamp"`
}


func (a *API) handleInvitationCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}












	if !a.selfIsMaster(r.Context()) {
		a.fail(w, r, http.StatusForbidden, "NOT_A_MASTER",
			"this control plane's node does not hold the master role",
			"Inviting adds a member to a network, and that is the master's act.", nil)
		return
	}

	var req struct {
		Invitee     string `json:"invitee"`
		FromNetwork string `json:"from_network"`
		TTL         string `json:"ttl"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Invitee == "" || req.FromNetwork == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"invitee and from_network are required",
			"An invitation names one agent and the network it is being taken from.", nil)
		return
	}

	ttl := time.Hour
	if req.TTL != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil || parsed <= 0 || parsed > 24*time.Hour {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
				"ttl must be between 1s and 24h", "Use a Go duration such as 1h.", nil)
			return
		}
		ttl = parsed
	}

	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	plaintext, hash, err := auth.NewToken()
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	expires := time.Now().Add(ttl)




	token := &spi.EnrollmentToken{
		Purpose:  spi.TokenPurposeNode,
		TokenID:     "tok_" + uuid.NewString(),
		TenantID:    spi.DefaultTenant,
		TokenHash:   hash,
		Roles:       spi.Roles{spi.RoleAgent},
		NamePattern: req.Invitee,
		MaxUses:     1,
		ExpiresAt:   expires,
		CreatedBy:   p.Subject,
		Invitee:     req.Invitee,
		FromNetwork: req.FromNetwork,
	}
	if err := a.Store.Tokens().Create(ctx, token); err != nil {
		a.failStore(w, r, err)
		return
	}

	expiresStr := expires.UTC().Format(time.RFC3339)
	nonce := uuid.NewString()
	ts := time.Now().UTC().Format(time.RFC3339)
	payload := inviteCanonical(purposeNetworkInvite, req.Invitee, req.FromNetwork,
		net.NetworkID, hash, expiresStr, ts, nonce)

	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(payload))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "network.invited", Actor: p.Subject, ActorType: "user",
		Action: "invite", Target: req.Invitee, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"from_network": req.FromNetwork, "token_id": token.TokenID},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, invitationView{
		APIVersion:           "baton.mailloop.dev/v1alpha1",
		Kind:                 "NetworkInvitation",
		Invitee:              req.Invitee,
		FromNetwork:          req.FromNetwork,
		ToNetwork:            net.NetworkID,
		ToNetworkKey:         net.PublicKeyPEM,
		ToNetworkFingerprint: net.Fingerprint,
		Token:                plaintext,
		TokenHash:            hash,
		ExpiresAt:            expiresStr,
		Payload:              base64.StdEncoding.EncodeToString([]byte(payload)),
		Signature:            base64.StdEncoding.EncodeToString(sig),
		Nonce:                nonce,
		Timestamp:            ts,
	})
}





func (a *API) handleTransferApprove(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var inv invitationView
	if !a.decodeJSON(w, r, &inv) {
		return
	}
	ctx := r.Context()





	payload, err := base64.StdEncoding.DecodeString(inv.Payload)
	sigBytes, err2 := base64.StdEncoding.DecodeString(inv.Signature)
	if err != nil || err2 != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"payload and signature must be base64", "Pass the invitation through unmodified.", nil)
		return
	}
	blk, _ := pem.Decode([]byte(inv.ToNetworkKey))
	if blk == nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the inviting network's key is not PEM", "Pass the invitation through unmodified.", nil)
		return
	}
	pub, err := x509.ParsePKIXPublicKey(blk.Bytes)
	edPub, isEd := pub.(ed25519.PublicKey)
	if err != nil || !isEd || !ed25519.Verify(edPub, payload, sigBytes) {
		a.fail(w, r, http.StatusUnauthorized, "INVITATION_NOT_SIGNED",
			"this invitation is not signed by the network it claims to come from",
			"A token proves somebody handed you a string; only the signature says who.", nil)
		return
	}







	want := inviteCanonical(purposeNetworkInvite, inv.Invitee, inv.FromNetwork,
		inv.ToNetwork, inv.TokenHash, inv.ExpiresAt, inv.Timestamp, inv.Nonce)
	if string(payload) != want {
		a.fail(w, r, http.StatusConflict, "INVITATION_TAMPERED",
			"the signed bytes do not match the fields beside them",
			"Some field was changed after signing — the invitee, most likely.", nil)
		return
	}


	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if inv.FromNetwork != net.NetworkID {
		a.fail(w, r, http.StatusConflict, "WRONG_NETWORK",
			"this invitation takes an agent from a different network",
			"It names "+inv.FromNetwork+"; this network is "+net.NetworkID+".", nil)
		return
	}
	if _, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, inv.Invitee); err != nil {
		a.fail(w, r, http.StatusNotFound, "NO_SUCH_AGENT",
			"no agent named "+inv.Invitee+" belongs to this network",
			"Approving a transfer of somebody who is not here would record a departure "+
				"that never happened.", nil)
		return
	}

	if exp, perr := time.Parse(time.RFC3339, inv.ExpiresAt); perr == nil && time.Now().After(exp) {



		a.fail(w, r, http.StatusGone, "INVITATION_EXPIRED",
			"this invitation expired at "+inv.ExpiresAt,
			"Ask the inviting network for a new one.", nil)
		return
	}



	if err := a.Store.Tokens().ReleaseMember(ctx, spi.DefaultTenant, inv.Invitee, inv.ToNetwork); err != nil {
		a.failStore(w, r, err)
		return
	}
















	revoked := 0
	if id, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, inv.Invitee); err == nil && id.NodeID != "" {
		certs, cerr := a.Store.Certs().ListByNode(ctx, id.NodeID)
		if cerr != nil {
			a.failStore(w, r, cerr)
			return
		}
		for _, c := range certs {
			if err := a.Store.Certs().Revoke(ctx, c.Serial, "released to "+inv.ToNetwork); err != nil {
				a.failStore(w, r, err)
				return
			}







			a.CA.Revoke(c.Serial)
			revoked++
		}



		if _, err := a.Store.Identities().Bind(ctx, spi.DefaultTenant, inv.Invitee, ""); err != nil {
			a.failStore(w, r, err)
			return
		}
	}




	nonce := uuid.NewString()
	ts := time.Now().UTC().Format(time.RFC3339)
	approvalPayload := inviteCanonical(purposeTransferApprove, inv.Invitee, inv.FromNetwork,
		inv.ToNetwork, inv.TokenHash, inv.ExpiresAt, ts, nonce)
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(approvalPayload))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "network.member_released", Actor: p.Subject, ActorType: "user",
		Action: "approve_transfer", Target: inv.Invitee, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"to_network": inv.ToNetwork, "certificates_revoked": revoked},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":              "baton.mailloop.dev/v1alpha1",
		"kind":                    "TransferApproval",
		"invitee":                 inv.Invitee,
		"from_network":            inv.FromNetwork,
		"from_network_key":        net.PublicKeyPEM,
		"from_network_fingerprint": net.Fingerprint,
		"to_network":              inv.ToNetwork,
		"token_hash":              inv.TokenHash,
		"expires_at":              inv.ExpiresAt,
		"payload":                 base64.StdEncoding.EncodeToString([]byte(approvalPayload)),
		"signature":               base64.StdEncoding.EncodeToString(sig),
		"nonce":                   nonce,
		"timestamp":               ts,
	})
}




































func (a *API) selfIsMaster(ctx context.Context) bool {
	self, err := a.Store.Nodes().GetByName(ctx, spi.DefaultTenant, a.Cfg.Metadata.Name)
	if err != nil || self == nil {



		return false
	}
	return self.Roles.Has(spi.RoleMaster)
}
