// SPDX-License-Identifier: Apache-2.0

package httpapi





















import (
	"bytes"
	"crypto/ed25519"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/delivery"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)




const registryTimeout = 10 * time.Second







func registryBase() string {
	return strings.TrimRight(os.Getenv("BATON_CLOUD_URL"), "/")
}














type registryResolution struct {
	Mode       string            `json:"mode"`
	Endpoints  []registryEndpoint `json:"endpoints,omitempty"`
	SpoolToken string            `json:"spool_token,omitempty"`
	Mailbox    string            `json:"mailbox,omitempty"`
}





type registryEndpoint struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}




func (e registryEndpoint) url() string {
	if e.Address == "" {
		return ""
	}
	scheme := e.Protocol
	if scheme == "" {
		scheme = "https"
	}
	if e.Port == 0 {
		return scheme + "://" + e.Address
	}
	return fmt.Sprintf("%s://%s:%d", scheme, e.Address, e.Port)
}







func registryCanonical(method, path string, body []byte, ts, nonce, networkID string) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{method, path, hex.EncodeToString(sum[:]), ts, nonce, networkID}, "\n")
}





func (a *API) callRegistry(ctx context.Context, method, path string, body []byte, out any) error {
	base := registryBase()
	if base == "" {
		return errors.New("no registry is configured (BATON_CLOUD_URL); this build cannot route across networks by registry")
	}
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return err
	}
	ts := a.now().UTC().Format(time.RFC3339)
	nonce := uuid.NewString()
	canon := registryCanonical(method, path, body, ts, nonce, net.NetworkID)
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(canon))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-baton-timestamp", ts)
	req.Header.Set("x-baton-nonce", nonce)
	req.Header.Set("x-baton-signature", "ed25519:"+base64.StdEncoding.EncodeToString(sig))
	req.Header.Set("x-baton-network-id", net.NetworkID)

	res, err := (&http.Client{Timeout: registryTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Code != "" {



			return &registryRefusal{Code: e.Code, Message: e.Message, Status: res.StatusCode}
		}
		return fmt.Errorf("registry answered %d with no code: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("the registry's answer is not the shape this build reads: %w", err)
		}
	}
	return nil
}




type registryRefusal struct {
	Code    string
	Message string
	Status  int
}

func (r *registryRefusal) Error() string { return r.Code + ": " + r.Message }









func (a *API) resolveViaRegistry(ctx context.Context, destination string) (*registryResolution, error) {
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return nil, err
	}
	path := "/api/crossnet/networks/" + net.NetworkID + "/resolve"
	body, _ := json.Marshal(map[string]string{"destination_network": destination})
	var out registryResolution
	if err := a.callRegistry(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	if out.Mode != "direct" && out.Mode != "spool" {



		return nil, fmt.Errorf("the registry answered with a mode this build does not know: %q", out.Mode)
	}
	return &out, nil
}










func (a *API) depositToSpool(ctx context.Context, token string, body []byte) error {
	base := registryBase()
	if base == "" {
		return errors.New("no registry is configured (BATON_CLOUD_URL)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/crossnet/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-crossnet-spool-token", token)
	res, err := (&http.Client{Timeout: peerTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Code != "" {
			return &registryRefusal{Code: e.Code, Message: e.Message, Status: res.StatusCode}
		}
		return fmt.Errorf("the spool answered %d with no code: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}










func (a *API) collectFromSpool(ctx context.Context) (fetched, landed int, err error) {
	net, e := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if e != nil {
		return 0, 0, e
	}
	base := "/api/crossnet/networks/" + net.NetworkID + "/messages"




	var list struct {
		Messages []struct {
			MessageID string `json:"message_id"`
		} `json:"messages"`
	}





	var raw map[string]json.RawMessage
	if err := a.callRegistry(ctx, http.MethodGet, base, nil, &raw); err != nil {
		return 0, 0, err
	}
	items, ok := raw["messages"]
	if !ok {
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return 0, 0, fmt.Errorf("the registry's mailbox listing has no `messages` field; it answered with %v", keys)
	}
	if err := json.Unmarshal(items, &list.Messages); err != nil {
		return 0, 0, fmt.Errorf("the registry's mailbox listing is not the shape this build reads: %w", err)
	}
	for _, m := range list.Messages {














		var full map[string]json.RawMessage
		if err := a.callRegistry(ctx, http.MethodGet, base+"/"+m.MessageID, nil, &full); err != nil {
			a.Logger.Warn("could not fetch a spooled message's body",
				"message_id", m.MessageID, "error", err)
			continue
		}
		str := func(k string) string {
			var v string
			if raw, ok := full[k]; ok {
				_ = json.Unmarshal(raw, &v)
			}
			return v
		}
		var grant struct {
			Payload   string `json:"payload"`
			Signature string `json:"signature"`
		}
		if raw, ok := full["grant"]; ok {
			_ = json.Unmarshal(raw, &grant)
		}
		fetched++




		if str("signature") == "" || str("envelope") == "" {
			keys := make([]string, 0, len(full))
			for k := range full {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			a.Logger.Warn("a spooled message came back without the fields needed to verify it",
				"message_id", m.MessageID, "fields_present", keys)
			continue
		}
		if err := a.landSpooled(ctx, net.NetworkID, str("envelope"), str("payload"), str("signature"),
			grant.Payload, grant.Signature); err != nil {





			var fatal *terminalLanding
			if errors.As(err, &fatal) {
				var srcNet string
				if envJSON, e := base64.StdEncoding.DecodeString(str("envelope")); e == nil {
					var w deliveryEnvelopeWire
					if json.Unmarshal(envJSON, &w) == nil {
						srcNet = w.SourceNetwork
					}
				}





				var payloadSHA string
				if raw, e := base64.StdEncoding.DecodeString(str("payload")); e == nil {
					sum := sha256.Sum256(raw)
					payloadSHA = hex.EncodeToString(sum[:])
				}
				told := a.reportTerminal(ctx, srcNet, m.MessageID,
					str("envelope"), payloadSHA, str("signature"), fatal)
				if told {
					if e := a.callRegistry(ctx, http.MethodPost, base+"/"+m.MessageID+"/ack", []byte("{}"), nil); e != nil {
						a.Logger.Warn("reported a terminal failure but could not release the message",
							"message_id", m.MessageID, "error", e)
					}
				}
			}




			a.Logger.Warn("could not land a spooled message",
				"message_id", m.MessageID, "error", err)
			continue
		}
		if err := a.callRegistry(ctx, http.MethodPost, base+"/"+m.MessageID+"/ack", []byte("{}"), nil); err != nil {



			a.Logger.Warn("landed a spooled message but could not ack it",
				"message_id", m.MessageID, "error", err)
		}
		landed++
	}
	return fetched, landed, nil
}

func (a *API) landSpooled(ctx context.Context, selfNetwork, envB64, payloadB64, sigB64, grantB64, grantSigB64 string) error {
	envJSON, err := base64.StdEncoding.DecodeString(envB64)
	if err != nil {
		return fmt.Errorf("envelope is not base64: %w", err)
	}
	var wire deliveryEnvelopeWire
	if err := json.Unmarshal(envJSON, &wire); err != nil {
		return fmt.Errorf("envelope is not an envelope: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("payload is not base64: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}





	grantCanon, err := base64.StdEncoding.DecodeString(grantB64)
	if err != nil {
		return fmt.Errorf("grant is not base64: %w", err)
	}
	grantSig, err := base64.StdEncoding.DecodeString(grantSigB64)
	if err != nil {
		return fmt.Errorf("grant signature is not base64: %w", err)
	}
	me, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return err
	}
	myPub, ok := ed25519FromPEM(me.PublicKeyPEM)
	if !ok {
		return errors.New("this network's own public key is not a valid ed25519 PKIX PEM")
	}
	if !ed25519.Verify(myPub, grantCanon, grantSig) {
		return &terminalLanding{Outcome: "GRANT_NOT_OURS", Detail: "the accompanying grant is not signed by the destination network"}
	}
	gf := strings.Split(string(grantCanon), "\n")
	if len(gf) != 8 || gf[0] != purposeDeliveryGrant || gf[2] != selfNetwork {
		return errors.New("the accompanying document is not a delivery grant this network issued")
	}
	senderPub, ok := ed25519FromB64DER(gf[4])
	if !ok {
		return errors.New("the grant's pinned key is not an ed25519 SPKI")
	}
	scope, err := parseDeliveryScope(gf[5])
	if err != nil {
		return fmt.Errorf("the grant's scope is not canonical: %w", err)
	}
	if !deliveryScopeAllows(scope, wire.Recipient) {
		return &terminalLanding{Outcome: "NOT_ADMITTED", Detail: "that grant does not cover " + wire.Recipient}
	}

	env, err := wire.toDelivery()
	if err != nil {
		return err
	}
	d := delivery.Delivery{Envelope: env, Payload: payload, Signature: sig}
	if !delivery.Verify(senderPub, d) {








		return fmt.Errorf("the delivery does not verify against the key that grant pins "+
			"(envelope %d bytes, payload %d, signature %d, sender %s, recipient %s) — "+
			"if a length here is zero, the field did not survive the mailbox",
			len(envJSON), len(payload), len(sig), wire.Sender, wire.Recipient)
	}
	msg, err := delivery.Land(d, delivery.Local{
		TenantID: spi.DefaultTenant, NetworkID: selfNetwork,
		VerifiedNetwork: gf[3], Now: a.now().UTC(), RetentionCap: MaxMessageTTL,



		SourceAddress: formatAgentAddress(wire.Sender, a.peerName(ctx, gf[3], a.now().UTC())),
	})
	if err != nil {
		return err
	}
	ident, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, msg.DestinationAgent)
	if err != nil {
		return &terminalLanding{Outcome: "RECIPIENT_UNKNOWN", Detail: "no agent here is called " + msg.DestinationAgent}
	}
	if err := a.Store.Messages().Enqueue(ctx, &msg); err != nil && !errors.Is(err, spi.ErrConflict) {
		return err
	}




	_ = a.tryDeliver(ctx, ident, &msg)
	return nil
}








func (a *API) handleSpoolCollect(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleOperator); !ok {
		return
	}
	got, put, err := a.collectFromSpool(r.Context())
	if err != nil {
		var refusal *registryRefusal
		if errors.As(err, &refusal) {
			a.fail(w, r, http.StatusConflict, refusal.Code,
				"the registry refused: "+refusal.Message,
				"The code is the registry's own.", nil)
			return
		}
		a.fail(w, r, http.StatusServiceUnavailable, "NETWORK_UNREACHABLE",
			"could not collect from the registry: "+err.Error(),
			"Transient; retry.", nil)
		return
	}









	a.writeJSON(w, http.StatusOK, map[string]any{"fetched": got, "landed": put})
}

























const HeartbeatInterval = 30 * time.Second







func (a *API) Heartbeat(ctx context.Context) error {
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return err
	}
	return a.callRegistry(ctx, http.MethodPost,
		"/api/crossnet/networks/"+net.NetworkID+"/heartbeat", []byte("{}"), nil)
}




func RegistryConfigured() bool { return registryBase() != "" }






func (a *API) CollectSpool(ctx context.Context) (fetched, landed int, err error) {
	return a.collectFromSpool(ctx)
}
