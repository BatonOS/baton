// SPDX-License-Identifier: Apache-2.0

package httpapi
































import (
	"crypto/ed25519"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/delivery"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const deliveryTokenTTL = 10 * time.Minute

const (
	hdrDeliveryToken     = "X-Baton-Delivery-Token"
	hdrDeliverySignature = "X-Baton-Delivery-Signature"
)

type deliveryTokenExchangeRequest struct {
	Grant   signedAttestation `json:"grant"`
	Request signedAttestation `json:"request"`
}

type deliveryTokenView struct {
	TokenID   string `json:"token_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	IssuedAt  string `json:"issued_at"`
	ExpiryAt  string `json:"expiry_at"`
	Scope     string `json:"scope"`
}



func (a *API) handleDeliveryTokenExchange(w http.ResponseWriter, r *http.Request) {
	var body deliveryTokenExchangeRequest
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
			Event: "delivery-token.refused", Actor: subject, ActorType: "network",
			Action: code, Target: net.NetworkID, Result: "denied",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		})
		a.fail(w, r, status, code, msg, hint, nil)
	}

	grantCanon, grantSig, ok := decodeAttestation(body.Grant)
	if !ok {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"grant payload and signature must be base64",
			"Pass the grant this network issued through unmodified.", "")
		return
	}
	reqCanon, reqSig, ok := decodeAttestation(body.Request)
	if !ok {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"request payload and signature must be base64",
			"Pass the request the sending network signed through unmodified.", "")
		return
	}

	reqFields := strings.Split(string(reqCanon), "\n")
	if len(reqFields) != 6 || reqFields[0] != purposeDeliveryTokenRequest {
		refuse(http.StatusBadRequest, "REQUEST_SIGNATURE_INVALID",
			"the request is not a 6-line delivery-token-request",
			"Rebuild it as six newline-separated lines: delivery-token-request, subject, grant_id, "+
				"scope, timestamp, nonce.", "")
		return
	}
	reqSubject, reqGrantID, reqScope, reqTS, reqNonce :=
		reqFields[1], reqFields[2], reqFields[3], reqFields[4], reqFields[5]


	bPub, ok := ed25519FromPEM(net.PublicKeyPEM)
	if !ok {
		a.failInternal(w, r, errors.New("this network's own public key is not a valid ed25519 PKIX PEM"))
		return
	}
	if !ed25519.Verify(bPub, grantCanon, grantSig) {
		refuse(http.StatusUnauthorized, "GRANT_SIGNATURE_INVALID",
			"this delivery grant is not signed by this network",
			"Only grants this network signed itself mint delivery tokens.", reqSubject)
		return
	}


	grantFields := strings.Split(string(grantCanon), "\n")
	if len(grantFields) != 8 || grantFields[0] != purposeDeliveryGrant {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant is not an 8-line delivery-grant",
			"An access-grant is not a delivery-grant: the purpose line differs on purpose.", reqSubject)
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


	aPub, ok := ed25519FromB64DER(grantSubjectKey)
	if !ok {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant's pinned subject key is not a valid ed25519 SPKI",
			"line4 must be single-line base64(DER SubjectPublicKeyInfo).", reqSubject)
		return
	}
	if !ed25519.Verify(aPub, reqCanon, reqSig) {
		refuse(http.StatusUnauthorized, "REQUEST_SIGNATURE_INVALID",
			"the request is not signed by the key this grant pins",
			"The sending network must sign with the key this network endorsed into the grant.", reqSubject)
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



	if err := a.Store.AccessNonces().Consume(ctx, spi.DefaultTenant, purposeDeliveryTokenRequest, reqSubject, reqNonce); err != nil {
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
			"this delivery grant has expired", "Ask this network's operator for a fresh grant.", reqSubject)
		return
	}
	if g, e := a.Store.Grants().Get(ctx, spi.DefaultTenant, grantID); e == nil && g.RevokedAt != nil {
		refuse(http.StatusForbidden, "GRANT_REVOKED",
			"this delivery grant has been revoked", "A revoked grant mints no further tokens.", reqSubject)
		return
	} else if e != nil && e != spi.ErrNotFound {
		a.failStore(w, r, e)
		return
	}


	reqEntries, e1 := parseDeliveryScope(reqScope)
	grantEntries, e2 := parseDeliveryScope(grantScope)
	if e1 != nil || e2 != nil || !deliveryScopeCovers(grantEntries, reqEntries) {
		refuse(http.StatusForbidden, "SCOPE_EXCEEDS_GRANT",
			"the requested recipients are not within the grant",
			"request.scope must name only recipients the grant covers.", reqSubject)
		return
	}


	tokenID := "dtoken-" + uuid.NewString()
	issued := a.now().UTC()
	expiry := issued.Add(deliveryTokenTTL)
	tokenCanon := deliveryTokenCanonical(tokenID, net.NetworkID, reqSubject, grantID, reqScope,
		issued.Format(time.RFC3339), expiry.Format(time.RFC3339))
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(tokenCanon))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "delivery-token.issued", Actor: reqSubject, ActorType: "network",
		Action: reqScope, Target: grantID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, deliveryTokenView{
		TokenID:   tokenID,
		Payload:   base64.StdEncoding.EncodeToString([]byte(tokenCanon)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		IssuedAt:  issued.Format(time.RFC3339),
		ExpiryAt:  expiry.Format(time.RFC3339),
		Scope:     reqScope,
	})
}






type deliveryAttachmentWire struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

type deliveryEnvelopeWire struct {
	MessageID          string                   `json:"message_id"`
	Sender             string                   `json:"sender"`
	Recipient          string                   `json:"recipient"`
	SourceNetwork      string                   `json:"source_network"`
	DestinationNetwork string                   `json:"destination_network"`
	CreatedAt          string                   `json:"created_at"`
	ExpiresAt          string                   `json:"expires_at,omitempty"`
	Type               string                   `json:"type,omitempty"`
	ContentType        string                   `json:"content_type,omitempty"`
	PayloadSize        int                      `json:"payload_size"`
	ThreadID           string                   `json:"thread_id,omitempty"`
	ReplyTo            string                   `json:"reply_to,omitempty"`
	Attachments        []deliveryAttachmentWire `json:"attachments,omitempty"`
}

type deliveryReceiveRequest struct {


	Grant     signedAttestation    `json:"grant"`
	Envelope  deliveryEnvelopeWire `json:"envelope"`
	Payload   string               `json:"payload"`
	Signature string               `json:"signature"`













	AttachmentBytes map[string]string `json:"attachment_bytes,omitempty"`
}

type deliveryReceiptView struct {
	MessageID string `json:"message_id"`
	Accepted  bool   `json:"accepted"`



	Duplicate bool `json:"duplicate"`
}



func (a *API) handleDeliveryReceive(w http.ResponseWriter, r *http.Request) {
	var body deliveryReceiveRequest






	if !a.decodeJSONWithin(w, r, &body, MaxDeliveryBodyBytes) {
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
			Event: "delivery.refused", Actor: subject, ActorType: "network",
			Action: code, Target: net.NetworkID, Result: "denied",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		})
		a.fail(w, r, status, code, msg, hint, nil)
	}


	tokB64, sigB64 := r.Header.Get(hdrDeliveryToken), r.Header.Get(hdrDeliverySignature)
	if tokB64 == "" || sigB64 == "" {
		refuse(http.StatusUnauthorized, "TOKEN_MISSING",
			"this delivery carries no delivery-token",
			"Present "+hdrDeliveryToken+" and "+hdrDeliverySignature+" from the token exchange.", "")
		return
	}
	tokCanon, terr := base64.StdEncoding.DecodeString(tokB64)
	tokSig, serr := base64.StdEncoding.DecodeString(sigB64)
	if terr != nil || serr != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the delivery-token headers are not base64", "Pass the token through unmodified.", "")
		return
	}
	bPub, ok := ed25519FromPEM(net.PublicKeyPEM)
	if !ok {
		a.failInternal(w, r, errors.New("this network's own public key is not a valid ed25519 PKIX PEM"))
		return
	}
	if !ed25519.Verify(bPub, tokCanon, tokSig) {
		refuse(http.StatusUnauthorized, "TOKEN_SIGNATURE_INVALID",
			"this token is not signed by this network",
			"Only tokens this network minted are honoured — nothing external is trusted.", "")
		return
	}


	tokFields := strings.Split(string(tokCanon), "\n")
	if len(tokFields) != 8 || tokFields[0] != purposeDeliveryToken {
		refuse(http.StatusBadRequest, "TOKEN_PAYLOAD_MISMATCH",
			"the token is not an 8-line delivery-token",
			"An access-token is not a delivery-token.", "")
		return
	}
	tokIssuer, tokSubject, tokGrantID, tokScope, tokExpiry :=
		tokFields[2], tokFields[3], tokFields[4], tokFields[5], tokFields[7]
	if tokIssuer != net.NetworkID {
		refuse(http.StatusUnauthorized, "TOKEN_PAYLOAD_MISMATCH",
			"the token was not issued by this network", "Exchange a grant here first.", tokSubject)
		return
	}
	if exp, e := time.Parse(time.RFC3339, tokExpiry); e != nil || !a.now().Before(exp) {
		refuse(http.StatusUnauthorized, "TOKEN_EXPIRED",
			"this delivery token has expired", "Exchange the grant again for a fresh token.", tokSubject)
		return
	}


	grantCanon, grantSig, ok := decodeAttestation(body.Grant)
	if !ok || !ed25519.Verify(bPub, grantCanon, grantSig) {
		refuse(http.StatusUnauthorized, "GRANT_SIGNATURE_INVALID",
			"the grant accompanying this delivery is not signed by this network",
			"Send the same grant the token was minted from.", tokSubject)
		return
	}
	grantFields := strings.Split(string(grantCanon), "\n")
	if len(grantFields) != 8 || grantFields[0] != purposeDeliveryGrant ||
		grantFields[1] != tokGrantID || grantFields[3] != tokSubject {
		refuse(http.StatusConflict, "GRANT_MISMATCH",
			"the accompanying grant is not the one this token was minted from",
			"grant.grant_id and grant.subject must match the token.", tokSubject)
		return
	}
	aPub, ok := ed25519FromB64DER(grantFields[4])
	if !ok {
		refuse(http.StatusConflict, "GRANT_PAYLOAD_MISMATCH",
			"the grant's pinned subject key is not a valid ed25519 SPKI",
			"line4 must be single-line base64(DER SubjectPublicKeyInfo).", tokSubject)
		return
	}






	tokEntries, serr2 := parseDeliveryScope(tokScope)
	if serr2 != nil {
		a.failInternal(w, r, serr2)
		return
	}
	if !deliveryScopeAllows(tokEntries, body.Envelope.Recipient) {
		refuse(http.StatusForbidden, "NOT_ADMITTED",
			"this token does not cover that recipient",
			"The grant names which agents this network accepts mail for.", tokSubject)
		return
	}


	payload, perr := base64.StdEncoding.DecodeString(body.Payload)
	if perr != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"payload must be base64", "Send the body base64-encoded.", tokSubject)
		return
	}
	msgSig, merr := base64.StdEncoding.DecodeString(body.Signature)
	if merr != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"signature must be base64", "Send the detached signature base64-encoded.", tokSubject)
		return
	}
	env, cerr := body.Envelope.toDelivery()
	if cerr != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the envelope is not well formed: "+cerr.Error(),
			"Timestamps are RFC3339.", tokSubject)
		return
	}
	d := delivery.Delivery{Envelope: env, Payload: payload, Signature: msgSig}
	if !delivery.Verify(aPub, d) {
		refuse(http.StatusUnauthorized, "SIGNATURE_INVALID",
			"the delivery is not signed by the key this grant pins",
			"Sign canonical(envelope)‖payload_sha256 with the network key endorsed in the grant.", tokSubject)
		return
	}


	msg, lerr := delivery.Land(d, delivery.Local{
		TenantID:  spi.DefaultTenant,
		NetworkID: net.NetworkID,


		VerifiedNetwork: tokSubject,



		SourceAddress: formatAgentAddress(d.Envelope.Sender, a.peerName(r.Context(), tokSubject, a.now().UTC())),
		Now:           a.now().UTC(),
		RetentionCap:  MaxMessageTTL,
	})
	if lerr != nil {
		code, status := landingRefusal(lerr)
		refuse(status, code, lerr.Error(),
			"What crosses a boundary is not taken at face value: every envelope field that crosses is "+
				"kept, verified, recomputed or stripped on landing.", tokSubject)
		return
	}


	ident, e := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, msg.DestinationAgent)
	if e != nil {
		if errors.Is(e, spi.ErrNotFound) {
			refuse(http.StatusNotFound, "RECIPIENT_UNKNOWN",
				"no agent by that name in this network",
				"The network is right and the agent is not — those are two different refusals.", tokSubject)
			return
		}
		a.failStore(w, r, e)
		return
	}








	var files map[string][]byte
	if len(d.Envelope.Attachments) > 0 {
		if a.Blobs == nil {



			refuse(http.StatusServiceUnavailable, CodeBlobsUnavailable,
				"this network cannot store attachment bytes",
				"A blob directory is missing beside the database here. Nothing is wrong with the delivery.", tokSubject)
			return
		}
		var code, why string
		files, code, why = attachmentBytesFor(d.Envelope.Attachments, body.AttachmentBytes)
		if code != "" {
			refuse(http.StatusUnprocessableEntity, code, why,
				"The manifest is inside the signature and the bytes are checked against it. "+
					"Either every file crosses or none does: a manifest whose bytes cannot be obtained "+
					"must never look like one whose bytes can.", tokSubject)
			return
		}
	}


	if e := a.Store.Messages().Enqueue(ctx, &msg); e != nil {
		if errors.Is(e, spi.ErrConflict) {



			a.writeJSON(w, http.StatusOK, deliveryReceiptView{
				MessageID: msg.MessageID, Accepted: true, Duplicate: true,
			})
			return
		}
		a.failStore(w, r, e)
		return
	}










	for digest, raw := range files {
		if _, _, e := a.Blobs.Put(bytes.NewReader(raw), MaxCrossNetworkAttachmentTotal); e != nil {
			a.failInternal(w, r, fmt.Errorf("store attachment %s: %w", digest, e))
			return
		}
	}
	if len(d.Envelope.Attachments) > 0 {
		if e := a.Store.Attachments().Attach(ctx, msg.MessageID, d.Envelope.Attachments); e != nil {
			a.failInternal(w, r, fmt.Errorf("attach manifest: %w", e))
			return
		}
	}













	_ = a.tryDeliver(ctx, ident, &msg)

	if _, e := a.Log.Append(ctx, audit.Record{
		Event: "delivery.accepted", Actor: tokSubject, ActorType: "network",
		Action: msg.DestinationAgent, Target: msg.MessageID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); e != nil {
		a.failInternal(w, r, e)
		return
	}
	a.writeJSON(w, http.StatusCreated, deliveryReceiptView{MessageID: msg.MessageID, Accepted: true})
}






func landingRefusal(err error) (code string, status int) {
	switch {
	case errors.Is(err, delivery.ErrSourceNetworkMismatch):
		return "SOURCE_NETWORK_MISMATCH", http.StatusConflict
	case errors.Is(err, delivery.ErrNotForThisNetwork):
		return "NOT_FOR_THIS_NETWORK", http.StatusConflict
	case errors.Is(err, delivery.ErrNoRecipient), errors.Is(err, delivery.ErrNoMessageID):
		return CodeInvalidRequest, http.StatusBadRequest
	default:



		return "INTERNAL", http.StatusInternalServerError
	}
}

func (e deliveryEnvelopeWire) toDelivery() (delivery.Envelope, error) {
	at, err := time.Parse(time.RFC3339, e.CreatedAt)
	if err != nil {
		return delivery.Envelope{}, errors.New("created_at is not RFC3339")
	}
	var exp time.Time
	if e.ExpiresAt != "" {
		exp, err = time.Parse(time.RFC3339, e.ExpiresAt)
		if err != nil {
			return delivery.Envelope{}, errors.New("expires_at is not RFC3339")
		}
	}
	atts := make([]spi.Attachment, 0, len(e.Attachments))
	for _, w := range e.Attachments {
		atts = append(atts, spi.Attachment{
			Index: w.Index, Name: w.Name, ContentType: w.ContentType,
			Size: w.Size, SHA256: w.SHA256,
		})
	}
	return delivery.Envelope{
		MessageID: e.MessageID, Sender: e.Sender, Recipient: e.Recipient,
		SourceNetwork: e.SourceNetwork, DestinationNetwork: e.DestinationNetwork,
		CreatedAt: at, ExpiresAt: exp,
		Type: e.Type, ContentType: e.ContentType, PayloadSize: e.PayloadSize,
		ThreadID: e.ThreadID, ReplyTo: e.ReplyTo, Attachments: atts,
	}, nil
}














const MaxCrossNetworkAttachmentTotal = 2 << 20




const MaxDeliveryBodyBytes = 8 << 20














func attachmentBytesFor(manifest []spi.Attachment, encoded map[string]string) (map[string][]byte, string, string) {
	out := make(map[string][]byte, len(manifest))
	var total int64
	for _, at := range manifest {
		if _, done := out[at.SHA256]; done {
			continue
		}
		enc, ok := encoded[at.SHA256]
		if !ok {
			return nil, "ATTACHMENT_BYTES_MISSING",
				"the manifest names " + at.Name + " but its bytes did not arrive"
		}
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, "ATTACHMENT_BYTES_MALFORMED",
				"the bytes for " + at.Name + " are not base64"
		}



		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != at.SHA256 {
			return nil, "ATTACHMENT_DIGEST_MISMATCH",
				"the bytes sent for " + at.Name + " are not the bytes its manifest names"
		}


		if int64(len(raw)) != at.Size {
			return nil, "ATTACHMENT_SIZE_MISMATCH",
				"the manifest says " + at.Name + " is a different size than what arrived"
		}
		total += int64(len(raw))
		if total > MaxCrossNetworkAttachmentTotal {
			return nil, "ATTACHMENTS_TOO_LARGE",
				"this delivery's attachments exceed what crosses a boundary here"
		}
		out[at.SHA256] = raw
	}
	return out, "", ""
}
