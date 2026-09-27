package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"clipp-relay/internal/auth"
	ma "github.com/multiformats/go-multiaddr"
)

// Discovery publishes an atomic, complete transport snapshot for this process ID.
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
	return d.PublishAt(addresses, time.Now())
}

// PublishAt retains the oldest source verification time when a snapshot is
// assembled from independent Service watches. Reuse cannot renew stale data.
func (d *Discovery) PublishAt(addresses []ma.Multiaddr, verified time.Time) error {
	if len(addresses) == 0 {
		d.withdraw()
		return nil
	}
	if verified.IsZero() || time.Since(verified) > 5*time.Minute {
		d.withdraw()
		return errors.New("relay address snapshot unverified or stale")
	}
	if !d.server.Serving() || len(d.server.Host.Network().ListenAddresses()) == 0 {
		d.withdraw()
		return errors.New("relay listener unavailable")
	}
	for _, transport := range []struct {
		name    string
		enabled bool
	}{{"tcp", d.server.tcpEnabled}, {"websocket", d.server.wsEnabled}, {"webrtc-direct", d.server.webrtcEnabled}} {
		if !transport.enabled {
			continue
		}
		if _, err := d.server.ListenAddressFor(transport.name); err != nil {
			d.withdraw()
			return errors.New("enabled relay listener unavailable")
		}
	}
	set := map[string]bool{}
	seen := map[string]bool{}
	currentWebRTCHash := ""
	if d.server.webrtcEnabled {
		bound, err := d.server.ListenAddressFor("webrtc-direct")
		if err != nil {
			d.withdraw()
			return errors.New("WebRTC Direct listener unavailable")
		}
		currentWebRTCHash, err = bound.ValueForProtocol(ma.P_CERTHASH)
		if err != nil {
			d.withdraw()
			return errors.New("WebRTC Direct certificate unavailable")
		}
	}
	for _, a := range addresses {
		if a == nil {
			d.withdraw()
			return errors.New("nil relay address")
		}
		if pid, err := a.ValueForProtocol(ma.P_P2P); err == nil && pid != d.server.Host.ID().String() {
			d.withdraw()
			return errors.New("address names another relay")
		}
		var kind string
		parts := a.Protocols()
		if len(parts) > 0 && parts[len(parts)-1].Code == ma.P_P2P {
			a, _ = ma.SplitLast(a)
			parts = parts[:len(parts)-1]
		}
		switch {
		case len(parts) == 2 && parts[1].Code == ma.P_TCP && d.server.tcpEnabled && (parts[0].Code == ma.P_IP4 || parts[0].Code == ma.P_IP6 || parts[0].Code == ma.P_DNS || parts[0].Code == ma.P_DNS4 || parts[0].Code == ma.P_DNS6):
			kind = "tcp"
		case len(parts) == 4 && parts[1].Code == ma.P_TCP && parts[2].Code == ma.P_TLS && parts[3].Code == ma.P_WS && d.server.wsEnabled:
			name, err := a.ValueForProtocol(parts[0].Code)
			if err == nil && (parts[0].Code == ma.P_DNS4 || parts[0].Code == ma.P_DNS6) && name == d.server.webSocketHostname {
				kind = "websocket"
			}
		case (len(parts) == 3 || len(parts) == 4) && parts[1].Code == ma.P_UDP && parts[2].Code == ma.P_WEBRTC_DIRECT && d.server.webrtcEnabled:
			if parts[0].Code == ma.P_IP4 || parts[0].Code == ma.P_IP6 || parts[0].Code == ma.P_DNS || parts[0].Code == ma.P_DNS4 || parts[0].Code == ma.P_DNS6 {
				kind = "webrtc-direct"
				if len(parts) == 4 {
					if parts[3].Code != ma.P_CERTHASH {
						kind = ""
					} else if hash, err := a.ValueForProtocol(ma.P_CERTHASH); err != nil || hash != currentWebRTCHash {
						kind = ""
					}
				} else {
					var join error
					a, join = ma.NewMultiaddr(a.String() + "/certhash/" + currentWebRTCHash)
					if join != nil {
						kind = ""
					}
				}
			}
		}
		if kind == "" {
			d.withdraw()
			return errors.New("invalid or disabled relay transport address")
		}
		seen[kind] = true
		var join error
		a, join = ma.NewMultiaddr(a.String() + "/p2p/" + d.server.Host.ID().String())
		if join != nil {
			d.withdraw()
			return join
		}
		set[a.String()] = true
	}
	if seen["tcp"] != d.server.tcpEnabled || seen["websocket"] != d.server.wsEnabled || seen["webrtc-direct"] != d.server.webrtcEnabled {
		d.withdraw()
		return errors.New("incomplete enabled relay transport set")
	}
	complete := make([]string, 0, len(set))
	for a := range set {
		complete = append(complete, a)
	}
	sort.Strings(complete)
	size := 256
	for _, address := range complete {
		size += len(address) + 3
	}
	if size > 16<<10 {
		d.withdraw()
		return errors.New("relay address snapshot exceeds discovery limit")
	}
	d.mu.Lock()
	d.addresses = complete
	d.verified = verified
	d.mu.Unlock()
	return nil
}

// Published reports whether the complete snapshot is still fresh and the
// process is admitting traffic. It uses only local state for readiness.
func (d *Discovery) Published() bool {
	d.mu.RLock()
	complete, verified := len(d.addresses) != 0, d.verified
	d.mu.RUnlock()
	return complete && time.Since(verified) <= 5*time.Minute && d.server.Serving()
}

func (d *Discovery) withdraw() {
	d.mu.Lock()
	d.addresses = nil
	d.verified = time.Time{}
	d.mu.Unlock()
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
	if !d.server.Serving() {
		discoveryError(w, 503, "temporarily_unavailable")
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
	d.mu.RLock()
	addresses := append([]string(nil), d.addresses...)
	verified := d.verified
	d.mu.RUnlock()
	if len(addresses) == 0 || time.Since(verified) > 5*time.Minute {
		discoveryError(w, 503, "temporarily_unavailable")
		return
	}
	if d.server.ActiveSessions() >= d.server.maxSessions {
		discoveryError(w, 429, "rate_limited")
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
