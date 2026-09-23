// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)




const DefaultApprovalTTL = 24 * time.Hour




func ApprovalExpiresAt(enteredAt time.Time, ttl time.Duration, notAfter *time.Time) time.Time {
	if ttl <= 0 {
		ttl = DefaultApprovalTTL
	}
	at := enteredAt.Add(ttl)
	if notAfter != nil && notAfter.Before(at) {
		return *notAfter
	}
	return at
}



var legal = map[spi.TxState]map[spi.TxState]spi.TxBy{
	spi.TxProposed: {
		spi.TxRejected:        spi.TxByCore,
		spi.TxPendingApproval: spi.TxByCore,
		spi.TxApproved:        spi.TxByCore,
		spi.TxWithdrawn:       spi.TxByActor,
	},
	spi.TxPendingApproval: {
		spi.TxApproved:  spi.TxByAuthority,
		spi.TxRejected:  spi.TxByAuthority,
		spi.TxModified:  spi.TxByAuthority,
		spi.TxExpired:   spi.TxByCore,
		spi.TxWithdrawn: spi.TxByActor,
	},

	spi.TxApproved: {
		spi.TxExecuting: spi.TxByCore,
		spi.TxFailed:    spi.TxByCore,
	},
	spi.TxExecuting: {
		spi.TxCompleted: spi.TxByCore,
		spi.TxFailed:    spi.TxByCore,
	},
}



var ErrIllegalTransition = errors.New("core: illegal transaction transition")


func CheckTransition(from, to spi.TxState, by spi.TxBy) error {
	who, ok := legal[from][to]
	if !ok {
		return fmt.Errorf("%w: %s → %s is not in the contract", ErrIllegalTransition, from, to)
	}
	if who != by {
		return fmt.Errorf("%w: %s → %s is caused by %s, not %s", ErrIllegalTransition, from, to, who, by)
	}
	return nil
}


func Transition(ctx context.Context, st spi.TransactionStore, tr spi.TxTransition) error {
	if err := CheckTransition(tr.From, tr.To, tr.By); err != nil {
		return err
	}
	if err := st.Transition(ctx, tr); err != nil {
		return err
	}




	writeEvidenceFor(ctx, st, tr.ID)
	return nil
}


type Recovered struct {
	ID    string
	State spi.TxState
	Now   spi.TxState
	Err   error


	Reconcile bool
}

















func Recover(ctx context.Context, txs spi.TransactionStore, ints spi.InteractionStore, now time.Time) ([]Recovered, error) {
	list, err := txs.ListNonTerminal(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Recovered, 0, len(list))
	for _, t := range list {
		r := Recovered{ID: t.ID, State: t.State, Now: t.State}
		switch t.State {
		case spi.TxPendingApproval:
			if expired, err := expireIfDue(ctx, txs, t, now); err != nil {
				r.Err = err
			} else if expired {
				r.Now = spi.TxExpired
			}
		case spi.TxProposed:




			state, err := revalidate(ctx, txs, ints, t, now)
			r.Now, r.Err = state, err
		case spi.TxApproved:





			state, err := recompile(ctx, txs, t, now)
			r.Now, r.Err = state, err
		case spi.TxExecuting:
			r.Reconcile = true
		}
		out = append(out, r)
	}
	return out, nil
}





func ExpireDue(ctx context.Context, st spi.TransactionStore, now time.Time) ([]string, error) {
	txs, err := st.ListNonTerminal(ctx)
	if err != nil {
		return nil, err
	}
	var expired []string
	var errs []error
	for _, t := range txs {
		if t.State != spi.TxPendingApproval {
			continue
		}
		ok, err := expireIfDue(ctx, st, t, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.ID, err))
			continue
		}
		if ok {
			expired = append(expired, t.ID)
		}
	}
	return expired, errors.Join(errs...)
}




var ErrNoExpiry = errors.New("core: pending_approval transaction has no expires_at")

func expireIfDue(ctx context.Context, st spi.TransactionStore, t spi.Transaction, now time.Time) (bool, error) {
	if t.Approval.ExpiresAt == nil {
		return false, ErrNoExpiry
	}
	if now.Before(*t.Approval.ExpiresAt) {
		return false, nil
	}




	raw, err := json.Marshal(&OutcomeError{
		Code: CodeApprovalExpired, Message: "nobody decided before the approval expired; expiry is not a rejection",
		Remediation: []Remedy{{Text: "Propose again if it is still wanted."}},
		Detail:      map[string]any{"expires_at": t.Approval.ExpiresAt.UTC().Format(time.RFC3339)},
	})
	if err != nil {
		return false, err
	}
	err = Transition(ctx, st, spi.TxTransition{
		ID: t.ID, From: spi.TxPendingApproval, To: spi.TxExpired, By: spi.TxByCore, At: now, Error: raw,
	})
	if errors.Is(err, spi.ErrConflict) {


		return false, nil
	}
	return err == nil, err
}


func revalidate(ctx context.Context, txs spi.TransactionStore, ints spi.InteractionStore, t spi.Transaction, now time.Time) (spi.TxState, error) {
	p, _, err := parse(t.Proposal)
	if err != nil {
		return t.State, fmt.Errorf("stored proposal of %s is unreadable: %w", t.ID, err)
	}
	owned := func(id string) bool {
		i, err := ints.Get(ctx, id)
		return err == nil && i.Actor == t.Actor
	}
	if _, verr := Validate(p, owned); verr == nil {



		if t.Plan == nil {
			return t.State, fmt.Errorf("proposed transaction %s has no compiled plan", t.ID)
		}
		state, err := applyPolicy(ctx, txs, &t, p, now)
		if err != nil {
			return t.State, err
		}
		return state, nil
	} else {
		raw, err := json.Marshal(verr)
		if err != nil {
			return t.State, err
		}
		if err := Transition(ctx, txs, spi.TxTransition{
			ID: t.ID, From: spi.TxProposed, To: spi.TxRejected, By: spi.TxByCore, At: now, Error: raw,
		}); err != nil {
			return t.State, err
		}
		return spi.TxRejected, nil
	}
}



func recompile(ctx context.Context, txs spi.TransactionStore, t spi.Transaction, now time.Time) (spi.TxState, error) {
	p, _, err := parse(t.Proposal)
	if err != nil {
		return t.State, fmt.Errorf("stored proposal of %s is unreadable: %w", t.ID, err)
	}
	if t.Plan == nil {
		return t.State, fmt.Errorf("approved transaction %s has no plan", t.ID)
	}



























	fresh := factsFor(ctx, p)
	frozen := factsOfPlan(t.Plan)
	if drift, same := sameFacts(frozen, fresh); !same {
		return planChanged(ctx, txs, t, now,
			"the world moved between the decision and now: "+drift.Sentence(),
			t.Plan.PlanDigest, "", drift)
	}










	if appeared := appearedSince(frozen, fresh); len(appeared) > 0 {
		factsAppearedFor(ctx, t.ID, appeared)
	}
	if raw, err := json.Marshal(fresh); err == nil {
		if err := txs.RecordRecheckedFacts(ctx, t.ID, raw); err != nil {
			return t.State, fmt.Errorf("recording the re-checked facts of %s: %w", t.ID, err)
		}
	}




	plan, err := Compile(t.ID, p, frozen)
	if err != nil {
		return t.State, err
	}
	if plan.PlanDigest == t.Plan.PlanDigest {
		return t.State, nil
	}




	return planChanged(ctx, txs, t, now,
		"the plan compiled now is not the plan that was approved; no step was run",
		t.Plan.PlanDigest, plan.PlanDigest, nil)
}




































func sameFacts(frozen, fresh []GovernanceFact) (*FactDrift, bool) {
	for _, f := range frozen {
		n, ok := factNamed(fresh, f.Name)
		if !ok {
			return &FactDrift{Name: f.Name, AtDecision: factReading(f)}, false
		}
		if n.Value != f.Value || n.Source != f.Source {
			now := factReading(n)
			return &FactDrift{Name: f.Name, AtDecision: factReading(f), Now: &now}, false
		}
	}
	return nil, true
}



type FactReading struct {
	Value  string `json:"value"`
	Source string `json:"source"`





	ResolvedAt string `json:"resolved_at"`
}


















type FactDrift struct {
	Name       string       `json:"name"`
	AtDecision FactReading  `json:"at_decision"`
	Now        *FactReading `json:"now"`
}

func factReading(f GovernanceFact) FactReading {
	return FactReading{Value: f.Value, Source: f.Source, ResolvedAt: f.ResolvedAt}
}






func (d *FactDrift) Sentence() string {
	if d == nil {
		return ""
	}
	if d.Now == nil {
		return fmt.Sprintf("%s is no longer available (it was %q, answered by %q at %s)",
			d.Name, d.AtDecision.Value, d.AtDecision.Source, d.AtDecision.ResolvedAt)
	}
	if d.Now.Value != d.AtDecision.Value {
		return fmt.Sprintf("%s was %q at the decision and is %q now", d.Name, d.AtDecision.Value, d.Now.Value)
	}
	return fmt.Sprintf("%s was answered by %q at the decision and by %q now", d.Name, d.AtDecision.Source, d.Now.Source)
}





func appearedSince(frozen, fresh []GovernanceFact) []string {
	var names []string
	for _, f := range fresh {
		if _, ok := factNamed(frozen, f.Name); !ok {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	return names
}


func planChanged(ctx context.Context, txs spi.TransactionStore, t spi.Transaction, now time.Time, message, approved, current string, drift *FactDrift) (spi.TxState, error) {
	detail := map[string]any{"approved_plan_digest": approved}
	if current != "" {
		detail["current_plan_digest"] = current
	}




	if drift != nil {
		detail["fact_drift"] = drift
	}
	raw, err := json.Marshal(&OutcomeError{
		Code: CodePlanChanged, Message: message,
		Remediation: []Remedy{{Text: "Propose again; the person approves the new plan."}},
		Detail:      detail,
	})
	if err != nil {
		return t.State, err
	}
	if err := Transition(ctx, txs, spi.TxTransition{
		ID: t.ID, From: spi.TxApproved, To: spi.TxFailed, By: spi.TxByCore, At: now, Error: raw,
	}); err != nil {
		return t.State, err
	}
	return spi.TxFailed, nil
}
