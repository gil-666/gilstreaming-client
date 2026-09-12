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
	offset          int
	localSocket     *net.UDPConn
	turnSocket      net.PacketConn
	turnClient      *turn.Client
	relayConnection net.PacketConn
	remote          *net.UDPAddr
	mu              sync.RWMutex
	peer            *net.UDPAddr
	sentFirst       sync.Once
	receivedFirst   sync.Once
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

	peerBase, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(
		config.PeerAddress, strconv.Itoa(config.PeerBasePort)))
	if err != nil {
		return fmt.Errorf("resolve Sunshine peer: %w", err)
	}

	endpoints := make(map[int]*localEndpoint, len(udpOffsets))
	for _, offset := range udpOffsets {
		endpoint, openErr := openEndpoint(config, peerBase, offset)
		if openErr != nil {
			closeEndpoints(endpoints)
			return openErr
		}
		endpoints[offset] = endpoint
	}
	defer closeEndpoints(endpoints)

	errorsChannel := make(chan error, len(endpoints)*2)
	for _, endpoint := range endpoints {
		go relayLocalPackets(endpoint, errorsChannel)
		go relayRemotePackets(endpoint, peerBase, errorsChannel)
	}

	// The parent waits for this exact line before allowing Moonlight to connect.
	fmt.Printf("READY %d allocations\n", len(endpoints))

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

func openEndpoint(config configuration, peerBase *net.UDPAddr, offset int) (*localEndpoint, error) {
	localAddress := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: config.LocalBasePort + offset}
	localSocket, err := net.ListenUDP("udp4", localAddress)
	if err != nil {
		return nil, fmt.Errorf("open local UDP port %d: %w", localAddress.Port, err)
	}
	_ = localSocket.SetReadBuffer(4 * 1024 * 1024)
	_ = localSocket.SetWriteBuffer(4 * 1024 * 1024)

	turnSocket, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		_ = localSocket.Close()
		return nil, fmt.Errorf("open TURN socket for UDP offset %d: %w", offset, err)
	}
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
		_ = turnSocket.Close()
		_ = localSocket.Close()
		return nil, fmt.Errorf("create TURN client for UDP offset %d: %w", offset, err)
	}
	if err = client.Listen(); err != nil {
		client.Close()
		_ = turnSocket.Close()
		_ = localSocket.Close()
		return nil, fmt.Errorf("start TURN client for UDP offset %d: %w", offset, err)
	}
	relayConnection, err := client.Allocate()
	if err != nil {
		client.Close()
		_ = turnSocket.Close()
		_ = localSocket.Close()
		return nil, fmt.Errorf("allocate TURN relay for UDP offset %d: %w", offset, err)
	}
	remote := &net.UDPAddr{IP: peerBase.IP, Port: peerBase.Port + offset}
	if err = client.CreatePermission(remote); err != nil {
		_ = relayConnection.Close()
		client.Close()
		_ = turnSocket.Close()
		_ = localSocket.Close()
		return nil, fmt.Errorf("create TURN permission for UDP offset %d: %w", offset, err)
	}

	return &localEndpoint{
		offset: offset, localSocket: localSocket, turnSocket: turnSocket,
		turnClient: client, relayConnection: relayConnection, remote: remote,
	}, nil
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

func relayLocalPackets(endpoint *localEndpoint, errorsChannel chan<- error) {
	buffer := make([]byte, 64*1024)
	for {
		count, sender, err := endpoint.localSocket.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		endpoint.mu.Lock()
		endpoint.peer = sender
		endpoint.mu.Unlock()
		if _, err = endpoint.relayConnection.WriteTo(buffer[:count], endpoint.remote); err != nil {
			reportError(errorsChannel, fmt.Errorf("send UDP offset %d through TURN: %w", endpoint.offset, err))
			return
		}
		endpoint.sentFirst.Do(func() {
			fmt.Fprintf(os.Stderr, "UDP offset %d sent first packet through %s to %s\n",
				endpoint.offset, endpoint.relayConnection.LocalAddr(), endpoint.remote)
		})
	}
}

func relayRemotePackets(endpoint *localEndpoint, peerBase *net.UDPAddr, errorsChannel chan<- error) {
	buffer := make([]byte, 64*1024)
	for {
		count, sender, err := endpoint.relayConnection.ReadFrom(buffer)
		if err != nil {
			reportError(errorsChannel, fmt.Errorf("receive TURN packet: %w", err))
			return
		}
		udpSender, ok := sender.(*net.UDPAddr)
		if !ok || !udpSender.IP.Equal(peerBase.IP) || udpSender.Port != endpoint.remote.Port {
			continue
		}
		endpoint.mu.RLock()
		localPeer := endpoint.peer
		endpoint.mu.RUnlock()
		if localPeer != nil {
			if _, err = endpoint.localSocket.WriteToUDP(buffer[:count], localPeer); err != nil {
				reportError(errorsChannel, fmt.Errorf("deliver TURN packet for UDP offset %d: %w", endpoint.offset, err))
				return
			}
			endpoint.receivedFirst.Do(func() {
				fmt.Fprintf(os.Stderr, "UDP offset %d received first packet from %s\n",
					endpoint.offset, udpSender)
			})
		}
	}
}

func closeEndpoints(endpoints map[int]*localEndpoint) {
	for _, endpoint := range endpoints {
		_ = endpoint.localSocket.Close()
		_ = endpoint.relayConnection.Close()
		endpoint.turnClient.Close()
		_ = endpoint.turnSocket.Close()
	}
}

func reportError(errorsChannel chan<- error, err error) {
	select {
	case errorsChannel <- err:
	default:
	}
}
