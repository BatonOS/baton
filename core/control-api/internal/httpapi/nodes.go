// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/control-api/internal/capdeny"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/grantproof"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type nodeView struct {
	NodeID         string            `json:"node_id"`
	DisplayName    string            `json:"display_name"`
	Roles          []string          `json:"roles"`
	MasterState    string            `json:"master_state,omitempty"`
	Deployment     string            `json:"deployment,omitempty"`
	Status         string            `json:"status"`
	Trust          string            `json:"trust"`
	Platform       string            `json:"platform,omitempty"`
	Arch           string            `json:"arch,omitempty"`
	AgentVersion   string            `json:"agent_version,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	EnrolledAt     time.Time         `json:"enrolled_at"`
	LastSeenAt     *time.Time        `json:"last_seen_at,omitempty"`























	LastSeenAgeSec *int64            `json:"last_seen_age_sec"`
	HeartbeatSeq   int64             `json:"heartbeat_seq"`
	Connected      bool              `json:"connected"`
	RevokedAt      *time.Time        `json:"revoked_at,omitempty"`
	RevokeReason   string            `json:"revoke_reason,omitempty"`







	Suspended      bool              `json:"suspended"`
	SuspendedAt    *time.Time        `json:"suspended_at,omitempty"`
	SuspendReason  string            `json:"suspend_reason,omitempty"`
	SuspendedBy    string            `json:"suspended_by,omitempty"`
	MirrorLagSec   *float64          `json:"mirror_lag_sec,omitempty"`



	Label          string            `json:"label,omitempty"`


	AvatarThumb    *avatarThumbView  `json:"avatar_thumb,omitempty"`







	Network string `json:"network,omitempty"`



	NetworkID string `json:"network_id,omitempty"`




	HostAddress string `json:"host_address,omitempty"`
	InflightCalls  int               `json:"inflight_calls"`
	UptimeSec      int64             `json:"uptime_sec,omitempty"`



	Workspace *workspaceView `json:"workspace"`

	Resources *resourcesView `json:"resources,omitempty"`



	Inbox *inboxView `json:"inbox,omitempty"`







	RemoteShell *bool `json:"remote_shell"`
}














type workspaceView struct {
	Name         string     `json:"name,omitempty"`
	Type         string     `json:"type,omitempty"`
	Image        string     `json:"image,omitempty"`


	Template     string     `json:"template,omitempty"`
	Enterable    bool       `json:"enterable"`
	State         string    `json:"state"`
	RuntimeStatus string    `json:"runtime_status"`
	RestartCount int        `json:"restart_count"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	LastExitCode *int       `json:"last_exit_code,omitempty"`
	OOMKilled    bool       `json:"oom_killed,omitempty"`
	LastError    string     `json:"last_error,omitempty"`



	Probe string `json:"probe"`
}















type inboxView struct {
	Waiting int `json:"waiting"`











	OldestWaitingAgeSec *int64 `json:"oldest_waiting_age_sec,omitempty"`
}

type resourcesView struct {




	Memory string `json:"memory,omitempty"`
}







func (a *API) networkNameOf(ctx context.Context, tenantID string) string {
	name, _ := a.networkNameAndID(ctx, tenantID)
	return name
}



func (a *API) networkNameAndID(ctx context.Context, tenantID string) (string, string) {
	if net, err := a.Store.Networks().Get(ctx, tenantID); err == nil && net != nil {
		name := net.DisplayName
		if name == "" {
			name = tenantID
		}
		return name, net.NetworkID
	}
	return tenantID, ""
}

func (a *API) nodeToView(ctx context.Context, n spi.Node) nodeView {
	v := nodeView{
		NodeID: n.NodeID, DisplayName: n.DisplayName,
		Roles: rolesOf(n.Roles), MasterState: string(n.MasterState),
		Deployment: string(n.Deployment),
		Status: string(n.Status), Trust: string(n.Trust), Platform: n.Platform,
		Arch: n.Arch, AgentVersion: n.AgentVersion, Labels: n.Labels,
		EnrolledAt: n.EnrolledAt, LastSeenAt: n.LastSeenAt,
		HeartbeatSeq: n.LastHeartbeatSeq, RevokedAt: n.RevokedAt,
		RevokeReason: n.RevokeReason, Label: n.Label, HostAddress: n.HostAddress,
		InflightCalls: n.InflightCalls, UptimeSec: n.UptimeSec,
		Suspended: n.SuspendedAt != nil, SuspendedAt: n.SuspendedAt,
		SuspendReason: n.SuspendReason, SuspendedBy: n.SuspendedBy,
		RemoteShell: n.RemoteShell,
	}
	v.Network, v.NetworkID = a.networkNameAndID(ctx, n.TenantID)
	if n.LastSeenAt != nil {
		age := int64(time.Since(*n.LastSeenAt).Seconds())
		v.LastSeenAgeSec = &age
	}
	if n.Inbox != nil {
		iv := &inboxView{Waiting: n.Inbox.Waiting}
		if !n.Inbox.OldestWaitingAt.IsZero() {
			age := int64(time.Since(n.Inbox.OldestWaitingAt).Seconds())
			iv.OldestWaitingAgeSec = &age
		}
		v.Inbox = iv
	}
	if n.Runtime.Memory != "" {
		v.Resources = &resourcesView{Memory: n.Runtime.Memory}
	}
	if n.Runtime.Supervised() {
		status := string(n.Runtime.RuntimeStatus)
		if status == "" {
			status = string(spi.StatusUnknown)
		}
		v.Workspace = &workspaceView{
			Name: n.Runtime.Name, Type: n.Runtime.Type, Image: n.Runtime.Image,
			Enterable: n.Runtime.Enterable, State: n.Runtime.State, RuntimeStatus: status,
			RestartCount: n.Runtime.RestartCount, StartedAt: n.Runtime.StartedAt,
			LastExitCode: n.Runtime.LastExitCode, OOMKilled: n.Runtime.OOMKilled,
			Template: n.Runtime.Template,
			LastError: n.Runtime.LastError, Probe: string(n.Runtime.Probe),
		}
	}
	_, v.Connected = a.Hub.Get(n.NodeID)
	return v
}



func toMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *API) handleNodeList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	nodes, next, err := a.Store.Nodes().List(r.Context(), spi.NodeFilter{
		Role:   spi.Role(r.URL.Query().Get("role")),
		Status: spi.NodeStatus(r.URL.Query().Get("status")),
		Limit:  queryInt(r, "limit", 50, 500),
		Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	items := make([]nodeView, 0, len(nodes))


	thumbs, _ := a.Store.Nodes().NodeThumbs(r.Context(), spi.DefaultTenant)
	for _, n := range nodes {
		v := a.nodeToView(r.Context(), n)
		if t, ok := thumbs[n.NodeID]; ok {
			v.AvatarThumb = &avatarThumbView{ContentType: t.ContentType, DataBase64: base64.StdEncoding.EncodeToString(t.Data)}
		}
		items = append(items, v)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}



func (a *API) resolveNode(r *http.Request, ref string) (*spi.Node, error) {
	node, err := a.Store.Nodes().Get(r.Context(), ref)
	if err == nil {
		return node, nil
	}
	return a.Store.Nodes().GetByName(r.Context(), "", ref)
}

func (a *API) handleNodeGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	ctx := r.Context()
	view := a.nodeToView(r.Context(), *node)

	certs, err := a.Store.Certs().ListByNode(ctx, node.NodeID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	caps, err := a.Store.Capabilities().ListByNode(ctx, node.NodeID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	events, err := a.Store.Events().List(ctx, spi.EventFilter{NodeID: node.NodeID, Limit: 20})
	if err != nil {
		a.failStore(w, r, err)
		return
	}







	body, err := toMap(view)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	revoked, err := capdeny.ActiveDenyMap(ctx, a.Store.Grants(), node.TenantID, time.Now())
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	body["capabilities"] = capabilityViews(caps, revoked)
	body["recent_events"] = eventViews(events)
	if len(certs) > 0 {
		body["certificate"] = certView(certs[0])
	}
	a.writeJSON(w, http.StatusOK, body)
}











func (a *API) handleNodeLabel(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if len(body.Label) > 200 {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"label is at most 200 characters", "It is a one-line note, not a document.", nil)
		return
	}
	ctx := r.Context()
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "node.labeled", Actor: p.Subject, ActorType: "user",
		Action: "label", Target: node.NodeID, Result: "ok",
		SourceIP: clientIP(r), NodeID: node.NodeID, RequestID: requestIDFrom(r),
		At: time.Now(), Detail: map[string]any{"label": body.Label, "display_name": node.DisplayName},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	if err := a.Store.Nodes().SetLabel(ctx, node.NodeID, body.Label); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"node_id": node.NodeID, "display_name": node.DisplayName, "label": body.Label})
}

func (a *API) handleNodeRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &body) {
		return
	}
	if body.Reason == "" {
		body.Reason = "revoked by operator"
	}

	ctx := r.Context()


	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "node.revoked", Actor: p.Subject, ActorType: "user",
		Action: "revoke", Target: node.NodeID, Result: "ok",
		SourceIP: clientIP(r), NodeID: node.NodeID, RequestID: requestIDFrom(r),
		At: time.Now(), Detail: map[string]any{"reason": body.Reason, "display_name": node.DisplayName},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

















	idents, err := a.Store.Identities().List(ctx, node.TenantID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	for _, id := range idents {
		if id.NodeID != node.NodeID {
			continue
		}
		if _, err := a.Store.Identities().Bind(ctx, node.TenantID, id.Name, ""); err != nil {
			a.failStore(w, r, err)
			return
		}
		if _, err := a.Log.Append(ctx, audit.Record{
			Event: "identity.unbound", Actor: p.Subject, ActorType: "user",
			Action: "bind", Target: id.Name, Result: "ok",
			SourceIP: clientIP(r), NodeID: node.NodeID, RequestID: requestIDFrom(r),
			At: time.Now(), Detail: map[string]any{"reason": "node revoked"},
		}); err != nil {
			a.failInternal(w, r, err)
			return
		}
	}

	certs, err := a.Store.Certs().ListByNode(ctx, node.NodeID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	for _, c := range certs {
		if c.RevokedAt != nil {
			continue
		}
		if err := a.Store.Certs().Revoke(ctx, c.Serial, body.Reason); err != nil {
			a.failStore(w, r, err)
			return
		}
		a.CA.Revoke(c.Serial)
	}

	if err := a.Store.Nodes().SetStatus(ctx, node.NodeID, spi.NodeStatusRevoked, body.Reason); err != nil {
		a.failStore(w, r, err)
		return
	}




	if conn, live := a.Hub.Get(node.NodeID); live {
		if frame, err := channel.NewFrame(channel.TypeRevoked, "", channel.Revoked{Reason: body.Reason}); err == nil {
			_ = conn.Send(ctx, frame)
		}
		conn.Close(channel.ClosePolicyViolation, "revoked")
	}

	a.writeJSON(w, http.StatusAccepted, map[string]any{
		"node_id": node.NodeID,
		"status":  string(spi.NodeStatusRevoked),
		"reason":  body.Reason,
	})
}



















func (a *API) handleNodeSuspend(suspend bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {



		p, ok := a.requireRole(w, r, console.RoleOperator)
		if !ok {
			return
		}
		node, err := a.resolveNode(r, r.PathValue("node_id"))
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		if node.Status == spi.NodeStatusRevoked {
			a.fail(w, r, http.StatusConflict, CodeForbidden,
				"this node has been revoked, so there is nothing to pause or resume",
				"Revocation is terminal. Re-enroll with a fresh token if it should rejoin.", nil)
			return
		}

		var body struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength > 0 && !a.decodeJSON(w, r, &body) {
			return
		}
		if suspend && body.Reason == "" {
			body.Reason = "suspended by operator"
		}

		ctx := r.Context()
		was := node.SuspendedAt != nil




		event, action := "node.resumed", "resume"
		if suspend {
			event, action = "node.suspended", "suspend"
		}
		if _, err := a.Log.Append(ctx, audit.Record{
			Event: event, Actor: p.Subject, ActorType: "user",
			Action: action, Target: node.NodeID, Result: "ok",
			SourceIP: clientIP(r), NodeID: node.NodeID, RequestID: requestIDFrom(r),
			At: time.Now(), Detail: map[string]any{
				"reason": body.Reason, "display_name": node.DisplayName,
				"from": suspendedLabel(was), "to": suspendedLabel(suspend),
			},
		}); err != nil {
			a.failInternal(w, r, err)
			return
		}

		if err := a.Store.Nodes().SetSuspended(ctx, node.NodeID, suspend, body.Reason, p.Subject); err != nil {
			a.failStore(w, r, err)
			return
		}





		if suspend {
			if conn, live := a.Hub.Get(node.NodeID); live {
				conn.Close(channel.ClosePolicyViolation, "suspended by operator")
			}
		}

		after, err := a.resolveNode(r, node.NodeID)
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{
			"node_id":      after.NodeID,
			"display_name": after.DisplayName,
			"suspended":    after.SuspendedAt != nil,



			"status": string(after.Status),
			"reason": after.SuspendReason,
		})
	}
}



func suspendedLabel(s bool) string {
	if s {
		return "suspended"
	}
	return "active"
}

type capabilityView struct {
	ID           string          `json:"id"`
	NodeID       string          `json:"node_id"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Risk         string          `json:"risk"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	AllowFrom    []string        `json:"allow_from,omitempty"`
	Health       string          `json:"health"`






	Revoked    *revocationView `json:"revoked"`
	LastCallAt *time.Time      `json:"last_call_at,omitempty"`
}










type revocationView struct {
	At time.Time `json:"at"`
	By string    `json:"by"`
}










func capabilityViews(caps []spi.Capability, revoked map[string]spi.Grant) []capabilityView {
	out := make([]capabilityView, 0, len(caps))
	for _, c := range caps {
		var rv *revocationView
		if d, found := revoked[c.ID]; found {
			rv = &revocationView{At: d.CreatedAt, By: d.Grantor}
		}
		out = append(out, capabilityView{
			ID: c.ID, NodeID: c.NodeID, Name: c.Name, Version: c.Version,
			Risk: string(c.Risk), InputSchema: c.InputSchema, OutputSchema: c.OutputSchema,
			AllowFrom: c.AllowFrom, Health: c.Health, Revoked: rv,
			LastCallAt: c.LastCallAt,
		})
	}
	return out
}

func certView(c spi.Certificate) map[string]any {
	return map[string]any{
		"serial": c.Serial, "fingerprint_sha256": c.FingerprintSHA256,
		"san_uri": c.SANURI, "not_before": c.NotBefore, "not_after": c.NotAfter,
		"revoked_at": c.RevokedAt,
	}
}

func (a *API) handleCapabilityList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	var (
		caps []spi.Capability
		err  error
	)
	if nodeRef := r.URL.Query().Get("node_id"); nodeRef != "" {
		node, nErr := a.resolveNode(r, nodeRef)
		if nErr != nil {
			a.failStore(w, r, nErr)
			return
		}
		caps, err = a.Store.Capabilities().ListByNode(r.Context(), node.NodeID)
	} else {
		caps, err = a.Store.Capabilities().List(r.Context(), "")
	}
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if name := r.URL.Query().Get("name"); name != "" {
		filtered := caps[:0]
		for _, c := range caps {
			if c.Name == name {
				filtered = append(filtered, c)
			}
		}
		caps = filtered
	}



	revoked, rErr := capdeny.ActiveDenyMap(r.Context(), a.Store.Grants(), spi.DefaultTenant, time.Now())
	if rErr != nil {
		a.failStore(w, r, rErr)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": capabilityViews(caps, revoked)})
}












func (a *API) handleCapabilityGrant(grant bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.requireRole(w, r, console.RoleOperator)
		if !ok {
			return
		}
		id := r.PathValue("id")



















		var err error
		if grant {
			var denies []spi.Grant
			denies, err = capdeny.ActiveDenies(r.Context(), a.Store.Grants(), spi.DefaultTenant, id, time.Now())
			for _, g := range denies {
				if err != nil {
					break
				}
				err = a.Store.Grants().Revoke(r.Context(), spi.DefaultTenant, g.GrantID, p.Subject)
			}
		} else {
			g := spi.Grant{
				TenantID:  spi.DefaultTenant,
				GrantID:   "grt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
				Grantor:   p.Subject,
				Grantee:   spi.GrantGranteeEveryone,
				Action:    spi.ActionCapabilityInvoke,
				Object:    id,
				Scope:     "network:self",
				Effect:    spi.GrantDeny,
				ValidFrom: time.Now().UTC(),
				CreatedAt: time.Now().UTC(),
			}





			if sig, sErr := a.Store.Networks().Sign(r.Context(), spi.DefaultTenant, grantproof.Canonical(g)); sErr == nil {
				g.Proof = sig
			} else {




				a.Logger.Warn("grant proof signing failed; issuing unproven deny",
					"grant_id", g.GrantID, "capability", id, "error", sErr)
				g.Constraints = "unproven:sign-failed"
			}
			_, err = a.Store.Grants().Issue(r.Context(), g)
		}
		if err != nil {
			a.failStore(w, r, err)
			return
		}

		event := "capability.revoked"
		if grant {
			event = "capability.granted"
		}
		if _, err := a.Log.Append(r.Context(), audit.Record{
			Event: event, Actor: p.Subject, ActorType: "user",
			Action: event, Target: id, Result: "ok",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		}); err != nil {
			a.failInternal(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}


func (a *API) handleCertRenew(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r)
	if !ok || !p.IsNode() {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"certificate renewal requires a node certificate",
			"Nodes renew their own certificates; this endpoint is not for operators.", nil)
		return
	}

	node, err := a.Store.Nodes().Get(r.Context(), p.Identity.Name)
	if err != nil {
		a.failStore(w, r, err)
		return
	}





	if node.Status == spi.NodeStatusRevoked {

		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this node has been revoked",
			"Re-enroll with a fresh token if the node should rejoin.", nil)
		return
	}

	var body struct {
		CSRPEM string `json:"csr_pem"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}

	certPEM, cert, err := a.CA.IssueFromCSR([]byte(body.CSRPEM), ca.Identity{
		TenantID: node.TenantID, Kind: ca.KindNode, Name: node.NodeID,
	}, ca.DefaultLeafLifetime)
	if err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"certificate request not usable: "+err.Error(),
			"Regenerate the CSR on the node.", nil)
		return
	}

	if err := a.Store.Certs().Issue(r.Context(), &spi.Certificate{
		Serial: ca.SerialString(cert), NodeID: node.NodeID,
		FingerprintSHA256: ca.Fingerprint(cert), SubjectCN: cert.Subject.CommonName,
		SANURI: cert.URIs[0].String(), NotBefore: cert.NotBefore,
		NotAfter: cert.NotAfter, IssuedAt: time.Now(),
	}); err != nil {
		a.failStore(w, r, err)
		return
	}

	a.Log.System(r.Context(), "certificate.rotated", map[string]any{
		"node_id": node.NodeID, "serial": ca.SerialString(cert),
	})

	a.writeJSON(w, http.StatusOK, map[string]any{
		"certificate_pem": string(certPEM),
		"not_after":       cert.NotAfter,
	})
}

var _ = auth.Principal{}




func rolesOf(rs spi.Roles) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, string(r))
	}
	return out
}

type avatarThumbView struct {
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}




func (a *API) handleNodeAvatarSet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	var body struct {
		ContentType string `json:"content_type"`
		FullBase64  string `json:"full_base64"`
		ThumbBase64 string `json:"thumb_base64"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.FullBase64 == "" {
		if err := a.Store.Nodes().SetNodeAvatar(r.Context(), node.NodeID, "", nil, nil); err != nil {
			a.failStore(w, r, err)
			return
		}
		a.writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
		return
	}
	if !avatarContentTypes[body.ContentType] {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "content_type must be image/png, image/jpeg or image/webp", "SVG is not accepted.", nil)
		return
	}
	full, err := base64.StdEncoding.DecodeString(body.FullBase64)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "full_base64 is not valid base64", "", nil)
		return
	}
	thumb, err := base64.StdEncoding.DecodeString(body.ThumbBase64)
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "thumb_base64 is not valid base64", "", nil)
		return
	}
	if len(full) > avatarMaxBytes || len(thumb) > avatarMaxBytes {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, "an image is larger than 2 MiB", "Shrink it.", nil)
		return
	}
	if len(thumb) == 0 {
		thumb = full
	}
	if err := a.Store.Nodes().SetNodeAvatar(r.Context(), node.NodeID, body.ContentType, full, thumb); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"node_id": node.NodeID, "content_type": body.ContentType, "full_bytes": len(full), "thumb_bytes": len(thumb)})
}


func (a *API) handleNodeAvatarGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	ct, full, thumb, err := a.Store.Nodes().NodeAvatar(r.Context(), node.NodeID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if ct == "" {
		a.fail(w, r, http.StatusNotFound, CodeNotFound, "this node has no avatar", "Set one: baton agents set-avatar <name>.", nil)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"avatar":       map[string]any{"content_type": ct, "data_base64": base64.StdEncoding.EncodeToString(full)},
		"avatar_thumb": map[string]any{"content_type": ct, "data_base64": base64.StdEncoding.EncodeToString(thumb)},
	})
}
