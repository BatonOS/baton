// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)













const accessTokenTTL = 10 * time.Minute




const accessRequestWindow = 5 * time.Minute

type signedAttestation struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type accessTokenExchangeRequest struct {
	Grant   signedAttestation `json:"grant"`
	Request signedAttestation `json:"request"`
}

type accessTokenView struct {
	TokenID   string `json:"token_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	IssuedAt  string `json:"issued_at"`
	ExpiryAt  string `json:"expiry_at"`
	Scope     string `json:"scope"`
}


func ed25519FromPEM(pemText string) (ed25519.PublicKey, bool) {
	blk, _ := pem.Decode([]byte(pemText))
	if blk == nil {
		return nil, false
	}
	pub, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, false
	}
	ed, ok := pub.(ed25519.PublicKey)
	return ed, ok
}




func ed25519FromB64DER(b64 string) (ed25519.PublicKey, bool) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, false
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, false
	}
	ed, ok := pub.(ed25519.PublicKey)
	return ed, ok
}

func decodeAttestation(a signedAttestation) (canonical, sig []byte, ok bool) {
	p, err := base64.StdEncoding.DecodeString(a.Payload)
	s, err2 := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil || err2 != nil {
		return nil, nil, false
	}
	return p, s, true
}




func (a *API) handleAccessTokenExchange(w http.ResponseWriter, r *http.Request) {
	var body accessTokenExchangeRequest
	if !a.decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	refuse := func(status int, code, msg, hint, subject string) {
		_, _ = a.Log.Append(ctx, audit.Record{
			Event: "access-token.refused", Actor: subject, ActorType: "network",
			Action: code, Target: net.NetworkID, Result: "denied",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		})
		a.fail(w, r, status, code, msg, hint, nil)
	}

	grantCanon, grantSig, ok := decodeAttestation(body.Grant)
	if !ok {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"grant payload and signature must be base64",
			"Pass the grant A issued through unmodified.", "")
		return
	}
	reqCanon, reqSig, ok := decodeAttestation(body.Request)
	if !ok {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"request payload and signature must be base64",
			"Pass the request B signed through unmodified.", "")
		return
	}




	reqFields := strings.Split(string(reqCanon), "\n")
	if len(reqFields) != 6 || reqFields[0] != purposeAccessTokenRequest {
		refuse(http.StatusBadRequest, "REQUEST_SIGNATURE_INVALID",
			"the request is not a 6-line access-token-request",
			"Rebuild it from the contract §二 canonical.", "")
		return
	}
	reqSubject, reqGrantID, reqScope, reqTS, reqNonce :=
		reqFields[1], reqFields[2], reqFields[3], reqFields[4], reqFields[5]


	aPub, ok := ed25519FromPEM(net.PublicKeyPEM)
	if !ok {
		a.failInternal(w, r, errors.New("this network's own public key is not a valid ed25519 PKIX PEM"))
		return
	}
	if !ed25519.Verify(aPub, grantCanon, grantSig) {
		refuse(http.StatusUnauthorized, "GRANT_SIGNATURE_INVALID",
			"this grant is not signed by this network",
			"A only mints tokens from grants it signed itself.", reqSubject)
		return
	}


	grantFields := strings.Split(string(grantCanon), "\n")
	if len(grantFields) != 8 || grantFields[0] != purposeAccessGrant {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant is not an 8-line access-grant",
			"Rebuild it from the contract §二 canonical.", reqSubject)
		return
	}
	grantID, grantIssuer, grantSubject, grantSubjectKey, grantScope, _, grantExpiry :=
		grantFields[1], grantFields[2], grantFields[3], grantFields[4], grantFields[5], grantFields[6], grantFields[7]
	if grantIssuer != net.NetworkID || grantSubject != reqSubject {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant's issuer or subject does not match",
			"The grant must be issued by this network and name the requesting network as subject.", reqSubject)
		return
	}





	bPub, ok := ed25519FromB64DER(grantSubjectKey)
	if !ok {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant's pinned subject key is not a valid ed25519 SPKI",
			"line4 must be single-line base64(DER SubjectPublicKeyInfo).", reqSubject)
		return
	}
	if !ed25519.Verify(bPub, reqCanon, reqSig) {
		refuse(http.StatusUnauthorized, "REQUEST_SIGNATURE_INVALID",
			"the request is not signed by the key this grant pins for B",
			"B must sign with the network key A endorsed into the grant.", reqSubject)
		return
	}
	ts, perr := time.Parse(time.RFC3339, reqTS)
	if perr != nil {
		refuse(http.StatusBadRequest, "REQUEST_SIGNATURE_INVALID",
			"the request timestamp is not RFC3339", "Send an RFC3339 UTC timestamp.", reqSubject)
		return
	}
	if skew := a.now().Sub(ts); skew > accessRequestWindow || skew < -accessRequestWindow {
		refuse(http.StatusUnprocessableEntity, CodeClockSkew,
			"the request timestamp is outside the signing window",
			"Re-sign the request with a current timestamp.", reqSubject)
		return
	}


	if err := a.Store.AccessNonces().Consume(ctx, spi.DefaultTenant, purposeAccessTokenRequest, reqSubject, reqNonce); err != nil {
		if err == spi.ErrReplayed {
			refuse(http.StatusConflict, "REPLAYED",
				"this request nonce has already been used",
				"Each token request signs a fresh nonce.", reqSubject)
			return
		}
		a.failStore(w, r, err)
		return
	}


	if reqGrantID != grantID {
		refuse(http.StatusConflict, "GRANT_MISMATCH",
			"the request asks for a different grant than the one presented",
			"request.grant_id must equal grant.grant_id.", reqSubject)
		return
	}


	if exp, e := time.Parse(time.RFC3339, grantExpiry); e != nil || !a.now().Before(exp) {
		refuse(http.StatusForbidden, "GRANT_EXPIRED",
			"this grant has expired", "Ask A's operator for a fresh grant.", reqSubject)
		return
	}







	if g, e := a.Store.Grants().Get(ctx, spi.DefaultTenant, grantID); e == nil && g.RevokedAt != nil {
		refuse(http.StatusForbidden, "GRANT_REVOKED",
			"this grant has been revoked", "A revoked grant mints no further tokens.", reqSubject)
		return
	} else if e != nil && e != spi.ErrNotFound {
		a.failStore(w, r, e)
		return
	}


	reqScopeEntries, e1 := parseScope(reqScope)
	grantScopeEntries, e2 := parseScope(grantScope)
	if e1 != nil || e2 != nil || !scopeSubset(reqScopeEntries, grantScopeEntries) {
		refuse(http.StatusForbidden, "SCOPE_EXCEEDS_GRANT",
			"the requested scope is not within the grant",
			"request.scope must be a subset of grant.scope.", reqSubject)
		return
	}


	tokenID := "token-" + uuid.NewString()
	issued := time.Now().UTC()
	expiry := issued.Add(accessTokenTTL)
	tokenCanon := accessTokenCanonical(tokenID, net.NetworkID, reqSubject, grantID, reqScope,
		issued.Format(time.RFC3339), expiry.Format(time.RFC3339))
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(tokenCanon))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "access-token.issued", Actor: reqSubject, ActorType: "network",
		Action: reqScope, Target: grantID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, accessTokenView{
		TokenID:   tokenID,
		Payload:   base64.StdEncoding.EncodeToString([]byte(tokenCanon)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		IssuedAt:  issued.Format(time.RFC3339),
		ExpiryAt:  expiry.Format(time.RFC3339),
		Scope:     reqScope,
	})
}









const (
	hdrAccessToken     = "X-Baton-Access-Token"
	hdrAccessSignature = "X-Baton-Access-Signature"
)

func (a *API) handleAccessResourceGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	deny := func(status int, code, msg, hint, subject string) {
		_, _ = a.Log.Append(ctx, audit.Record{
			Event: "access-resource.denied", Actor: subject, ActorType: "network",
			Action: code, Target: r.PathValue("id"), Result: "denied",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		})
		a.fail(w, r, status, code, msg, hint, nil)
	}


	tokCanonB64, sigB64 := r.Header.Get(hdrAccessToken), r.Header.Get(hdrAccessSignature)
	if tokCanonB64 == "" || sigB64 == "" {
		deny(http.StatusUnauthorized, "TOKEN_MISSING",
			"this fetch carries no access-token",
			"Present "+hdrAccessToken+" and "+hdrAccessSignature+" from the token exchange.", "")
		return
	}
	tokCanon, err1 := base64.StdEncoding.DecodeString(tokCanonB64)
	tokSig, err2 := base64.StdEncoding.DecodeString(sigB64)
	if err1 != nil || err2 != nil {
		deny(http.StatusBadRequest, CodeInvalidRequest,
			"the access-token headers are not base64", "Pass the token through unmodified.", "")
		return
	}


	aPub, ok := ed25519FromPEM(net.PublicKeyPEM)
	if !ok {
		a.failInternal(w, r, errors.New("this network's own public key is not a valid ed25519 PKIX PEM"))
		return
	}
	if !ed25519.Verify(aPub, tokCanon, tokSig) {
		deny(http.StatusUnauthorized, "TOKEN_SIGNATURE_INVALID",
			"this token is not signed by this network",
			"Only tokens this network minted are honoured — nothing external is trusted.", "")
		return
	}


	fields := strings.Split(string(tokCanon), "\n")
	if len(fields) != 8 || fields[0] != purposeAccessToken || fields[2] != net.NetworkID {
		deny(http.StatusConflict, "TOKEN_PAYLOAD_MISMATCH",
			"the token is not an access-token issued by this network",
			"purpose must be access-token and issuer must be this network.", "")
		return
	}
	subject, scopeStr, expiryStr := fields[3], fields[5], fields[7]


	if exp, e := time.Parse(time.RFC3339, expiryStr); e != nil || !a.now().Before(exp) {
		deny(http.StatusUnauthorized, "TOKEN_EXPIRED",
			"this access-token has expired",
			"Exchange the grant for a fresh token — they are short-lived by design.", subject)
		return
	}


	scope, e := parseScope(scopeStr)
	if e != nil {
		deny(http.StatusConflict, "TOKEN_PAYLOAD_MISMATCH",
			"the token scope is not canonical", "The scope A signed is malformed.", subject)
		return
	}
	res, rerr := a.Store.Resources().Get(ctx, spi.DefaultTenant, r.PathValue("id"), r.URL.Query().Get("type"))
	if rerr != nil {



		a.failStore(w, r, rerr)
		return
	}
	if !scopeAllows(scope, "read", res.Type, res.Name) {
		deny(http.StatusForbidden, "SCOPE_DENIED",
			"this token's scope does not cover "+res.Type+"/"+res.Name,
			"The grant behind this token did not include this resource.", subject)
		return
	}


	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "access-resource.served", Actor: subject, ActorType: "network",
		Action: "read:" + res.Type + "/" + res.Name, Target: res.ResourceID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, true))
}
