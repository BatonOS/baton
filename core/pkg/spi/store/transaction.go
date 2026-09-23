// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)









type TxState string

const (
	TxProposed        TxState = "proposed"
	TxPendingApproval TxState = "pending_approval"
	TxApproved        TxState = "approved"
	TxExecuting       TxState = "executing"
	TxCompleted       TxState = "completed"
	TxFailed          TxState = "failed"
	TxRejected        TxState = "rejected"
	TxExpired         TxState = "expired"
	TxModified        TxState = "modified"
	TxWithdrawn       TxState = "withdrawn"
)


var TxStates = []TxState{
	TxProposed, TxPendingApproval, TxApproved, TxExecuting,
	TxCompleted, TxFailed, TxRejected, TxExpired, TxModified, TxWithdrawn,
}




func (s TxState) Terminal() bool {
	switch s {
	case TxCompleted, TxFailed, TxRejected, TxExpired, TxModified, TxWithdrawn:
		return true
	}
	return false
}


type TxBy string

const (
	TxByCore      TxBy = "core"
	TxByAuthority TxBy = "authority"
	TxByActor     TxBy = "actor"
)

















type StepStatus string

const (
	StepNotStarted StepStatus = "not_started"
	StepRunning    StepStatus = "running"
	StepSucceeded  StepStatus = "succeeded"
	StepFailed     StepStatus = "failed"
	StepUnknown    StepStatus = "unknown"
	StepSkipped    StepStatus = "skipped"
)


var StepStatuses = []StepStatus{
	StepNotStarted, StepRunning, StepSucceeded, StepFailed, StepUnknown, StepSkipped,
}


type TxStateEntry struct {
	State TxState
	At    time.Time
	By    TxBy
}


type TxStep struct {
	Index       int
	Name        string
	Executor    string
	Summary     string
	OperationID string
	Status      StepStatus
	StartedAt   *time.Time
	EndedAt     *time.Time



	Nonce string

	Evidence json.RawMessage
}





type TxPlan struct {
	PlanDigest string
	Steps      []TxStep
	Detail     json.RawMessage
}




type TxApproval struct {
	Required  bool
	Basis     string
	RuleRef   string
	RequestID string
	ExpiresAt *time.Time
	Decision  json.RawMessage













	RequiredRung string
	ClearedRung  string
}




type Transaction struct {
	ID             string
	TenantID       string
	Proposal       json.RawMessage
	ProposalDigest string
	Actor          string
	Via            json.RawMessage
	ActionName     string
	ActionVersion  string


	TargetKind string
	TargetID   string

	InteractionID string



	IdempotencyKey string



	Error      json.RawMessage
	State      TxState
	History    []TxStateEntry
	Plan       *TxPlan
	Approval   TxApproval
	OutcomeRef string
	ReceiptRef string










	RecheckedFacts json.RawMessage




	ProviderInstanceID string









	Authorization json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}





type TxTransition struct {
	ID       string
	From     TxState
	To       TxState
	By       TxBy
	At       time.Time
	Plan     *TxPlan
	Approval *TxApproval

	Error json.RawMessage
}






type TransactionStore interface {
















	Create(ctx context.Context, t *Transaction, opts CreateOptions) (*Transaction, error)
	GetByIdempotencyKey(ctx context.Context, actor, key string) (*Transaction, error)
	Get(ctx context.Context, id string) (*Transaction, error)


	ListNonTerminal(ctx context.Context) ([]Transaction, error)



	ListRecentByActor(ctx context.Context, actor string, limit int) ([]Transaction, error)


	Transition(ctx context.Context, tr TxTransition) error




	UpdateStep(ctx context.Context, u TxStepUpdate) error




	PinProviderInstance(ctx context.Context, id, instance string) error











	RecordRecheckedFacts(ctx context.Context, id string, facts json.RawMessage) error
}







type TxStepUpdate struct {
	ID       string
	Index    int
	From     StepStatus
	To       StepStatus
	At       time.Time
	Nonce    string
	Evidence json.RawMessage
}

type CreateOptions struct {




	CheckInteraction bool













	CheckSamePlanOpen bool
}

var (

	ErrIdempotencyKeyHeld = errors.New("store: idempotency key already held by this actor")


	ErrOpenOnInteraction = errors.New("store: a transaction with the same action and target is open on this interaction")


	ErrSamePlanOpen = errors.New("store: the same plan is already open on this action and target")
)



type Interaction struct {
	ID        string
	TenantID  string
	Actor     string
	CreatedAt time.Time
}


type InteractionStore interface {
	Create(ctx context.Context, i *Interaction) error
	Get(ctx context.Context, id string) (*Interaction, error)
}
