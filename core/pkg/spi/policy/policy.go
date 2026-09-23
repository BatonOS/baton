// SPDX-License-Identifier: Apache-2.0











package policy

import (
	"context"
	"time"
)


type Effect string

const (

	EffectAllow Effect = "allow"




	EffectConfirm Effect = "confirm"

	EffectDeny Effect = "deny"
)




type Obligation struct {
	Kind   string
	Params map[string]any
}






type Decision struct {
	Effect      Effect
	Reason      string
	Obligations []Obligation



	TTL time.Duration
}


type Request struct {
	TenantID       string
	Requester      string
	RequesterType  string
	ActingFor      string
	TargetNodeID   string
	CapabilityName string
	CapabilityRisk string



	Origin string
	Input  map[string]any
}





type Decider interface {
	Decide(ctx context.Context, r Request) (Decision, error)
}


type DeciderFunc func(ctx context.Context, r Request) (Decision, error)


func (f DeciderFunc) Decide(ctx context.Context, r Request) (Decision, error) { return f(ctx, r) }
