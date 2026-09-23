// SPDX-License-Identifier: Apache-2.0

package httpapi



























import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/delivery"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)




const peerTimeout = 20 * time.Second







func peerClient(caPEM string) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("the held grant carries no usable CA for this peer")
	}
	return &http.Client{
		Timeout: peerTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}, nil
}








func (a *API) deliverAcross(ctx context.Context, held *spi.ReceivedGrant, env delivery.Envelope, payload []byte) (peerCode string, err error) {
	client, err := peerClient(held.CAPEM)
	if err != nil {
		return "", err
	}


	reqCanon := deliveryTokenRequestCanonical(
		env.SourceNetwork, held.GrantID, env.Recipient,
		a.now().UTC().Format(time.RFC3339), uuid.NewString())
	reqSig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(reqCanon))
	if err != nil {
		return "", err
	}
	exchange, _ := json.Marshal(deliveryTokenExchangeRequest{
		Grant: signedAttestation{Payload: held.Payload, Signature: held.Signature},
		Request: signedAttestation{
			Payload:   base64.StdEncoding.EncodeToString([]byte(reqCanon)),
			Signature: base64.StdEncoding.EncodeToString(reqSig),
		},
	})
	var tok deliveryTokenView
	code, err := postToPeer(ctx, client, held.Endpoint+BasePath+"/networks/self/delivery-tokens", nil, exchange, &tok)
	if err != nil || code != "" {
		return code, err
	}


	msgSig, err := a.signDelivery(ctx, env, payload)
	if err != nil {
		return "", err
	}
	atts := make([]deliveryAttachmentWire, 0, len(env.Attachments))
	for _, at := range env.Attachments {
		atts = append(atts, deliveryAttachmentWire{at.Index, at.Name, at.ContentType, at.Size, at.SHA256})
	}





	files, err := a.attachmentBytes(env.Attachments)
	if err != nil {
		return "", err
	}
	exp := ""
	if !env.ExpiresAt.IsZero() {
		exp = env.ExpiresAt.UTC().Format(time.RFC3339)
	}
	body, _ := json.Marshal(deliveryReceiveRequest{
		Grant: signedAttestation{Payload: held.Payload, Signature: held.Signature},
		Envelope: deliveryEnvelopeWire{
			MessageID: env.MessageID, Sender: env.Sender, Recipient: env.Recipient,
			SourceNetwork: env.SourceNetwork, DestinationNetwork: env.DestinationNetwork,
			CreatedAt: env.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: exp,
			Type: env.Type, ContentType: env.ContentType, PayloadSize: env.PayloadSize,
			ThreadID: env.ThreadID, ReplyTo: env.ReplyTo, Attachments: atts,
		},
		Payload:         base64.StdEncoding.EncodeToString(payload),
		Signature:       base64.StdEncoding.EncodeToString(msgSig),
		AttachmentBytes: files,
	})
	var receipt deliveryReceiptView
	return postToPeer(ctx, client, held.Endpoint+BasePath+"/networks/self/deliveries",
		map[string]string{
			hdrDeliveryToken:     tok.Payload,
			hdrDeliverySignature: tok.Signature,
		}, body, &receipt)
}





func (a *API) signDelivery(ctx context.Context, env delivery.Envelope, payload []byte) ([]byte, error) {
	canon, err := delivery.Canonical(env, payload)
	if err != nil {
		return nil, err
	}
	return a.Store.Networks().Sign(ctx, spi.DefaultTenant, canon)
}





func postToPeer(ctx context.Context, c *http.Client, url string, hdr map[string]string, body []byte, out any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode >= 400 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Code != "" {
			return e.Code, nil
		}
		return "", fmt.Errorf("peer answered %d with no code: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return "", fmt.Errorf("peer's answer is not the shape this build reads: %w", err)
		}
	}
	return "", nil
}

















func (a *API) sendAcrossNetworks(w http.ResponseWriter, r *http.Request, req sendRequest) {
	ctx := r.Context()
	self, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	addr, aerr := parseAgentAddress(req.To)
	if aerr != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"that is not an address: "+aerr.Error(),
			"An agent elsewhere is <network>.<agent>@<domain>, for example dns.coder@batonnetwork.org. "+
				"A bare name addresses an agent on this network.", nil)
		return
	}





	held, err := a.peerByDomain(ctx, addr.Network, addr.Agent, a.now())
	if err != nil {
		if errors.Is(err, spi.ErrNotFound) {




			any, e2 := a.peerByDomain(ctx, addr.Network, "", a.now())
			if e2 == nil {
				a.fail(w, r, http.StatusForbidden, "NOT_ADMITTED",
					addr.Network+" has not endorsed this network to write to "+addr.Agent,
					"It covers: "+any.Scope+". Ask that network's operator to widen it.", nil)
				return
			}
			a.fail(w, r, http.StatusForbidden, "NO_DELIVERY_GRANT",
				"this network holds no live delivery grant from "+addr.Network,
				"Its operator issues one with `delivery grant issue`, and it is imported here. "+
					"An address is never resolved at send time — a network this one has no "+
					"relationship with is a refusal, not a lookup.", nil)
			return
		}
		a.failStore(w, r, err)
		return
	}
	if held.Issuer == self.NetworkID {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"that address names this network",
			"Use a bare agent name for a recipient here.", nil)
		return
	}








	ttl := DefaultMessageTTL
	if req.TTL != "" {
		d, terr := time.ParseDuration(req.TTL)
		if terr != nil {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"ttl is not a duration", "For example 24h.", nil)
			return
		}
		ttl = d
	}
	messageID := req.MessageID
	if messageID == "" {
		messageID = "msg_" + uuid.NewString()
	}








	atts, merr := a.resolveManifest(ctx, req.Attachments, held.Issuer)
	if merr != nil {
		merr.write(a, w, r)
		return
	}
	now := a.now().UTC()
	env := delivery.Envelope{
		MessageID: messageID,




		Sender:             operatorSender(r),
		Recipient:          addr.Agent,
		SourceNetwork:      self.NetworkID,
		DestinationNetwork: held.Issuer,
		CreatedAt:          now,
		ExpiresAt:          now.Add(ttl),
		Type:               req.Type,
		ContentType:        req.ContentType,
		PayloadSize:        len(req.Body),
		ReplyTo:            req.ReplyTo,
		Attachments:        atts,
	}






	var code string
	var derr error
	switch held.PeerResolver {
	case "dns":




		code, derr = a.deliverAcross(ctx, held, env, req.Body)
	case "cloud-registry":
		var answered bool
		answered, code, derr = a.deliverViaRegistry(ctx, held, env, req.Body, w, r)
		if answered {



			return
		}
	default:
		a.fail(w, r, http.StatusConflict, "PEER_RESOLVER_UNDECLARED",
			"the grant held for "+held.Issuer+" does not record which resolver that network declared",
			"Re-import it with connection.resolver = dns or cloud-registry. Guessing would pick a "+
				"channel on the destination's behalf, and guessing means trying — a downgrade: switching to a "+
				"different resolver after a failure is forbidden, because an attacker who breaks one resolver "+
				"could then choose which one is used.", nil)
		return
	}
	if derr != nil {












		hint := "Transient. The peer's control plane did not answer; retry."
		if held.PeerResolver == "dns" {
			hint = "Transient — but there is no mailbox behind this one. " +
				addr.Network + " publishes its own address (DNS), which means it is " +
				"responsible for its own reachability and nothing holds mail for it while it " +
				"is down. The message was not stored anywhere: retry when that network is back. " +
				"Only networks that declare a registry have an offline mailbox."
		}
		a.fail(w, r, http.StatusServiceUnavailable, "NETWORK_UNREACHABLE",
			"could not reach "+addr.Network+": "+derr.Error(), hint, nil)
		return
	}
	if code != "" {



		a.fail(w, r, http.StatusConflict, code,
			"the destination network refused this delivery",
			"The code is the peer's own; each code names exactly one fact and is classed terminal or "+
				"transient, and only a transient one is worth retrying.",
			map[string]any{"destination_network": held.Issuer, "peer_code": code})
		return
	}

	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "message.sent-across", Actor: env.Sender, ActorType: "operator",
		Action: req.To, Target: messageID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: now,
	})
	a.writeJSON(w, http.StatusAccepted, map[string]any{
		"message_id":          messageID,
		"to":                  addr.Agent,
		"destination_network": held.Issuer,





		"accepted_by_network": true,
	})
}















func operatorSender(r *http.Request) string {
	if p, ok := r.Context().Value(ctxPrincipal).(auth.Principal); ok && p.Subject != "" {
		return p.Subject
	}
	return "operator"
}



















func (a *API) deliverViaRegistry(ctx context.Context, held *spi.ReceivedGrant, env delivery.Envelope,
	payload []byte, w http.ResponseWriter, r *http.Request) (answered bool, peerCode string, err error) {

	res, err := a.resolveViaRegistry(ctx, env.DestinationNetwork)
	if err != nil {
		var refusal *registryRefusal
		if errors.As(err, &refusal) {




			a.fail(w, r, http.StatusConflict, refusal.Code,
				"the registry refused to route this delivery: "+refusal.Message,
				"The code is the registry's own; each code names exactly one fact and is classed terminal or transient.",
				map[string]any{
					"destination_network": env.DestinationNetwork,
					"registry_code":       refusal.Code,
					"registry_message":    refusal.Message,
				})
			return true, "", nil
		}
		return false, "", err
	}

	if res.Mode == "direct" {
		if len(res.Endpoints) == 0 {
			return false, "", errors.New("the registry said direct and named no endpoint")
		}
		target := res.Endpoints[0].url()
		if target == "" {
			return false, "", errors.New("the registry said direct and named an endpoint with no address")
		}


		routed := *held
		routed.Endpoint = strings.TrimRight(target, "/")
		c, e := a.deliverAcross(ctx, &routed, env, payload)
		return false, c, e
	}














	if len(env.Attachments) > 0 {
		a.fail(w, r, http.StatusUnprocessableEntity, "ATTACHMENTS_NOT_RELAYED",
			"this delivery has attachments and "+env.DestinationNetwork+" is offline",
			"The offline mailbox stores the envelope, not the files, and a manifest whose bytes "+
				"cannot be fetched must not look like one whose bytes can. "+
				"Terminal for the mailbox and not for the message: it will go through when that "+
				"network is online, or send it now without the attachments.",
			map[string]any{
				"destination_network": env.DestinationNetwork,
				"attachments":         len(env.Attachments),
				"retry_via_mailbox":   false,
				"retry_when_online":   true,
			})
		return true, "", nil
	}
	body, err := a.spoolBody(ctx, held, env, payload)
	if err != nil {
		return false, "", err
	}
	if err := a.depositToSpool(ctx, res.SpoolToken, body); err != nil {
		var refusal *registryRefusal
		if errors.As(err, &refusal) {
			a.fail(w, r, http.StatusConflict, refusal.Code,
				"the offline mailbox refused this delivery: "+refusal.Message,
				"The code is the registry's own: SPOOL_FULL is transient (retry later); PAYLOAD_TOO_LARGE is terminal.",
				map[string]any{
					"destination_network": env.DestinationNetwork,
					"registry_code":       refusal.Code,
					"registry_message":    refusal.Message,
				})
			return true, "", nil
		}
		return false, "", err
	}

	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "message.spooled", Actor: env.Sender, ActorType: "operator",
		Action: env.Recipient + "@" + env.DestinationNetwork, Target: env.MessageID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: a.now(),
	})
	a.writeJSON(w, http.StatusAccepted, map[string]any{
		"message_id":          env.MessageID,
		"to":                  env.Recipient,
		"destination_network": env.DestinationNetwork,









		"accepted_by_mailbox": true,
		"mailbox":             res.Mailbox,
		"note":                "the destination network is offline; this is its registry-run mailbox, not delivery",
	})
	return true, "", nil
}












func (a *API) spoolBody(ctx context.Context, held *spi.ReceivedGrant, env delivery.Envelope, payload []byte) ([]byte, error) {
	sig, err := a.signDelivery(ctx, env, payload)
	if err != nil {
		return nil, err
	}
	exp := ""
	if !env.ExpiresAt.IsZero() {
		exp = env.ExpiresAt.UTC().Format(time.RFC3339)
	}
	envJSON, err := json.Marshal(deliveryEnvelopeWire{
		MessageID: env.MessageID, Sender: env.Sender, Recipient: env.Recipient,
		SourceNetwork: env.SourceNetwork, DestinationNetwork: env.DestinationNetwork,
		CreatedAt: env.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: exp,
		Type: env.Type, ContentType: env.ContentType, PayloadSize: env.PayloadSize,
		ThreadID: env.ThreadID, ReplyTo: env.ReplyTo,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"envelope": base64.StdEncoding.EncodeToString(envJSON),
		"payload":  base64.StdEncoding.EncodeToString(payload),
		"signature": base64.StdEncoding.EncodeToString(sig),




		"grant": map[string]string{"payload": held.Payload, "signature": held.Signature},
	})
}












func (a *API) nodeSendAcrossNetworks(ctx context.Context, node *spi.Node, req channel.MessageSend) (string, channel.MessageSendAck) {
	addr, aerr := parseAgentAddress(req.To)
	if aerr != nil {
		return "", channel.MessageSendAck{
			Error:  "INVALID_ARGUMENT",
			Reason: "that is not an address: " + aerr.Error() + " — an agent elsewhere is <network>.<agent>@<domain>",
		}
	}
	sender, senderAck := a.senderFor(ctx, node, req.Identity)
	if senderAck.Error != "" {
		return "", senderAck
	}
	self, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return "", channel.MessageSendAck{Error: "INTERNAL", Reason: "could not read this network's identity"}
	}
	held, err := a.peerByDomain(ctx, addr.Network, addr.Agent, a.now())
	if err != nil {
		if any, e2 := a.peerByDomain(ctx, addr.Network, "", a.now()); e2 == nil {
			return "", channel.MessageSendAck{
				Error:  "NOT_ADMITTED",
				Reason: addr.Network + " has not endorsed this network to write to " + addr.Agent + "; it covers " + any.Scope,
			}
		}
		return "", channel.MessageSendAck{
			Error:  "NO_DELIVERY_GRANT",
			Reason: "this network holds no live delivery grant from " + addr.Network,
		}
	}
	if held.PeerResolver == "" {
		return "", channel.MessageSendAck{
			Error:  "PEER_RESOLVER_UNDECLARED",
			Reason: "the grant held for " + addr.Network + " does not record which resolver that network declared",
		}
	}






	messageID := req.MessageID
	if messageID == "" {
		messageID = newMessageID()
	} else if !validMessageID(messageID) {
		return "", channel.MessageSendAck{Error: "INVALID_ARGUMENT", Reason: "message_id is not in the form msg_<uuid>"}
	}





	atts, merr := a.resolveManifest(ctx, nodeRefs(req.Attachments), held.Issuer)
	if merr != nil {
		return "", channel.MessageSendAck{Error: merr.code, Reason: merr.message}
	}
	now := a.now().UTC()
	env := delivery.Envelope{
		MessageID: messageID, Sender: sender, Recipient: addr.Agent,
		SourceNetwork: self.NetworkID, DestinationNetwork: held.Issuer,
		CreatedAt: now, ExpiresAt: now.Add(DefaultMessageTTL),
		Type: req.Type, ContentType: req.ContentType, PayloadSize: len(req.Payload),
		ReplyTo: req.ReplyTo, Attachments: atts,
	}





	var code, ourReason string
	var derr error
	switch held.PeerResolver {
	case "dns":
		code, derr = a.deliverAcross(ctx, held, env, req.Payload)
	case "cloud-registry":
		code, ourReason, derr = a.nodeDeliverViaRegistry(ctx, held, env, req.Payload)
	default:
		return "", channel.MessageSendAck{
			Error:  "PEER_RESOLVER_UNDECLARED",
			Reason: "that relationship records no declared resolver",
		}
	}
	if derr != nil {
		reason := "could not reach " + addr.Network + ": " + derr.Error()
		if held.PeerResolver == "dns" {



			reason += " — that network publishes its own address, so nothing holds mail for it " +
				"while it is down; retry when it is back"
		}
		return "", channel.MessageSendAck{Error: "NETWORK_UNREACHABLE", Reason: reason}
	}
	if code != "" {





		if ourReason != "" {
			return "", channel.MessageSendAck{Error: code, Reason: ourReason}
		}
		return "", channel.MessageSendAck{Error: code, Reason: "the destination network refused this delivery"}
	}
	return messageID, channel.MessageSendAck{}
}












func (a *API) nodeDeliverViaRegistry(ctx context.Context, held *spi.ReceivedGrant, env delivery.Envelope, payload []byte) (code string, ours string, err error) {
	res, err := a.resolveViaRegistry(ctx, env.DestinationNetwork)
	if err != nil {
		var refusal *registryRefusal
		if errors.As(err, &refusal) {
			return refusal.Code, "", nil
		}
		return "", "", err
	}
	if res.Mode == "direct" {
		if len(res.Endpoints) == 0 || res.Endpoints[0].url() == "" {
			return "", "", errors.New("the registry said direct and named no usable endpoint")
		}
		routed := *held
		routed.Endpoint = strings.TrimRight(res.Endpoints[0].url(), "/")
		c, e := a.deliverAcross(ctx, &routed, env, payload)
		return c, "", e
	}



	if len(env.Attachments) > 0 {




		return "ATTACHMENTS_NOT_RELAYED",
			"this network did not put it in the offline mailbox: that leg carries the envelope, " +
				"not the files. Terminal for the mailbox and not for the message — it goes through " +
				"when " + env.DestinationNetwork + " is online, or send it now without the attachments.",
			nil
	}
	body, err := a.spoolBody(ctx, held, env, payload)
	if err != nil {
		return "", "", err
	}
	if err := a.depositToSpool(ctx, res.SpoolToken, body); err != nil {
		var refusal *registryRefusal
		if errors.As(err, &refusal) {
			return refusal.Code, "", nil
		}
		return "", "", err
	}
	return "", "", nil
}












func (a *API) attachmentBytes(manifest []spi.Attachment) (map[string]string, error) {
	if len(manifest) == 0 {
		return nil, nil
	}
	if a.Blobs == nil {
		return nil, errors.New("attachments were named but this control plane stores no bytes")
	}
	out := make(map[string]string, len(manifest))
	for _, at := range manifest {
		if _, done := out[at.SHA256]; done {
			continue
		}
		rc, _, err := a.Blobs.Open(at.SHA256)
		if err != nil {
			return nil, fmt.Errorf("attachment %s (%s): %w", at.Name, at.SHA256, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", at.Name, err)
		}
		out[at.SHA256] = base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}
