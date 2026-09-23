// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/pkg/spi/console"
)










func (a *API) handleCoreStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	a.writeJSON(w, http.StatusOK, core.CurrentStatus(a.Version))
}
