package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	relayDialTimeout     = 5 * time.Second
	relayLeaseCheckEvery = 10 * time.Second
	relayWriteTimeout    = 15 * time.Second
	relayCloseWait       = 2 * time.Second
	relayMaxMessageSize  = 1024 * 1024
)

var relayUpgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	// GilStreaming is a native client and intentionally sends no browser Origin.
	// Reject browser-originated upgrades even if a bearer token is exposed there.
	CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

var relayAllowedOffsets = map[string]map[int]bool{
	"tcp": {
		-5: true, // HTTPS (47984)
		0:  true, // HTTP (47989)
		6:  true, // legacy control (47995)
		7:  true, // legacy first-frame channel (47996)
		21: true, // RTSP (48010)
	},
	"udp": {
		9:  true, // video (47998)
		10: true, // control (47999)
		11: true, // audio (48000)
		13: true, // microphone (48002)
		21: true, // ENet RTSP (48010)
	},
}

func (s *Server) relay(w http.ResponseWriter, r *http.Request, owner string) {
	leaseID := r.URL.Query().Get("leaseId")
	transport := r.URL.Query().Get("transport")
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if leaseID == "" || !relayAllowedOffsets[transport][offset] {
		writeError(w, http.StatusBadRequest, "INVALID_RELAY", "invalid relay channel")
		return
	}

	lease, vm, err := s.store.LeaseVM(leaseID, owner)
	if errors.Is(err, errLeaseNotFound) {
		writeError(w, http.StatusGone, "LEASE_EXPIRED", "lease is missing or expired")
		return
	}
	if errors.Is(err, errLeaseForbidden) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "lease belongs to another user")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not validate relay lease")
		return
	}

	targetPort := vm.StreamPort + offset
	if targetPort < 1 || targetPort > 65535 {
		writeError(w, http.StatusBadRequest, "INVALID_RELAY", "relay target port is invalid")
		return
	}
	target := net.JoinHostPort(vm.StreamAddress, strconv.Itoa(targetPort))
	upstream, err := dialRelayUpstream(transport, target, offset)
	if err != nil {
		writeError(w, http.StatusBadGateway, "VM_UNREACHABLE", "assigned VM relay channel is unavailable")
		return
	}

	client, err := relayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		upstream.Close()
		return
	}
	client.SetReadLimit(relayMaxMessageSize)
	started := time.Now()
	log.Printf("VM relay opened lease=%s vm=%s transport=%s offset=%d", lease.ID, vm.ID, transport, offset)

	done := make(chan struct{})
	go s.watchRelayLease(done, client, upstream, leaseID, owner)
	var relayErr error
	if transport == "tcp" {
		relayErr = relayTCP(client, upstream)
	} else {
		relayErr = relayUDP(client, upstream)
	}
	close(done)
	client.Close()
	upstream.Close()
	log.Printf("VM relay closed lease=%s vm=%s transport=%s offset=%d duration=%s reason=%v",
		lease.ID, vm.ID, transport, offset, time.Since(started).Round(time.Second), relayErr)
}

func dialRelayUpstream(transport, target string, offset int) (net.Conn, error) {
	deadline := time.Now().Add(relayDialTimeout)
	for {
		connection, err := net.DialTimeout(transport, target, time.Until(deadline))
		if err == nil || transport != "tcp" || offset != 21 || time.Now().After(deadline) {
			return connection, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Server) watchRelayLease(done <-chan struct{}, client *websocket.Conn, upstream net.Conn, leaseID, owner string) {
	ticker := time.NewTicker(relayLeaseCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if _, _, err := s.store.LeaseVM(leaseID, owner); err != nil {
				_ = client.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "lease expired"),
					time.Now().Add(time.Second))
				client.Close()
				upstream.Close()
				return
			}
		}
	}
}

type tcpRelayResult struct {
	err      error
	upstream bool
}

func relayTCP(client *websocket.Conn, upstream net.Conn) error {
	results := make(chan tcpRelayResult, 2)
	go func() {
		for {
			messageType, payload, err := client.ReadMessage()
			if err != nil {
				results <- tcpRelayResult{err: err}
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			if err := writeAll(upstream, payload); err != nil {
				results <- tcpRelayResult{err: err}
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, 64*1024)
		for {
			count, err := upstream.Read(buffer)
			if count > 0 {
				_ = client.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
				if writeErr := client.WriteMessage(websocket.BinaryMessage, buffer[:count]); writeErr != nil {
					results <- tcpRelayResult{err: writeErr, upstream: true}
					return
				}
			}
			if err != nil {
				results <- tcpRelayResult{err: err, upstream: true}
				return
			}
		}
	}()

	result := <-results
	if result.upstream && errors.Is(result.err, io.EOF) {
		// A Close frame is ordered after all preceding binary frames. Wait for
		// the peer's close acknowledgement before the handler closes the TLS
		// socket, otherwise Schannel can discard the tail of Sunshine's RTSP
		// response and Moonlight times out waiting for ANNOUNCE.
		if err := client.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "upstream closed"),
			time.Now().Add(time.Second)); err != nil {
			return fmt.Errorf("send relay close: %w", err)
		}

		timer := time.NewTimer(relayCloseWait)
		select {
		case <-results:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
	return result.err
}

func relayUDP(client *websocket.Conn, upstream net.Conn) error {
	errorsDone := make(chan error, 2)
	go func() {
		for {
			messageType, payload, err := client.ReadMessage()
			if err != nil {
				errorsDone <- err
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			if len(payload) > 65507 {
				errorsDone <- fmt.Errorf("UDP datagram too large: %d", len(payload))
				return
			}
			if _, err := upstream.Write(payload); err != nil {
				errorsDone <- err
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, 65507)
		for {
			count, err := upstream.Read(buffer)
			if count > 0 {
				_ = client.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
				if writeErr := client.WriteMessage(websocket.BinaryMessage, buffer[:count]); writeErr != nil {
					errorsDone <- writeErr
					return
				}
			}
			if err != nil {
				errorsDone <- err
				return
			}
		}
	}()
	return <-errorsDone
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) != 0 {
		count, err := writer.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[count:]
	}
	return nil
}
