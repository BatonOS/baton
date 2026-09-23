// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strconv"
	"strings"
	"time"
	"io"
	"net/http"
	"os"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)




func (a *API) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}






func (a *API) handleReadyz(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"ready": true, "db": "ok"}

	if _, _, err := a.Store.Nodes().List(r.Context(), spi.NodeFilter{Limit: 1}); err != nil {
		a.Logger.Warn("readiness probe failed", "error", err)
		a.writeJSON(w, http.StatusServiceUnavailable, Error{
			Code:        CodeInternal,
			Message:     "the database is not answering: " + err.Error(),
			RequestID:   requestIDFrom(r),
			Remediation: "Check disk space and the data directory, then `baton doctor`.",
		})
		return
	}

	if a.Mirror != nil {



		lag := a.Mirror.LagSeconds()
		body["snapshot_lag_seconds"] = lag
		body["mode"] = "mirror"





		if e := a.Mirror.LastError(); e != "" {
			body["last_error"] = e
		}
























		if lag < 0 {
			a.Logger.Warn("readiness probe: the mirror has never applied a snapshot",
				"last_error", a.Mirror.LastError())
			body["ready"] = false
			a.writeJSON(w, http.StatusServiceUnavailable, body)
			return
		}
	}



















	switch restartIdentity(a.RestartIdentityFn) {
	case "would-refuse-every-client":
		body["ready"] = false
		body["restart_identity"] = "would-refuse-every-client"
		a.Logger.Warn("readiness probe: this control plane has no authority to verify anyone against",
			"consequence", "it is serving on a CA it has already given away; the next restart refuses every operator and every node")
		a.writeJSON(w, http.StatusServiceUnavailable, Error{
			Code:      CodeNoSigningAuthority,
			Message:   "this control plane holds no client-CA: it can verify nobody, and a restart would refuse every operator and every node",
			RequestID: requestIDFrom(r),
			Remediation: "It gave its signing key away on demote and has no cluster bundle. " +
				"Give it the bundle, or point it at the master it should mirror (BATON_MASTER_URL).",
		})
		return
	case "same":
		body["restart_identity"] = "same"
	default:


		body["restart_identity"] = "unknown"
	}

	a.writeJSON(w, http.StatusOK, body)
}







func standbyPeers(nodes []spi.Node) []map[string]any {
	list := []map[string]any{}
	for _, n := range nodes {
		if !n.Roles.Has(spi.RoleMaster) || n.MasterState != spi.MasterStandby {
			continue
		}




		if n.Status == spi.NodeStatusRevoked {
			continue
		}
		var lastSeen any = nil
		if n.LastSeenAt != nil {
			lastSeen = n.LastSeenAt.UTC().Format(time.RFC3339)
		}
		list = append(list, map[string]any{
			"name":      n.DisplayName,
			"role":      "standby",
			"status":    string(n.Status),
			"last_seen": lastSeen,
		})
	}
	return list
}


func (a *API) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}



	role := "master"
	if a.Store.ReadOnly() {
		role = "standby"
	}
	epoch := a.Epoch
	if a.EpochFn != nil {
		epoch = a.EpochFn()
	}







	var peers any = nil
	if !a.Store.ReadOnly() {
		if nodes, _, err := a.Store.Nodes().List(r.Context(), spi.NodeFilter{Role: spi.RoleMaster}); err == nil {
			peers = standbyPeers(nodes)
		}

	}







	self := map[string]any{
		"role":         role,
		"host":         a.Cfg.Metadata.Name,
		"leader_epoch": epoch,
	}






	var caFP any
	if a.CA != nil {
		if fp := caFingerprint(a.CA.RootCertPEM()); fp != "" {
			caFP = fp
		}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"version":        a.Version,
		"observed_by":    a.Cfg.Metadata.Name,
		"observed_at":    time.Now().UTC().Format(time.RFC3339),
		"self":           self,
		"peers":          peers,
		"role":           role,
		"edition":        a.Edition,
		"ca_fingerprint": caFP,



		"features":     a.Features,





		"cluster_id":    clusterIDOrNull(a.ClusterID),
		"cluster_state": clusterState(a.ClusterID),
		"leader_epoch":  epoch,
		"db_driver":    a.Store.Driver(),
		"read_only":    a.Store.ReadOnly(),








		"restart_identity": restartIdentity(a.RestartIdentityFn),
		"console_commands": consoleCommandNames(),
	})
}





func (a *API) handleDemote(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	if a.Demote == nil {
		a.fail(w, r, http.StatusNotImplemented, "NOT_SUPPORTED", "this control plane has no demotion path", "", nil)
		return
	}
	var req struct {
		NewMaster string             `json:"new_master"`
		Receipt   *signedAttestation `json:"receipt"`
		Force     bool               `json:"force"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}







	verified := false
	if req.Receipt != nil && req.Receipt.Payload != "" {
		net, err := a.Store.Networks().Get(r.Context(), spi.DefaultTenant)
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		if reason := a.verifyTransferReceipt(net, *req.Receipt); reason != "" {
			a.fail(w, r, http.StatusConflict, "RECEIPT_INVALID",
				"the transfer receipt does not prove a takeover: "+reason,
				"Pass the receipt `transfer master accept` printed on the new master, unmodified.", nil)
			return
		}
		verified = true
	}
	if !verified && !req.Force {
		a.fail(w, r, http.StatusConflict, "RECEIPT_REQUIRED",
			"demoting without proof the new master took over risks leaving no writable master",
			"Pass --receipt <file> from `transfer master accept`, or --force for an emergency demote.", nil)
		return
	}




	if req.NewMaster != "" && a.Hub != nil {
		frame, ferr := channel.NewFrame(channel.TypeChanged, "", channel.Changed{Kind: "endpoint", Endpoint: req.NewMaster})
		if ferr == nil {
			for _, nodeID := range a.Hub.Connected() {
				if conn, ok := a.Hub.Get(nodeID); ok {
					_ = conn.Send(r.Context(), frame)
				}
			}
		}
	}
	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "control_plane.demoted", Actor: p.Subject, ActorType: "user",
		Action: "demote", Target: a.ClusterID, Result: "ok",
		Detail:   map[string]any{"receipt_verified": verified},
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	if err := a.Demote(r.Context(), req.NewMaster); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"role": "standby", "read_only": a.Store.ReadOnly(),
	})
}








func (a *API) verifyTransferReceipt(net *spi.Network, rcpt signedAttestation) string {
	payload, err := base64.StdEncoding.DecodeString(rcpt.Payload)
	sig, err2 := base64.StdEncoding.DecodeString(rcpt.Signature)
	if err != nil || err2 != nil {
		return "payload and signature must be base64"
	}
	pub, ok := ed25519FromPEM(net.PublicKeyPEM)
	if !ok {
		return "this network's own key is unreadable"
	}
	if !ed25519.Verify(pub, payload, sig) {
		return "it is not signed by this network"
	}
	fields := strings.Split(string(payload), "\n")
	if len(fields) != 6 || fields[0] != purposeMasterTransferReceipt {
		return "it is not a master-transfer-receipt"
	}
	if fields[1] != net.NetworkID {
		return "it is for a different network"
	}
	if strings.TrimSpace(fields[2]) == "" {
		return "it names no gaining node"
	}
	newEpoch, perr := strconv.ParseInt(fields[3], 10, 64)
	if perr != nil {
		return "its epoch is not a number"
	}
	if cur := a.epochNow(); newEpoch <= cur {
		return "its epoch " + strconv.FormatInt(newEpoch, 10) +
			" does not exceed this master's epoch " + strconv.FormatInt(cur, 10)
	}
	return ""
}







type offerEnvelope struct {
	PublicKey string `json:"public_key"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}







func (a *API) verifyMasterTransferOffer(w http.ResponseWriter, r *http.Request, net *spi.Network, offer offerEnvelope, expectGaining string) (string, bool) {
	payload, e1 := base64.StdEncoding.DecodeString(offer.Payload)
	sig, e2 := base64.StdEncoding.DecodeString(strings.TrimPrefix(offer.Signature, "ed25519:"))
	if e1 != nil || e2 != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "payload and signature must be base64", "Pass the offer through unmodified.", nil)
		return "", false
	}


	if strings.TrimSpace(offer.PublicKey) != strings.TrimSpace(net.PublicKeyPEM) {
		a.fail(w, r, http.StatusConflict, "WRONG_NETWORK", "this offer is for a different network", "An offer is redeemed by the network it names.", nil)
		return "", false
	}
	blk, _ := pem.Decode([]byte(net.PublicKeyPEM))
	if blk == nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal, "network key is not PEM", "", nil)
		return "", false
	}
	pub, perr := x509.ParsePKIXPublicKey(blk.Bytes)
	edPub, isEd := pub.(ed25519.PublicKey)
	if perr != nil || !isEd || !ed25519.Verify(edPub, payload, sig) {
		a.fail(w, r, http.StatusUnauthorized, "OFFER_NOT_SIGNED",
			"this transfer offer is not signed by this network's master",
			"A file proves somebody handed you an offer; only the signature says who signed it.", nil)
		return "", false
	}


	lines := strings.Split(string(payload), "\n")
	if len(lines) != 6 || lines[0] != "master-transfer-offer" {
		a.fail(w, r, http.StatusConflict, "OFFER_MALFORMED", "the signed bytes are not a master-transfer offer", "Pass the offer through unmodified.", nil)
		return "", false
	}
	offerNet, gaining, expires := lines[1], lines[2], lines[3]
	if offerNet != net.NetworkID {
		a.fail(w, r, http.StatusConflict, "WRONG_NETWORK", "the offer names a different network id", "An offer is redeemed by the network it names.", nil)
		return "", false
	}

	if gaining != expectGaining {
		a.fail(w, r, http.StatusConflict, "NOT_THE_GAINING_NODE",
			"this offer hands the master role to a different node",
			"It names "+gaining+"; expected "+expectGaining+".", nil)
		return "", false
	}

	exp, terr := time.Parse(time.RFC3339, expires)
	if terr != nil || time.Now().After(exp) {
		a.fail(w, r, http.StatusGone, "OFFER_EXPIRED", "this transfer offer has expired",
			"Ask the current master for a fresh `baton transfer master --to` offer.", nil)
		return "", false
	}
	return gaining, true
}

func (a *API) handleTransferAccept(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var offer offerEnvelope
	if !a.decodeJSON(w, r, &offer) {
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	gaining, ok := a.verifyMasterTransferOffer(w, r, net, offer, a.Cfg.Metadata.Name)
	if !ok {
		return
	}






	if a.InstallCA != nil {
		if err := a.InstallCA(ctx, offer.PublicKey, offer.Payload, offer.Signature); err != nil {
			a.fail(w, r, http.StatusBadGateway, "CA_TRANSFER_FAILED",
				"could not obtain the network's signing key from the current master: "+err.Error(),
				"The master must be reachable to hand over the CA key. A promotion without it can verify certs but not issue new ones.", nil)
			return
		}
	}


	if a.Promote == nil {
		a.fail(w, r, http.StatusNotImplemented, "NOT_SUPPORTED", "this control plane has no promotion path",
			"Accepting a transfer promotes a standby; this build has no mirror.", nil)
		return
	}
	epoch, perr2 := a.Promote(ctx)
	if perr2 != nil {
		a.failInternal(w, r, perr2)
		return
	}
	if _, aerr := a.Log.Append(ctx, audit.Record{
		Event: "master.transfer_accepted", Actor: p.Subject, ActorType: "user",
		Action: "transfer-accept", Target: net.NetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); aerr != nil {
		a.failInternal(w, r, aerr)
		return
	}




	rNonce := uuid.NewString()
	rTS := time.Now().UTC().Format(time.RFC3339)
	receiptPayload := masterTransferReceiptCanonical(net.NetworkID, gaining, strconv.FormatInt(epoch, 10), rTS, rNonce)
	receiptSig, rerr := a.Store.Networks().Sign(ctx, spi.DefaultTenant, []byte(receiptPayload))
	if rerr != nil {
		a.failInternal(w, r, rerr)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"role": "master", "read_only": a.Store.ReadOnly(), "leader_epoch": epoch,
		"network_id": net.NetworkID, "gaining": gaining,



		"receipt": map[string]any{
			"payload":    base64.StdEncoding.EncodeToString([]byte(receiptPayload)),
			"signature":  base64.StdEncoding.EncodeToString(receiptSig),
			"public_key": net.PublicKeyPEM,
			"nonce":      rNonce,
			"timestamp":  rTS,
		},
	})
}







func (a *API) handleCABundle(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r)
	if !ok || !p.IsNode() {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"the CA signing key is served only to an enrolled node",
			"The gaining node pulls it with its own mTLS identity during a transfer.", nil)
		return
	}
	if a.CA == nil {
		a.fail(w, r, http.StatusConflict, "NOT_A_SIGNER",
			"this control plane holds no signing key to hand over",
			"Only the current master serves the CA bundle; a mirror has none.", nil)
		return
	}
	var offer offerEnvelope
	if !a.decodeJSON(w, r, &offer) {
		return
	}
	ctx := r.Context()
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	caller, cerr := a.Store.Nodes().Get(ctx, p.Identity.Name)
	if cerr != nil || caller == nil {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"the calling node is not in this network's registry",
			"Only an enrolled node of this network pulls the CA bundle.", nil)
		return
	}
	gaining, ok := a.verifyMasterTransferOffer(w, r, net, offer, caller.DisplayName)
	if !ok {
		return
	}
	mat, err := ca.ReadSigningMaterial(a.Cfg.CADir())
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	if _, aerr := a.Log.Append(ctx, audit.Record{
		Event: "ca.signing_key_handed_over", Actor: p.Identity.Name, ActorType: "node",
		Action: "transfer-ca", Target: net.NetworkID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{"gaining": gaining},
	}); aerr != nil {
		a.failInternal(w, r, aerr)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"root_crt":         string(mat.RootCert),
		"root_key":         string(mat.RootKey),
		"intermediate_crt": string(mat.IntermediateCert),
		"intermediate_key": string(mat.IntermediateKey),
	})
}






func (a *API) handlePromote(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	if a.Promote == nil {
		a.fail(w, r, http.StatusNotImplemented, "NOT_SUPPORTED",
			"this control plane has no promotion path",
			"Promotion is a standby (mirror) becoming primary; this build has no mirror.", nil)
		return
	}
	epoch, err := a.Promote(r.Context())
	if err != nil {
		a.failInternal(w, r, err)
		return
	}


	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "control_plane.promoted", Actor: p.Subject, ActorType: "user",
		Action: "promote", Target: a.ClusterID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"role": "master", "read_only": a.Store.ReadOnly(), "leader_epoch": epoch,
	})
}


func (a *API) handleSnapshot(w http.ResponseWriter, r *http.Request) {






	p, ok := principalFrom(r)
	standby := false
	if ok && p.IsNode() {
		if n, err := a.Store.Nodes().Get(r.Context(), p.Identity.Name); err == nil && n != nil {
			standby = n.Roles.Has(spi.RoleMaster) && n.MasterState == spi.MasterStandby
		}
	}
	if !standby {



		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"snapshots are only served to a node holding the master role in the standby state",
			"Enrol the mirror with `baton token create --role master`, which arrives in the "+
				"standby state.", nil)
		return
	}
















	if n, err := a.Store.Nodes().Get(r.Context(), p.Identity.Name); err == nil && n != nil {
		if err := a.Store.Nodes().Touch(r.Context(), n.DisplayName, time.Now().UTC()); err != nil {



			a.Logger.Warn("could not record the mirror's pull", "node", n.DisplayName, "error", err)
		}
	}

	snapshotter, ok := a.Store.(interface {
		SnapshotTo(path string) (string, int64, error)
	})
	if !ok {
		a.fail(w, r, http.StatusNotImplemented, CodeInternal,
			"this storage backend cannot produce snapshots",
			"Mirrors require the SQLite backend in this release.", nil)
		return
	}













	dir := a.Cfg.Spec.DataDir
	if dir == "" {



		a.failInternal(w, r, errors.New("no data directory is configured, so there is nowhere to stage a snapshot"))
		return
	}
	tmp, err := os.CreateTemp(dir, "baton-snapshot-*.db")
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	sum, size, err := snapshotter.SnapshotTo(tmpPath)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	defer f.Close()




	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Baton-Snapshot-Sha256", sum)
	w.Header().Set("Content-Length", itoa(size))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		a.Logger.Warn("snapshot stream interrupted", "error", err)
	}

	a.Log.System(r.Context(), "snapshot.served", map[string]any{
		"node_id": p.Identity.Name, "size_bytes": size, "sha256": sum,
	})
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}







func restartIdentity(fn func() string) string {
	if fn == nil {
		return "unknown"
	}
	if v := fn(); v != "" {
		return v
	}
	return "unknown"
}
