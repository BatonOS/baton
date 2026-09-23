// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type enrollRequest struct {
	Token        string            `json:"token"`
	CSRPEM       string            `json:"csr_pem"`


	TransferApproval json.RawMessage `json:"transfer_approval,omitempty"`
	DisplayName  string            `json:"display_name"`
	Roles        []string          `json:"roles"`
	MasterState  string            `json:"master_state"`



	ServingHosts []string          `json:"serving_hosts,omitempty"`
	AgentVersion string            `json:"agent_version"`
	Platform     string            `json:"platform"`
	Arch         string            `json:"arch"`
	Labels       map[string]string `json:"labels"`
	Nonce        string            `json:"nonce"`
	ClientTS     time.Time         `json:"client_ts"`
}

type enrollResponse struct {
	NodeID               string    `json:"node_id"`
	CertificatePEM       string    `json:"certificate_pem"`
	CABundlePEM          string    `json:"ca_bundle_pem"`
	ControlEndpoints     []string  `json:"control_endpoints"`
	CertNotAfter         time.Time `json:"cert_not_after"`
	HeartbeatIntervalSec int       `json:"heartbeat_interval_sec"`
	ClusterID            string    `json:"cluster_id"`
	LeaderEpoch          int64     `json:"leader_epoch"`



	ServerCertificatePEM string `json:"server_certificate_pem,omitempty"`
}






func (a *API) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if a.Store.ReadOnly() {
		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror and cannot enroll nodes",
			"Enroll against the primary control plane.", nil)
		return
	}

	var req enrollRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}



	if !req.ClientTS.IsZero() {
		if skew := time.Since(req.ClientTS); skew > MaxClockSkew || skew < -MaxClockSkew {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeClockSkew,
				"this node's clock differs from the control plane by "+skew.Round(time.Second).String(),
				"Enable NTP on the node (`timedatectl set-ntp true`) and retry. "+
					"A certificate issued against a skewed clock fails later in ways that look unrelated.",
				map[string]any{"skew_seconds": int(skew.Seconds())})
			return
		}
	}

	if err := auth.ValidateNodeName(req.DisplayName); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeNameInvalid, err.Error(),
			"Pick a name matching ^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$ and retry.", nil)
		return
	}




	roles := spi.Roles{}
	for _, r := range req.Roles {
		roles = append(roles, spi.Role(r))
	}





















	requested := roles
	if !roles.Has(spi.RoleAgent) {
		roles = append(append(spi.Roles{}, roles...), spi.RoleAgent)
	}
	if !roles.Valid() {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"roles "+strings.Join(req.Roles, ",")+" are not roles this control plane issues",
			"Use master, agent, or both.", nil)
		return
	}
	masterState := spi.MasterState(req.MasterState)
	if !masterState.Valid() || (masterState != "" && !roles.Has(spi.RoleMaster)) {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"master_state "+req.MasterState+" is not a state the master role can be in",
			"Use active or standby, and ask for the master role alongside it.", nil)
		return
	}
	if roles.Has(spi.RoleMaster) && masterState == "" {
		masterState = spi.MasterActive
	}

	ctx := r.Context()





	if existing, err := a.Store.Nodes().GetByName(ctx, spi.DefaultTenant, req.DisplayName); err == nil {
		a.fail(w, r, http.StatusConflict, CodeNameConflict,
			"a node named "+req.DisplayName+" already exists",
			"Choose another name, or remove the old registration with `baton node revoke "+
				req.DisplayName+" --yes`. Your enrollment token has not been spent.",
			map[string]any{"existing_node_id": existing.NodeID, "status": string(existing.Status)})
		return
	} else if !errors.Is(err, spi.ErrNotFound) {
		a.failStore(w, r, err)
		return
	}














	preview, err := a.Store.Tokens().GetByHash(ctx, auth.HashToken(req.Token))
	if err != nil {
		a.failEnrollToken(w, r, err)
		return
	}




































	for _, want := range requested {
		if !preview.Roles.Has(want) {
			a.fail(w, r, http.StatusUnauthorized, CodeTokenRoleMismatch,
				"this token does not authorize the "+string(want)+" role",
				"An agent is admitted by name: `baton network add --agent <name>`; a standby by `baton token create --role standby`. "+
					"Your enrollment token has not been spent.", nil)
			return
		}
	}

	if ok, err := auth.MatchesNamePattern(preview.NamePattern, req.DisplayName); err != nil {
		a.failInternal(w, r, err)
		return
	} else if !ok {
		a.fail(w, r, http.StatusUnauthorized, CodeNameInvalid,
			"this token only permits names matching "+preview.NamePattern,
			"An admission is for one named agent (`baton network add --agent <name>`): join under that name. "+
				"Your enrollment token has not been spent.", nil)
		return
	}





	if preview.BoundKeyFingerprint != "" {
		got, err := csrPublicKeyFingerprint(req.CSRPEM)
		if err != nil || got != preview.BoundKeyFingerprint {
			a.fail(w, r, http.StatusUnauthorized, CodeJoinKeyMismatch,
				"this admission answers a different node's key",
				"The node that applied must be the node that enrols: enrol from the same data volume "+
					"whose key made the application. Your enrollment token has not been spent.", nil)
			return
		}
	}

	token, err := a.Store.Tokens().Consume(ctx, auth.HashToken(req.Token), spi.TokenPurposeNode)
	if err != nil {
		a.failEnrollToken(w, r, err)
		return
	}

	_ = a.Store.JoinRequests().ConsumeByTokenHash(ctx, token.TokenHash)

	nodeID := uuid.NewString()



	certPEM, cert, err := a.CA.IssueFromCSR([]byte(req.CSRPEM), ca.Identity{
		TenantID: token.TenantID,
		Kind:     ca.KindNode,
		Name:     nodeID,
	}, ca.DefaultLeafLifetime)
	if err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"the certificate request was not usable: "+err.Error(),
			"Regenerate the CSR. The node's private key must never leave the node.", nil)
		return
	}









	owner, err := a.Store.Identities().Ensure(ctx, token.TenantID, req.DisplayName)
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	if owner.NodeID != "" && owner.NodeID != nodeID {
		a.fail(w, r, http.StatusConflict, CodeNameConflict,
			"the agent name "+req.DisplayName+" is already placed on another node",
			"Choose another name for this node, or move that identity first.", nil)
		return
	}

	node := &spi.Node{
		NodeID:       nodeID,
		OwnerIdentityID: owner.IdentityID,
		TenantID:     token.TenantID,
		DisplayName:  req.DisplayName,
		Roles:        roles,
		MasterState:  masterState,
		Deployment:   spi.DeploymentLocal,
		Status:       spi.NodeStatusPending,
		Trust:        spi.TrustUnverified,
		Platform:     req.Platform,
		Arch:         req.Arch,
		AgentVersion: req.AgentVersion,
		Labels:       req.Labels,
		EnrolledAt:   time.Now(),
	}
	if err := a.Store.Nodes().Create(ctx, node); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			a.fail(w, r, http.StatusConflict, CodeNameConflict,
				"a node named "+req.DisplayName+" already exists",
				"Choose another name, or revoke the existing node with `baton node revoke "+
					req.DisplayName+"`.", nil)
			return
		}
		a.failStore(w, r, err)
		return
	}









































	if _, err := a.Store.Identities().Bind(ctx, node.TenantID, node.DisplayName, node.NodeID); err != nil {
		a.Logger.Warn("could not register an agent for the joining node",
			"node", node.DisplayName, "error", err)
	}

	if err := a.Store.Certs().Issue(ctx, &spi.Certificate{
		Serial:            ca.SerialString(cert),
		NodeID:            nodeID,
		FingerprintSHA256: ca.Fingerprint(cert),
		SubjectCN:         cert.Subject.CommonName,
		SANURI:            cert.URIs[0].String(),
		NotBefore:         cert.NotBefore,
		NotAfter:          cert.NotAfter,
		IssuedAt:          time.Now(),
	}); err != nil {
		a.failStore(w, r, err)
		return
	}




	if _, err := a.Log.Append(ctx, audit.Record{
		Event:     "node.enrolled",
		Actor:     "token:" + token.TokenID,
		ActorType: "service",
		Action:    "enroll",
		Target:    nodeID,
		Result:    "ok",
		SourceIP:  clientIP(r),
		NodeID:    nodeID,
		RequestID: requestIDFrom(r),
		At:        time.Now(),
		Detail: map[string]any{
			"display_name": req.DisplayName,
			"roles":        strings.Join(req.Roles, ","),
			"fingerprint":  ca.Fingerprint(cert),
			"platform":     req.Platform,
			"agent_version": req.AgentVersion,
		},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}



	var serverCertPEM string
	if masterState == spi.MasterStandby {




		hosts := []string{"localhost", "127.0.0.1", req.DisplayName}







		for _, h := range req.ServingHosts {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		issued, err := a.CA.IssueServerForCSR([]byte(req.CSRPEM), hosts, ca.ServerLifetime)
		if err != nil {
			a.failInternal(w, r, err)
			return
		}
		serverCertPEM = string(issued)
	}

	a.writeJSON(w, http.StatusCreated, enrollResponse{
		NodeID:               nodeID,
		CertificatePEM:       string(certPEM),
		CABundlePEM:          string(a.CA.Bundle()),
		ControlEndpoints:     a.controlEndpoints(),
		CertNotAfter:         cert.NotAfter,
		HeartbeatIntervalSec: 15,
		ClusterID:            a.ClusterID,
		LeaderEpoch:          a.Epoch,
		ServerCertificatePEM: serverCertPEM,
	})
}













func (a *API) failEnrollToken(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, spi.ErrNotFound):









		a.fail(w, r, http.StatusUnauthorized, CodeTokenNotFound,
			"that enrollment token is not recognised",
			"Have the network admit the agent again: `baton network add --agent <name> --out-file <f>`. "+
				"Tokens are stored hashed, so this server cannot tell which of these it is: "+
				"a token that was mistyped, one that was never issued, or one issued by an "+
				"earlier control plane whose data has since been reset — a saved token file "+
				"outlives the control plane that wrote it.", nil)
	case strings.Contains(msg, "already used"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that enrollment token has already been used",
			"An admission is one use. Ask the network for another: `baton network add --agent <name>`.", nil)
	case strings.Contains(msg, "expired"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that enrollment token has expired",
			"Ask the network for another: `baton network add --agent <name> --ttl 15m`.", nil)
	case strings.Contains(msg, "revoked"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that enrollment token was revoked",
			"Ask the network for another: `baton network add --agent <name>`.", nil)
	case strings.Contains(msg, "not issued for this purpose"):













		a.fail(w, r, http.StatusUnauthorized, CodeTokenNotFound,
			"that token cannot be redeemed here",
			"Have the network admit the agent again: `baton network add --agent <name> --out-file <f>`. "+
				"Your token has not been spent.", nil)
	default:
		a.failStore(w, r, err)
	}
}








func (a *API) failOperatorCode(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()
	const askAgain = "Ask whoever invited you to run `baton operator invite --name <you>` again " +
		"on the master and send you the new code and CA fingerprint."
	switch {
	case errors.Is(err, spi.ErrNotFound):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenNotFound,
			"that invitation code is not recognised",



			askAgain+" Codes are stored hashed, so this master cannot tell a mistyped code "+
				"from one that was never issued here.", nil)
	case strings.Contains(msg, "already used"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that invitation code has already been redeemed",



			"An invitation is one use. If you did not redeem it, someone else did: have the "+
				"master run `baton operator revoke <name>` before minting another. "+askAgain, nil)
	case strings.Contains(msg, "expired"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that invitation code has expired",
			"Invitations are short-lived on purpose. "+askAgain, nil)
	case strings.Contains(msg, "revoked"):
		a.fail(w, r, http.StatusUnauthorized, CodeTokenExhausted,
			"that invitation code was revoked",
			askAgain, nil)
	case strings.Contains(msg, "not issued for this purpose"):



		a.fail(w, r, http.StatusUnauthorized, CodeTokenNotFound,
			"that code cannot be redeemed here",
			askAgain, nil)
	default:
		a.failStore(w, r, err)
	}
}

func (a *API) controlEndpoints() []string {
	if len(a.Cfg.Spec.ControlEndpoints) > 0 {
		return a.Cfg.Spec.ControlEndpoints
	}
	return []string{a.Cfg.Spec.AdvertiseURL}
}

func clientIP(r *http.Request) string {



	if host, _, found := strings.Cut(r.RemoteAddr, ":"); found {
		return host
	}
	return r.RemoteAddr
}
