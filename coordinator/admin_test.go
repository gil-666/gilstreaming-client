package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminGILidLoginAssignsAndKicksLease(t *testing.T) {
	t.Setenv("SUNSHINE_USERNAME", "sunshine")
	t.Setenv("SUNSHINE_PASSWORD", "secret")
	closed := false
	sunshine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "sunshine" || password != "secret" {
			t.Error("Sunshine close request did not use configured credentials")
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/apps/close" {
			t.Errorf("unexpected Sunshine close request: %s %s", r.Method, r.URL.Path)
		}
		closed = true
		writeJSON(w, http.StatusOK, map[string]bool{"status": true})
	}))
	defer sunshine.Close()

	identity := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			writeJSON(w, http.StatusOK, map[string]string{"access_token": "gilid-access-token"})
		case "/auth/me":
			writeJSON(w, http.StatusOK, gilIDProfile{ID: "gilid-admin", Username: "gil", Email: "gil@example.com", IsAdmin: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer identity.Close()

	store := NewStore([]VM{{
		ID: "vm-1", DisplayName: "Gaming VM 1", StreamAddress: "192.168.1.21",
		StreamPort: 47989, SunshineAPIURL: sunshine.URL, Enabled: true,
	}}, time.Minute, filepath.Join(t.TempDir(), "state.json"))
	broker := testAdminBroker(identity)
	broker.sessions["lease-owner"] = coordinatorSession{
		Owner: "gilid-user", Profile: gilIDProfile{ID: "gilid-user", FirstName: "Gil", LastName: "Admin", Email: "user@example.com"},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	server := newServer(store, broker)
	mux := adminTestMux(server)

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if page.Code != http.StatusSeeOther || page.Header().Get("Location") != "/admin/login" {
		t.Fatalf("expected GILid login redirect, got %d %q", page.Code, page.Header().Get("Location"))
	}
	api := httptest.NewRecorder()
	mux.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/admin/api/status", nil))
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("expected API authentication rejection, got %d", api.Code)
	}
	logo := httptest.NewRecorder()
	mux.ServeHTTP(logo, httptest.NewRequest(http.MethodGet, "/admin/assets/gilstreaming-logo.png", nil))
	if logo.Code != http.StatusOK || logo.Header().Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(logo.Body.String(), "\x89PNG") {
		t.Fatalf("dashboard logo was not served: %d %q", logo.Code, logo.Header().Get("Content-Type"))
	}

	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	authorizeURL, err := url.Parse(login.Header().Get("Location"))
	if err != nil || login.Code != http.StatusSeeOther || authorizeURL.Query().Get("state") == "" {
		t.Fatalf("unexpected authorize redirect: %d %q", login.Code, login.Header().Get("Location"))
	}
	callback := httptest.NewRecorder()
	mux.ServeHTTP(callback, httptest.NewRequest(http.MethodGet,
		"/auth/callback?code=admin-code&state="+url.QueryEscape(authorizeURL.Query().Get("state")), nil))
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/admin/" {
		t.Fatalf("unexpected callback: %d %s", callback.Code, callback.Body.String())
	}
	cookies := callback.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminSessionCookie || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("callback did not issue a secure admin cookie: %#v", cookies)
	}
	cookie := cookies[0]

	missingCSRF := adminRequest(http.MethodPost, "/admin/api/leases",
		`{"vmId":"vm-1","ownerId":"gilid-user","deviceId":"device-1"}`, false, cookie)
	missingCSRFResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingCSRFResponse, missingCSRF)
	if missingCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF rejection, got %d", missingCSRFResponse.Code)
	}

	assign := adminRequest(http.MethodPost, "/admin/api/leases",
		`{"vmId":"vm-1","ownerId":"gilid-user","deviceId":"device-1","deviceName":"Gaming PC"}`, true, cookie)
	assignResponse := httptest.NewRecorder()
	mux.ServeHTTP(assignResponse, assign)
	if assignResponse.Code != http.StatusCreated {
		t.Fatalf("assign returned %d: %s", assignResponse.Code, assignResponse.Body.String())
	}
	var assignment struct {
		Lease adminLeaseStatus `json:"lease"`
	}
	if err := json.NewDecoder(assignResponse.Body).Decode(&assignment); err != nil {
		t.Fatal(err)
	}

	statusResponse := httptest.NewRecorder()
	mux.ServeHTTP(statusResponse, adminRequest(http.MethodGet, "/admin/api/status", "", false, cookie))
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"status":"busy"`) ||
		!strings.Contains(statusResponse.Body.String(), `"ownerName":"Gil Admin"`) ||
		!strings.Contains(statusResponse.Body.String(), `"username":"gil"`) ||
		!strings.Contains(statusResponse.Body.String(), `"connectedSeconds":`) {
		t.Fatalf("unexpected admin status: %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	kickResponse := httptest.NewRecorder()
	mux.ServeHTTP(kickResponse, adminRequest(http.MethodDelete,
		"/admin/api/leases/"+assignment.Lease.ID, "", true, cookie))
	if kickResponse.Code != http.StatusNoContent || !closed {
		t.Fatalf("kick returned %d, closed=%v: %s", kickResponse.Code, closed, kickResponse.Body.String())
	}

	logout := httptest.NewRecorder()
	mux.ServeHTTP(logout, adminRequest(http.MethodPost, "/admin/logout", "", true, cookie))
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d", logout.Code)
	}
	afterLogout := httptest.NewRecorder()
	mux.ServeHTTP(afterLogout, adminRequest(http.MethodGet, "/admin/api/status", "", false, cookie))
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out cookie remained valid: %d", afterLogout.Code)
	}
}

func TestAdminGILidLoginRejectsNonAdminAccount(t *testing.T) {
	identity := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			writeJSON(w, http.StatusOK, map[string]string{"access_token": "token"})
		case "/auth/me":
			writeJSON(w, http.StatusOK, gilIDProfile{ID: "regular-user"})
		}
	}))
	defer identity.Close()
	server := newServer(testStore(t), testAdminBroker(identity))
	mux := adminTestMux(server)
	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	authorizeURL, _ := url.Parse(login.Header().Get("Location"))
	callback := httptest.NewRecorder()
	mux.ServeHTTP(callback, httptest.NewRequest(http.MethodGet,
		"/auth/callback?code=regular&state="+url.QueryEscape(authorizeURL.Query().Get("state")), nil))
	if callback.Code != http.StatusForbidden || len(callback.Result().Cookies()) != 0 {
		t.Fatalf("non-admin login was not rejected: %d %s", callback.Code, callback.Body.String())
	}
}

func testAdminBroker(identity *httptest.Server) *AuthBroker {
	return &AuthBroker{
		clientID: "client", clientSecret: "secret", redirectURI: "https://stream.example/auth/callback",
		authorizeURL: identity.URL + "/oauth/authorize", tokenURL: identity.URL + "/oauth/token",
		profileURL: identity.URL + "/auth/me", devAuthEnabled: true,
		pending: make(map[string]*pendingAuth), stateToRequest: make(map[string]string),
		sessions: make(map[string]coordinatorSession), httpClient: identity.Client(), now: time.Now,
	}
}

func adminTestMux(server *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", server.oauthCallback)
	mux.HandleFunc("GET /admin/login", server.adminAuth.Start)
	mux.HandleFunc("GET /admin/assets/gilstreaming-logo.png", server.adminLogo)
	mux.HandleFunc("GET /admin/", server.admin(server.adminPage))
	mux.HandleFunc("GET /admin/api/status", server.admin(server.adminStatus))
	mux.HandleFunc("POST /admin/api/leases", server.admin(server.adminAssign))
	mux.HandleFunc("DELETE /admin/api/leases/{leaseId}", server.admin(server.adminKick))
	mux.HandleFunc("POST /admin/logout", server.admin(server.adminAuth.Logout))
	return mux
}

func adminRequest(method, target, body string, mutation bool, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.AddCookie(cookie)
	if mutation {
		request.Header.Set("X-GilStreaming-Admin", "1")
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
