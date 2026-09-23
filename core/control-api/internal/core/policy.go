// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)






















const (
	approvalBasisPolicy     = "policy"
	approvalBasisUndeclared = "undeclared-consequences"
)




const (
	ruleUndeclaredConsequences = "undeclared-consequences"
	ruleConsequentialClass     = "consequential-class"
	ruleCloneDefault           = "clone-default"
	ruleHarmlessApproved       = "harmless-approved-by-core"
	ruleExternalSideEffect     = "external-side-effect"
)











var ruleTexts = map[string]string{
	ruleUndeclaredConsequences: "this action declares no consequence that holds for these parameters; " +
		"a plan whose consequences are not stated is one a person has to look at (Q32 rule 2)",
	ruleConsequentialClass: "this plan costs money, moves data off this host, or cannot be undone; " +
		"the default policy asks a person (Q32 rule 3)",
	ruleCloneDefault: "clone copies out a new identity; the default policy asks for a person (Phase 1 §六, Q32 rule 4)",
	ruleHarmlessApproved: "every consequence of this plan is read-only or reversible, and it starts nothing " +
		"outside the workspace it names; Core approves it without a person (Q32 rule 5)",
	ruleExternalSideEffect: "this action starts something that keeps running outside the workspace it names; " +
		"Dogfood v1 requires a person to approve that, whatever the default policy said (needs 页 §三)",
}













type Escalation struct {
	ID   string
	When func(Effects) bool
	Text string
}



var escalations = []Escalation{{
	ID:   ruleExternalSideEffect,
	When: func(e Effects) bool { return e.ExternalSideEffect },
	Text: ruleTexts[ruleExternalSideEffect],
}}













func decideApproval(decl ActionDecl, eff []map[string]any) (required bool, basis, ruleRef string, rung ConfirmRung) {
	required, basis, ruleRef = defaultPolicy(decl, eff)
	if !required {
		for _, e := range escalations {
			if e.When(decl.Effects) {
				required, basis, ruleRef = true, approvalBasisPolicy, e.ID
				break
			}
		}
	}
	return required, basis, ruleRef, requiredRung(decl, required)
}















func requiredRung(decl ActionDecl, required bool) ConfirmRung {
	if !required {
		return ""
	}
	if decl.ApprovalFloor != "" {
		return decl.ApprovalFloor
	}
	return ConfirmRungButton
}



func defaultPolicy(decl ActionDecl, eff []map[string]any) (required bool, basis, ruleRef string) {






	if len(eff) == 0 {
		return true, approvalBasisUndeclared, ruleUndeclaredConsequences
	}

	for _, c := range eff {
		switch c["class"] {
		case ConsequenceCostsMoney, ConsequenceDataEgress, ConsequenceIrreversible:
			return true, approvalBasisPolicy, ruleConsequentialClass
		}
	}

	if decl.Name == ActionClone {
		return true, approvalBasisPolicy, ruleCloneDefault
	}

	return false, approvalBasisPolicy, ruleHarmlessApproved
}









func applyPolicy(ctx context.Context, txs spi.TransactionStore, t *spi.Transaction, p map[string]any, now time.Time) (spi.TxState, error) {
	decl, _ := lookupAction(t.ActionName)



	consequences, _ := effectiveConsequences(decl, p, factsOfPlan(t.Plan))
	required, basis, rule, rung := decideApproval(decl, consequences)
	if required {
		return spi.TxPendingApproval, requestApproval(ctx, txs, t, p, now, basis, rule, rung)
	}
	return spi.TxApproved, approveByCore(ctx, txs, t, now, basis, rule)
}













func approveByCore(ctx context.Context, txs spi.TransactionStore, t *spi.Transaction, now time.Time, basis, rule string) error {
	decision, err := json.Marshal(map[string]any{
		"decided_by": spi.TxByCore, "decided_at": now.UTC(),
		"surface": nil, "plan_digest": nil,
	})
	if err != nil {
		return err
	}
	return Transition(ctx, txs, spi.TxTransition{
		ID: t.ID, From: spi.TxProposed, To: spi.TxApproved, By: spi.TxByCore, At: now,
		Approval: &spi.TxApproval{Required: false, Basis: basis, RuleRef: rule, Decision: decision},
	})
}








func requestApproval(ctx context.Context, txs spi.TransactionStore, t *spi.Transaction, p map[string]any, now time.Time, basis, rule string, rung ConfirmRung) error {
	var notAfter *time.Time
	if c, ok := p["constraints"].(map[string]any); ok {
		if s, ok := c["not_after"].(string); ok {
			if na, err := time.Parse(time.RFC3339, s); err == nil {
				notAfter = &na
			}
		}
	}
	exp := ApprovalExpiresAt(now, DefaultApprovalTTL, notAfter)
	return Transition(ctx, txs, spi.TxTransition{
		ID: t.ID, From: spi.TxProposed, To: spi.TxPendingApproval, By: spi.TxByCore, At: now,
		Approval: &spi.TxApproval{Required: true, Basis: basis, RuleRef: rule, RequestID: "apr_" + uuid.NewString(), ExpiresAt: &exp, RequiredRung: rung},
	})
}
