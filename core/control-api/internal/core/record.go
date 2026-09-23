// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)
















type Record struct {
	Transaction string      `json:"transaction"`
	State       spi.TxState `json:"state"`
	Actor       string      `json:"actor"`
	Interaction string      `json:"interaction"`
	PlanDigest  *string     `json:"plan_digest"`






	ProviderInstanceID *string        `json:"provider_instance_id"`
	Approval           RecordApproval `json:"approval"`
	Steps              []RecordStep   `json:"steps"`
	History            []RecordEntry  `json:"state_history"`
}


type RecordApproval struct {
	Required  bool       `json:"required"`
	Basis     string     `json:"basis"`
	RuleRef   string     `json:"rule_ref"`
	RequestID string     `json:"request_id"`
	ExpiresAt *time.Time `json:"expires_at"`
	Decision  *Decision  `json:"decision"`
}


type RecordStep struct {
	Index       int            `json:"index"`
	Name        string         `json:"name"`
	Executor    string         `json:"executor"`
	OperationID string         `json:"operation_id"`
	Status      spi.StepStatus `json:"status"`
	StartedAt   *time.Time     `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at"`



	ViaCore bool `json:"via_core"`



	Nonce *string `json:"nonce"`

	Evidence json.RawMessage `json:"evidence"`











	DeclaredBy *DeclaredBy `json:"declared_by"`
}


type RecordEntry struct {
	State spi.TxState `json:"state"`
	At    time.Time   `json:"at"`
	By    spi.TxBy    `json:"by"`
}



func RecordOf(t *spi.Transaction) Record {
	r := Record{
		Transaction: t.ID, State: t.State, Actor: t.Actor, Interaction: t.InteractionID,
		Approval: RecordApproval{
			Required: t.Approval.Required, Basis: t.Approval.Basis, RuleRef: t.Approval.RuleRef,
			RequestID: t.Approval.RequestID, ExpiresAt: t.Approval.ExpiresAt,
		},
		Steps:   []RecordStep{},
		History: []RecordEntry{},
	}
	if t.ProviderInstanceID != "" {
		pi := t.ProviderInstanceID
		r.ProviderInstanceID = &pi
	}
	if len(t.Approval.Decision) > 0 {
		var d Decision
		if err := json.Unmarshal(t.Approval.Decision, &d); err == nil {
			r.Approval.Decision = &d
		}
	}
	if t.Plan != nil {
		digest := t.Plan.PlanDigest
		r.PlanDigest = &digest
		for _, s := range t.Plan.Steps {
			step := RecordStep{
				Index: s.Index, Name: s.Name, Executor: s.Executor, OperationID: s.OperationID,
				Status: s.Status, StartedAt: s.StartedAt, EndedAt: s.EndedAt,
				ViaCore: dispatchedToAnExecutor(s.Executor), Evidence: s.Evidence,



				DeclaredBy: declaredByOfTransaction(t),
			}
			if s.Nonce != "" {
				n := s.Nonce
				step.Nonce = &n
			}
			r.Steps = append(r.Steps, step)
		}
	}
	for _, h := range t.History {
		r.History = append(r.History, RecordEntry{State: h.State, At: h.At.UTC(), By: h.By})
	}
	return r
}
