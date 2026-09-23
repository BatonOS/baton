// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/dispatch"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type callRequest struct {
	NodeID         string          `json:"node_id"`
	NodeName       string          `json:"node_name"`
	Capability     string          `json:"capability"`
	Version        string          `json:"version"`
	Input          json.RawMessage `json:"input"`
	IdempotencyKey string          `json:"idempotency_key"`
	TimeoutSec     int             `json:"timeout_sec"`
}

type callView struct {
	CallID       string          `json:"call_id"`
	NodeID       string          `json:"node_id"`
	Capability   string          `json:"capability"`
	Version      string          `json:"version,omitempty"`
	Status       string          `json:"status"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	DurationMS   int64           `json:"duration_ms"`
	TraceID      string          `json:"trace_id,omitempty"`
	Replayed     bool            `json:"replayed,omitempty"`
}

func toCallView(c *spi.Call, replayed bool) callView {
	return callView{
		CallID: c.CallID, NodeID: c.NodeID, Capability: c.CapabilityName,
		Version: c.CapabilityVersion, Status: string(c.Status),
		Input: rawOrNil(c.Input), Output: rawOrNil(c.Output),
		ErrorCode: c.ErrorCode, ErrorMessage: c.ErrorMessage,
		CreatedAt: c.CreatedAt, CompletedAt: c.CompletedAt,
		DurationMS: c.DurationMS, TraceID: c.TraceID, Replayed: replayed,
	}
}

func rawOrNil(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}


func (a *API) handleCallCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}

	var req callRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Capability == "" {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"capability is required",
			"Name the capability to invoke, e.g. sys.echo.", nil)
		return
	}
	if req.IdempotencyKey == "" {



		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"idempotency_key is required",
			"Send a unique key per logical invocation, and reuse it when retrying. "+
				"Capability calls can have irreversible effects.", nil)
		return
	}

	ref := req.NodeID
	if ref == "" {
		ref = req.NodeName
	}
	if ref == "" {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"node_id or node_name is required",
			"Name the node to run this on: `baton call <capability> --node <name>`.", nil)
		return
	}
	node, err := a.resolveNode(r, ref)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "call.requested", Actor: p.Subject, ActorType: "user",
		Action: "invoke", Target: node.NodeID + "/" + req.Capability, Result: "ok",
		SourceIP: clientIP(r), NodeID: node.NodeID, RequestID: requestIDFrom(r),
		At: time.Now(), Detail: map[string]any{"capability": req.Capability},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	timeout := time.Duration(req.TimeoutSec) * time.Second
	call, replayed, err := a.Dispatcher.Invoke(r.Context(), dispatch.Request{
		NodeID:         node.NodeID,
		CapabilityName: req.Capability,
		Version:        req.Version,
		Input:          req.Input,
		IdempotencyKey: req.IdempotencyKey,
		Requester:      p.Subject,
		Origin:         "local",
		Timeout:        timeout,
		RequestID:      requestIDFrom(r),
	})

	switch {
	case errors.Is(err, dispatch.ErrCapabilityUnknown):
		a.fail(w, r, http.StatusNotFound, CodeCapabilityUnknown, err.Error(),
			"List what the node offers with `baton node show "+node.DisplayName+"`.", nil)
		return
	case errors.Is(err, dispatch.ErrCapabilityNotGranted):
		a.fail(w, r, http.StatusForbidden, CodeCapabilityNotGranted, err.Error(),
			"An operator revoked it. `baton capability grant <id>` puts it back.", nil)
		return
	case errors.Is(err, dispatch.ErrNodeUnavailable):


		a.fail(w, r, http.StatusServiceUnavailable, CodeNodeOffline,
			"node "+node.DisplayName+" is enrolled but has no live control channel",
			"Check the node with `baton status "+node.DisplayName+"` and its logs with "+
				"`baton logs "+node.DisplayName+"`.", nil)
		return
	case errors.Is(err, dispatch.ErrConfirmUnsupported):
		a.fail(w, r, http.StatusForbidden, CodeConfirmRequired, err.Error(),
			"This capability is marked as needing confirmation, and this release has no "+
				"approval surface yet. Lower its risk level only if that is genuinely correct.", nil)
		return
	case errors.Is(err, dispatch.ErrDenied):
		a.fail(w, r, http.StatusForbidden, CodeForbidden, err.Error(),
			"Policy refused this invocation.", nil)
		return
	case err != nil:
		a.failStore(w, r, err)
		return
	}

	if queryBool(r, "wait") && !call.Status.Terminal() {
		waitFor := timeout
		if waitFor <= 0 {
			waitFor = dispatch.DefaultTimeout
		}
		if settled, wErr := a.Dispatcher.Wait(r.Context(), call.CallID, waitFor); wErr == nil {
			call = settled
		}
	}

	status := http.StatusAccepted
	if call.Status.Terminal() {
		status = http.StatusOK
	}
	a.writeJSON(w, status, toCallView(call, replayed))
}

func (a *API) handleCallGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	call, err := a.Store.Calls().Get(r.Context(), r.PathValue("call_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, toCallView(call, false))
}

type eventView struct {
	Seq         int64           `json:"seq"`
	EventID     string          `json:"event_id"`
	TS          time.Time       `json:"ts"`
	Category    string          `json:"category"`
	Event       string          `json:"event"`
	Actor       string          `json:"actor,omitempty"`
	ActorType   string          `json:"actor_type,omitempty"`
	ActingFor   string          `json:"acting_for,omitempty"`
	Action      string          `json:"action,omitempty"`
	Target      string          `json:"target,omitempty"`
	Result      string          `json:"result,omitempty"`
	SourceIP    string          `json:"source_ip,omitempty"`
	NodeID      string          `json:"node_id,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
	TraceID     string          `json:"trace_id,omitempty"`
	LeaderEpoch int64           `json:"leader_epoch"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	Hash        string          `json:"hash"`
}

func eventViews(events []spi.Event) []eventView {
	out := make([]eventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventView{
			Seq: e.Seq, EventID: e.EventID, TS: e.TS, Category: string(e.Category),
			Event: e.Event, Actor: e.Actor, ActorType: e.ActorType, ActingFor: e.ActingFor,
			Action: e.Action, Target: e.Target, Result: e.Result, SourceIP: e.SourceIP,
			NodeID: e.NodeID, RequestID: e.RequestID, TraceID: e.TraceID,
			LeaderEpoch: e.LeaderEpoch, Detail: rawOrNil(e.Detail), Hash: e.Hash,
		})
	}
	return out
}
























func (a *API) handleEventsLatest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ev, err := a.Store.Events().Last(r.Context())
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if ev == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"seq": 0, "item": nil})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"seq": ev.Seq, "item": eventViews([]spi.Event{*ev})[0]})
}

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	sinceSeq := int64(queryInt(r, "since_seq", 0, 1<<30))
	events, err := a.Store.Events().List(r.Context(), spi.EventFilter{
		SinceSeq: sinceSeq,
		Category: spi.EventCategory(r.URL.Query().Get("category")),
		Event:    r.URL.Query().Get("event"),
		NodeID:   r.URL.Query().Get("node_id"),
		Limit:    queryInt(r, "limit", 100, 1000),
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	body := map[string]any{"items": eventViews(events)}
	if n := len(events); n > 0 {
		body["next_seq"] = events[n-1].Seq
	} else {
		body["next_seq"] = sinceSeq
	}

	if queryBool(r, "verify") {
		brokenAt, err := a.Store.Events().VerifyChain(r.Context(), 0)
		if err != nil {
			a.failStore(w, r, err)
			return
		}



		body["chain_valid"] = brokenAt == 0
		if brokenAt != 0 {
			body["chain_broken_at"] = brokenAt
		}
	}

	a.writeJSON(w, http.StatusOK, body)
}
