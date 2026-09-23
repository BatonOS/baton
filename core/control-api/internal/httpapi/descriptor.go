// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)















const (
	CodeNetworkNotPublished = "NETWORK_NOT_PUBLISHED"
	descriptorKind          = "NetworkDescriptor"
	descriptorTTL           = 30 * 24 * time.Hour
)

type descriptorRecord struct {
	Version   int    `json:"version"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`

	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type descriptorView struct {
	APIVersion  string `json:"apiVersion"`
	Kind        string `json:"kind"`
	NetworkID   string `json:"network_id"`
	DisplayName string `json:"display_name"`
	Identity    struct {
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
	} `json:"identity"`
	Addresses []networkAddressView  `json:"addresses"`
	Endpoints []networkEndpointView `json:"endpoints"`
	Record    descriptorRecord      `json:"record"`
	Note      string                `json:"note"`
}



func DescriptorCanonical(networkID, fingerprint string, eps []networkEndpointView, issued, expires string) string {
	list := make([]string, 0, len(eps))
	for _, e := range eps {
		list = append(list, fmt.Sprintf("%s:%d/%s", e.Address, e.Port, e.Protocol))
	}
	sort.Strings(list)
	return strings.Join([]string{"network-descriptor", networkID, fingerprint, strings.Join(list, ","), issued, expires}, "\n")
}


func (a *API) handleNetworkDescriptor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	eps, err := a.Store.Networks().Endpoints(ctx, net.NetworkID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if len(eps) == 0 {
		a.fail(w, r, http.StatusNotFound, CodeNetworkNotPublished,
			"this network has not published where it answers",
			"Its operator publishes with `baton network publish --endpoint https://host:8443`; until then it is not "+
				"resolvable by name and its identity is not served here.", nil)
		return
	}
	var v descriptorView
	v.APIVersion = "baton.mailloop.dev/v1alpha1"
	v.Kind = descriptorKind
	v.NetworkID = net.NetworkID
	v.DisplayName = net.DisplayName
	v.Identity.PublicKey = net.PublicKeyPEM
	v.Identity.Fingerprint = net.Fingerprint
	v.Addresses = []networkAddressView{}
	if addrs, err := a.Store.Networks().Addresses(ctx, net.NetworkID); err == nil {
		for _, ad := range addrs {
			v.Addresses = append(v.Addresses, networkAddressView{Address: ad.Address, Source: ad.Source})
		}
	}
	v.Endpoints = make([]networkEndpointView, 0, len(eps))
	for _, ep := range eps {
		v.Endpoints = append(v.Endpoints, networkEndpointView{Address: ep.Address, Port: ep.Port, Protocol: ep.Protocol, Priority: ep.Priority})
	}
	now := time.Now().UTC()
	issued := now.Format(time.RFC3339)
	expires := now.Add(descriptorTTL).Format(time.RFC3339)
	payload := DescriptorCanonical(net.NetworkID, net.Fingerprint, v.Endpoints, issued, expires)
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(payload))
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	v.Record = descriptorRecord{
		Version: 1, IssuedAt: issued, ExpiresAt: expires,
		Payload:   base64.StdEncoding.EncodeToString([]byte(payload)),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	v.Note = "candidate locator only — verify record.signature over record.payload with identity.public_key, and compare identity.fingerprint with what named this network"
	a.writeJSON(w, http.StatusOK, v)
}



func (a *API) handleNetworkEndpointPut(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		Priority int    `json:"priority"`


		Replace bool `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "endpoint is required", "Send {endpoint: \"https://host:8443\"}.", nil)
		return
	}
	u, err := url.Parse(req.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"endpoint must be an https:// URL with a host",
			"For example https://baton.example.com:8443. This is where applicants and peers will reach the control plane.", nil)
		return
	}
	port := 8443
	if u.Port() != "" {
		fmt.Sscanf(u.Port(), "%d", &port)
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	ep := spi.NetworkEndpoint{Address: u.Hostname(), Port: port, Protocol: "https", Priority: req.Priority, UpdatedAt: time.Now().UTC()}
	ns := a.Store.Networks()
	put := ns.PutEndpoint
	if req.Replace {
		put = ns.ReplaceEndpoint
	}
	if err := put(ctx, net.NetworkID, ep); err != nil {
		a.failStore(w, r, err)
		return
	}
	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "network.published", Actor: p.Subject, ActorType: "user",
		Action: "publish", Target: net.NetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"endpoint": req.Endpoint},
	})
	a.writeJSON(w, http.StatusOK, map[string]any{
		"network_id": net.NetworkID, "fingerprint": net.Fingerprint,
		"endpoint": networkEndpointView{Address: ep.Address, Port: ep.Port, Protocol: ep.Protocol, Priority: ep.Priority},
	})
}
