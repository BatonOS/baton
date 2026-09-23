// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)



func (a *API) handleCoreTransactionGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	t, ok := a.coreTransaction(w, r)
	if !ok {
		return
	}
	a.writeJSON(w, http.StatusOK, core.OutcomeOf(t))
}









func (a *API) handleCoreTransactionRecord(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	t, ok := a.coreTransaction(w, r)
	if !ok {
		return
	}
	a.writeJSON(w, http.StatusOK, core.RecordOf(t))
}



const CodeTransactionNotTerminal = "TRANSACTION_NOT_TERMINAL"





func (a *API) handleCoreTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	t, ok := a.coreTransaction(w, r)
	if !ok {
		return
	}
	rc, done := core.ReceiptOf(t)
	if !done {
		a.fail(w, r, http.StatusConflict, CodeTransactionNotTerminal,
			"this transaction has not finished, so it has no receipt yet",
			"Read its Outcome for where it is now; the receipt exists once it is completed, failed, rejected, expired, modified or withdrawn.",
			map[string]any{"state": string(t.State)})
		return
	}
	a.writeJSON(w, http.StatusOK, rc)
}








func (a *API) handleCoreContext(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleViewer)
	if !ok {
		return
	}
	c, err := core.ContextOf(r.Context(), a.Store.Transactions(), p.Subject)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, c)
}





func (a *API) coreTransaction(w http.ResponseWriter, r *http.Request) (*spi.Transaction, bool) {
	t, err := a.Store.Transactions().Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, spi.ErrNotFound) {
		a.fail(w, r, http.StatusNotFound, CodeNotFound, "no transaction with this id",
			"Check the transaction id: it is the `transaction` field of the Outcome the proposal returned.", nil)
		return nil, false
	}
	if err != nil {
		a.failStore(w, r, err)
		return nil, false
	}
	return t, true
}


































func (a *API) handleCoreTransactionsList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}


	if s := r.URL.Query().Get("status"); s != string(spi.TxPendingApproval) {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"this route answers one question: ?status=pending_approval",
			"Add `?status=pending_approval`. It is ⊘ a general list — see the route's contract.", nil)
		return
	}
	txs, err := a.Store.Transactions().ListNonTerminal(r.Context())
	if err != nil {
		a.failStore(w, r, err)
		return
	}





	out := make([]pendingApproval, 0, len(txs))
	for i := range txs {
		t := &txs[i]
		if t.State != spi.TxPendingApproval {
			continue
		}



		o := core.OutcomeOf(t)
		out = append(out, pendingApproval{
			Transaction: t.ID,
			Action:      pendingAction{Name: o.Action.Name, Summary: o.Action.Summary},
			ExpiresAt:   t.Approval.ExpiresAt,
		})
	}
	a.writeJSON(w, http.StatusOK, out)
}

type pendingApproval struct {
	Transaction string        `json:"transaction"`
	Action      pendingAction `json:"action"`


	ExpiresAt *time.Time `json:"expires_at"`
}

type pendingAction struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}
