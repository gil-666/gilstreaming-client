package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTCPRelayForAssignedVM(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		buffer := make([]byte, 64)
		count, readErr := connection.Read(buffer)
		if readErr == nil {
			_, _ = connection.Write(buffer[:count])
		}
	}()

	server, token, leaseID := testRelayServer(t, "127.0.0.1", listener.Addr().(*net.TCPAddr).Port)
	defer server.Close()
	connection := dialTestRelay(t, server.URL, token, leaseID, "tcp", 0)
	defer connection.Close()

	message := []byte("relay TCP works")
	if err := connection.WriteMessage(websocket.BinaryMessage, message); err != nil {
		t.Fatal(err)
	}
	messageType, response, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage || string(response) != string(message) {
		t.Fatalf("unexpected relay response: type=%d body=%q", messageType, response)
	}

	// The upstream closes after its response, like Sunshine does for each RTSP
	// transaction. The WebSocket must preserve that as an orderly EOF after the
	// response rather than abruptly resetting the client-side loopback socket.
	_, _, err = connection.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("expected normal relay close after upstream EOF, got %v", err)
	}
}

func TestUDPRelayPreservesDatagrams(t *testing.T) {
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buffer := make([]byte, 64)
		count, sender, readErr := upstream.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = upstream.WriteToUDP(buffer[:count], sender)
		}
	}()

	basePort := upstream.LocalAddr().(*net.UDPAddr).Port - 9
	server, token, leaseID := testRelayServer(t, "127.0.0.1", basePort)
	defer server.Close()
	connection := dialTestRelay(t, server.URL, token, leaseID, "udp", 9)
	defer connection.Close()

	message := []byte("one UDP datagram")
	if err := connection.WriteMessage(websocket.BinaryMessage, message); err != nil {
		t.Fatal(err)
	}
	messageType, response, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage || string(response) != string(message) {
		t.Fatalf("unexpected relay response: type=%d body=%q", messageType, response)
	}
}

func TestRelayRejectsUnauthorizedAndUnapprovedChannels(t *testing.T) {
	server, token, leaseID := testRelayServer(t, "127.0.0.1", 47989)
	defer server.Close()

	websocketURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/v1/relay?leaseId=" + url.QueryEscape(leaseID) + "&transport=tcp&offset=0"
	_, response, err := websocket.DefaultDialer.Dial(websocketURL, nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized handshake, got response=%v error=%v", response, err)
	}
	response.Body.Close()

	header := http.Header{"Authorization": []string{"Bearer " + token}}
	websocketURL = strings.Replace(server.URL, "http://", "ws://", 1) +
		"/v1/relay?leaseId=" + url.QueryEscape(leaseID) + "&transport=tcp&offset=123"
	_, response, err = websocket.DefaultDialer.Dial(websocketURL, header)
	if err == nil || response == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid-channel handshake, got response=%v error=%v", response, err)
	}
	response.Body.Close()
}

func testRelayServer(t *testing.T, address string, basePort int) (*httptest.Server, string, string) {
	t.Helper()
	store := NewStore([]VM{{
		ID: "vm-1", DisplayName: "Relay VM", StreamAddress: address,
		StreamPort: basePort, Enabled: true,
	}}, time.Minute, filepath.Join(t.TempDir(), "state.json"))
	broker := &AuthBroker{
		devAuthEnabled: true, pending: make(map[string]*pendingAuth),
		stateToRequest: make(map[string]string), sessions: make(map[string]coordinatorSession),
		now: time.Now,
	}
	server := newServer(store, broker)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/dev", broker.DevLogin)
	mux.HandleFunc("GET /v1/relay", server.auth(server.relay))
	testServer := httptest.NewServer(mux)
	token := developmentToken(t, mux, "relay-device")
	owner := broker.AuthenticateToken(token)
	lease, _, err := store.CreateOrRecover(owner, "relay-device", "Relay test")
	if err != nil {
		t.Fatal(err)
	}
	return testServer, token, lease.ID
}

func dialTestRelay(t *testing.T, serverURL, token, leaseID, transport string, offset int) *websocket.Conn {
	t.Helper()
	websocketURL := strings.Replace(serverURL, "http://", "ws://", 1) +
		"/v1/relay?leaseId=" + url.QueryEscape(leaseID) +
		"&transport=" + url.QueryEscape(transport) + "&offset=" + url.QueryEscape(strconv.Itoa(offset))
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	connection, response, err := websocket.DefaultDialer.Dial(websocketURL, header)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	return connection
}
