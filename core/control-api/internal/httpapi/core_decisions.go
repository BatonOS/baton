// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)





const (
	CodeTransactionNotPending = "TRANSACTION_NOT_PENDING"
	CodePlanDigestMismatch    = "PLAN_DIGEST_MISMATCH"
	CodePlanUnseen            = "PLAN_UNSEEN"




	CodeAnonymousDecision = "ANONYMOUS_DECISION"
	CodeSelfDecision      = "SELF_DECISION"
	CodeDecisionSurface   = "DECISION_SURFACE_REQUIRED"
)

type decisionBody struct {
	PlanDigest string `json:"plan_digest"`
	Note       string `json:"note"`
}

























func (a *API) handleCoreTransactionDecide(verdict spi.TxState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
		if !ok {
			return
		}
		actingFor := actingForFrom(r)
		if !strings.HasPrefix(actingFor, "panel-session:") {
			a.fail(w, r, http.StatusForbidden, CodeDecisionSurface,
				"a decision is taken on the local approval page, and this request did not come through it",
				"Open the approval page in `baton panel` and decide there. (The label this checks is not a credential: it says where the request says it came from.)",
				map[string]any{"acting_for": actingFor})
			return
		}
		var body decisionBody
		if !a.decodeJSON(w, r, &body) {
			return
		}
		if body.PlanDigest == "" {
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"plan_digest is required: a decision is about one version of the plan",
				"Send the plan_digest of the plan the person was shown — the one in the same Outcome as the plan.", nil)
			return
		}
		id := r.PathValue("id")
		now := time.Now().UTC()
		verb := "approve"
		if verdict == spi.TxRejected {
			verb = "reject"
		}
		var decided *spi.Transaction
		err := a.Store.WithTx(r.Context(), func(s spi.Store) error {
			t, err := core.Decide(r.Context(), s.Transactions(), id, core.Decision{
				Decision: verdict, PlanDigest: body.PlanDigest, DecidedBy: p.Subject,
				DecidedAt: now, Surface: "local-web", Comment: body.Note,
			})
			if err != nil {
				return err
			}
			if _, err := a.Log.In(s.Events()).Append(r.Context(), audit.Record{
				Event: "core.transaction." + verb, Actor: p.Subject, ActorType: "user", ActingFor: actingFor,
				Action: verb, Target: id, Result: "ok", SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: now,
				Detail: map[string]any{"plan_digest": body.PlanDigest, "note": body.Note},
			}); err != nil {
				return err
			}
			decided = t
			return nil
		})
		switch {
		case err == nil:
			a.writeJSON(w, http.StatusOK, core.OutcomeOf(decided))
		case errors.Is(err, spi.ErrNotFound):
			a.fail(w, r, http.StatusNotFound, CodeNotFound, "no transaction with this id",
				"Check the transaction id: it is the `transaction` field of the Outcome the proposal returned.", nil)
		case errors.Is(err, core.ErrDecisionExpired):



			a.fail(w, r, http.StatusConflict, core.CodeApprovalExpired,
				"the approval expired before this decision; expiry is not a verdict",
				"Nothing was approved. The proposer can propose again.", nil)
		case errors.Is(err, core.ErrAnonymousDecision):




			a.fail(w, r, http.StatusForbidden, CodeAnonymousDecision,
				"this decision has no named decider or no named proposer, so self-approval could not be checked",
				"Present a certificate whose Common Name names the subject. ⚠ This is ⊘ a refusal of YOUR decision — it is a refusal to decide without being able to tell the two parties apart.",
				nil)
		case errors.Is(err, core.ErrSelfDecision):

			a.fail(w, r, http.StatusForbidden, CodeSelfDecision,
				"the party that proposed this cannot be the party that decides it",
				"Someone other than the proposer has to read the plan and decide.", nil)
		case errors.Is(err, core.ErrStaleDecision):
			a.fail(w, r, http.StatusConflict, CodePlanDigestMismatch,
				"this decision names a plan that is not the one awaiting approval",
				"The page was out of date. Reload it, read the plan again, and decide on what it shows now.", nil)
		case errors.Is(err, core.ErrPlanUnseen):
			a.fail(w, r, http.StatusUnprocessableEntity, CodePlanUnseen,
				"the plan awaiting approval cannot be read, so it cannot have been shown to anyone",
				"Nothing was decided. The plan did not change — it cannot be displayed; report it.", nil)
		case errors.Is(err, core.ErrNotPending):
			a.fail(w, r, http.StatusConflict, CodeTransactionNotPending,
				"this transaction is not waiting for a decision",
				"Read its Outcome: it was decided already, expired, or withdrawn. The first verdict stands.", nil)
		case errors.Is(err, core.ErrUnsupportedDecision):
			a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error(), "Approve or reject.", nil)
		default:
			a.failStore(w, r, err)
		}
	}
}
