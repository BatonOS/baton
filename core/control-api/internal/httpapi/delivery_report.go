// SPDX-License-Identifier: Apache-2.0

package httpapi






































import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/control-api/internal/delivery"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type deliveryReportRequest struct {

	Report signedAttestation `json:"report"`


	Original struct {
		Envelope string `json:"envelope"`




		PayloadSHA256 string `json:"payload_sha256"`
		Signature     string `json:"signature"`
	} `json:"original"`
}


func (a *API) handleDeliveryReportReceive(w http.ResponseWriter, r *http.Request) {
	var body deliveryReportRequest
	if !a.decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	self, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	refuse := func(status int, code, msg, hint string) {
		_, _ = a.Log.Append(ctx, audit.Record{
			Event: "delivery-report.refused", ActorType: "network",
			Action: code, Target: self.NetworkID, Result: "denied",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		})
		a.fail(w, r, status, code, msg, hint, nil)
	}






	origEnv, e1 := base64.StdEncoding.DecodeString(body.Original.Envelope)
	origSig, e2 := base64.StdEncoding.DecodeString(body.Original.Signature)
	if e1 != nil || e2 != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the original envelope and signature must be base64",
			"Echo back the bytes exactly as they were delivered.")
		return
	}
	selfPub, ok := ed25519FromPEM(self.PublicKeyPEM)
	if !ok {
		a.failInternal(w, r, errors.New("this network's own public key is not a valid ed25519 PKIX PEM"))
		return
	}
	var wire deliveryEnvelopeWire
	if err := json.Unmarshal(origEnv, &wire); err != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the original is not an envelope", "Echo back what was delivered.")
		return
	}
	env, cerr := wire.toDelivery()
	if cerr != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the original envelope is not well formed: "+cerr.Error(), "Echo back what was delivered.")
		return
	}



	sum := sha256.Sum256(origEnv)
	originalSHA := hex.EncodeToString(sum[:])


	repCanon, repSig, ok := decodeAttestation(body.Report)
	if !ok {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the report payload and signature must be base64", "Send them unmodified.")
		return
	}
	f := strings.Split(string(repCanon), "\n")
	if len(f) != 8 || f[0] != purposeDeliveryReport {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the report is not an 8-line delivery-report",
			"Rebuild it as eight newline-separated lines: delivery-report, reporter, sender, "+
				"message_id, original_sha256, outcome, detail, timestamp.")
		return
	}
	reporter, sender, messageID, sha, outcome := f[1], f[2], f[3], f[4], f[5]


	if sender != self.NetworkID {
		refuse(http.StatusConflict, "NOT_OUR_DELIVERY",
			"this report names a different sending network",
			"A report is delivered to the network that sent the message it is about.")
		return
	}
	if reporter != wire.DestinationNetwork {
		refuse(http.StatusConflict, "NOT_THE_DESTINATION",
			"the reporting network is not the one this delivery was addressed to",
			"Only the destination can report on a delivery — otherwise anyone who saw it "+
				"in transit could tell its sender it failed.")
		return
	}
	if messageID != wire.MessageID || sha != originalSHA {
		refuse(http.StatusConflict, "REPORT_MISMATCH",
			"the report does not describe the original it carries",
			"message_id and original_sha256 must match the echoed envelope.")
		return
	}


	held, herr := a.deliveryGrantFor(ctx, reporter, "", a.now())
	if herr != nil {
		refuse(http.StatusForbidden, "NO_DELIVERY_GRANT",
			"this network holds no live delivery grant from "+reporter,
			"A report is only honoured from a network we have a relationship with.")
		return
	}
	repPub, ok := ed25519FromPEM(held.IssuerKey)
	if !ok {
		a.failInternal(w, r, errors.New("the held grant's issuer key is not a valid ed25519 PKIX PEM"))
		return
	}
	if !ed25519.Verify(repPub, repCanon, repSig) {
		refuse(http.StatusUnauthorized, "REPORT_SIGNATURE_INVALID",
			"the report is not signed by the network it claims to come from",
			"It must be signed with the key that network's grant carries.")
		return
	}






	canon, cerr2 := delivery.CanonicalWithDigest(env, body.Original.PayloadSHA256)
	if cerr2 != nil {
		refuse(http.StatusBadRequest, CodeInvalidRequest,
			"the original envelope cannot be canonicalised: "+cerr2.Error(),
			"Echo back what was delivered.")
		return
	}
	if !ed25519.Verify(selfPub, canon, origSig) {
		refuse(http.StatusForbidden, "NOT_OUR_DELIVERY",
			"this network did not sign the delivery this report is about",
			"A report is admitted only about a delivery we actually sent — which is proved "+
				"by our own signature, not by a record we kept.")
		return
	}

	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "delivery-report.received", Actor: reporter, ActorType: "network",
		Action: outcome, Target: messageID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: a.now(),
	})
	a.writeJSON(w, http.StatusAccepted, map[string]any{
		"message_id": messageID,
		"outcome":    outcome,
		"recorded":   true,
	})
}









type terminalLanding struct {
	Outcome string
	Detail  string
}

func (t *terminalLanding) Error() string { return t.Outcome + ": " + t.Detail }



































func (a *API) reportTerminal(ctx context.Context, senderNetwork, messageID, envelopeB64, payloadSHA, originalSig string, fail *terminalLanding) bool {




	endpoint, caPEM := a.replyRoute(ctx, senderNetwork, a.now())
	if endpoint == "" || caPEM == "" {
		a.Logger.Info("cannot report a terminal landing failure: the endorsement recorded no way back",
			"sender_network", senderNetwork, "message_id", messageID, "outcome", fail.Outcome,
			"fix", "re-issue that network's delivery grant with endpoint and ca")
		return false
	}
	self, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.Logger.Warn("report: own identity", "error", err)
		return false
	}
	envBytes, derr := base64.StdEncoding.DecodeString(envelopeB64)
	if derr != nil {
		a.Logger.Warn("report: the spooled envelope is not base64", "message_id", messageID)
		return false
	}
	sum := sha256.Sum256(envBytes)
	canon := deliveryReportCanonical(self.NetworkID, senderNetwork, messageID,
		hex.EncodeToString(sum[:]), fail.Outcome, fail.Detail,
		a.now().UTC().Format(time.RFC3339))
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(canon))
	if err != nil {
		a.Logger.Warn("report: sign", "error", err)
		return false
	}
	body, _ := json.Marshal(deliveryReportRequestOut{
		Report: signedAttestation{
			Payload:   base64.StdEncoding.EncodeToString([]byte(canon)),
			Signature: base64.StdEncoding.EncodeToString(sig),
		},
		Original: originalOut{
			Envelope: envelopeB64, PayloadSHA256: payloadSHA,



			Signature: originalSig,
		},
	})
	client, cerr := peerClient(caPEM)
	if cerr != nil {
		a.Logger.Info("cannot report: the recorded CA is not usable", "sender_network", senderNetwork)
		return false
	}
	code, perr := postToPeer(ctx, client, endpoint+BasePath+"/networks/self/delivery-reports", nil, body, nil)
	switch {
	case perr != nil:
		a.Logger.Info("could not deliver a failure report", "sender_network", senderNetwork,
			"message_id", messageID, "outcome", fail.Outcome, "error", perr)
	case code != "":
		a.Logger.Info("the sender refused a failure report", "sender_network", senderNetwork,
			"message_id", messageID, "outcome", fail.Outcome, "sender_code", code)
	default:
		a.Logger.Info("reported a terminal landing failure", "sender_network", senderNetwork,
			"message_id", messageID, "outcome", fail.Outcome)
		return true
	}
	return false
}




type deliveryReportRequestOut struct {
	Report   signedAttestation `json:"report"`
	Original originalOut       `json:"original"`
}

type originalOut struct {
	Envelope      string `json:"envelope"`
	PayloadSHA256 string `json:"payload_sha256"`
	Signature     string `json:"signature"`
}
