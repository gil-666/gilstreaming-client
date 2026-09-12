package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTurnProviderGeneratesUDPcredentials(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iceServers":[{"urls":["stun:stun.cloudflare.com:3478","turn:turn.cloudflare.com:3478?transport=udp","turns:turn.cloudflare.com:443?transport=tcp"],"username":"temporary-user","credential":"temporary-password"}]}`))
	}))
	defer server.Close()

	provider := &TurnProvider{
		keyID: "key-id", apiToken: "turn-key-secret", baseURL: server.URL,
		httpClient: server.Client(), credentialTTL: time.Hour,
	}
	credentials, err := provider.Credentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer turn-key-secret" {
		t.Fatalf("unexpected authorization header %q", authorization)
	}
	if credentials.Server != "turn.cloudflare.com" || credentials.Port != 3478 ||
		credentials.Username != "temporary-user" || credentials.Credential != "temporary-password" {
		t.Fatalf("unexpected TURN credentials: %#v", credentials)
	}
}

func TestParseTurnUDPURLRejectsTCP(t *testing.T) {
	if _, _, ok := parseTurnUDPURL("turn:turn.cloudflare.com:3478?transport=tcp"); ok {
		t.Fatal("accepted a TURN/TCP endpoint as UDP")
	}
}
