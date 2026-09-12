package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTurnCredentialsBaseURL = "https://rtc.live.cloudflare.com/v1/turn/keys"
	turnCredentialTTL             = 24 * time.Hour
)

type TurnProvider struct {
	keyID         string
	apiToken      string
	baseURL       string
	httpClient    *http.Client
	credentialTTL time.Duration
}

type TurnCredentials struct {
	Server     string
	Port       int
	Username   string
	Credential string
	ExpiresAt  time.Time
}

func NewTurnProviderFromEnvironment() (*TurnProvider, error) {
	keyID := strings.TrimSpace(os.Getenv("CLOUDFLARE_TURN_KEY_ID"))
	apiToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_TURN_API_TOKEN"))
	if keyID == "" && apiToken == "" {
		return nil, nil
	}
	if keyID == "" || apiToken == "" {
		return nil, errors.New("CLOUDFLARE_TURN_KEY_ID and CLOUDFLARE_TURN_API_TOKEN must both be set")
	}
	return &TurnProvider{
		keyID: keyID, apiToken: apiToken, baseURL: defaultTurnCredentialsBaseURL,
		httpClient: &http.Client{Timeout: 8 * time.Second}, credentialTTL: turnCredentialTTL,
	}, nil
}

func (p *TurnProvider) Credentials(ctx context.Context) (TurnCredentials, error) {
	requestBody, err := json.Marshal(map[string]int{"ttl": int(p.credentialTTL.Seconds())})
	if err != nil {
		return TurnCredentials{}, err
	}
	endpoint := strings.TrimRight(p.baseURL, "/") + "/" + url.PathEscape(p.keyID) +
		"/credentials/generate-ice-servers"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return TurnCredentials{}, err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := p.httpClient.Do(request)
	if err != nil {
		return TurnCredentials{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return TurnCredentials{}, fmt.Errorf("Cloudflare TURN credentials returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		ICEServers []struct {
			URLs       []string `json:"urls"`
			Username   string   `json:"username"`
			Credential string   `json:"credential"`
		} `json:"iceServers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return TurnCredentials{}, fmt.Errorf("decode Cloudflare TURN credentials: %w", err)
	}
	for _, iceServer := range payload.ICEServers {
		if iceServer.Username == "" || iceServer.Credential == "" {
			continue
		}
		for _, rawURL := range iceServer.URLs {
			server, port, ok := parseTurnUDPURL(rawURL)
			if ok {
				return TurnCredentials{
					Server: server, Port: port, Username: iceServer.Username,
					Credential: iceServer.Credential, ExpiresAt: time.Now().UTC().Add(p.credentialTTL),
				}, nil
			}
		}
	}
	return TurnCredentials{}, errors.New("Cloudflare response did not include a TURN/UDP endpoint")
}

func parseTurnUDPURL(rawURL string) (string, int, bool) {
	if !strings.HasPrefix(rawURL, "turn:") || !strings.Contains(rawURL, "transport=udp") {
		return "", 0, false
	}
	address := strings.TrimPrefix(strings.SplitN(rawURL, "?", 2)[0], "turn:")
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || host == "" {
		return "", 0, false
	}
	return host, port, true
}
