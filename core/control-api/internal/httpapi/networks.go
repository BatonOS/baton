// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)








type networkView struct {
	NetworkID   string `json:"network_id"`
	DisplayName string `json:"display_name"`



	PublicKey string `json:"public_key"`



	Fingerprint string `json:"fingerprint"`
















	FingerprintAlg string `json:"fingerprint_alg"`
















	KeyID string `json:"key_id"`







	CloudAddress string `json:"cloud_address,omitempty"`





	Addresses []networkAddressView  `json:"addresses"`
	Endpoints []networkEndpointView `json:"endpoints"`



	Avatar *networkAvatarView `json:"avatar"`




	Admission          string `json:"admission"`
	AdmissionChangedAt string `json:"admission_changed_at,omitempty"`
	AdmissionChangedBy string `json:"admission_changed_by,omitempty"`

	CreatedAt string `json:"created_at"`
}

type networkAvatarView struct {
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}

type networkAddressView struct {
	Address string `json:"address"`



	Source string `json:"source"`
}

type networkEndpointView struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Priority int    `json:"priority"`
}







func (a *API) handleNetworkSelf(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	view := networkView{
		NetworkID:      net.NetworkID,
		DisplayName:    net.DisplayName,
		PublicKey:      net.PublicKeyPEM,
		Fingerprint:    net.Fingerprint,
		FingerprintAlg: "sha256-b64/canonical-pem",
		KeyID:          keyIDFromPEM(net.PublicKeyPEM),
		Addresses:      []networkAddressView{},
		Endpoints:      []networkEndpointView{},
		Admission:      string(net.Admission),
		CreatedAt:      net.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if !net.AdmissionChangedAt.IsZero() {
		view.AdmissionChangedAt = net.AdmissionChangedAt.UTC().Format("2006-01-02T15:04:05Z")
		view.AdmissionChangedBy = net.AdmissionChangedBy
	}




	if ct, data, err := a.Store.Networks().GetAvatar(ctx, spi.DefaultTenant); err == nil && ct != "" {
		view.Avatar = &networkAvatarView{ContentType: ct, DataBase64: base64.StdEncoding.EncodeToString(data)}
	}
	if addrs, err := a.Store.Networks().Addresses(ctx, net.NetworkID); err == nil {
		for _, ad := range addrs {
			if ad.Source == "cloud-registry" && view.CloudAddress == "" {
				view.CloudAddress = ad.Address
			}
			view.Addresses = append(view.Addresses, networkAddressView{Address: ad.Address, Source: ad.Source})
		}
	}
	if eps, err := a.Store.Networks().Endpoints(ctx, net.NetworkID); err == nil {
		for _, ep := range eps {
			view.Endpoints = append(view.Endpoints, networkEndpointView{
				Address: ep.Address, Port: ep.Port, Protocol: ep.Protocol, Priority: ep.Priority,
			})
		}
	}

	a.writeJSON(w, http.StatusOK, view)
}















var networkNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)







func (a *API) handleNetworkRename(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DisplayName == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"display_name is required", "Send {display_name}.", nil)
		return
	}
	if !networkNamePattern.MatchString(req.DisplayName) {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"display_name must be lowercase letters, digits and dashes, starting with a letter or digit, at most 63 characters",
			"The same shape as a node name.", nil)
		return
	}
	ctx := r.Context()
	if err := a.Store.Networks().Rename(ctx, spi.DefaultTenant, req.DisplayName); err != nil {
		a.failStore(w, r, err)
		return
	}
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"network_id": net.NetworkID, "display_name": net.DisplayName})
}










func (a *API) handleNetworkAdmission(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var req struct {
		Admission string `json:"admission"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"admission is required", `Send {"admission":"open"} or {"admission":"closed"}.`, nil)
		return
	}



	want := spi.NetworkAdmission(req.Admission)
	if want != spi.AdmissionOpen && want != spi.AdmissionClosed {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"admission must be open or closed, not "+req.Admission,
			"Two values and no third: `open` takes new applications, `closed` refuses them. "+
				"The switch is deliberately two-valued; automatic admission by name or pattern is not offered.", nil)
		return
	}
	ctx := r.Context()
	before, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if err := a.Store.Networks().SetAdmission(ctx, spi.DefaultTenant, want, p.Subject); err != nil {
		a.failStore(w, r, err)
		return
	}



	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "network.admission.changed", Actor: p.Subject, ActorType: "user",
		Action: string(want), Target: before.NetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now().UTC(),
		Detail: map[string]any{"from": string(before.Admission), "to": string(want)},
	})
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"network_id":           net.NetworkID,
		"admission":            string(net.Admission),
		"admission_changed_at": net.AdmissionChangedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"admission_changed_by": net.AdmissionChangedBy,
	})
}




var avatarContentTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

const avatarMaxBytes = 2 << 20




func (a *API) handleNetworkAvatar(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var body struct {
		ContentType string `json:"content_type"`
		DataBase64  string `json:"data_base64"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	if body.DataBase64 == "" {
		if err := a.Store.Networks().SetAvatar(ctx, spi.DefaultTenant, "", nil); err != nil {
			a.failStore(w, r, err)
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
		return
	}
	if !avatarContentTypes[body.ContentType] {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"content_type must be image/png, image/jpeg or image/webp",
			"A network avatar is a raster image; SVG is not accepted.", nil)
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataBase64)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "data_base64 is not valid base64", "Send the image bytes base64-encoded.", nil)
		return
	}
	if len(data) > avatarMaxBytes {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the avatar is larger than 2 MiB", "Shrink the image.", nil)
		return
	}
	if err := a.Store.Networks().SetAvatar(ctx, spi.DefaultTenant, body.ContentType, data); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"content_type": body.ContentType, "bytes": len(data)})
}







func (a *API) handleNetworkCloudSign(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var req struct {
		Method     string `json:"method"`
		Path       string `json:"path"`
		BodySHA256 string `json:"body_sha256"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Method == "" || req.Path == "" || req.BodySHA256 == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "method, path and body_sha256 are required", "The CLI computes body_sha256 over the exact bytes it will send.", nil)
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	nb := make([]byte, 12)
	if _, err := rand.Read(nb); err != nil {
		a.failInternal(w, r, err)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(nb)

	canonical := strings.Join([]string{req.Method, req.Path, req.BodySHA256, ts, nonce, net.NetworkID}, "\n")
	sig, err := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(canonical))
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"network_id": net.NetworkID,
		"timestamp":  ts,
		"nonce":      nonce,
		"signature":  "ed25519:" + base64.StdEncoding.EncodeToString(sig),
		"public_key": net.PublicKeyPEM,
	})
}







func keyIDFromPEM(pemText string) string {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return ""
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return ""
	}
	ed, ok := pub.(ed25519.PublicKey)
	if !ok {
		return ""
	}
	sum := sha256.Sum256(ed)
	return "ed25519:" + hex.EncodeToString(sum[:8])
}
