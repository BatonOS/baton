// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/control-api/internal/auth"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)











const (
	CodeJoinRequestNotFound = "JOIN_REQUEST_NOT_FOUND"
	CodeJoinRequestState    = "JOIN_REQUEST_STATE"
	CodeJoinKeyMismatch     = "JOIN_KEY_MISMATCH"
	CodeRateLimited         = "RATE_LIMITED"



	CodeAdmissionClosed = "ADMISSION_CLOSED"

	joinRequestTTL = 24 * time.Hour



	joinRequestsPerMinute = 10
)

type joinRequestBody struct {
	Agent            string `json:"agent"`
	NodePublicKeyPEM string `json:"node_public_key_pem"`
}

type joinRequestView struct {
	RequestID   string `json:"request_id"`
	Agent       string `json:"agent"`
	Fingerprint string `json:"fingerprint"`
	FromIP      string `json:"from_ip,omitempty"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	DecidedAt   string `json:"decided_at,omitempty"`
	DecidedBy   string `json:"decided_by,omitempty"`
}

func viewJoinRequest(r *spi.JoinRequest, withIP bool) joinRequestView {
	v := joinRequestView{
		RequestID: r.RequestID, Agent: r.Agent, Fingerprint: r.Fingerprint,
		State: string(r.State), Reason: r.Reason,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339), ExpiresAt: r.ExpiresAt.UTC().Format(time.RFC3339),
		DecidedBy: r.DecidedBy,
	}
	if withIP {
		v.FromIP = r.FromIP
	}
	if !r.DecidedAt.IsZero() {
		v.DecidedAt = r.DecidedAt.UTC().Format(time.RFC3339)
	}
	return v
}







func KeyFingerprint(spkiDER []byte) string {





	canon := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spkiDER})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(canon))))
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}


func publicKeyFingerprintFromPEM(pemText string) (string, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil || block.Type != "PUBLIC KEY" {
		return "", errors.New("not a PEM PUBLIC KEY block")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return "", err
	}
	return KeyFingerprint(block.Bytes), nil
}



func csrPublicKeyFingerprint(csrPEM string) (string, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return "", errors.New("not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return "", err
	}
	return KeyFingerprint(der), nil
}











type ipLimiter struct {
	mu      sync.Mutex
	window  time.Time
	counts  map[string]int
	perMin  int
}

var joinLimit = &ipLimiter{counts: map[string]int{}, perMin: joinRequestsPerMinute}

func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window = now
		l.counts = map[string]int{}
	}
	l.counts[ip]++
	return l.counts[ip] <= l.perMin
}




func (l *ipLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.window = time.Time{}
	l.counts = map[string]int{}
}


func (a *API) handleJoinRequestCreate(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !joinLimit.allow(ip, time.Now()) {
		a.fail(w, r, http.StatusTooManyRequests, CodeRateLimited,
			"too many applications from this address",
			"Wait a minute and apply again. One application per agent is enough; the master decides at its own pace.", nil)
		return
	}







	if net, err := a.Store.Networks().Get(r.Context(), spi.DefaultTenant); err == nil &&
		net.Admission == spi.AdmissionClosed {










		a.fail(w, r, http.StatusForbidden, CodeAdmissionClosed,
			"this network is not accepting applications",
			"The network is open or closed by its operator (`baton network admission open`). "+
				"Nothing is wrong with the address you used: the network is reachable and it answered.", nil)
		return
	}
	var req joinRequestBody
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if err := auth.ValidateNodeName(req.Agent); err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"agent name "+req.Agent+" is not valid: "+err.Error(),
			"Lowercase letters, digits and interior hyphens, 1-40 characters.", nil)
		return
	}
	fp, err := publicKeyFingerprintFromPEM(req.NodePublicKeyPEM)
	if err != nil {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"node_public_key_pem is not a usable public key: "+err.Error(),
			"Send the node's public key as a PEM PUBLIC KEY (SPKI). The private key never leaves the node.", nil)
		return
	}
	now := time.Now().UTC()
	jr := &spi.JoinRequest{
		Agent: req.Agent, NodePublicKeyPEM: req.NodePublicKeyPEM, Fingerprint: fp, FromIP: ip,
		State: spi.JoinPending, CreatedAt: now, ExpiresAt: now.Add(joinRequestTTL),
	}
	if err := a.Store.JoinRequests().Create(r.Context(), jr); err != nil {
		if errors.Is(err, spi.ErrConflict) {


			if open, e2 := a.Store.JoinRequests().PendingByAgent(r.Context(), spi.DefaultTenant, req.Agent); e2 == nil {
				a.fail(w, r, http.StatusConflict, CodeJoinRequestState,
					"an application by "+req.Agent+" is already pending",
					"Poll it: GET /join-requests/"+open.RequestID+". A second application does not queue behind the first.",
					map[string]any{"request_id": open.RequestID, "fingerprint": open.Fingerprint})
				return
			}
		}
		a.failStore(w, r, err)
		return
	}
	_, _ = a.Log.Append(r.Context(), audit.Record{
		Event: "join.applied", Actor: req.Agent, ActorType: "node",
		Action: "apply", Target: jr.RequestID, Result: "ok",
		SourceIP: ip, RequestID: requestIDFrom(r), At: now,
		Detail: map[string]any{"agent": req.Agent, "fingerprint": fp},
	})
	a.writeJSON(w, http.StatusCreated, viewJoinRequest(jr, false))
}




func (a *API) handleJoinRequestGet(w http.ResponseWriter, r *http.Request) {
	jr, ok := a.loadJoinRequest(w, r, r.PathValue("request_id"))
	if !ok {
		return
	}
	a.writeJSON(w, http.StatusOK, viewJoinRequest(jr, false))
}






func (a *API) handleJoinRequestCollect(w http.ResponseWriter, r *http.Request) {
	jr, ok := a.loadJoinRequest(w, r, r.PathValue("request_id"))
	if !ok {
		return
	}
	if jr.State != spi.JoinAdmitted {
		a.fail(w, r, http.StatusConflict, CodeJoinRequestState,
			"this application is "+string(jr.State)+", not admitted",
			joinStateAdvice(jr.State), map[string]any{"state": string(jr.State)})
		return
	}
	plaintext, hash, err := auth.NewToken()
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	now := time.Now().UTC()
	tok := &spi.EnrollmentToken{
		Purpose:  spi.TokenPurposeNode,
		TokenID:             "tok_" + strings.TrimPrefix(jr.RequestID, "jr_"),
		TenantID:            jr.TenantID,
		TokenHash:           hash,
		Roles:               spi.Roles{spi.RoleAgent},
		NamePattern:         "^" + jr.Agent + "$",
		BoundKeyFingerprint: jr.Fingerprint,
		MaxUses:             1,
		ExpiresAt:           now.Add(15 * time.Minute),
		CreatedBy:           "join-request:" + jr.RequestID,
		CreatedAt:           now,
	}



	if err := a.Store.JoinRequests().SetCollected(r.Context(), jr.RequestID, hash); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			a.fail(w, r, http.StatusConflict, CodeJoinRequestState,
				"this application was already collected",
				"The token was handed out once. If the node lost it, ask the master to admit a fresh application.", nil)
			return
		}
		a.failStore(w, r, err)
		return
	}
	if err := a.Store.Tokens().Create(r.Context(), tok); err != nil {
		a.failStore(w, r, err)
		return
	}
	_, _ = a.Log.Append(r.Context(), audit.Record{
		Event: "join.collected", Actor: jr.Agent, ActorType: "node",
		Action: "collect", Target: jr.RequestID, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: now,
		Detail: map[string]any{"token_id": tok.TokenID},
	})
	a.writeJSON(w, http.StatusOK, map[string]any{
		"request_id": jr.RequestID, "state": string(spi.JoinCollected),
		"token": plaintext, "expires_at": tok.ExpiresAt.Format(time.RFC3339),
	})
}



func (a *API) handleJoinRequestList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	_, _ = a.Store.JoinRequests().ExpirePending(r.Context(), time.Now())
	items, err := a.Store.JoinRequests().List(r.Context(), spi.DefaultTenant, spi.JoinRequestState(r.URL.Query().Get("state")))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]joinRequestView, 0, len(items))
	for i := range items {
		out = append(out, viewJoinRequest(&items[i], true))
	}









	counts := map[string]int{}
	if byState, err := a.Store.JoinRequests().CountsByState(r.Context(), spi.DefaultTenant); err == nil {
		for st, n := range byState {
			counts[string(st)] = n
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": out, "counts": counts})
}

type joinDecisionBody struct {
	Reason string `json:"reason"`
}




func (a *API) handleJoinRequestDecide(decision spi.JoinRequestState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
		if !ok {
			return
		}
		agent := r.PathValue("agent")
		var body joinDecisionBody
		if r.ContentLength != 0 && !a.decodeJSON(w, r, &body) {
			return
		}
		_, _ = a.Store.JoinRequests().ExpirePending(r.Context(), time.Now())
		jr, err := a.Store.JoinRequests().PendingByAgent(r.Context(), spi.DefaultTenant, agent)
		if errors.Is(err, spi.ErrNotFound) {
			a.fail(w, r, http.StatusNotFound, CodeJoinRequestNotFound,
				"nobody called "+agent+" has applied to join",
				"An admission answers an application. On the agent's machine: `baton agent join --name "+agent+" --network <this network>`; then admit it here.", nil)
			return
		}
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		if err := a.Store.JoinRequests().Decide(r.Context(), jr.RequestID, decision, body.Reason, p.Subject); err != nil {
			a.failStore(w, r, err)
			return
		}
		verb := "admit"
		if decision == spi.JoinDenied {
			verb = "deny"
		}
		_, _ = a.Log.Append(r.Context(), audit.Record{
			Event: "join." + verb, Actor: p.Subject, ActorType: "user",
			Action: verb, Target: jr.RequestID, Result: "ok",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
			Detail: map[string]any{"agent": agent, "fingerprint": jr.Fingerprint, "reason": body.Reason},
		})
		jr.State = decision
		jr.DecidedBy = p.Subject
		jr.DecidedAt = time.Now().UTC()
		a.writeJSON(w, http.StatusOK, viewJoinRequest(jr, true))
	}
}

func (a *API) loadJoinRequest(w http.ResponseWriter, r *http.Request, id string) (*spi.JoinRequest, bool) {
	_, _ = a.Store.JoinRequests().ExpirePending(r.Context(), time.Now())
	jr, err := a.Store.JoinRequests().Get(r.Context(), id)
	if errors.Is(err, spi.ErrNotFound) {
		a.fail(w, r, http.StatusNotFound, CodeJoinRequestNotFound,
			"no application with that id",
			"Apply again: POST /join-requests. Ids are not guessable, and an expired application is still findable by id for a while.", nil)
		return nil, false
	}
	if err != nil {
		a.failStore(w, r, err)
		return nil, false
	}
	return jr, true
}

func joinStateAdvice(s spi.JoinRequestState) string {
	switch s {
	case spi.JoinPending:
		return "Wait: the network's master has not decided. It admits with `baton network add --agent <name>`."
	case spi.JoinDenied:
		return "The master denied it. Ask them; a new application can be made."
	case spi.JoinExpired:
		return "It expired before a decision. Apply again."
	case spi.JoinCollected, spi.JoinConsumed:
		return "The token was already handed out for this application."
	}
	return "Apply again."
}
