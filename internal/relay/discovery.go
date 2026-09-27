package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"clipp-relay/internal/auth"
	ma "github.com/multiformats/go-multiaddr"
)

// Discovery publishes an atomic, complete TCP snapshot for this process ID.
// A missing or stale snapshot withdraws the entire document.
type Discovery struct {
	server    *Server
	authority Authority
	host      string
	mu        sync.RWMutex
	addresses []string
	verified  time.Time
}

func NewDiscovery(server *Server, authority Authority, host string, addresses []ma.Multiaddr) (*Discovery, error) {
	if host == "" || strings.ContainsAny(host, " /") {
		return nil, errors.New("canonical discovery host required")
	}
	d := &Discovery{server: server, authority: authority, host: host}
	if err := d.Publish(addresses); err != nil {
		return nil, err
	}
	return d, nil
}

// Publish replaces the whole set after validating every address and binding
// it to the live Peer ID. An empty set deliberately withdraws discovery.
func (d *Discovery) Publish(addresses []ma.Multiaddr) error {
	set := map[string]bool{}
	for _, a := range addresses {
		if a == nil {
			return errors.New("nil relay address")
		}
		if _, err := a.ValueForProtocol(ma.P_TCP); err != nil {
			return errors.New("non-TCP address in TCP snapshot")
		}
		if pid, err := a.ValueForProtocol(ma.P_P2P); err == nil && pid != d.server.Host.ID().String() {
			return errors.New("address names another relay")
		}
		if _, err := a.ValueForProtocol(ma.P_P2P); err != nil {
			var join error
			a, join = ma.NewMultiaddr(a.String() + "/p2p/" + d.server.Host.ID().String())
			if join != nil {
				return join
			}
		}
		set[a.String()] = true
	}
	complete := make([]string, 0, len(set))
	for a := range set {
		complete = append(complete, a)
	}
	d.mu.Lock()
	d.addresses = complete
	d.verified = time.Now()
	d.mu.Unlock()
	return nil
}

func discoveryError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status == 429 || status == 503 {
		w.Header().Set("Retry-After", "5")
	}
	w.WriteHeader(status)
	if status == 429 || status == 503 {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "retryAfterMillis": 5000})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code})
	}
}

func (d *Discovery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" || r.URL.Path != "/v1/relay" || r.URL.RawQuery != "" || r.Host != d.host || r.ContentLength > 0 || r.Header.Get("Content-Encoding") != "" {
		discoveryError(w, 400, "invalid_request")
		return
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") || strings.Count(authHeader, " ") != 1 {
		discoveryError(w, 401, "authentication_failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, err := d.authority.AuthenticateRelay(ctx, strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidAccess) {
			discoveryError(w, 401, "authentication_failed")
		} else {
			discoveryError(w, 503, "temporarily_unavailable")
		}
		return
	}
	if !d.server.Serving() {
		discoveryError(w, 503, "temporarily_unavailable")
		return
	}
	if d.server.ActiveSessions() >= d.server.maxSessions {
		discoveryError(w, 429, "rate_limited")
		return
	}
	d.mu.RLock()
	addresses := append([]string(nil), d.addresses...)
	verified := d.verified
	d.mu.RUnlock()
	if len(addresses) == 0 || time.Since(verified) > 5*time.Minute {
		discoveryError(w, 503, "temporarily_unavailable")
		return
	}
	expiry := time.Now().Add(time.Minute)
	if stale := verified.Add(5 * time.Minute); stale.Before(expiry) {
		expiry = stale
	}
	document := struct {
		Version int `json:"version"`
		Relay   struct {
			PeerID    string   `json:"peerId"`
			Addresses []string `json:"addresses"`
		} `json:"relay"`
		ValidUntil string `json:"validUntil"`
	}{Version: 1, ValidUntil: expiry.UTC().Format(time.RFC3339Nano)}
	document.Relay.PeerID = d.server.Host.ID().String()
	document.Relay.Addresses = addresses
	data, err := json.Marshal(document)
	if err != nil || len(data) > 16<<10 {
		discoveryError(w, 503, "temporarily_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
