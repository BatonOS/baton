// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)














type Receipt struct {
	Kind            string `json:"kind"`
	ContractVersion string `json:"contract_version"`
	ID              string `json:"id"`
	Transaction     string `json:"transaction"`
	Actor           string `json:"actor"`

	Via            json.RawMessage `json:"via"`
	Action         OutcomeAction   `json:"action"`
	Target         *OutcomeRef     `json:"target"`
	ProposalDigest string          `json:"proposal_digest"`
	Approval       ReceiptApproval `json:"approval"`



	Authorization json.RawMessage `json:"authorization"`
	Steps         []ReceiptStep   `json:"steps"`









































	GovernanceFacts   []GovernanceFact `json:"governance_facts"`














	RecheckedFacts []GovernanceFact `json:"rechecked_facts"`
	ExecutionBoundary string           `json:"execution_boundary"`
	StateChanges      []StateChange    `json:"state_changes"`
	FinalStatus       spi.TxState      `json:"final_status"`
	ErrorCode         *string          `json:"error_code"`
	FailureClass      *string          `json:"failure_class"`

	PreviousReceiptRef *string         `json:"previous_receipt_ref"`
	Measures           json.RawMessage `json:"measures"`
	EvidenceChain      json.RawMessage `json:"evidence_chain"`

	NotFilled map[string]string `json:"not_filled"`
}




type ReceiptApproval struct {
	Required bool   `json:"required"`
	Basis    string `json:"basis"`
	RuleRef  string `json:"rule_ref"`
	Rule     string `json:"rule,omitempty"`








	RequiredRung string    `json:"required_rung"`
	ClearedRung  string    `json:"cleared_rung"`
	Decision     *Decision `json:"decision"`
}


type ReceiptStep struct {
	Index       int             `json:"index"`
	Name        string          `json:"name"`
	Executor    string          `json:"executor"`
	OperationID string          `json:"operation_id"`
	ViaCore     bool            `json:"via_core"`
	StartedAt   *time.Time      `json:"started_at"`
	EndedAt     *time.Time      `json:"ended_at"`
	Status      spi.StepStatus  `json:"status"`
	Evidence    json.RawMessage `json:"evidence"`
}



func ReceiptOf(t *spi.Transaction) (Receipt, bool) {
	if !t.State.Terminal() {
		return Receipt{}, false
	}
	o := OutcomeOf(t)
	r := Receipt{
		Kind: "receipt", ContractVersion: ContractVersion, ID: "rcpt_" + t.ID, Transaction: t.ID,
		Actor: t.Actor, Via: t.Via, Action: o.Action, Target: o.Target, ProposalDigest: t.ProposalDigest,
		Approval: ReceiptApproval{
			Required: t.Approval.Required, Basis: t.Approval.Basis, RuleRef: t.Approval.RuleRef,
			RequiredRung: t.Approval.RequiredRung, ClearedRung: t.Approval.ClearedRung,
			Rule: ruleTexts[t.Approval.RuleRef],
		},
		Authorization:     t.Authorization,
		GovernanceFacts:   orEmptyFacts(factsOfPlan(t.Plan)),
		RecheckedFacts:    orEmptyFacts(unmarshalFacts(t.RecheckedFacts)),
		ExecutionBoundary: "l1",
		StateChanges:      []StateChange{},
		FinalStatus:       t.State,
		NotFilled: map[string]string{
			"previous_receipt_ref": "this build keeps no lineage of receipts per agent; there is no earlier receipt to point at",
			"measures":             "not produced in this build (no judgement layer, no metering)",
			"evidence_chain":       "not produced in this build: the receipt does not yet cite a range of the event log",
		},
	}
	if len(t.Approval.Decision) > 0 {
		var d Decision
		if json.Unmarshal(t.Approval.Decision, &d) == nil {
			r.Approval.Decision = &d
		}
	}
	if o.StateChanges != nil {
		r.StateChanges = *o.StateChanges
	}
	if o.Error != nil {
		code := o.Error.Code
		r.ErrorCode = &code
		if fc, ok := o.Error.Detail["failure_class"].(string); ok {
			r.FailureClass = &fc
		}
	}
	if t.Plan != nil {
		for _, s := range t.Plan.Steps {
			status := s.Status




			if status == spi.StepNotStarted {
				status = spi.StepSkipped
			}
			r.Steps = append(r.Steps, ReceiptStep{
				Index: s.Index, Name: s.Name, Executor: s.Executor, OperationID: s.OperationID,
				ViaCore: dispatchedToAnExecutor(s.Executor), StartedAt: s.StartedAt, EndedAt: s.EndedAt,
				Status: status, Evidence: s.Evidence,
			})
		}
	}
	return r, true
}






func orEmptyFacts(f []GovernanceFact) []GovernanceFact {
	if f == nil {
		return []GovernanceFact{}
	}
	return f
}




func unmarshalFacts(raw []byte) []GovernanceFact {
	if len(raw) == 0 {
		return nil
	}
	var out []GovernanceFact
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
