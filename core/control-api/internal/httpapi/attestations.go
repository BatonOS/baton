// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)




















const (
	purposeCloudRegister  = "cloud-register"
	purposeBindingConfirm = "binding-confirm"

	purposeNetworkInvite = "network-invite"



	purposeTransferApprove = "transfer-approve"





	purposeResourceRegister   = "resource-register"
	purposeResourceUnregister = "resource-unregister"






	purposeNetworkRegister = "network-register"



	purposeMasterTransferOffer = "master-transfer-offer"






	purposeMasterTransferReceipt = "master-transfer-receipt"






	purposeAccessTokenRequest = "access-token-request"
	purposeAccessGrant        = "access-grant"
	purposeAccessToken        = "access-token"


















	purposeDeliveryTokenRequest = "delivery-token-request"
	purposeDeliveryGrant        = "delivery-grant"
	purposeDeliveryToken        = "delivery-token"




	purposeDeliveryReport = "delivery-report"
)

type attestationRequest struct {
	Purpose string `json:"purpose"`


	NetworkName string `json:"network_name,omitempty"`
	Endpoints   []struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"endpoints,omitempty"`


	BindingID              string `json:"binding_id,omitempty"`
	ProviderKeyFingerprint string `json:"provider_key_fingerprint,omitempty"`


	Invitee     string `json:"invitee,omitempty"`
	FromNetwork string `json:"from_network,omitempty"`
	ToNetwork   string `json:"to_network,omitempty"`
	TokenHash   string `json:"token_hash,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`


	Gaining string `json:"gaining,omitempty"`



	GrantID string `json:"grant_id,omitempty"`
	Scope   string `json:"scope,omitempty"`


	ResourceID      string `json:"resource_id,omitempty"`
	ResourceType    string `json:"resource_type,omitempty"`
	ResourceName    string `json:"resource_name,omitempty"`
	ResourceVersion string `json:"resource_version,omitempty"`
	ResourceHash    string `json:"resource_hash,omitempty"`











	ContentHash string `json:"content_hash,omitempty"`



	ParentRef string `json:"parent_ref,omitempty"`
}









func inviteCanonical(purpose, invitee, from, to, tokenHash, expires, ts, nonce string) string {
	return strings.Join([]string{purpose, invitee, from, to, tokenHash, expires, ts, nonce}, "\n")
}






func resourceRegisterCanonical(netID, resID, rtype, name, version, hash, ts, nonce string) string {
	return strings.Join([]string{purposeResourceRegister, netID, resID, rtype, name, version, hash, ts, nonce}, "\n")
}

func networkRegisterCanonical(netID, name, ts, nonce string) string {
	return strings.Join([]string{purposeNetworkRegister, netID, name, ts, nonce}, "\n")
}





func masterTransferOfferCanonical(netID, gaining, expires, ts, nonce string) string {
	return strings.Join([]string{purposeMasterTransferOffer, netID, gaining, expires, ts, nonce}, "\n")
}







func masterTransferReceiptCanonical(netID, gaining, newEpoch, ts, nonce string) string {
	return strings.Join([]string{purposeMasterTransferReceipt, netID, gaining, newEpoch, ts, nonce}, "\n")
}










func accessTokenRequestCanonical(subject, grantID, scope, ts, nonce string) string {
	return strings.Join([]string{purposeAccessTokenRequest, subject, grantID, scope, ts, nonce}, "\n")
}









func accessGrantCanonical(grantID, issuer, subject, subjectKey, scope, issued, expiry string) string {
	return strings.Join([]string{purposeAccessGrant, grantID, issuer, subject, subjectKey, scope, issued, expiry}, "\n")
}



func accessTokenCanonical(tokenID, issuer, subject, grantID, scope, issued, expiry string) string {
	return strings.Join([]string{purposeAccessToken, tokenID, issuer, subject, grantID, scope, issued, expiry}, "\n")
}











func deliveryGrantCanonical(grantID, issuer, subject, subjectKey, scope, issued, expiry string) string {
	return strings.Join([]string{purposeDeliveryGrant, grantID, issuer, subject, subjectKey, scope, issued, expiry}, "\n")
}





func deliveryTokenRequestCanonical(subject, grantID, scope, ts, nonce string) string {
	return strings.Join([]string{purposeDeliveryTokenRequest, subject, grantID, scope, ts, nonce}, "\n")
}



func deliveryTokenCanonical(tokenID, issuer, subject, grantID, scope, issued, expiry string) string {
	return strings.Join([]string{purposeDeliveryToken, tokenID, issuer, subject, grantID, scope, issued, expiry}, "\n")
}








func deliveryReportCanonical(reporter, sender, messageID, originalSHA, outcome, detail, ts string) string {
	return strings.Join([]string{purposeDeliveryReport, reporter, sender, messageID, originalSHA, outcome, detail, ts}, "\n")
}

type attestationView struct {
	NetworkID string `json:"network_id"`
	PublicKey string `json:"public_key"`




	Payload   string `json:"payload"`
	Signature string `json:"signature"`


	Nonce     string `json:"nonce"`
	Timestamp string `json:"timestamp"`
}

func (a *API) handleAttestationCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req attestationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "the body is not JSON",
			"Send {purpose, …the fields that purpose needs}.", nil)
		return
	}

	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	nonce := uuid.NewString()
	ts := time.Now().UTC().Format(time.RFC3339)

	var payload string
	switch req.Purpose {
	case purposeCloudRegister:




		eps := make([]string, 0, len(req.Endpoints))
		for _, e := range req.Endpoints {
			eps = append(eps, fmt.Sprintf("%s:%d/%s", e.Address, e.Port, e.Protocol))
		}
		sort.Strings(eps)
		payload = strings.Join([]string{
			purposeCloudRegister, net.NetworkID, req.NetworkName,
			strings.Join(eps, ","), ts, nonce,
		}, "\n")

	case purposeBindingConfirm:
		if req.BindingID == "" || req.ProviderKeyFingerprint == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"binding-confirm needs binding_id and provider_key_fingerprint",
				"The fingerprint is what proves the key reached the network it was minted for.", nil)
			return
		}
		payload = strings.Join([]string{
			purposeBindingConfirm, net.NetworkID, req.BindingID,
			req.ProviderKeyFingerprint, ts, nonce,
		}, "\n")

	case purposeNetworkInvite, purposeTransferApprove:
		if req.Invitee == "" || req.FromNetwork == "" || req.ToNetwork == "" || req.TokenHash == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				req.Purpose+" needs invitee, from_network, to_network and token_hash",
				"The invitee is signed over, not recorded beside the signature — a field next "+
					"to a signature can be swapped and the signature still checks out.", nil)
			return
		}
		payload = inviteCanonical(req.Purpose, req.Invitee, req.FromNetwork, req.ToNetwork,
			req.TokenHash, req.ExpiresAt, ts, nonce)

	case purposeResourceRegister:
		if req.ResourceID == "" || req.ResourceType == "" || req.ResourceName == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"resource-register needs resource_id, resource_type and resource_name",
				"The hash is signed over too, so the directory cannot list bytes the "+
					"publisher did not put its key behind.", nil)
			return
		}
		payload = resourceRegisterCanonical(net.NetworkID, req.ResourceID, req.ResourceType,
			req.ResourceName, req.ResourceVersion, req.ResourceHash, ts, nonce)

	case purposeDNARegister:








		if req.ResourceID == "" || req.ResourceType == "" || req.ResourceName == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"dna-register needs resource_id, resource_type and resource_name",
				"The registry records WHICH resource was registered; a record that cannot name it is not a record.", nil)
			return
		}
		if strings.TrimSpace(req.ContentHash) == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"dna-register needs content_hash",
				"Without it the record binds no bytes: the token would resolve and prove only that something was registered, not that this file is it.", nil)
			return
		}
		payload = dnaRegisterCanonical(net.NetworkID, req.ResourceID, req.ResourceType,
			req.ResourceName, req.ResourceVersion, req.ContentHash, req.ParentRef, ts, nonce)

	case purposeNetworkRegister:
		if strings.TrimSpace(req.NetworkName) == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"network-register needs network_name",
				"register claims a formal name; connect (no name) is what assigns the default id address.", nil)
			return
		}



		payload = networkRegisterCanonical(net.NetworkID, req.NetworkName, ts, nonce)

	case purposeMasterTransferOffer:
		if strings.TrimSpace(req.Gaining) == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"master-transfer-offer needs gaining", "Name the node that will take over the master role.", nil)
			return
		}
		if strings.TrimSpace(req.ExpiresAt) == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"master-transfer-offer needs expires_at", "An offer to hand over the master role must expire.", nil)
			return
		}
		payload = masterTransferOfferCanonical(net.NetworkID, req.Gaining, req.ExpiresAt, ts, nonce)

	case purposeAccessTokenRequest:





		if strings.TrimSpace(req.GrantID) == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"access-token-request needs grant_id", "Name the grant A issued to this network.", nil)
			return
		}
		if _, err := parseScope(req.Scope); err != nil {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"access-token-request scope is not canonical: "+err.Error(),
				"Use verb:type/pattern entries, comma-separated, ascending and unique.", nil)
			return
		}
		payload = accessTokenRequestCanonical(net.NetworkID, req.GrantID, req.Scope, ts, nonce)

	case purposeResourceUnregister:
		if req.ResourceID == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"resource-unregister needs resource_id", "The id is what the directory keys on.", nil)
			return
		}

		payload = strings.Join([]string{
			purposeResourceUnregister, net.NetworkID, req.ResourceID, ts, nonce,
		}, "\n")

	default:



		a.fail(w, r, http.StatusBadRequest, "UNKNOWN_PURPOSE",
			"this control plane does not attest "+req.Purpose,
			"It signs for: "+purposeCloudRegister+", "+purposeBindingConfirm+", "+
				purposeNetworkInvite+", "+purposeTransferApprove+", "+
				purposeResourceRegister+", "+purposeResourceUnregister+", "+
				purposeNetworkRegister+", "+purposeMasterTransferOffer+", "+
				purposeAccessTokenRequest+". "+
				"There is no route that signs arbitrary bytes, on purpose.", nil)
		return
	}

	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(payload))
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "network.attested", Actor: p.Subject, ActorType: "user",
		Action: req.Purpose, Target: net.NetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}














	if req.Purpose == purposeMasterTransferOffer && a.RecordTransferOffer != nil {
		if err := a.RecordTransferOffer(ctx); err != nil {
			a.failInternal(w, r, err)
			return
		}
	}

	a.writeJSON(w, http.StatusCreated, attestationView{
		NetworkID: net.NetworkID,
		PublicKey: net.PublicKeyPEM,
		Payload:   base64.StdEncoding.EncodeToString([]byte(payload)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		Nonce:     nonce,
		Timestamp: ts,
	})
}












func (a *API) handleNetworkAddressPut(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var req struct {
		Address string `json:"address"`
		Source  string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"address is required", "Send {address, source}.", nil)
		return
	}
	if req.Source != "dns" && req.Source != "cloud-registry" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"source must be dns or cloud-registry",
			"A network declares which resolver is authoritative for an address; this is not "+
				"a fallback order: a failure is retried against the same declared resolver, never a different one.", nil)
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

















	existing, err := a.Store.Networks().Addresses(ctx, net.NetworkID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	for _, e := range existing {
		if e.Source != req.Source {
			a.fail(w, r, http.StatusConflict, "RESOLVER_ALREADY_DECLARED",
				"this network already declares "+e.Source+" as its authoritative resolver",
				"One network, one authoritative resolver — a second one is a fallback, and "+
					"recovery re-asks the same authority, never a different one. "+
					"Remove "+e.Address+" first if the network is genuinely moving.",
				map[string]any{"declared_source": e.Source, "declared_address": e.Address})
			return
		}
	}
	if err := a.Store.Networks().PutAddress(ctx, net.NetworkID, req.Address, req.Source); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"address": req.Address, "source": req.Source})
}
