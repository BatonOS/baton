// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)








const (
	TakeoverLeaseTTL   = 90 * time.Second
	TakeoverRenewEvery = 30 * time.Second
)

type takeoverRequest struct {
	SessionID string `json:"session_id"`


	Force  bool   `json:"force"`
	Reason string `json:"reason"`
}

type takeoverView struct {
	NodeID     string    `json:"node_id"`
	Holder     string    `json:"holder"`
	SessionID  string    `json:"session_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`



	RenewWithinSec int    `json:"renew_within_sec"`
	PreemptedFrom  string `json:"preempted_from,omitempty"`
}

func lockView(l *spi.TakeoverLock) takeoverView {
	return takeoverView{
		NodeID: l.NodeID, Holder: l.Holder, SessionID: l.SessionID,
		AcquiredAt: l.AcquiredAt, ExpiresAt: l.ExpiresAt,
		RenewWithinSec: int(TakeoverRenewEvery.Seconds()),
		PreemptedFrom:  l.PreemptedFrom,
	}
}






func (a *API) handleTakeoverAcquire(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	node, req, ok := a.takeoverTarget(w, r)
	if !ok {
		return
	}

	ctx := r.Context()













	if node.Runtime.State == "" {
		a.fail(w, r, http.StatusConflict, "NO_RUNTIME",
			"this node has not reported a runtime — it supervises none, or its first heartbeat has not arrived yet",
			"A node that just enrolled reports within its first heartbeat: retry shortly, and `baton node show` tells which reading is true. A node with no runtime: use `baton logs` for its output.", nil)
		return
	}
	if !node.Runtime.Enterable {
		a.fail(w, r, http.StatusConflict, "RUNTIME_NOT_ENTERABLE",
			"this runtime does not declare session: tty, so it has no terminal to enter",
			"Use `baton attach` to watch it, or `baton logs` for its output. "+
				"Set session: tty in the runtime spec if it should be enterable.", nil)
		return
	}

	event := "node.takeover.acquired"
	if req.Force {



		event = "node.takeover.preempted"
	}
	rec, err := a.Log.Append(ctx, audit.Record{
		Event:     event,
		Actor:     p.Subject,
		ActorType: "user",
		Action:    "takeover",
		Target:    node.NodeID,
		Result:    "pending",
		SourceIP:  clientIP(r),
		NodeID:    node.NodeID,
		RequestID: requestIDFrom(r),
		Detail: map[string]any{
			"session_id": req.SessionID,
			"reason":     req.Reason,
			"force":      req.Force,
		},
	})
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, "AUDIT_UNAVAILABLE",
			"the audit trail could not record this takeover, so it was not granted",
			"An interactive session on a working agent must be recorded. Fix the "+
				"control plane and try again.", nil)
		return
	}

	lock, err := a.Store.Locks().Acquire(ctx, node.NodeID, p.Subject, req.SessionID,
		TakeoverLeaseTTL, req.Force)
	if err != nil {
		if errors.Is(err, spi.ErrConflict) {





			a.recordTakeoverOutcome(ctx, node, rec.EventID, "denied", req.SessionID)




			a.fail(w, r, http.StatusConflict, "TAKEOVER_HELD", err.Error(),
				"Wait for them to finish, or take it with --force, which is audited "+
					"as a preemption and tells them they were displaced.", nil)
			return
		}
		a.recordTakeoverOutcome(ctx, node, rec.EventID, "error", req.SessionID)
		a.failStore(w, r, err)
		return
	}

	a.recordTakeoverOutcome(ctx, node, rec.EventID, "ok", req.SessionID)

	view := lockView(lock)
	a.writeJSON(w, http.StatusOK, map[string]any{
		"lock": view, "audit_event_id": rec.EventID,
	})
}





func (a *API) recordTakeoverOutcome(ctx context.Context, node *spi.Node, attemptID, result, sessionID string) {
	a.Log.System(ctx, "node.takeover.result", map[string]any{
		"node_id":        node.NodeID,
		"result":         result,
		"session_id":     sessionID,
		"audit_event_id": attemptID,
	})
}


func (a *API) handleTakeoverRenew(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleOperator); !ok {
		return
	}
	node, req, ok := a.takeoverTarget(w, r)
	if !ok {
		return
	}

	lock, err := a.Store.Locks().Renew(r.Context(), node.NodeID, req.SessionID, TakeoverLeaseTTL)
	if err != nil {
		if errors.Is(err, spi.ErrConflict) {



			a.fail(w, r, http.StatusConflict, "TAKEOVER_LOST",
				"this takeover session no longer holds the node",
				"The lease lapsed and someone else may now be in control. "+
					"Close this session and re-acquire.", nil)
			return
		}
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"lock": lockView(lock)})
}






func (a *API) handleTakeoverRelease(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	node, req, ok := a.takeoverTarget(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	if err := a.Store.Locks().Release(ctx, node.NodeID, req.SessionID); err != nil {
		a.failStore(w, r, err)
		return
	}




	if _, err := a.Log.Append(ctx, audit.Record{
		Event:     "node.takeover.released",
		Actor:     p.Subject,
		ActorType: "user",
		Action:    "takeover.release",
		Target:    node.NodeID,
		Result:    "ok",
		SourceIP:  clientIP(r),
		NodeID:    node.NodeID,
		RequestID: requestIDFrom(r),
		Detail:    map[string]any{"session_id": req.SessionID},
	}); err != nil {
		a.Logger.Warn("record takeover release", "node_id", node.NodeID, "error", err)
	}

	a.writeJSON(w, http.StatusOK, map[string]any{"released": true})
}


func (a *API) handleTakeoverStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	lock, err := a.Store.Locks().Get(r.Context(), node.NodeID)
	if errors.Is(err, spi.ErrNotFound) {
		a.writeJSON(w, http.StatusOK, map[string]any{"held": false})
		return
	}
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	a.writeJSON(w, http.StatusOK, map[string]any{
		"held": lock.Held(time.Now()), "lock": lockView(lock),
	})
}


func (a *API) takeoverTarget(w http.ResponseWriter, r *http.Request) (*spi.Node, takeoverRequest, bool) {
	var req takeoverRequest
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) {
		return nil, req, false
	}
	if req.SessionID == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"session_id is required",
			"Generate one per takeover session; it is what stops a stale client "+
				"from releasing the session that replaced it.", nil)
		return nil, req, false
	}

	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return nil, req, false
	}
	return node, req, true
}
