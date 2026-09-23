// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/pkg/spi/console"
)



const maxProposalBytes = 1 << 20
























func (a *API) handleCoreProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProposalBytes))
	if err != nil {
		a.fail(w, r, http.StatusRequestEntityTooLarge, CodeInvalidRequest,
			"the proposal is larger than this control plane accepts",
			"A proposal carries an action and its parameters, never workspace content.", nil)
		return
	}




	t, created, err := core.Submit(core.WithFacts(r.Context(), a.Facts), core.Deps{Transactions: a.Store.Transactions(), Interactions: a.Store.Interactions(), Nodes: a.Store.Nodes(), Grants: a.Store.Grants()}, p.Subject, body, time.Now())
	if errors.Is(err, core.ErrNotAnObject) {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the body is not a JSON object",
			"Send a Proposal: {kind: \"proposal\", contract_version, source, idempotency_key, via, action, …}.", nil)
		return
	}





	var ng *core.NotGrantedError
	if errors.As(err, &ng) {
		remedy := ""
		if len(ng.Outcome.Remediation) > 0 {
			remedy = ng.Outcome.Remediation[0].Text
		}
		a.fail(w, r, http.StatusForbidden, ng.Outcome.Code, ng.Outcome.Message, remedy, ng.Outcome.Detail)
		return
	}
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	a.writeJSON(w, status, core.OutcomeOf(t))
}
