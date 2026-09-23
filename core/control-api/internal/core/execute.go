// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/eventlog"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const CodeExecutionFailed = "EXECUTION_FAILED"


const (
	FailureProviderError = "provider-error"
	FailureTimeout       = "timeout"
)










const AwaitEnrolledWindow = 10 * time.Minute


var ErrNotApproved = errors.New("core: only an approved transaction is executed")








type Executor struct {
	Store    spi.Store
	Provider *ProviderClient







	Apps map[string]*ProviderClient


	Log       *eventlog.Log
	Now       func() time.Time
	PollEvery time.Duration




	Logger *slog.Logger
}

func (e *Executor) note(msg string, id string, st spi.TxStep, attrs ...any) {
	if e.Logger == nil {
		return
	}
	e.Logger.Info(msg, append([]any{"transaction_id", id, "operation_id", st.OperationID, "step", st.Name}, attrs...)...)
}


type stepRun struct {
	status   spi.StepStatus
	evidence json.RawMessage
	class    string




	dispatch string
}



const (
	DispatchNotSent          = "not_sent"
	DispatchRefusedOnArrival = "refused_on_arrival"
	DispatchSentNoAnswer     = "sent_no_answer"
	DispatchMayHaveBeenSent  = "may_have_been_sent"
	DispatchAnswered         = "answered"
	DispatchCoreStep         = "core_step"
)




func (e *Executor) Execute(ctx context.Context, id string) (*spi.Transaction, error) {
	txs := e.Store.Transactions()
	t, err := txs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.State != spi.TxApproved {
		return t, fmt.Errorf("%w: %s is %s", ErrNotApproved, id, t.State)
	}


	if state, err := recompile(ctx, txs, *t, e.Now()); err != nil {
		return nil, err
	} else if state != spi.TxApproved {
		return txs.Get(ctx, id)
	}
	if err := Transition(ctx, txs, spi.TxTransition{ID: id, From: spi.TxApproved, To: spi.TxExecuting, By: spi.TxByCore, At: e.Now()}); err != nil {
		return nil, err
	}
	return e.run(ctx, id)
}


var ErrNotExecuting = errors.New("core: only an executing transaction is resumed")















func (e *Executor) Resume(ctx context.Context, id string) (*spi.Transaction, error) {
	t, err := e.Store.Transactions().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.State != spi.TxExecuting {
		return t, fmt.Errorf("%w: %s is %s", ErrNotExecuting, id, t.State)
	}
	return e.run(ctx, id)
}




func (e *Executor) run(ctx context.Context, id string) (*spi.Transaction, error) {
	txs := e.Store.Transactions()
	t, err := txs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p, _, err := parse(t.Proposal)
	if err != nil {
		return nil, err
	}
	target, _ := p["target"].(map[string]any)
	src, _ := target["id"].(string)
	params, _ := p["parameters"].(map[string]any)
	dst, _ := params["name"].(string)

	steps := t.Plan.Steps
	for i, st := range steps {
		switch st.Status {
		case spi.StepSucceeded, spi.StepSkipped:
			continue
		case spi.StepFailed, spi.StepUnknown:


			return e.fail(ctx, id, steps, i, stepRun{status: st.Status, evidence: st.Evidence, class: FailureProviderError})
		}
		var run stepRun













		if strings.HasPrefix(st.Executor, ExecutorAppPrefix) {
			run = e.dispatch(ctx, id, t.Actor, st, appParams(t.ActionName, params))
			if run.status != spi.StepSucceeded {
				return e.fail(ctx, id, steps, i, run)
			}
			continue
		}
		switch st.Name {
		case "capture":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"source": src})
		case "place":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"source": src, "name": dst})
		case "enroll":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"name": dst})
		case "materialize":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"name": dst, "archive_operation_id": steps[0].OperationID})


		case "file.write":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{
				"workspace": src, "path": str(params["path"]), "content": str(params["content"]),
				"expected_sha_before": str(params["expected_sha_before"]),
			})
		case "test.run":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"workspace": src, "path": str(params["test_path"])})



		case "provision":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{
				"name": str(params["name"]), "harness": str(params["harness"]),
				"placement": str(params["placement"]),
			})
		case "runtime.read":
			run = e.dispatch(ctx, id, t.Actor, st, map[string]string{"workspace": src})
		case "admit", "await_enrolled":
			if st.Status == spi.StepRunning {



				run = e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError,
					evidence: evidenceOf(map[string]any{"reason": "Core restarted during its own step " + st.Name + "; resuming it is not built (P1)"})})
			} else if st.Name == "admit" {
				run = e.admit(ctx, t, st, requestIDOf(ctx, txs, id))
			} else {
				run = e.awaitEnrolled(ctx, id, st, requestIDOf(ctx, txs, id), dst)
			}
		default:
			run = e.refuseStep(ctx, id, st, fmt.Sprintf("this build has no executor for a step named %q", st.Name))
		}
		if run.status != spi.StepSucceeded {
			return e.fail(ctx, id, steps, i, run)
		}
	}
	if err := Transition(ctx, txs, spi.TxTransition{ID: id, From: spi.TxExecuting, To: spi.TxCompleted, By: spi.TxByCore, At: e.Now()}); err != nil {
		return nil, err
	}
	return txs.Get(ctx, id)
}














func (e *Executor) clientFor(executor string) (*ProviderClient, error) {
	if executor == ExecutorLocalProvider {
		return e.Provider, nil
	}
	if id, ok := strings.CutPrefix(executor, ExecutorAppPrefix); ok {
		if c := e.Apps[id]; c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("%w: no executor is wired for app %q", ErrNoExecutor, id)
	}
	return nil, fmt.Errorf("%w: %q", ErrNoExecutor, executor)
}



var ErrNoExecutor = errors.New("core: no executor for this step")














func (e *Executor) dispatch(ctx context.Context, id, subject string, st spi.TxStep, params map[string]string) stepRun {
	txs := e.Store.Transactions()
	resuming := st.Status == spi.StepRunning
	client, cerr := e.clientFor(st.Executor)
	if cerr != nil {



		return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
			evidence: evidenceOf(map[string]any{"reason": cerr.Error(), "executor": st.Executor})})
	}
	q, qerr := client.Query(ctx, st.OperationID)
	e.note("query before dispatch", id, st, "resuming", resuming, "answer", queryShape(q, qerr))




	notSent := func(reason string, detail map[string]any) stepRun {
		detail["reason"] = reason
		detail["dispatch"] = DispatchNotSent
		if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now()}); err != nil {
			return stepRun{status: spi.StepUnknown, class: FailureProviderError, evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running", "error": err.Error()})}
		}
		return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError, evidence: evidenceOf(detail), dispatch: DispatchNotSent})
	}


	cannotTell := func(reason string, detail map[string]any) stepRun {
		detail["reason"] = reason
		detail["dispatch"] = DispatchMayHaveBeenSent
		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError, evidence: evidenceOf(detail), dispatch: DispatchMayHaveBeenSent})
	}

	if qerr != nil {
		if resuming {
			return cannotTell("the Provider could not be asked what became of this step; whether it ran is not known", map[string]any{"error": qerr.Error()})
		}
		return notSent("the Provider was asked before dispatching and gave no answer; the step was not sent", map[string]any{"error": qerr.Error()})
	}
	if q.Answer != nil {



		if !resuming {
			if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now()}); err != nil {
				return stepRun{status: spi.StepUnknown, class: FailureProviderError, evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running", "error": err.Error()})}
			}
		}
		return e.adopt(ctx, id, st, q.Answer, st.Nonce)
	}
	if q.ReceivedNoResult {





		detail := map[string]any{"answering_instance": q.ProviderInstanceID}
		if resuming {
			return cannotTell("the Provider has received this step and has no result for it yet; it was not sent again, and how it ends is not known", detail)
		}
		if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now()}); err != nil {
			return stepRun{status: spi.StepUnknown, class: FailureProviderError, evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running", "error": err.Error()})}
		}
		detail["reason"] = "Core never sent this step, and the Provider has a request for it in hand: somebody else sent it, and how it ends is not known here"
		detail["dispatch"] = DispatchNotSent
		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError, evidence: evidenceOf(detail), dispatch: DispatchNotSent})
	}


	pinned, err := e.pin(ctx, id, q.ProviderInstanceID)
	if err != nil || pinned != q.ProviderInstanceID {
		detail := map[string]any{"answering_instance": q.ProviderInstanceID, "pinned_instance": pinned}
		if err != nil {
			detail["error"] = err.Error()
		}
		if resuming {
			return cannotTell("the Provider that says it has no record is not the one this transaction dealt with; its answer says nothing about this step", detail)
		}
		return notSent("the Provider answering is not the one this transaction dealt with; the step was not sent", detail)
	}

	nonce := st.Nonce
	if !resuming {
		nonce = newNonce()
		if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now(), Nonce: nonce}); err != nil {
			return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError, dispatch: DispatchNotSent,
				evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running, so it was not sent", "dispatch": DispatchNotSent, "error": err.Error()})})
		}
	}
	e.note("execute", id, st, "nonce_set", nonce != "")
	ans, err := client.Execute(ctx, st.OperationID, st.Name, subject, nonce, params)
	if err != nil && !errors.Is(err, ErrProviderNotReached) && !errors.As(err, new(*ProviderRefused)) {



		e.note("execute answer lost; querying", id, st, "error", err.Error())
		if recovered, ok := e.recoverLostAnswer(ctx, id, subject, st, nonce, pinned, params); ok {
			return recovered
		}
	}
	var refused *ProviderRefused
	switch {
	case errors.Is(err, ErrProviderNotReached):
		return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError, dispatch: DispatchNotSent,
			evidence: evidenceOf(map[string]any{"reason": "the local Provider could not be reached; the step was not sent", "dispatch": DispatchNotSent, "error": err.Error()})})
	case errors.As(err, &refused):
		return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError, dispatch: DispatchRefusedOnArrival,
			evidence: evidenceOf(map[string]any{"reason": "the local Provider refused the step before recording it", "dispatch": DispatchRefusedOnArrival, "code": refused.Code, "message": refused.Message})})
	case err != nil:

		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError, dispatch: DispatchSentNoAnswer,
			evidence: evidenceOf(map[string]any{"reason": "the step was sent and no answer came back; whether it ran is not known", "dispatch": DispatchSentNoAnswer, "error": err.Error()})})
	}
	if ans.ProviderInstanceID != pinned {
		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError, dispatch: DispatchAnswered,
			evidence: evidenceOf(map[string]any{"reason": "the answer came from another Provider instance than the one asked a moment before", "dispatch": DispatchAnswered, "answer": ans, "pinned_instance": pinned})})
	}
	return e.adopt(ctx, id, st, ans, nonce)
}











func (e *Executor) recoverLostAnswer(ctx context.Context, id, subject string, st spi.TxStep, nonce, pinned string, params map[string]string) (stepRun, bool) {
	client, cerr := e.clientFor(st.Executor)
	if cerr != nil {
		return stepRun{}, false
	}
	q, err := client.Query(ctx, st.OperationID)
	e.note("query after lost answer", id, st, "answer", queryShape(q, err))
	if err != nil {
		return stepRun{}, false
	}
	if q.Answer != nil {
		return e.adopt(ctx, id, st, q.Answer, nonce), true
	}
	if q.ReceivedNoResult {
		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureProviderError, dispatch: DispatchSentNoAnswer,
			evidence: evidenceOf(map[string]any{"reason": "the answer was lost, and the Provider has the request and no result for it yet; it was not sent again, and how it ends is not known",
				"dispatch": DispatchSentNoAnswer, "answering_instance": q.ProviderInstanceID})}), true
	}
	if q.ProviderInstanceID != pinned {
		return stepRun{}, false
	}
	e.note("execute again: the first request never reached the Provider", id, st)
	ans, err := client.Execute(ctx, st.OperationID, st.Name, subject, nonce, params)
	if err != nil || ans.ProviderInstanceID != pinned {
		return stepRun{}, false
	}
	return e.adopt(ctx, id, st, ans, nonce), true
}


func queryShape(q *QueryResult, err error) string {
	switch {
	case errors.Is(err, ErrProviderNotReached):
		return "not_reached"
	case errors.Is(err, ErrNotAnAnswer):
		return "not_an_answer"
	case err != nil:
		return "error"
	case q.NotFound:
		return "not_found:" + q.ProviderInstanceID
	case q.ReceivedNoResult:
		return "received_no_result:" + q.ProviderInstanceID
	default:
		return "recorded:" + q.Answer.Status
	}
}




func (e *Executor) adopt(ctx context.Context, id string, st spi.TxStep, ans *ProviderAnswer, nonce string) stepRun {
	raw, _ := json.Marshal(ans)
	run := stepRun{status: spi.StepStatus(ans.Status), evidence: raw, dispatch: DispatchAnswered}
	if nonce == "" || ans.Nonce != nonce {
		run.status = spi.StepUnknown
		run.evidence = evidenceOf(map[string]any{"reason": "the Provider's answer does not carry the nonce this step was dispatched with", "answer": ans})
	}
	if run.status != spi.StepSucceeded {
		run.class = FailureProviderError
	}
	return e.end(ctx, id, st, run)
}



func (e *Executor) pin(ctx context.Context, id, instance string) (string, error) {
	txs := e.Store.Transactions()
	err := txs.PinProviderInstance(ctx, id, instance)
	if err != nil && !errors.Is(err, spi.ErrConflict) {
		return "", err
	}
	t, gerr := txs.Get(ctx, id)
	if gerr != nil {
		return "", gerr
	}
	return t.ProviderInstanceID, nil
}





func (e *Executor) admit(ctx context.Context, t *spi.Transaction, st spi.TxStep, requestID string) stepRun {
	txs := e.Store.Transactions()
	if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: t.ID, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now()}); err != nil {
		return e.end(ctx, t.ID, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
			evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running", "error": err.Error()})})
	}
	if requestID == "" {
		return e.end(ctx, t.ID, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
			evidence: evidenceOf(map[string]any{"reason": "enroll recorded no request_id, so there is no application of this operation's to admit"})})
	}
	approvedBy := decidedBy(t)
	now := e.Now()
	evidence := evidenceOf(map[string]any{"request_id": requestID, "admitted_by": "core", "on_approval_by": approvedBy})
	err := e.Store.WithTx(ctx, func(s spi.Store) error {
		if _, err := s.JoinRequests().ExpirePending(ctx, now); err != nil {
			return err
		}
		if err := s.JoinRequests().Decide(ctx, requestID, spi.JoinAdmitted, "", "core:"+t.ID); err != nil {
			return err
		}
		if _, err := e.Log.In(s.Events()).Append(ctx, audit.Record{
			Event: "join.admit", Actor: "core", ActorType: "service", ActingFor: approvedBy,
			Action: "admit", Target: requestID, Result: "ok", At: now,
			Detail: map[string]any{"transaction": t.ID, "operation_id": st.OperationID},
		}); err != nil {
			return err
		}
		return s.Transactions().UpdateStep(ctx, spi.TxStepUpdate{ID: t.ID, Index: st.Index, From: spi.StepRunning, To: spi.StepSucceeded, At: now, Evidence: evidence})
	})
	if err == nil {
		return stepRun{status: spi.StepSucceeded, evidence: evidence}
	}

	return e.end(ctx, t.ID, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
		evidence: evidenceOf(map[string]any{"reason": "the application could not be admitted; nothing was changed", "request_id": requestID, "error": err.Error()})})
}






func (e *Executor) awaitEnrolled(ctx context.Context, id string, st spi.TxStep, requestID, node string) stepRun {
	txs := e.Store.Transactions()
	started := e.Now()
	if err := txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: started}); err != nil {
		return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureTimeout,
			evidence: evidenceOf(map[string]any{"reason": "the step could not be marked running, so the wait did not start", "error": err.Error()})})
	}
	if requestID == "" {
		return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
			evidence: evidenceOf(map[string]any{"reason": "enroll recorded no request_id, so there is no application of this operation's to watch"})})
	}
	deadline := started.Add(AwaitEnrolledWindow)
	var lastErr error
	for {
		jr, err := e.Store.JoinRequests().Get(ctx, requestID)
		switch {
		case errors.Is(err, spi.ErrNotFound):
			return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
				evidence: evidenceOf(map[string]any{"reason": "the application this operation lodged no longer exists", "request_id": requestID})})
		case err != nil:
			lastErr = err
		default:
			lastErr = nil
			switch jr.State {
			case spi.JoinDenied, spi.JoinExpired:
				return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError,
					evidence: evidenceOf(map[string]any{"reason": "the application ended " + string(jr.State) + " before the node joined", "request_id": requestID, "application_state": jr.State})})
			case spi.JoinConsumed:
				if n, nerr := e.Store.Nodes().GetByName(ctx, spi.DefaultTenant, node); nerr == nil && n.Status == spi.NodeStatusActive {
					return e.end(ctx, id, st, stepRun{status: spi.StepSucceeded,
						evidence: evidenceOf(map[string]any{"request_id": requestID, "application_state": jr.State, "node_id": n.NodeID, "node_status": n.Status})})
				}
			}
		}
		if !e.Now().Before(deadline) {
			if lastErr != nil {
				return e.end(ctx, id, st, stepRun{status: spi.StepUnknown, class: FailureTimeout,
					evidence: evidenceOf(map[string]any{"reason": "the application could not be read, so whether the node joined is not known", "request_id": requestID, "error": lastErr.Error()})})
			}
			return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureTimeout,
				evidence: evidenceOf(map[string]any{"reason": node + " had not joined by the deadline", "request_id": requestID, "deadline": deadline.UTC().Format(time.RFC3339)})})
		}
		select {
		case <-ctx.Done():
			return e.end(context.WithoutCancel(ctx), id, st, stepRun{status: spi.StepUnknown, class: FailureTimeout,
				evidence: evidenceOf(map[string]any{"reason": "the wait was interrupted", "error": ctx.Err().Error()})})
		case <-time.After(e.PollEvery):
		}
	}
}

func (e *Executor) refuseStep(ctx context.Context, id string, st spi.TxStep, reason string) stepRun {
	_ = e.Store.Transactions().UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepNotStarted, To: spi.StepRunning, At: e.Now()})
	return e.end(ctx, id, st, stepRun{status: spi.StepFailed, class: FailureProviderError, evidence: evidenceOf(map[string]any{"reason": reason})})
}




func (e *Executor) end(ctx context.Context, id string, st spi.TxStep, run stepRun) stepRun {
	e.note("step ended", id, st, "status", string(run.status), "dispatch", run.dispatch)
	if err := e.Store.Transactions().UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: st.Index, From: spi.StepRunning, To: run.status, At: e.Now(), Evidence: run.evidence}); err != nil {
		return stepRun{status: spi.StepUnknown, class: FailureProviderError,
			evidence: evidenceOf(map[string]any{"reason": "how the step ended could not be recorded", "would_have_been": run.status, "error": err.Error()})}
	}
	return run
}



func (e *Executor) fail(ctx context.Context, id string, steps []spi.TxStep, stopped int, run stepRun) (*spi.Transaction, error) {
	txs := e.Store.Transactions()
	for _, later := range steps[stopped+1:] {
		_ = txs.UpdateStep(ctx, spi.TxStepUpdate{ID: id, Index: later.Index, From: spi.StepNotStarted, To: spi.StepSkipped, At: e.Now()})
	}
	st := steps[stopped]
	msg := "step " + st.Name + " failed"
	remedy := "Read the step's evidence in the transaction record; propose again once the cause is dealt with."
	if run.status == spi.StepUnknown {
		msg = "whether step " + st.Name + " took effect is not known"
		remedy = "Check what exists before proposing again: the step may have run. Nothing retries it on its own (L1)."
	}





	dispatch := run.dispatch
	if dispatch == "" {
		if st.Executor == ExecutorCore {
			dispatch = DispatchCoreStep
		} else {



			var ev struct {
				Dispatch string `json:"dispatch"`
			}
			if json.Unmarshal(run.evidence, &ev) == nil && ev.Dispatch != "" {
				dispatch = ev.Dispatch
			} else {
				dispatch = DispatchAnswered
			}
		}
	}
	nonce := ""
	if now, err := txs.Get(ctx, id); err == nil && now.Plan != nil && stopped < len(now.Plan.Steps) {
		nonce = now.Plan.Steps[stopped].Nonce
	}
	raw, err := json.Marshal(&OutcomeError{
		Code: CodeExecutionFailed, Message: msg,
		Remediation: []Remedy{{Text: remedy}},
		Detail: map[string]any{
			"step": st.Name, "step_index": st.Index, "step_status": string(run.status),
			"failure_class": run.class, "reason": plannerSafeReason(run.evidence, nonce), "dispatch": dispatch,
		},
	})
	if err != nil {
		return nil, err
	}
	if err := Transition(ctx, txs, spi.TxTransition{ID: id, From: spi.TxExecuting, To: spi.TxFailed, By: spi.TxByCore, At: e.Now(), Error: raw}); err != nil {
		return nil, err
	}
	return txs.Get(ctx, id)
}





func plannerSafeReason(evidence json.RawMessage, nonce string) string {
	var e struct {
		Reason   string `json:"reason"`
		Evidence struct {
			Reason string `json:"reason"`
		} `json:"evidence"`
	}
	_ = json.Unmarshal(evidence, &e)
	reason := e.Reason
	if reason == "" {
		reason = e.Evidence.Reason
	}
	if nonce != "" && strings.Contains(reason, nonce) {
		return "withheld here because it quotes the step nonce; read it on the transaction record"
	}
	return reason
}



func requestIDOf(ctx context.Context, txs spi.TransactionStore, id string) string {
	t, err := txs.Get(ctx, id)
	if err != nil || t.Plan == nil {
		return ""
	}
	for _, st := range t.Plan.Steps {
		if st.Name != "enroll" || st.Status != spi.StepSucceeded {
			continue
		}
		var a struct {
			Evidence struct {
				RequestID string `json:"request_id"`
			} `json:"evidence"`
		}
		if json.Unmarshal(st.Evidence, &a) == nil {
			return a.Evidence.RequestID
		}
	}
	return ""
}

func decidedBy(t *spi.Transaction) string {
	var d Decision
	if json.Unmarshal(t.Approval.Decision, &d) == nil {
		return d.DecidedBy
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func newNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("core: no randomness for a step nonce: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func evidenceOf(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}









func appParams(action string, params map[string]any) map[string]string {
	decl, ok := lookupAction(action)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(decl.Params))
	for _, spec := range decl.Params {
		if v, present := params[spec.Name]; present {
			out[spec.Name] = str(v)
		}
	}
	return out
}

