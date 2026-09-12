package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen          string `json:"listen"`
	LeaseTTLSeconds int    `json:"leaseTTLSeconds"`
	StateFile       string `json:"stateFile"`
	DevAuthEnabled  bool   `json:"devAuthEnabled"`
	VMs             []VM   `json:"vms"`
}

type Server struct {
	store      *Store
	authBroker *AuthBroker
	adminAuth  *AdminAuth
	pairer     *SunshinePairer
	discovery  *SunshineDiscovery
	turn       *TurnProvider
}

func main() {
	configPath := flag.String("config", "config.json", "path to coordinator configuration")
	envPath := flag.String("env-file", ".env", "path to optional environment file")
	discoverOnly := flag.Bool("discover-only", false, "discover Sunshine VMs, print their endpoints, and exit")
	flag.Parse()
	if err := loadDotEnv(*envPath); err != nil {
		log.Fatalf("load environment file: %v", err)
	}

	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	store := NewStore(config.VMs, time.Duration(config.LeaseTTLSeconds)*time.Second, config.StateFile)
	if err := store.Load(); err != nil {
		log.Fatalf("load lease state: %v", err)
	}
	discovery := NewSunshineDiscovery(3 * time.Second)
	if *discoverOnly {
		if err := discovery.Refresh(context.Background(), store); err != nil {
			log.Fatal(err)
		}
		for _, vm := range store.VMs() {
			log.Printf("VM %s endpoint: %s:%d", vm.ID, vm.StreamAddress, vm.StreamPort)
		}
		return
	}
	authBroker, err := NewAuthBrokerFromEnvironment(config.DevAuthEnabled)
	if err != nil {
		log.Fatal(err)
	}
	server := newServer(store, authBroker)
	server.discovery = discovery
	server.turn, err = NewTurnProviderFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	if server.turn == nil {
		log.Printf("Cloudflare TURN is not configured; public clients will use the WebSocket fallback")
	} else {
		// Credential generation is external and must never delay VM allocation.
		// Keep a reusable credential warm and refresh it well before expiry.
		server.turn.Start(context.Background())
	}
	if err := discovery.Refresh(context.Background(), store); err != nil {
		log.Printf("initial Sunshine discovery failed; VMs will refresh on demand: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("POST /v1/auth/start", server.authBroker.Start)
	mux.HandleFunc("GET /v1/auth/status/{requestId}", server.authBroker.Status)
	mux.HandleFunc("GET /auth/callback", server.oauthCallback)
	mux.HandleFunc("POST /v1/auth/dev", server.authBroker.DevLogin)
	mux.HandleFunc("POST /v1/leases", server.auth(server.createLease))
	mux.HandleFunc("GET /v1/relay", server.auth(server.relay))
	mux.HandleFunc("POST /v1/leases/{leaseId}/pair", server.auth(server.pairLease))
	mux.HandleFunc("POST /v1/leases/{leaseId}/heartbeat", server.auth(server.heartbeat))
	mux.HandleFunc("DELETE /v1/leases/{leaseId}", server.auth(server.release))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET /admin/login", server.adminAuth.Start)
	mux.HandleFunc("GET /admin/assets/gilstreaming-logo.png", server.adminLogo)
	mux.HandleFunc("GET /admin/", server.admin(server.adminPage))
	mux.HandleFunc("GET /admin/api/status", server.admin(server.adminStatus))
	mux.HandleFunc("POST /admin/api/leases", server.admin(server.adminAssign))
	mux.HandleFunc("DELETE /admin/api/leases/{leaseId}", server.admin(server.adminKick))
	mux.HandleFunc("POST /admin/logout", server.admin(server.adminAuth.Logout))

	httpServer := &http.Server{
		Addr:              config.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// Streaming relay connections are long-lived WebSocket upgrades. Per-request
		// limits remain in the handlers, while ReadHeaderTimeout protects handshakes.
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}
	log.Printf("GilStreaming coordinator listening on %s", config.Listen)
	log.Fatal(httpServer.ListenAndServe())
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	if config.Listen == "" || config.StateFile == "" || config.LeaseTTLSeconds < 30 {
		return Config{}, errors.New("listen, stateFile, and leaseTTLSeconds >= 30 are required")
	}
	if len(config.VMs) == 0 {
		return Config{}, errors.New("at least one VM is required")
	}
	for _, vm := range config.VMs {
		if vm.ID == "" || (vm.DiscoveryName == "" && (vm.StreamAddress == "" || vm.StreamPort < 1)) || vm.StreamPort > 65535 {
			return Config{}, fmt.Errorf("invalid VM configuration for %q", vm.ID)
		}
		if vm.PublicPort < 0 || vm.PublicPort > 65535 {
			return Config{}, fmt.Errorf("invalid public endpoint for VM %q", vm.ID)
		}
	}
	return config, nil
}

func newServer(store *Store, authBroker *AuthBroker) *Server {
	return &Server{store: store, authBroker: authBroker, adminAuth: NewAdminAuth(authBroker), pairer: NewSunshinePairer()}
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth.Callback(w, r) {
		return
	}
	s.authBroker.Callback(w, r)
}

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
			return
		}
		owner := s.authBroker.AuthenticateToken(strings.TrimPrefix(header, "Bearer "))
		if owner == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bearer token")
			return
		}
		next(w, r, owner)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createLease(w http.ResponseWriter, r *http.Request, owner string) {
	var request struct {
		DeviceID   string `json:"deviceId"`
		DeviceName string `json:"deviceName"`
	}
	if err := decodeJSON(w, r, &request); err != nil || request.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "deviceId is required")
		return
	}
	if s.discovery != nil {
		if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
			log.Printf("Sunshine discovery failed; using last known VM addresses: %v", err)
		}
	}
	lease, vm, err := s.store.CreateOrRecover(owner, request.DeviceID, request.DeviceName)
	if errors.Is(err, errPoolExhausted) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code": "POOL_EXHAUSTED", "message": "all gaming VMs are in use", "retryAfterSeconds": 15,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create lease")
		return
	}
	clientAddress, clientPort := endpointForRequest(vm, r)
	response := map[string]any{
		"leaseId": lease.ID, "state": "reserved", "expiresAt": lease.ExpiresAt,
		"host":            map[string]any{"name": vm.DisplayName, "address": clientAddress, "port": clientPort},
		"pairingRequired": true,
	}
	if r.Header.Get("X-GilStreaming-LAN") != "1" {
		response["relay"] = map[string]any{
			"url": relayURLForRequest(r), "basePort": vm.StreamPort,
		}
		if s.turn != nil {
			if credentials, ok := s.turn.CachedCredentials(); ok {
				peerAddress, peerPort := vm.ClientEndpoint()
				clientPort := credentials.Port
				clientPorts := []int{credentials.Port}
				// Cloudflare's native TURN service also accepts UDP on port 53.
				// New clients may try it when UDP 3478 is blocked. Keep the primary
				// port in the legacy field so 6.1.24 falls back to WebSocket instead
				// of accepting a TURN allocation whose data path cannot be verified.
				if strings.EqualFold(credentials.Server, "turn.cloudflare.com") && credentials.Port != 53 {
					clientPorts = append(clientPorts, 53)
				}
				response["turn"] = map[string]any{
					"server": credentials.Server, "port": clientPort, "ports": clientPorts,
					"username": credentials.Username, "credential": credentials.Credential,
					"expiresAt":   credentials.ExpiresAt,
					"peerAddress": peerAddress, "peerBasePort": peerPort,
				}
			} else {
				log.Printf("Cloudflare TURN credentials not ready for lease=%s; using direct/WebSocket fallback", lease.ID)
			}
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

func relayURLForRequest(r *http.Request) string {
	scheme := "wss"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "ws"
	}
	return scheme + "://" + r.Host + "/v1/relay"
}

func endpointForRequest(vm VM, r *http.Request) (string, int) {
	if r.Header.Get("X-GilStreaming-LAN") == "1" {
		return vm.StreamAddress, vm.StreamPort
	}
	host := r.Host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		return vm.StreamAddress, vm.StreamPort
	}
	return vm.ClientEndpoint()
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, owner string) {
	lease, err := s.store.Heartbeat(r.PathValue("leaseId"), owner)
	if errors.Is(err, errLeaseNotFound) {
		writeError(w, http.StatusGone, "LEASE_EXPIRED", "lease is missing or expired")
		return
	}
	if errors.Is(err, errLeaseForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "lease belongs to another user")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not renew lease")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leaseId": lease.ID, "expiresAt": lease.ExpiresAt})
}

func (s *Server) pairLease(w http.ResponseWriter, r *http.Request, owner string) {
	var request struct {
		PIN        string `json:"pin"`
		DeviceName string `json:"deviceName"`
	}
	if err := decodeJSON(w, r, &request); err != nil || len(request.PIN) != 4 || request.DeviceName == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "a four-digit pin and deviceName are required")
		return
	}
	for _, digit := range request.PIN {
		if digit < '0' || digit > '9' {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "pin must contain four digits")
			return
		}
	}
	lease, vm, err := s.store.LeaseVM(r.PathValue("leaseId"), owner)
	if errors.Is(err, errLeaseNotFound) {
		writeError(w, http.StatusGone, "LEASE_EXPIRED", "lease is missing or expired")
		return
	}
	if errors.Is(err, errLeaseForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "lease belongs to another user")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load lease")
		return
	}
	// Use a stable, coordinator-specific Sunshine name so the same device can
	// safely replace its old certificate record when it needs to pair again.
	// The client-provided hostname is included only to clean up records created
	// by older GilStreaming coordinator versions.
	pairingName := "GilStreaming-" + lease.DeviceID
	if err := s.pairer.Pair(r.Context(), vm, request.PIN, pairingName, request.DeviceName); err != nil {
		log.Printf("automatic pairing failed for VM %s: %v", vm.ID, err)
		writeError(w, http.StatusBadGateway, "PAIRING_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"paired": true})
}

func (s *Server) release(w http.ResponseWriter, r *http.Request, owner string) {
	lease, _, lookupErr := s.store.LeaseVM(r.PathValue("leaseId"), owner)
	if errors.Is(lookupErr, errLeaseForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "lease belongs to another user")
		return
	}
	err := s.store.Release(r.PathValue("leaseId"), owner)
	if errors.Is(err, errLeaseForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "lease belongs to another user")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not release lease")
		return
	}
	if lease != nil {
		logLeaseEnded(lease, "client-release")
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
