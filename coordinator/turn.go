package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTurnCredentialsBaseURL = "https://rtc.live.cloudflare.com/v1/turn/keys"
	turnCredentialTTL             = 24 * time.Hour
	turnCredentialRefreshAdvance  = time.Hour
	turnCredentialMinimumValidity = 5 * time.Minute
	turnCredentialRetryDelay      = 15 * time.Second
)

type TurnProvider struct {
	keyID         string
	apiToken      string
	baseURL       string
	httpClient    *http.Client
	credentialTTL time.Duration
	mu            sync.RWMutex
	cached        TurnCredentials
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
	if credentials, ok := p.CachedCredentials(); ok {
		return credentials, nil
	}
	return p.refresh(ctx)
}

func (p *TurnProvider) CachedCredentials() (TurnCredentials, bool) {
	p.mu.RLock()
	credentials := p.cached
	p.mu.RUnlock()
	if credentials.Credential == "" ||
		!credentials.ExpiresAt.After(time.Now().UTC().Add(turnCredentialMinimumValidity)) {
		return TurnCredentials{}, false
	}
	return credentials, true
}

func (p *TurnProvider) Start(ctx context.Context) {
	go func() {
		var delay time.Duration
		for {
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}

			credentials, err := p.refresh(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("Cloudflare TURN credential refresh failed; retrying in %s: %v",
					turnCredentialRetryDelay, err)
				delay = turnCredentialRetryDelay
				continue
			}

			delay = time.Until(credentials.ExpiresAt.Add(-turnCredentialRefreshAdvance))
			if delay < time.Minute {
				delay = time.Minute
			}
			log.Printf("Cloudflare TURN credentials ready; refresh scheduled in %s",
				delay.Round(time.Minute))
		}
	}()
}

func (p *TurnProvider) refresh(ctx context.Context) (TurnCredentials, error) {
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
				credentials := TurnCredentials{
					Server: server, Port: port, Username: iceServer.Username,
					Credential: iceServer.Credential, ExpiresAt: time.Now().UTC().Add(p.credentialTTL),
				}
				p.mu.Lock()
				p.cached = credentials
				p.mu.Unlock()
				return credentials, nil
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
