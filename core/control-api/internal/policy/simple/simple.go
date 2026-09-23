// SPDX-License-Identifier: Apache-2.0













package simple

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/batonos/baton/core/control-api/internal/capdeny"
	"github.com/batonos/baton/core/pkg/spi/policy"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


type Decider struct {
	store spi.Store
}


func New(store spi.Store) *Decider { return &Decider{store: store} }


func (d *Decider) Decide(ctx context.Context, r policy.Request) (policy.Decision, error) {
	capability, err := d.store.Capabilities().Resolve(ctx, r.TargetNodeID, r.CapabilityName, "")
	if errors.Is(err, spi.ErrNotFound) {
		return policy.Decision{
			Effect: policy.EffectDeny,
			Reason: fmt.Sprintf("%s is not registered on that node", r.CapabilityName),
		}, nil
	}
	if err != nil {


		return policy.Decision{Effect: policy.EffectDeny, Reason: "registry unavailable"}, err
	}

	deny, err := capdeny.FirstActiveDeny(ctx, d.store.Grants(), r.TenantID, capability.ID, time.Now())
	if err != nil {




		return policy.Decision{Effect: policy.EffectDeny, Reason: "could not verify the grant record"}, err
	}
	if deny != nil {




		reason := fmt.Sprintf("%s has been revoked on that node", capability.Name)
		if deny.Grantor != "" {
			reason = fmt.Sprintf("%s was revoked on that node by %s", capability.Name, deny.Grantor)
		}
		return policy.Decision{Effect: policy.EffectDeny, Reason: reason}, nil
	}

	if !originAllowed(capability.AllowFrom, r.Origin) {



		return policy.Decision{
			Effect: policy.EffectDeny,
			Reason: fmt.Sprintf("%s does not accept calls from %s", capability.Name, r.Origin),
		}, nil
	}

	switch capability.Risk {
	case spi.RiskBlocked:
		return policy.Decision{
			Effect: policy.EffectDeny,
			Reason: fmt.Sprintf("%s is marked blocked and is never invoked automatically", capability.Name),
		}, nil
	case spi.RiskConfirm:
		return policy.Decision{
			Effect: policy.EffectConfirm,
			Reason: fmt.Sprintf("%s is marked as needing confirmation before it runs", capability.Name),
		}, nil
	default:
		return policy.Decision{Effect: policy.EffectAllow, Reason: "low-risk capability"}, nil
	}
}

func originAllowed(allowFrom []string, origin string) bool {
	if len(allowFrom) == 0 {
		return origin == "local" || origin == ""
	}
	for _, a := range allowFrom {
		if a == origin {
			return true
		}
	}
	return false
}

var _ policy.Decider = (*Decider)(nil)
