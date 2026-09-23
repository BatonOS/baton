// SPDX-License-Identifier: Apache-2.0

package httpapi









































import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)







const operatorCodeLifetime = 15 * time.Minute







const operatorEnrolsPerMinute = 5

var operatorEnrolLimit = &ipLimiter{counts: map[string]int{}, perMin: operatorEnrolsPerMinute}

type operatorInviteRequest struct {
	Name string `json:"name"`
	TTL  string `json:"ttl"`
}

type operatorInviteResponse struct {
	Name      string    `json:"name"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenID   string    `json:"token_id"`







	CAFingerprint string `json:"ca_fingerprint"`
}





func (a *API) handleOperatorInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	if a.Store.ReadOnly() {
		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror and cannot invite operators",
			"Invite from the primary control plane.", nil)
		return
	}

	var req operatorInviteRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if err := auth.ValidateOperatorName(req.Name); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeNameInvalid, err.Error(),
			"Pick a name for the person, not for the machine: it is what the audit log "+
				"will show for everything they do.", nil)
		return
	}

	ttl := operatorCodeLifetime
	if req.TTL != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
				"ttl "+req.TTL+" is not a duration",
				"Use a Go duration such as 15m or 1h.", nil)
			return
		}



		if parsed <= 0 || parsed > time.Hour {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
				"ttl must be between 1s and 1h, got "+req.TTL,
				"An operator invitation redeems into a super-admin certificate; "+
					"it is meant to be pasted into a terminal within minutes.", nil)
			return
		}
		ttl = parsed
	}

	plaintext, hash, err := auth.NewToken()
	if err != nil {
		a.failInternal(w, r, err)
		return
	}

	token := &spi.EnrollmentToken{
		TokenID:   "opinv_" + uuid.NewString(),
		TenantID:  spi.DefaultTenant,
		TokenHash: hash,
		Purpose:   spi.TokenPurposeOperator,




		Roles: spi.Roles{},




		NamePattern: "^" + req.Name + "$",
		MaxUses:     1,
		ExpiresAt:   time.Now().Add(ttl),
		CreatedBy:   p.Subject,
	}
	if err := a.Store.Tokens().Create(r.Context(), token); err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "operator.invited", Actor: p.Subject, ActorType: "user",
		Action: "create", Target: req.Name, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"token_id": token.TokenID, "expires_at": token.ExpiresAt},
	}); err != nil {
		a.Logger.Warn("operator invitation not audited", "error", err)
	}

	a.writeJSON(w, http.StatusCreated, operatorInviteResponse{
		Name:          req.Name,
		Code:          plaintext,
		ExpiresAt:     token.ExpiresAt,
		TokenID:       token.TokenID,
		CAFingerprint: caFingerprint(string(a.CA.Bundle())),
	})
}

type operatorEnrollRequest struct {
	Name     string    `json:"name"`
	Code     string    `json:"code"`
	CSRPEM   string    `json:"csr_pem"`
	ClientTS time.Time `json:"client_ts"`
}

type operatorEnrollResponse struct {
	Name           string    `json:"name"`
	CertificatePEM string    `json:"certificate_pem"`
	CABundlePEM    string    `json:"ca_bundle_pem"`
	CertNotAfter   time.Time `json:"cert_not_after"`
	Serial         string    `json:"serial"`
}









func (a *API) handleOperatorEnroll(w http.ResponseWriter, r *http.Request) {
	if a.Store.ReadOnly() {
		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror and cannot enrol operators",
			"Enrol against the primary control plane.", nil)
		return
	}

	ip := clientIP(r)
	if !operatorEnrolLimit.allow(ip, time.Now()) {
		a.fail(w, r, http.StatusTooManyRequests, CodeRateLimited,
			"too many enrolment attempts from this address",
			"Wait a minute and try again.", nil)
		return
	}

	var req operatorEnrollRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}




	if !req.ClientTS.IsZero() {
		if skew := time.Since(req.ClientTS); skew > MaxClockSkew || skew < -MaxClockSkew {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeClockSkew,
				"this machine's clock differs from the control plane by "+skew.Round(time.Second).String(),
				"Enable NTP (`timedatectl set-ntp true`) and retry. Your code has not been spent.", nil)
			return
		}
	}

	if err := auth.ValidateOperatorName(req.Name); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeNameInvalid, err.Error(),
			"Use the name the invitation was minted for. Your code has not been spent.", nil)
		return
	}
	if req.CSRPEM == "" {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"a certificate request is required",
			"`baton operator enrol` generates one. Your code has not been spent.", nil)
		return
	}

	ctx := r.Context()



	preview, err := a.Store.Tokens().GetByHash(ctx, auth.HashToken(req.Code))
	if err != nil {
		a.failOperatorCode(w, r, err)
		return
	}
	if ok, err := auth.MatchesNamePattern(preview.NamePattern, req.Name); err != nil {
		a.failInternal(w, r, err)
		return
	} else if !ok {



		a.fail(w, r, http.StatusUnauthorized, CodeNameInvalid,
			"this invitation was not issued for that name",
			"Enrol under the name the person who invited you used. Your code has not been spent.", nil)
		return
	}

	token, err := a.Store.Tokens().Consume(ctx, auth.HashToken(req.Code), spi.TokenPurposeOperator)
	if err != nil {
		a.failOperatorCode(w, r, err)
		return
	}

	certPEM, cert, err := a.CA.IssueFromCSR([]byte(req.CSRPEM), ca.Identity{
		TenantID: token.TenantID,
		Kind:     ca.KindUser,
		Name:     req.Name,
	}, ca.DefaultLeafLifetime)
	if err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"the certificate request was not usable: "+err.Error(),
			"Regenerate it. Your private key must never leave this machine.", nil)
		return
	}




	if err := a.Store.OperatorCertificates().Issue(ctx, &spi.OperatorCertificate{
		Serial:       ca.SerialString(cert),
		TenantID:     token.TenantID,
		OperatorName: req.Name,
		Fingerprint:  ca.Fingerprint(cert),
		SubjectCN:    cert.Subject.CommonName,
		SANURI:       cert.URIs[0].String(),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
		IssuedAt:     time.Now(),
		IssuedBy:     token.CreatedBy,
		FromIP:       ip,
	}); err != nil {
		a.failStore(w, r, err)
		return
	}

















	if _, err := a.Store.Identities().Ensure(ctx, token.TenantID, cert.Subject.CommonName); err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "operator.enrolled", Actor: req.Name, ActorType: "user",
		Action: "create", Target: req.Name, Result: "ok",
		SourceIP: ip, RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{
			"serial":      ca.SerialString(cert),
			"fingerprint": ca.Fingerprint(cert),
			"invited_by":  token.CreatedBy,
		},
	}); err != nil {
		a.Logger.Warn("operator enrolment not audited", "error", err)
	}

	a.writeJSON(w, http.StatusCreated, operatorEnrollResponse{
		Name:           req.Name,
		CertificatePEM: string(certPEM),
		CABundlePEM:    string(a.CA.Bundle()),
		CertNotAfter:   cert.NotAfter,
		Serial:         ca.SerialString(cert),
	})
}

type operatorCertView struct {
	Serial       string     `json:"serial"`
	Name         string     `json:"name"`
	Fingerprint  string     `json:"fingerprint_sha256"`
	IssuedAt     time.Time  `json:"issued_at"`
	NotAfter     time.Time  `json:"not_after"`
	IssuedBy     string     `json:"issued_by"`
	FromIP       string     `json:"from_ip"`
	RevokedAt    *time.Time `json:"revoked_at"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
}








func (a *API) handleOperatorList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	certs, err := a.Store.OperatorCertificates().List(r.Context(), spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]operatorCertView, 0, len(certs))
	for _, c := range certs {
		out = append(out, operatorCertView{
			Serial: c.Serial, Name: c.OperatorName, Fingerprint: c.Fingerprint,
			IssuedAt: c.IssuedAt, NotAfter: c.NotAfter, IssuedBy: c.IssuedBy,
			FromIP: c.FromIP, RevokedAt: c.RevokedAt, RevokeReason: c.RevokeReason,
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"operators": out})
}

type operatorRevokeRequest struct {
	Reason string `json:"reason"`
}

type operatorRevokeResponse struct {
	Name    string   `json:"name"`
	Revoked int      `json:"revoked"`
	Serials []string `json:"serials"`
}









func (a *API) handleOperatorRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	if a.Store.ReadOnly() {
		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror and cannot revoke",
			"Revoke on the primary control plane.", nil)
		return
	}

	name := r.PathValue("name")
	if err := auth.ValidateOperatorName(name); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeNameInvalid, err.Error(), "", nil)
		return
	}

	var req operatorRevokeRequest


	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) {
		return
	}
	reason := req.Reason
	if reason == "" {
		reason = "revoked by " + p.Subject
	}

	ctx := r.Context()
	certs, err := a.Store.OperatorCertificates().ListByName(ctx, name)
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if len(certs) == 0 {
		a.fail(w, r, http.StatusNotFound, CodeNotFound,
			"no operator named "+name+" has ever enrolled here",
			"`baton operator list` shows the names this control plane has issued to.", nil)
		return
	}

	serials := []string{}
	for _, c := range certs {
		if c.RevokedAt != nil {
			continue
		}
		if err := a.Store.OperatorCertificates().Revoke(ctx, c.Serial, reason); err != nil {
			a.failStore(w, r, err)
			return
		}




		a.CA.Revoke(c.Serial)
		serials = append(serials, c.Serial)
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "operator.revoked", Actor: p.Subject, ActorType: "user",
		Action: "revoke", Target: name, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"serials": serials, "reason": reason},
	}); err != nil {
		a.Logger.Warn("operator revocation not audited", "error", err)
	}

	a.writeJSON(w, http.StatusOK, operatorRevokeResponse{
		Name: name, Revoked: len(serials), Serials: serials,
	})
}
