package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestSunshinePairerUsesCurrentPairingIDAPI(t *testing.T) {
	t.Setenv("SUNSHINE_USERNAME", "admin")
	t.Setenv("SUNSHINE_PASSWORD", "secret")
	const pairingID = "0123456789abcdef0123456789abcdef"

	sunshine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatal("Sunshine request did not contain the configured credentials")
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"pairings": []map[string]string{{"id": pairingID}}})
		case http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["pairing_id"] != pairingID || body["pin"] != "1234" || body["name"] != "Test PC" {
				t.Fatalf("unexpected pairing request: %#v", body)
			}
			writeJSON(w, http.StatusOK, map[string]bool{"status": true})
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	defer sunshine.Close()

	pairer := NewSunshinePairer()
	vm := VM{SunshineAPIURL: sunshine.URL}
	if err := pairer.Pair(context.Background(), vm, "1234", "Test PC"); err != nil {
		t.Fatal(err)
	}
}

func TestSunshinePairerSupportsLegacyAPI(t *testing.T) {
	t.Setenv("SUNSHINE_USERNAME", "admin")
	t.Setenv("SUNSHINE_PASSWORD", "secret")

	sunshine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, present := body["pairing_id"]; present {
			t.Fatal("legacy pairing request included pairing_id")
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "true"})
	}))
	defer sunshine.Close()

	pairer := NewSunshinePairer()
	vm := VM{SunshineAPIURL: sunshine.URL}
	if err := pairer.Pair(context.Background(), vm, "9876", "Legacy PC"); err != nil {
		t.Fatal(err)
	}
}

func TestSunshinePairerRemovesStaleNamedClientsBeforePairing(t *testing.T) {
	t.Setenv("SUNSHINE_USERNAME", "admin")
	t.Setenv("SUNSHINE_PASSWORD", "secret")
	const pairingID = "0123456789abcdef0123456789abcdef"
	var removed []string

	sunshine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatal("Sunshine request did not contain the configured credentials")
		}
		switch r.URL.Path {
		case "/api/clients/list":
			writeJSON(w, http.StatusOK, map[string]any{"named_certs": []map[string]any{
				{"name": "GilStreaming-device-1", "uuid": "stable-old", "enabled": true},
				{"name": "DESKTOP-OLD", "uuid": "legacy-one", "enabled": true},
				{"name": "DESKTOP-OLD", "uuid": "legacy-two", "enabled": true},
				{"name": "Other Client", "uuid": "keep-me", "enabled": true},
			}})
		case "/api/csrf-token":
			writeJSON(w, http.StatusOK, map[string]string{"csrf_token": "test-token"})
		case "/api/clients/unpair":
			if r.Header.Get("X-CSRF-Token") != "test-token" {
				t.Fatal("stale-client removal did not include the Sunshine CSRF token")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			removed = append(removed, body["uuid"])
			writeJSON(w, http.StatusOK, map[string]bool{"status": true})
		case "/api/pin":
			if r.Method == http.MethodGet {
				writeJSON(w, http.StatusOK, map[string]any{"pairings": []map[string]string{{"id": pairingID}}})
				return
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "GilStreaming-device-1" || body["pin"] != "1234" {
				t.Fatalf("unexpected pairing request: %#v", body)
			}
			writeJSON(w, http.StatusOK, map[string]bool{"status": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer sunshine.Close()

	pairer := NewSunshinePairer()
	vm := VM{SunshineAPIURL: sunshine.URL}
	if err := pairer.Pair(context.Background(), vm, "1234", "GilStreaming-device-1", "DESKTOP-OLD"); err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if !slices.Equal(removed, []string{"legacy-one", "legacy-two", "stable-old"}) {
		t.Fatalf("unexpected stale clients removed: %#v", removed)
	}
}
