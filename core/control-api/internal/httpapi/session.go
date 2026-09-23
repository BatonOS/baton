// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
)

























const (

	handoffTTL = 60 * time.Second

	sessionTTL = 12 * time.Hour

	sessionCookie = "baton_session"
)

type webSession struct {
	subject string
	expires time.Time
}

type handoff struct {
	subject string
	expires time.Time
}






type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]webSession
	handoffs map[string]handoff
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		sessions: map[string]webSession{},
		handoffs: map[string]handoff{},
	}
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *sessionStore) newHandoff(subject string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	s.handoffs[token] = handoff{subject: subject, expires: time.Now().Add(handoffTTL)}
	return token, nil
}




func (s *sessionStore) redeem(token string) (sessionID string, subject string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, found := s.handoffs[token]
	delete(s.handoffs, token)
	if !found || time.Now().After(h.expires) {
		return "", "", false
	}

	id, err := randomToken()
	if err != nil {
		return "", "", false
	}
	s.sessions[id] = webSession{subject: h.subject, expires: time.Now().Add(sessionTTL)}
	return id, h.subject, true
}

func (s *sessionStore) lookup(sessionID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()




	sess, found := s.sessions[sessionID]
	if !found || time.Now().After(sess.expires) {
		delete(s.sessions, sessionID)
		return "", false
	}
	return sess.subject, true
}

func (s *sessionStore) revoke(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *sessionStore) sweepLocked() {
	now := time.Now()
	for token, h := range s.handoffs {
		if now.After(h.expires) {
			delete(s.handoffs, token)
		}
	}
	for id, sess := range s.sessions {
		if now.After(sess.expires) {
			delete(s.sessions, id)
		}
	}
}



func (a *API) handleWebHandoff(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	token, err := a.Sessions.newHandoff(p.Subject)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}

	if _, err := a.Log.Append(r.Context(), audit.Record{
		Event: "web.session_requested", Actor: p.Subject, ActorType: "user",
		Action: "issue_handoff", Target: "web-console", Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, map[string]any{
		"handoff_token": token,
		"expires_in":    int(handoffTTL.Seconds()),
		"url":           a.Cfg.Spec.AdvertiseURL + "/web/session?token=" + token,
	})
}


func (a *API) handleWebSession(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		a.fail(w, r, http.StatusUnauthorized, CodeUnauthorized,
			"this link carries no session token",
			"Open the console with `baton web`, which mints a fresh link.", nil)
		return
	}

	sessionID, subject, ok := a.Sessions.redeem(token)
	if !ok {
		a.fail(w, r, http.StatusUnauthorized, CodeUnauthorized,
			"this link has already been used or has expired",
			"Links are single-use and last 60 seconds. Run `baton web` again.", nil)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	a.Log.System(r.Context(), "web.session_started", map[string]any{"actor": subject})



	http.Redirect(w, r, "/", http.StatusSeeOther)
}


func (a *API) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.Sessions.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}






func (a *API) principalFromSession(r *http.Request) (string, bool) {
	if a.Sessions == nil {
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	subject, ok := a.Sessions.lookup(c.Value)
	if !ok {
		return "", false
	}

	if subtle.ConstantTimeCompare([]byte(c.Value), []byte("")) == 1 {
		return "", false
	}
	return subject, true
}
