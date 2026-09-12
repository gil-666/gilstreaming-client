package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/pion/turn/v4"
)

var udpOffsets = []int{9, 10, 11, 13, 21}

type configuration struct {
	ServerAddress string `json:"serverAddress"`
	Username      string `json:"username"`
	Credential    string `json:"credential"`
	PeerAddress   string `json:"peerAddress"`
	PeerBasePort  int    `json:"peerBasePort"`
	LocalBasePort int    `json:"localBasePort"`
}

type localEndpoint struct {
	offset int
	socket *net.UDPConn
	mu     sync.RWMutex
	peer   *net.UDPAddr
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "TURN relay error:", err)
		os.Exit(1)
	}
}

func run() error {
	var config configuration
	decoder := json.NewDecoder(bufio.NewReader(io.LimitReader(os.Stdin, 64*1024)))
	if err := decoder.Decode(&config); err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}
	if err := validateConfiguration(config); err != nil {
		return err
	}

	turnSocket, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return fmt.Errorf("open TURN socket: %w", err)
	}
	defer turnSocket.Close()

	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: config.ServerAddress,
		TURNServerAddr: config.ServerAddress,
		Username:       config.Username,
		Password:       config.Credential,
		Software:       "GilStreaming",
		RTO:            250 * time.Millisecond,
		Conn:           turnSocket,
	})
	if err != nil {
		return fmt.Errorf("create TURN client: %w", err)
	}
	defer client.Close()
	if err := client.Listen(); err != nil {
		return fmt.Errorf("start TURN client: %w", err)
	}
	relayConnection, err := client.Allocate()
	if err != nil {
		return fmt.Errorf("allocate TURN relay: %w", err)
	}
	defer relayConnection.Close()

	peerBase, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(
		config.PeerAddress, strconv.Itoa(config.PeerBasePort)))
	if err != nil {
		return fmt.Errorf("resolve Sunshine peer: %w", err)
	}
	if err := client.CreatePermission(peerBase); err != nil {
		return fmt.Errorf("create TURN permission: %w", err)
	}

	endpoints := make(map[int]*localEndpoint, len(udpOffsets))
	for _, offset := range udpOffsets {
		address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: config.LocalBasePort + offset}
		socket, listenErr := net.ListenUDP("udp4", address)
		if listenErr != nil {
			closeEndpoints(endpoints)
			return fmt.Errorf("open local UDP port %d: %w", address.Port, listenErr)
		}
		_ = socket.SetReadBuffer(4 * 1024 * 1024)
		_ = socket.SetWriteBuffer(4 * 1024 * 1024)
		endpoints[offset] = &localEndpoint{offset: offset, socket: socket}
	}
	defer closeEndpoints(endpoints)

	errorsChannel := make(chan error, len(endpoints)+1)
	for _, endpoint := range endpoints {
		go relayLocalPackets(endpoint, relayConnection, peerBase, errorsChannel)
	}
	go relayRemotePackets(relayConnection, peerBase, config.PeerBasePort, endpoints, errorsChannel)

	// The parent waits for this exact line before allowing Moonlight to connect.
	fmt.Printf("READY %s\n", relayConnection.LocalAddr())

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChannel)

	select {
	case <-signalChannel:
		return nil
	case relayErr := <-errorsChannel:
		return relayErr
	}
}

func validateConfiguration(config configuration) error {
	if config.ServerAddress == "" || config.Username == "" || config.Credential == "" ||
		config.PeerAddress == "" {
		return errors.New("configuration is missing a required value")
	}
	if config.PeerBasePort < 6 || config.PeerBasePort+21 > 65535 ||
		config.LocalBasePort < 6 || config.LocalBasePort+21 > 65535 {
		return errors.New("configuration contains an invalid port range")
	}
	return nil
}

func relayLocalPackets(endpoint *localEndpoint, relayConnection net.PacketConn,
	peerBase *net.UDPAddr, errorsChannel chan<- error,
) {
	buffer := make([]byte, 64*1024)
	remote := &net.UDPAddr{IP: peerBase.IP, Port: peerBase.Port + endpoint.offset}
	for {
		count, sender, err := endpoint.socket.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		endpoint.mu.Lock()
		endpoint.peer = sender
		endpoint.mu.Unlock()
		if _, err = relayConnection.WriteTo(buffer[:count], remote); err != nil {
			reportError(errorsChannel, fmt.Errorf("send UDP offset %d through TURN: %w", endpoint.offset, err))
			return
		}
	}
}

func relayRemotePackets(relayConnection net.PacketConn, peerBase *net.UDPAddr,
	peerBasePort int, endpoints map[int]*localEndpoint, errorsChannel chan<- error,
) {
	buffer := make([]byte, 64*1024)
	for {
		count, sender, err := relayConnection.ReadFrom(buffer)
		if err != nil {
			reportError(errorsChannel, fmt.Errorf("receive TURN packet: %w", err))
			return
		}
		udpSender, ok := sender.(*net.UDPAddr)
		if !ok || !udpSender.IP.Equal(peerBase.IP) {
			continue
		}
		endpoint := endpoints[udpSender.Port-peerBasePort]
		if endpoint == nil {
			continue
		}
		endpoint.mu.RLock()
		localPeer := endpoint.peer
		endpoint.mu.RUnlock()
		if localPeer != nil {
			_, _ = endpoint.socket.WriteToUDP(buffer[:count], localPeer)
		}
	}
}

func closeEndpoints(endpoints map[int]*localEndpoint) {
	for _, endpoint := range endpoints {
		_ = endpoint.socket.Close()
	}
}

func reportError(errorsChannel chan<- error, err error) {
	select {
	case errorsChannel <- err:
	default:
	}
}
