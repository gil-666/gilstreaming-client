package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const adminSessionCookie = "gilstreaming_admin_session"

type adminSession struct {
	Profile   gilIDProfile
	ExpiresAt time.Time
}

type AdminAuth struct {
	mu       sync.Mutex
	broker   *AuthBroker
	pending  map[string]time.Time
	sessions map[string]adminSession
	now      func() time.Time
}

func NewAdminAuth(broker *AuthBroker) *AdminAuth {
	return &AdminAuth{
		broker: broker, pending: make(map[string]time.Time),
		sessions: make(map[string]adminSession), now: time.Now,
	}
}

func (a *AdminAuth) Start(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.Authenticate(r); ok {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	state, err := randomID()
	if err != nil {
		http.Error(w, "Could not start GILid sign-in", http.StatusInternalServerError)
		return
	}
	now := a.now().UTC()
	a.mu.Lock()
	a.cleanupLocked(now)
	a.pending[state] = now.Add(authRequestLifetime)
	a.mu.Unlock()

	authorize, _ := url.Parse(a.broker.authorizeURL)
	query := authorize.Query()
	query.Set("client_id", a.broker.clientID)
	query.Set("redirect_uri", a.broker.redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "profile email")
	query.Set("state", state)
	authorize.RawQuery = query.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authorize.String(), http.StatusSeeOther)
}

// Callback returns false when the state belongs to a client login instead.
func (a *AdminAuth) Callback(w http.ResponseWriter, r *http.Request) bool {
	state := r.URL.Query().Get("state")
	now := a.now().UTC()
	a.mu.Lock()
	a.cleanupLocked(now)
	expiresAt, ok := a.pending[state]
	if ok {
		delete(a.pending, state)
	}
	a.mu.Unlock()
	if !ok {
		return false
	}
	if state == "" || !expiresAt.After(now) {
		http.Error(w, "Invalid or expired admin login", http.StatusBadRequest)
		return true
	}
	if r.URL.Query().Get("error") != "" {
		writeCallbackPage(w, "Login cancelled", "GILid denied the admin login request.")
		return true
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return true
	}

	profile, err := a.broker.exchangeAndLoadProfile(r.Context(), code)
	if err != nil {
		http.Error(w, "GILid authentication failed", http.StatusBadGateway)
		return true
	}
	if !profile.IsAdmin {
		writeCallbackError(w, http.StatusForbidden, "Admin access required",
			"This GILid account does not have administrator access.")
		return true
	}
	token, err := randomID()
	if err != nil {
		http.Error(w, "Could not create admin session", http.StatusInternalServerError)
		return true
	}
	hash := sha256.Sum256([]byte(token))
	expiresAt = now.Add(coordinatorSessionLifetime)
	a.mu.Lock()
	a.sessions[hex.EncodeToString(hash[:])] = adminSession{Profile: profile, ExpiresAt: expiresAt}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookie, Value: token, Path: "/admin", Expires: expiresAt,
		MaxAge: int(coordinatorSessionLifetime.Seconds()), HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	return true
}

func (a *AdminAuth) Authenticate(r *http.Request) (gilIDProfile, bool) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return gilIDProfile{}, false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	key := hex.EncodeToString(hash[:])
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupLocked(now)
	session, ok := a.sessions[key]
	return session.Profile, ok && session.ExpiresAt.After(now)
}

func (a *AdminAuth) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(adminSessionCookie); err == nil {
		hash := sha256.Sum256([]byte(cookie.Value))
		a.mu.Lock()
		delete(a.sessions, hex.EncodeToString(hash[:]))
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookie, Value: "", Path: "/admin", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *AdminAuth) cleanupLocked(now time.Time) {
	for state, expiresAt := range a.pending {
		if !expiresAt.After(now) {
			delete(a.pending, state)
		}
	}
	for hash, session := range a.sessions {
		if !session.ExpiresAt.After(now) {
			delete(a.sessions, hash)
		}
	}
}

func writeCallbackError(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title><style>body{font:18px system-ui;background:#111827;color:#fff;display:grid;place-items:center;min-height:90vh}main{max-width:34rem;text-align:center}h1{color:#f87171}a{color:#c084fc}</style><main><h1>%s</h1><p>%s</p><p><a href="/admin/login">Try another account</a></p></main>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}
