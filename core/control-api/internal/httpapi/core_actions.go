// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/pkg/spi/console"
)











func (a *API) handleCoreActions(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	entries, err := core.Catalogue(core.WithFacts(r.Context(), a.Facts), a.Store.Grants(), p.Subject, time.Now())
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	a.writeJSON(w, http.StatusOK, map[string]any{"actions": entries})
}
