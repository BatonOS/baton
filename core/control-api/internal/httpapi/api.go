// SPDX-License-Identifier: Apache-2.0












package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/control-api/internal/blob"
	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/config"
	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/control-api/internal/dispatch"
	"github.com/batonos/baton/core/control-api/internal/skills"
	"github.com/batonos/baton/core/control-api/internal/eventlog"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


const BasePath = "/api/v1alpha1"


type API struct {
	Cfg        config.Config
	Store      spi.Store
	CA         *ca.CA




	Blobs      *blob.Store
	Hub        *channel.Hub
	Dispatcher *dispatch.Dispatcher
	Log        *eventlog.Log
	Logger     *slog.Logger
	Version    string
	Edition    string
	Features   []string
	ClusterID  string
	Epoch      int64






	Now func() time.Time


	Promote func(ctx context.Context) (int64, error)


	EpochFn func() int64




	Facts core.FactResolver






















	RestartIdentityFn func() string





	InstallCA func(ctx context.Context, publicKey, payload, signature string) error


	Demote func(ctx context.Context, newMaster string) error
















	TransferOffered func(ctx context.Context) (bool, error)





	RecordTransferOffer func(ctx context.Context) error



	SkillCache *skills.Cache

	Sessions *sessionStore

	Mirror interface {
		LagSeconds() float64




		LastError() string
	}






	deliveriesInFlight sync.Map
}


type Error struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	RequestID   string         `json:"request_id"`
	Details     map[string]any `json:"details,omitempty"`
	Remediation string         `json:"remediation"`
}



const (
	CodeTokenNotFound     = "TOKEN_NOT_FOUND"
	CodeTokenExhausted    = "TOKEN_EXHAUSTED"
	CodeTokenRoleMismatch = "TOKEN_ROLE_MISMATCH"
	CodeNameInvalid       = "NAME_POLICY_VIOLATION"
	CodeNameConflict      = "NAME_CONFLICT"
	CodeClockSkew         = "CLOCK_SKEW"
	CodeNotFound          = "NOT_FOUND"



	CodeEndpointMissing = "ENDPOINT_MISSING"

	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeForbidden         = "FORBIDDEN"
	CodeUnauthorized      = "UNAUTHORIZED"
	CodeReadOnlyMirror    = "READONLY_MIRROR"









	CodeNoSigningAuthority = "NO_SIGNING_AUTHORITY"
	CodeNodeOffline       = "NODE_OFFLINE"




	CodeNodeSuspended = "NODE_SUSPENDED"
	CodeCapabilityUnknown = "CAPABILITY_NOT_FOUND"




	CodeCapabilityNotGranted = "CAPABILITY_NOT_GRANTED"
	CodeConfirmRequired   = "CONFIRMATION_UNSUPPORTED"
	CodeInvalidRequest    = "INVALID_REQUEST"
	CodeInternal          = "INTERNAL"
)






const MaxClockSkew = 120 * time.Second

type ctxKey string

const (
	ctxRequestID ctxKey = "request_id"
	ctxPrincipal ctxKey = "principal"
)


func (a *API) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		if err := json.NewEncoder(w).Encode(body); err != nil {
			a.Logger.Error("encode response", "error", err)
		}
	}
}




func (a *API) fail(w http.ResponseWriter, r *http.Request, status int, code, message, remediation string, details map[string]any) {
	requestID := requestIDFrom(r)
	a.Logger.Warn("request failed",
		"code", code, "status", status, "request_id", requestID,
		"path", r.URL.Path, "message", message)
	a.writeJSON(w, status, Error{
		Code:        code,
		Message:     message,
		RequestID:   requestID,
		Details:     details,
		Remediation: remediation,
	})
}



func (a *API) failInternal(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDFrom(r)
	a.Logger.Error("internal error", "request_id", requestID, "path", r.URL.Path, "error", err)
	a.writeJSON(w, http.StatusInternalServerError, Error{
		Code:      CodeInternal,
		Message:   "the control plane failed to handle this request",
		RequestID: requestID,
		Remediation: "Check the control plane logs for request_id " + requestID +
			", or run `baton doctor`.",
	})
}


func (a *API) failStore(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, spi.ErrNotFound):
		a.fail(w, r, http.StatusNotFound, CodeNotFound, "no such resource",
			"Check the identifier with `baton node list`.", nil)
	case errors.Is(err, spi.ErrReadOnly):

		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror",
			"Send writes to the primary. A Personal mirror is a backup, not a failover target.", nil)
	case errors.Is(err, spi.ErrInvalid):






		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			strings.TrimPrefix(err.Error(), spi.ErrInvalid.Error()+": "),
			"Fix the value and try again.", nil)
	case errors.Is(err, spi.ErrConflict):
		a.fail(w, r, http.StatusConflict, CodeNameConflict, err.Error(),
			"Choose a different name, or remove the existing object first.", nil)
	default:
		a.failInternal(w, r, err)
	}
}

func requestIDFrom(r *http.Request) string {
	if v, ok := r.Context().Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}




func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}







func actingForFrom(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("X-Baton-Acting-For"))
	v = strings.Map(func(c rune) rune {
		if c < ' ' {
			return -1
		}
		return c
	}, v)
	if len(v) > 128 {
		v = v[:128]
	}
	return v
}



func (a *API) epochNow() int64 {
	if a.EpochFn != nil {
		return a.EpochFn()
	}
	return a.Epoch
}

func principalFrom(r *http.Request) (auth.Principal, bool) {
	p, ok := r.Context().Value(ctxPrincipal).(auth.Principal)
	return p, ok
}


func (a *API) requireRole(w http.ResponseWriter, r *http.Request, need console.Role) (auth.Principal, bool) {
	p, ok := principalFrom(r)
	if !ok {
		a.fail(w, r, http.StatusUnauthorized, CodeUnauthorized,
			"no client certificate was presented",
			"Use the admin certificate written by `baton setup master` (or `baton agent create`, which founds one).", nil)
		return auth.Principal{}, false
	}
	if !auth.RequireRole(p, need) {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this credential is not permitted to perform that action",
			"This action needs the "+string(need)+" role.",
			map[string]any{"have": string(p.Role), "need": string(need)})
		return auth.Principal{}, false
	}
	return p, true
}


func (a *API) decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {




	return a.decodeJSONWithin(w, r, v, 4<<20)
}






func (a *API) decodeJSONWithin(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"request body could not be read: "+err.Error(),
			"Check the request against the OpenAPI contract in packages/api-contracts/openapi.yaml.", nil)
		return false
	}
	return true
}


func queryInt(r *http.Request, name string, def, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || strings.EqualFold(v, "true")
}


func NewSessionStore() *sessionStore { return newSessionStore() }


func newRequestID() string { return "req_" + uuid.NewString() }




const ClusterIDPending = "pending-first-snapshot"




func clusterIDOrNull(id string) any {
	if id == "" || id == ClusterIDPending {
		return nil
	}
	return id
}



func clusterState(id string) string {
	if id == "" || id == ClusterIDPending {
		return ClusterIDPending
	}
	return "ready"
}
