package main

import (
	_ "embed"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed admin.html
var adminHTML []byte

type adminVMStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Enabled        bool   `json:"enabled"`
	LocalEndpoint  string `json:"localEndpoint"`
	PublicEndpoint string `json:"publicEndpoint,omitempty"`
	Lease          any    `json:"lease,omitempty"`
}

type adminLeaseStatus struct {
	ID         string    `json:"id"`
	OwnerID    string    `json:"ownerId"`
	OwnerName  string    `json:"ownerName"`
	OwnerEmail string    `json:"ownerEmail,omitempty"`
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Duration   int64     `json:"connectedSeconds"`
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if _, ok := s.adminAuth.Authenticate(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/admin/api/") {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "GILid admin sign-in is required")
			} else {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			}
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-GilStreaming-Admin") != "1" {
			writeError(w, http.StatusForbidden, "CSRF_REJECTED", "missing admin request header")
			return
		}
		next(w, r)
	}
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(adminHTML)
}

func (s *Server) adminLogo(w http.ResponseWriter, _ *http.Request) {
	var data []byte
	var err error
	for _, path := range []string{"../app/res/gilstreaming-logo.png", "app/res/gilstreaming-logo.png", "../../app/res/gilstreaming-logo.png"} {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		http.Error(w, "logo unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" && s.discovery != nil {
		if err := s.discovery.Refresh(r.Context(), s.store); err != nil {
			writeError(w, http.StatusBadGateway, "DISCOVERY_FAILED", err.Error())
			return
		}
	}
	vms, leases, err := s.store.AdminSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load VM status")
		return
	}
	leaseByVM := make(map[string]*Lease, len(leases))
	for _, lease := range leases {
		leaseByVM[lease.VMID] = lease
	}
	result := make([]adminVMStatus, 0, len(vms))
	for _, vm := range vms {
		status := "available"
		if !vm.Enabled {
			status = "disabled"
		} else if vm.StreamAddress == "" || vm.StreamPort < 1 {
			status = "offline"
		}
		entry := adminVMStatus{
			ID: vm.ID, Name: vm.DisplayName, Status: status, Enabled: vm.Enabled,
			LocalEndpoint: endpointString(vm.StreamAddress, vm.StreamPort),
		}
		publicAddress, publicPort := vm.ClientEndpoint()
		entry.PublicEndpoint = endpointString(publicAddress, publicPort)
		if lease := leaseByVM[vm.ID]; lease != nil {
			entry.Status = "busy"
			entry.Lease = s.adminLeaseResponse(lease)
		}
		result = append(result, entry)
	}
	adminProfile, _ := s.adminAuth.Authenticate(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"generatedAt": time.Now().UTC(), "vms": result,
		"admin": map[string]string{
			"id": adminProfile.ID, "username": adminProfile.Username, "email": adminProfile.Email,
		},
	})
}

func (s *Server) adminAssign(w http.ResponseWriter, r *http.Request) {
	var request struct {
		VMID       string `json:"vmId"`
		OwnerID    string `json:"ownerId"`
		DeviceID   string `json:"deviceId"`
		DeviceName string `json:"deviceName"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid assignment request")
		return
	}
	request.VMID = strings.TrimSpace(request.VMID)
	request.OwnerID = strings.TrimSpace(request.OwnerID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.DeviceName = strings.TrimSpace(request.DeviceName)
	if request.VMID == "" || request.OwnerID == "" || request.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "vmId, ownerId, and deviceId are required")
		return
	}
	if len(request.OwnerID) > 256 || len(request.DeviceID) > 256 || len(request.DeviceName) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "assignment fields are too long")
		return
	}
	lease, vm, err := s.store.AdminAssign(request.VMID, request.OwnerID, request.DeviceID, request.DeviceName)
	switch {
	case errors.Is(err, errVMNotFound):
		writeError(w, http.StatusNotFound, "VM_NOT_FOUND", "VM was not found")
	case errors.Is(err, errVMUnavailable):
		writeError(w, http.StatusConflict, "VM_UNAVAILABLE", "VM is disabled or offline")
	case errors.Is(err, errVMOccupied):
		writeError(w, http.StatusConflict, "VM_OCCUPIED", "VM is already assigned")
	case errors.Is(err, errClientHasLease):
		writeError(w, http.StatusConflict, "CLIENT_HAS_LEASE", "this owner and device already have a lease")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not assign VM")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{
			"lease": s.adminLeaseResponse(lease), "vmId": vm.ID,
		})
	}
}

func (s *Server) adminKick(w http.ResponseWriter, r *http.Request) {
	lease, vm, err := s.store.AdminLease(r.PathValue("leaseId"))
	if errors.Is(err, errLeaseNotFound) {
		writeError(w, http.StatusNotFound, "LEASE_NOT_FOUND", "lease was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load lease")
		return
	}
	if r.URL.Query().Get("force") != "1" {
		if err := s.pairer.CloseApp(r.Context(), vm); err != nil {
			writeError(w, http.StatusBadGateway, "SUNSHINE_CLOSE_FAILED", err.Error())
			return
		}
	}
	if err := s.store.AdminRelease(lease.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not release lease")
		return
	}
	logLeaseEnded(lease, "admin-kick")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminLeaseResponse(lease *Lease) adminLeaseStatus {
	result := adminLeaseStatus{
		ID: lease.ID, OwnerID: lease.Owner, OwnerName: lease.Owner,
		DeviceID: lease.DeviceID, DeviceName: lease.DeviceName,
		CreatedAt: lease.CreatedAt, ExpiresAt: lease.ExpiresAt,
		Duration: max(0, int64(time.Since(lease.CreatedAt).Seconds())),
	}
	if profile, ok := s.authBroker.ProfileForOwner(lease.Owner); ok {
		result.OwnerEmail = profile.Email
		result.OwnerName = strings.TrimSpace(profile.FirstName + " " + profile.LastName)
		if result.OwnerName == "" {
			result.OwnerName = profile.Username
		}
		if result.OwnerName == "" {
			result.OwnerName = profile.Email
		}
		if result.OwnerName == "" {
			result.OwnerName = lease.Owner
		}
	}
	return result
}

func logLeaseEnded(lease *Lease, reason string) {
	logLeaseEndedAt(lease, reason, time.Now())
}

func logLeaseEndedAt(lease *Lease, reason string, endedAt time.Time) {
	duration := endedAt.Sub(lease.CreatedAt).Round(time.Second)
	if duration < 0 {
		duration = 0
	}
	log.Printf("VM session ended lease=%s vm=%s owner=%s device=%s duration=%s reason=%s",
		lease.ID, lease.VMID, lease.Owner, lease.DeviceID, duration, reason)
}

func endpointString(address string, port int) string {
	if address == "" || port < 1 {
		return ""
	}
	return address + ":" + strconv.Itoa(port)
}
