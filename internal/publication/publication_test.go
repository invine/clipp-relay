package publication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
	ma "github.com/multiformats/go-multiaddr"
)

type testAuthority struct{}

func (testAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{AccountID: "a", SessionLimit: 1, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type testCredit struct{}

func (testCredit) Ensure(context.Context, string, int64) (quota.Result, error) {
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}
func (testCredit) Take(context.Context, string, int64, int64) (quota.Result, error) {
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}

func TestServiceChangesAndOutagePublishCompleteSnapshot(t *testing.T) {
	var mu sync.Mutex
	ingress := []map[string]string{{"ip": "127.0.0.1"}}
	broken := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if broken {
			http.Error(w, "unavailable", 503)
			return
		}
		if r.URL.Path != "/api/v1/namespaces/clipp/services/relay-tcp" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "spec": map[string]any{"ports": []map[string]any{{"protocol": "TCP", "port": 4001}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": ingress}}})
	}))
	defer api.Close()
	s, err := relay.New(testAuthority{}, testCredit{}, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := relay.NewDiscovery(s, testAuthority{}, "relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := false
	c, err := New(d, api.Client(), api.URL, "token", Config{TCP: Transport{Enabled: true, PublicPort: 4001, Service: ServiceRef{Namespace: "clipp", Name: "relay-tcp"}}}, func(v bool) { ready = v })
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background()); err != nil || !ready {
		t.Fatalf("first sync: %v ready=%v", err, ready)
	}
	check := func(want []string) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
		r.Header.Set("Authorization", "Bearer authorized")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("discovery %d: %s", w.Code, w.Body.String())
		}
		var doc struct {
			Relay struct {
				Addresses []string `json:"addresses"`
			} `json:"relay"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Relay.Addresses) != len(want) {
			t.Fatalf("addresses %v want %v", doc.Relay.Addresses, want)
		}
		for i, v := range want {
			if doc.Relay.Addresses[i][:len(v)] != v {
				t.Fatalf("addresses %v want %v", doc.Relay.Addresses, want)
			}
		}
	}
	check([]string{"/ip4/127.0.0.1/tcp/4001"})
	mu.Lock()
	ingress = []map[string]string{{"ip": "127.0.0.2"}, {"ip": "127.0.0.1"}, {"ip": "127.0.0.2"}}
	mu.Unlock()
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	check([]string{"/ip4/127.0.0.1/tcp/4001", "/ip4/127.0.0.2/tcp/4001"})
	c.now = func() time.Time { return time.Now().Add(-4*time.Minute - 59*time.Second) }
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	var nearlyStale struct {
		ValidUntil time.Time `json:"validUntil"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &nearlyStale); err != nil || time.Until(nearlyStale.ValidUntil) > 2*time.Second {
		t.Fatalf("staleness cap %s %v", w.Body.String(), err)
	}
	c.now = time.Now
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	d.ServeHTTP(w, r)
	var renewed struct {
		ValidUntil time.Time `json:"validUntil"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &renewed); err != nil || time.Until(renewed.ValidUntil) < 30*time.Second {
		t.Fatalf("unchanged resync did not verify %s %v", w.Body.String(), err)
	}
	mu.Lock()
	broken = true
	mu.Unlock()
	if err := c.Sync(context.Background()); err == nil || !ready {
		t.Fatalf("outage should retain last good: %v ready=%v", err, ready)
	}
	c.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	_ = c.Sync(context.Background())
	if ready || d.Published() {
		t.Fatal("stale publication remained ready")
	}
	mu.Lock()
	broken = false
	mu.Unlock()
	c.now = time.Now
	if err := c.Sync(context.Background()); err != nil || !ready {
		t.Fatalf("recovery: %v ready=%v", err, ready)
	}
	check([]string{"/ip4/127.0.0.1/tcp/4001", "/ip4/127.0.0.2/tcp/4001"})
}

func TestNamedServiceWatchRepublishesOnEvent(t *testing.T) {
	changed := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "1" {
			if r.URL.Path != "/api/v1/namespaces/clipp/services" || r.URL.Query().Get("fieldSelector") != "metadata.name=relay-tcp" {
				http.Error(w, "wrong watch", 400)
				return
			}
			select {
			case <-changed:
				_, _ = w.Write([]byte(`{"type":"MODIFIED"}` + "\n"))
			case <-r.Context().Done():
			}
			return
		}
		ip := "127.0.0.1"
		select {
		case <-changed:
			ip = "127.0.0.2"
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "spec": map[string]any{"ports": []map[string]any{{"protocol": "TCP", "port": 4001}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": []map[string]string{{"ip": ip}}}}})
	}))
	defer api.Close()
	s, err := relay.New(testAuthority{}, testCredit{}, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := relay.NewDiscovery(s, testAuthority{}, "relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(d, api.Client(), api.URL, "token", Config{TCP: Transport{Enabled: true, PublicPort: 4001, Service: ServiceRef{Namespace: "clipp", Name: "relay-tcp"}}}, func(bool) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for !d.Published() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !d.Published() {
		t.Fatal("initial publication missing")
	}
	close(changed)
	for time.Now().Before(deadline) {
		r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code == 200 && strings.Contains(w.Body.String(), "127.0.0.2") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watch event did not republish")
}

func TestOverrideRemovesOnlyItsTransportDependencyAndMissingUDPWithdrawsAll(t *testing.T) {
	available := true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/clipp/services/relay-udp" {
			http.NotFound(w, r)
			return
		}
		ingress := []map[string]string{{"ip": "127.0.0.1"}}
		if !available {
			ingress = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "spec": map[string]any{"ports": []map[string]any{{"protocol": "UDP", "port": 4003}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": ingress}}})
	}))
	defer api.Close()
	s, err := relay.New(testAuthority{}, testCredit{}, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := relay.NewDiscovery(s, testAuthority{}, "relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := false
	c, err := New(d, api.Client(), api.URL, "token", Config{TCP: Transport{Enabled: true, Overrides: []ma.Multiaddr{ma.StringCast("/ip4/127.0.0.1/tcp/4001")}}, WebRTC: Transport{Enabled: true, PublicPort: 4003, Service: ServiceRef{Namespace: "clipp", Name: "relay-udp"}}}, func(v bool) { ready = v })
	if err != nil {
		t.Fatal(err)
	}
	if len(c.sources) != 1 || c.sources["tcp"] != nil {
		t.Fatalf("override still watches TCP: %v", c.sources)
	}
	if err = c.Sync(context.Background()); err != nil || !ready {
		t.Fatalf("complete snapshot: %v ready=%v", err, ready)
	}
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/webrtc-direct/certhash/") || !strings.Contains(w.Body.String(), "/tcp/4001") {
		t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
	}
	available = false
	if err = c.Sync(context.Background()); err != nil || ready {
		t.Fatalf("missing UDP did not withdraw: %v ready=%v", err, ready)
	}
	w = httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("partial success: %d %s", w.Code, w.Body.String())
	}
}

func TestServiceHostnameKeepsDNSAddressFamilyOpen(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "spec": map[string]any{"ports": []map[string]any{{"protocol": "UDP", "port": 4003}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": []map[string]string{{"hostname": "ipv6-only.example.test"}}}}})
	}))
	defer api.Close()
	s, err := relay.New(testAuthority{}, testCredit{}, relay.Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := relay.NewDiscovery(s, testAuthority{}, "relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(d, api.Client(), api.URL, "token", Config{WebRTC: Transport{Enabled: true, PublicPort: 4003, Service: ServiceRef{Namespace: "clipp", Name: "relay-udp"}}}, func(bool) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/dns/ipv6-only.example.test/udp/4003/webrtc-direct/certhash/") {
		t.Fatalf("family-bound hostname: %d %s", w.Code, w.Body.String())
	}
}
