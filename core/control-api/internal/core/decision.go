// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



type Decision struct {
	Decision   spi.TxState `json:"decision"`
	PlanDigest string      `json:"plan_digest"`
	DecidedBy  string      `json:"decided_by"`
	DecidedAt  time.Time   `json:"decided_at"`
	Surface    string      `json:"surface"`
	Comment    string      `json:"comment,omitempty"`
}

var (







	ErrSelfDecision = errors.New("core: the proposer cannot decide their own proposal")
























	ErrAnonymousDecision = errors.New("core: a decision needs a named decider and a named proposer")


	ErrNotPending = errors.New("core: the transaction is not waiting for a decision")



	ErrStaleDecision = errors.New("core: the decision names a plan that is not the one awaiting approval")



	ErrDecisionExpired = errors.New("core: the approval expired before this decision")









	ErrPlanUnseen = errors.New("core: the plan awaiting approval cannot be read, so it cannot have been shown to anyone")



	ErrUnsupportedDecision = errors.New("core: this build accepts approved or rejected")
)


const (
	CodeApprovalRejected = "APPROVAL_REJECTED"
	CodeApprovalExpired  = "APPROVAL_EXPIRED"
)














func Decide(ctx context.Context, txs spi.TransactionStore, id string, d Decision) (*spi.Transaction, error) {
	if d.Decision != spi.TxApproved && d.Decision != spi.TxRejected {
		return nil, fmt.Errorf("%w (got %q)", ErrUnsupportedDecision, d.Decision)
	}
	t, err := txs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.State != spi.TxPendingApproval {
		return t, fmt.Errorf("%w: it is %s", ErrNotPending, t.State)
	}

























	if t.Actor == "" || d.DecidedBy == "" {
		return t, fmt.Errorf("%w: proposer=%q decider=%q", ErrAnonymousDecision, t.Actor, d.DecidedBy)
	}
	if d.DecidedBy == t.Actor {
		return t, fmt.Errorf("%w: %s proposed it", ErrSelfDecision, t.Actor)
	}
	if t.Plan != nil {
		if _, err := planBody(t.Plan.Steps, t.Plan.Detail); err != nil {
			return t, fmt.Errorf("%w: %v", ErrPlanUnseen, err)
		}
	}
	if t.Plan == nil || d.PlanDigest != t.Plan.PlanDigest {
		return t, ErrStaleDecision
	}
	if expired, err := expireIfDue(ctx, txs, *t, d.DecidedAt); err != nil {
		return nil, err
	} else if expired {
		got, _ := txs.Get(ctx, id)
		return got, ErrDecisionExpired
	}

	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	approval := t.Approval
	approval.Decision = raw







	approval.ClearedRung = ConfirmRungButton
	tr := spi.TxTransition{
		ID: t.ID, From: spi.TxPendingApproval, To: d.Decision, By: spi.TxByAuthority,
		At: d.DecidedAt, Approval: &approval,
	}
	if d.Decision == spi.TxRejected {
		detail := map[string]any{}
		if d.Comment != "" {
			detail["comment"] = d.Comment
		}
		tr.Error, err = json.Marshal(&OutcomeError{
			Code: CodeApprovalRejected, Message: "the person rejected this proposal",
			Remediation: []Remedy{{Text: "Read the person's comment, if any, and propose something else."}},
			Detail:      detail,
		})
		if err != nil {
			return nil, err
		}
	}
	if err := Transition(ctx, txs, tr); err != nil {
		if errors.Is(err, spi.ErrConflict) {

			got, _ := txs.Get(ctx, id)
			return got, fmt.Errorf("%w: it was decided or expired concurrently", ErrNotPending)
		}
		return nil, err
	}
	return txs.Get(ctx, id)
}
