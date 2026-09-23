// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)





type Outcome struct {
	Kind            string        `json:"kind"`
	ContractVersion string        `json:"contract_version"`
	Transaction     string        `json:"transaction"`
	Action          OutcomeAction `json:"action"`








	Parameters map[string]any `json:"parameters"`
	Target     *OutcomeRef    `json:"target"`



	Interaction string      `json:"interaction"`
	Status      spi.TxState `json:"status"`




	Approval *OutcomeApproval `json:"approval"`













	Authorization json.RawMessage `json:"authorization"`






	Plan map[string]any `json:"plan,omitempty"`

	Progress *OutcomeProgress `json:"progress,omitempty"`



	StateChanges *[]StateChange `json:"state_changes,omitempty"`
	Terminal     bool           `json:"terminal"`
	Error        *OutcomeError  `json:"error,omitempty"`

	Retryable *bool `json:"retryable,omitempty"`
}


type OutcomeApproval struct {











	Path *string `json:"path"`


	ExpiresAt *time.Time `json:"expires_at"`
	Basis     string     `json:"basis"`






	Required bool   `json:"required"`
	RuleRef  string `json:"rule_ref"`



	Rule string `json:"rule"`





	Decision json.RawMessage `json:"decision"`









	RequiredRung string `json:"required_rung"`
}


type OutcomeProgress struct {
	StepIndex int    `json:"step_index"`
	StepCount int    `json:"step_count"`
	StepName  string `json:"step_name"`
}



type StateChange struct {
	Subject string `json:"subject"`
	Field   string `json:"field"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
}


type OutcomeAction struct {
	Name    string `json:"name"`
	Version string `json:"version"`











	Summary string   `json:"summary"`
	Effects *Effects `json:"effects"`





	DeclaredBy *DeclaredBy `json:"declared_by"`
}






type OutcomeRef struct {
	Kind string  `json:"kind"`
	ID   *string `json:"id"`
	Name string  `json:"name"`
}


var retryableByCode = map[string]bool{
	CodePreconditionUnmet:   false,
	CodePlanChanged:         false,
	CodeApprovalRejected:    false,
	CodeApprovalExpired:     true,
	CodeInvalidProposal:     false,
	CodeUnknownAction:       false,
	CodeStateConflict:       true,
	CodeIdempotencyConflict: false,

}





func executionRetryable(detail map[string]any) bool {
	if detail["step_status"] == string(spi.StepUnknown) {
		return false
	}
	switch detail["failure_class"] {
	case "capacity", "provider-error":
		return true
	case "timeout", "network":
		return detail["step"] == "await_enrolled" || detail["step"] == "capture"
	}
	return false
}


func OutcomeOf(t *spi.Transaction) Outcome {
	decl, _ := lookupAction(t.ActionName)
	o := Outcome{
		Kind: "outcome", ContractVersion: ContractVersion, Transaction: t.ID,
		Action: OutcomeAction{
			Name: t.ActionName, Version: t.ActionVersion,
			Summary: decl.Summary, Effects: &decl.Effects,
			DeclaredBy: declaredByOfTransaction(t),
		},



		Parameters: map[string]any{},
		Status:     t.State,
		Terminal:   t.State.Terminal(),
	}



	if decl.Name == "" {
		o.Action.Effects = nil
	}
	var props struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(t.Proposal, &props); err == nil && props.Parameters != nil {
		o.Parameters = props.Parameters
	}







	if t.Approval.Basis != "" {
		a := &OutcomeApproval{
			Required: t.Approval.Required, Basis: t.Approval.Basis,
			RuleRef: t.Approval.RuleRef, Rule: ruleTexts[t.Approval.RuleRef],
			Decision: t.Approval.Decision, RequiredRung: t.Approval.RequiredRung,
		}
		if t.Approval.ExpiresAt != nil {
			exp := t.Approval.ExpiresAt.UTC()
			a.ExpiresAt = &exp
		}
		o.Approval = a
	}
	if (t.State == spi.TxPendingApproval || t.State == spi.TxApproved) && t.Plan != nil {
		body, err := planBody(t.Plan.Steps, t.Plan.Detail)
		if err != nil {



			body = map[string]any{"unreadable": "this transaction's stored plan could not be read: " + err.Error()}
		}
		body["plan_digest"] = t.Plan.PlanDigest
		o.Plan = body
	}
	if t.TargetKind != "" || t.TargetID != "" {





		o.Target = &OutcomeRef{Kind: t.TargetKind, ID: nil, Name: t.TargetID}
	}
	o.Authorization = t.Authorization
	o.Interaction = t.InteractionID
	if o.Interaction == "" {



		var p struct {
			Interaction string `json:"interaction"`
		}
		_ = json.Unmarshal(t.Proposal, &p)
		o.Interaction = p.Interaction
	}
	if len(t.Error) > 0 {
		var oe OutcomeError
		if err := json.Unmarshal(t.Error, &oe); err != nil {


			oe = OutcomeError{
				Message: "this transaction's stored error could not be read",
				Detail:  map[string]any{"stored": string(t.Error)},
			}
		}
		o.Error = &oe
		r := retryableByCode[oe.Code]
		if oe.Code == CodeExecutionFailed {
			r = executionRetryable(oe.Detail)
		}
		o.Retryable = &r
	}
	if t.State == spi.TxExecuting && t.Plan != nil {
		o.Progress = progressOf(t.Plan.Steps)
	}


	if t.State == spi.TxCompleted || t.State == spi.TxFailed {
		sc := stateChangesOf(t)
		o.StateChanges = &sc






		if o.Retryable != nil && *o.Retryable && (len(sc) > 0 || anyUnknown(t)) {
			f := false
			o.Retryable = &f
		}
	}
	return o
}

func anyUnknown(t *spi.Transaction) bool {
	if t.Plan == nil {
		return false
	}
	for _, st := range t.Plan.Steps {
		if st.Status == spi.StepUnknown {
			return true
		}
	}
	return false
}


func progressOf(steps []spi.TxStep) *OutcomeProgress {
	for _, want := range []spi.StepStatus{spi.StepRunning, spi.StepNotStarted} {
		for _, st := range steps {
			if st.Status == want {
				return &OutcomeProgress{StepIndex: st.Index, StepCount: len(steps), StepName: st.Name}
			}
		}
	}
	return nil
}





func stateChangesOf(t *spi.Transaction) []StateChange {
	out := []StateChange{}
	if t.Plan == nil {
		return out
	}
	var p struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Parameters struct {
			Name string `json:"name"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(t.Proposal, &p)
	src, dst := p.Target.ID, p.Parameters.Name
	var requestID string
	for _, st := range t.Plan.Steps {
		if st.Status != spi.StepSucceeded && st.Status != spi.StepUnknown {
			continue
		}
		after := func(v any) any {
			if st.Status == spi.StepUnknown {
				return "unknown"
			}
			return v
		}
		switch st.Name {
		case "place":
			out = append(out, StateChange{Subject: "workspace:" + dst, Field: "exists", Before: false, After: after(true)})
		case "enroll":
			var a struct {
				Evidence struct {
					RequestID string `json:"request_id"`
				} `json:"evidence"`
			}
			_ = json.Unmarshal(st.Evidence, &a)
			requestID = a.Evidence.RequestID
			subject := "join_request:" + requestID
			if requestID == "" {
				subject = "join_request:(of " + dst + ", id not known)"
			}
			out = append(out, StateChange{Subject: subject, Field: "state", Before: nil, After: after("pending")})
		case "admit":
			out = append(out, StateChange{Subject: "join_request:" + requestID, Field: "state", Before: "pending", After: after("admitted")})
		case "materialize":
			out = append(out, StateChange{Subject: "workspace:" + dst, Field: "state", Before: "empty", After: after("captured from " + src)})
		case "file.write":


			var a struct {
				Evidence struct {
					Path      string `json:"path"`
					ShaBefore string `json:"sha_before"`
					ShaAfter  string `json:"sha_after"`
				} `json:"evidence"`
			}
			_ = json.Unmarshal(st.Evidence, &a)
			out = append(out, StateChange{Subject: "file:" + src + "/" + a.Evidence.Path, Field: "sha256", Before: a.Evidence.ShaBefore, After: after(a.Evidence.ShaAfter)})
		}
	}
	return out
}



func declaredByFor(name string) *DeclaredBy {
	d, ok := declaredByOf(name)
	if !ok {
		return nil
	}
	return &d
}

















func declaredByOfTransaction(t *spi.Transaction) *DeclaredBy {
	if t.Plan != nil && len(t.Plan.Detail) > 0 {
		var frozen struct {
			DeclaredBy *DeclaredBy `json:"declared_by"`
		}
		if err := json.Unmarshal(t.Plan.Detail, &frozen); err == nil && frozen.DeclaredBy != nil {
			return frozen.DeclaredBy
		}


	}
	return declaredByFor(t.ActionName)
}
