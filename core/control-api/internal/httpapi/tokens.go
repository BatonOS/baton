// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type tokenCreateRequest struct {
	Role        string `json:"role"`
	TTL         string `json:"ttl"`
	MaxUses     int    `json:"max_uses"`
	NamePattern string `json:"name_pattern"`
}






func (a *API) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}

	var req tokenCreateRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}

	role := spi.Role(req.Role)
	if !role.Valid() {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"role "+req.Role+" is not a role this control plane issues",




			"The roles are `master` and `agent`. A standby enrols with a master-role token; "+
				"an agent is admitted by name rather than tokened: it applies, and the master approves its pending application.", nil)
		return
	}

	ttl := 15 * time.Minute
	if req.TTL != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
				"ttl "+req.TTL+" is not a duration",
				`Use a Go duration such as 15m, 1h, or 24h.`, nil)
			return
		}
		if parsed <= 0 || parsed > 24*time.Hour {


			a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
				"ttl must be between 1s and 24h, got "+req.TTL,
				"Enrollment tokens are meant to be short-lived; use 15m unless you have a reason.", nil)
			return
		}
		ttl = parsed
	}

	maxUses := req.MaxUses
	if maxUses <= 0 {
		maxUses = 1
	}

	if _, err := auth.MatchesNamePattern(req.NamePattern, "probe"); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest, err.Error(),
			"Fix the name pattern, or omit it to allow any valid node name.", nil)
		return
	}

	plaintext, hash, err := auth.NewToken()
	if err != nil {
		a.failInternal(w, r, err)
		return
	}

	token := &spi.EnrollmentToken{
		Purpose:  spi.TokenPurposeNode,
		TokenID:     "tok_" + uuid.NewString(),
		TenantID:    spi.DefaultTenant,
		TokenHash:   hash,
		Roles:       spi.Roles{role},
		NamePattern: req.NamePattern,
		MaxUses:     maxUses,
		ExpiresAt:   time.Now().Add(ttl),
		CreatedBy:   p.Subject,
	}
	if err := a.Store.Tokens().Create(r.Context(), token); err != nil {
		a.failStore(w, r, err)
		return
	}


	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "token.created", Actor: p.Subject, ActorType: "user",
		Action: "create", Target: token.TokenID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
		Detail: map[string]any{
			"role": string(role), "max_uses": maxUses, "ttl": ttl.String(),
		},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, map[string]any{
		"token_id":   token.TokenID,
		"token":      plaintext,
		"expires_at": token.ExpiresAt,
		"note": "This is the only time the token is shown. Write it to a file with " +
			"mode 0600; it must never appear in a command line or shell history.",
	})
}

func (a *API) handleTokenList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	tokens, err := a.Store.Tokens().List(r.Context(), "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(tokens))
	for _, t := range tokens {


		items = append(items, map[string]any{
			"token_id": t.TokenID, "roles": rolesOf(t.Roles),
			"max_uses": t.MaxUses, "used_count": t.UsedCount,
			"expires_at": t.ExpiresAt, "revoked_at": t.RevokedAt,
			"name_pattern": t.NamePattern,
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	tokenID := r.PathValue("token_id")

	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "token.revoked", Actor: p.Subject, ActorType: "user",
		Action: "revoke", Target: tokenID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	if err := a.Store.Tokens().Revoke(r.Context(), tokenID); err != nil {
		a.failStore(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
