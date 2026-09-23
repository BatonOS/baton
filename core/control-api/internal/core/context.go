// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"fmt"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const recentTerminal = 5













type Context struct {
	Kind            string       `json:"kind"`
	ContractVersion string       `json:"contract_version"`
	Subject         string       `json:"subject"`
	Workspace       ContextPlace `json:"workspace"`


	Transactions []ContextTransaction `json:"transactions"`




	Next *ContextNext `json:"next"`

	NotFilled map[string]string `json:"not_filled"`
}


type ContextPlace struct {
	ID   *string `json:"id"`
	Path *string `json:"path"`
}


type ContextTransaction struct {
	TransactionID string        `json:"transaction_id"`
	Interaction   string        `json:"interaction"`
	Target        *OutcomeRef   `json:"target"`
	State         spi.TxState   `json:"state"`
	Action        ContextAction `json:"action"`
	PlanDigest    *string       `json:"plan_digest"`


	Approval   *ContextApproval   `json:"approval,omitempty"`
	Operations []ContextOperation `json:"operations"`

	ReceiptRef *string `json:"receipt_ref"`
}



















type ContextAction struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}




type ContextApproval struct {
	Path      *string    `json:"path"`
	ExpiresAt *time.Time `json:"expires_at"`
	Basis     string     `json:"basis"`
}



type ContextOperation struct {
	OperationID string         `json:"operation_id"`
	Index       int            `json:"index"`
	Name        string         `json:"name"`
	Status      spi.StepStatus `json:"status"`
	StartedAt   *time.Time     `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at"`
}



type ContextNext struct {
	TransactionID  string `json:"transaction_id"`
	OperationIndex *int   `json:"operation_index"`
	Operation      string `json:"operation,omitempty"`
	Why            string `json:"why"`
}


func ContextOf(ctx context.Context, txs spi.TransactionStore, subject string) (Context, error) {
	c := Context{
		Kind: "context", ContractVersion: ContractVersion, Subject: subject,
		Transactions: []ContextTransaction{},
		NotFilled: map[string]string{
			"workspace.id": "Core keeps no workspace registry in this build: a workspace is known only by the name a proposal targets, " +
				"and a name is not an id (T-BE-126) — see each transaction's target.name",
			"workspace.path": "where the workspace is on disk is the local Provider's (`baton provider serve --workspace-dir`), not Core's",
		},
	}
	open, err := txs.ListNonTerminal(ctx)
	if err != nil {
		return c, err
	}


	for i := len(open) - 1; i >= 0; i-- {
		if open[i].Actor != subject {
			continue
		}
		if c.Next == nil {
			c.Next = nextOf(&open[i])
		}
		c.Transactions = append(c.Transactions, contextTransaction(&open[i]))
	}
	if c.Next == nil {
		c.NotFilled["next"] = "no transaction of this subject is open"
	}
	recent, err := txs.ListRecentByActor(ctx, subject, recentTerminal*4)
	if err != nil {
		return c, err
	}
	shown := 0
	for i := range recent {
		if shown == recentTerminal {
			break
		}
		if recent[i].State.Terminal() {
			c.Transactions = append(c.Transactions, contextTransaction(&recent[i]))
			shown++
		}
	}
	return c, nil
}

func contextTransaction(t *spi.Transaction) ContextTransaction {
	o := OutcomeOf(t)
	ct := ContextTransaction{
		TransactionID: t.ID, Interaction: o.Interaction, Target: o.Target, State: t.State,
		Action:     ContextAction{Name: o.Action.Name, Version: o.Action.Version},
		Operations: []ContextOperation{},
	}




	if o.Approval != nil && o.Approval.Required {
		ct.Approval = &ContextApproval{
			Path: o.Approval.Path, ExpiresAt: o.Approval.ExpiresAt, Basis: o.Approval.Basis,
		}
	}
	if t.Plan != nil {
		d := t.Plan.PlanDigest
		ct.PlanDigest = &d


		for _, s := range t.Plan.Steps {
			ct.Operations = append(ct.Operations, ContextOperation{
				OperationID: s.OperationID, Index: s.Index, Name: s.Name, Status: s.Status,
				StartedAt: s.StartedAt, EndedAt: s.EndedAt,
			})
		}
	}
	if t.State.Terminal() {
		r := "rcpt_" + t.ID
		ct.ReceiptRef = &r
	}
	return ct
}



func nextOf(t *spi.Transaction) *ContextNext {
	n := &ContextNext{TransactionID: t.ID}
	switch t.State {
	case spi.TxProposed:
		n.Why = "the proposal is being validated; no Operation is next yet"
		return n
	case spi.TxPendingApproval:
		n.Why = "awaiting a person's decision; no Operation runs before it"
		return n
	}
	if t.Plan != nil {
		for _, want := range []spi.StepStatus{spi.StepRunning, spi.StepNotStarted} {
			for _, s := range t.Plan.Steps {
				if s.Status == want {
					i := s.Index
					n.OperationIndex, n.Operation = &i, s.Name
					verb := "is running"
					if want == spi.StepNotStarted {
						verb = "is next"
					}
					n.Why = fmt.Sprintf("operation %d (%s) %s; Core carries it out", i, s.Name, verb)
					return n
				}
			}
		}
	}
	n.Why = "every Operation has ended; Core is closing the transaction"
	return n
}
