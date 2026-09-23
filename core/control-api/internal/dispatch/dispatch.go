// SPDX-License-Identifier: Apache-2.0



package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/capdeny"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/eventlog"
	"github.com/batonos/baton/core/pkg/spi/policy"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


const DefaultTimeout = 30 * time.Second



const MaxTimeout = 5 * time.Minute


type Request struct {
	NodeID         string
	CapabilityName string
	Version        string
	Input          json.RawMessage
	IdempotencyKey string
	Requester      string
	Origin         string
	Timeout        time.Duration
	RequestID      string
	TraceID        string
}


type Dispatcher struct {
	store  spi.Store
	hub    *channel.Hub
	policy policy.Decider
	log    *eventlog.Log
}


func New(store spi.Store, hub *channel.Hub, decider policy.Decider, log *eventlog.Log) *Dispatcher {
	return &Dispatcher{store: store, hub: hub, policy: decider, log: log}
}


var (
	ErrCapabilityUnknown  = errors.New("dispatch: capability is not registered on that node")







	ErrCapabilityNotGranted = errors.New("dispatch: capability is not granted")
	ErrNodeUnavailable    = errors.New("dispatch: node is not connected")
	ErrDenied             = errors.New("dispatch: denied by policy")
	ErrConfirmUnsupported = errors.New("dispatch: capability requires confirmation, which this release cannot ask for")
)





func (d *Dispatcher) Invoke(ctx context.Context, r Request) (*spi.Call, bool, error) {
	if r.IdempotencyKey == "" {
		return nil, false, errors.New("dispatch: idempotency_key is required")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	node, err := d.store.Nodes().Get(ctx, r.NodeID)
	if err != nil {
		return nil, false, err
	}
	if node.Status == spi.NodeStatusRevoked {
		return nil, false, fmt.Errorf("%w: node was revoked", ErrNodeUnavailable)
	}

	capability, err := d.store.Capabilities().Resolve(ctx, r.NodeID, r.CapabilityName, r.Version)
	if errors.Is(err, spi.ErrNotFound) {
		return nil, false, fmt.Errorf("%w: %s", ErrCapabilityUnknown, r.CapabilityName)
	}
	if err != nil {
		return nil, false, err
	}







	deny, err := capdeny.FirstActiveDeny(ctx, d.store.Grants(), node.TenantID, capability.ID, time.Now())
	if err != nil {
		return nil, false, fmt.Errorf("could not verify the grant record for %s — refusing unverified, which is not the same as verified-invalid: %w", r.CapabilityName, err)
	}
	if deny != nil {
		return nil, false, fmt.Errorf("%w: %s", ErrCapabilityNotGranted, r.CapabilityName)
	}



	decision, err := d.policy.Decide(ctx, policy.Request{
		TenantID:       node.TenantID,
		Requester:      r.Requester,
		RequesterType:  "user",
		TargetNodeID:   r.NodeID,
		CapabilityName: capability.Name,
		CapabilityRisk: string(capability.Risk),
		Origin:         orDefault(r.Origin, "local"),
	})
	if err != nil {


		return nil, false, fmt.Errorf("%w: %v", ErrDenied, err)
	}
	switch decision.Effect {
	case policy.EffectDeny:
		return nil, false, fmt.Errorf("%w: %s", ErrDenied, decision.Reason)
	case policy.EffectConfirm:



		return nil, false, fmt.Errorf("%w: %s", ErrConfirmUnsupported, decision.Reason)
	}

	call := &spi.Call{
		CallID:            "call_" + uuid.NewString(),
		TenantID:          node.TenantID,
		NodeID:            r.NodeID,
		CapabilityName:    capability.Name,
		CapabilityVersion: capability.Version,
		IdempotencyKey:    r.IdempotencyKey,
		Requester:         r.Requester,
		Status:            spi.CallQueued,
		Input:             r.Input,
		RequestID:         r.RequestID,
		TraceID:           orDefault(r.TraceID, "trace_"+uuid.NewString()),
	}

	stored, err := d.store.Calls().Create(ctx, call)
	if errors.Is(err, spi.ErrConflict) {


		return stored, true, nil
	}
	if err != nil {
		return nil, false, err
	}

	d.log.System(ctx, "call.created", map[string]any{
		"call_id": call.CallID, "node_id": r.NodeID, "capability": capability.Name,
	})

	conn, ok := d.hub.Get(r.NodeID)
	if !ok {


		d.complete(ctx, call, spi.CallFailed, nil, channel.ErrNodeOffline,
			"node is enrolled but has no live control channel")
		return call, false, ErrNodeUnavailable
	}

	if err := d.store.Calls().SetStatus(ctx, call.CallID, spi.CallDispatched); err != nil {
		return nil, false, err
	}
	d.log.System(ctx, "call.dispatched", map[string]any{
		"call_id": call.CallID, "node_id": r.NodeID,
	})

	started := time.Now()
	reply, err := conn.Request(ctx, channel.TypeCallRequest, channel.CallRequest{
		CallID:     call.CallID,
		Capability: capability.Name,
		Version:    capability.Version,
		Input:      r.Input,
		DeadlineMS: timeout.Milliseconds(),
		TraceID:    call.TraceID,
	}, timeout)

	if errors.Is(err, channel.ErrTimeout) {
		d.complete(ctx, call, spi.CallExpired, nil, channel.ErrCapabilityTimeout,
			fmt.Sprintf("node did not answer within %s", timeout))
		return call, false, nil
	}
	if err != nil {
		d.complete(ctx, call, spi.CallFailed, nil, channel.ErrExecutionFailed, err.Error())
		return call, false, nil
	}

	var result channel.CallResult
	if err := reply.Decode(&result); err != nil {
		d.complete(ctx, call, spi.CallFailed, nil, channel.ErrExecutionFailed,
			"node returned a result frame that could not be decoded: "+err.Error())
		return call, false, nil
	}

	status := spi.CallSucceeded
	if result.Status != "succeeded" {
		status = spi.CallFailed
	}
	duration := result.DurationMS
	if duration == 0 {
		duration = time.Since(started).Milliseconds()
	}
	call.DurationMS = duration
	d.complete(ctx, call, status, result.Output, result.ErrorCode, result.ErrorMessage)

	return call, false, nil
}






func (d *Dispatcher) Wait(ctx context.Context, callID string, timeout time.Duration) (*spi.Call, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		call, err := d.store.Calls().Get(ctx, callID)
		if err != nil {
			return nil, err
		}
		if call.Status.Terminal() {
			return call, nil
		}
		if time.Now().After(deadline) {
			return call, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return call, ctx.Err()
		}
	}
}

func (d *Dispatcher) complete(ctx context.Context, call *spi.Call, status spi.CallStatus,
	output json.RawMessage, errCode, errMsg string) {
	now := time.Now()
	call.Status = status
	call.Output = output
	call.ErrorCode = errCode
	call.ErrorMessage = errMsg
	call.CompletedAt = &now
	if call.DurationMS == 0 {
		call.DurationMS = now.Sub(call.CreatedAt).Milliseconds()
	}

	if err := d.store.Calls().Complete(ctx, call); err != nil && !errors.Is(err, spi.ErrConflict) {
		d.log.System(ctx, "call.complete_failed", map[string]any{
			"call_id": call.CallID, "error": err.Error(),
		})
		return
	}
	event := "call.succeeded"
	if status != spi.CallSucceeded {
		event = "call." + string(status)
	}
	d.log.System(ctx, event, map[string]any{
		"call_id": call.CallID, "node_id": call.NodeID,
		"duration_ms": call.DurationMS, "error_code": errCode,
	})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
