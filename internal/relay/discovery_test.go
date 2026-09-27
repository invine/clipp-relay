package relay

import (
	"clipp-relay/internal/auth"
	"context"
	"encoding/json"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
)

func TestDiscoveryPublishesOnlyCurrentPeerAddresses(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr, err := s.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscovery(s, wireAuthority{}, "relay.example.test", []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer authorized")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), s.Host.ID().String()) || strings.Contains(w.Body.String(), "account") || len(w.Body.Bytes()) > 16<<10 {
		t.Fatalf("discovery: %d %s", w.Code, w.Body.String())
	}
	d.verified = time.Now().Add(-4 * time.Minute)
	before := httptest.NewRecorder()
	d.ServeHTTP(before, r)
	var first struct {
		ValidUntil string `json:"validUntil"`
	}
	if err = json.Unmarshal(before.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	bad, err := ma.NewMultiaddr("/ip4/127.0.0.1/udp/9999")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Publish([]ma.Multiaddr{bad}); err == nil {
		t.Fatal("accepted incomplete TCP snapshot")
	}
	after := httptest.NewRecorder()
	d.ServeHTTP(after, r)
	if first.ValidUntil == "" || after.Code != 503 || !d.verified.IsZero() {
		t.Fatalf("incomplete snapshot stayed published: %d %s", after.Code, after.Body.String())
	}
	if err = d.Publish([]ma.Multiaddr{addr}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err = d.Publish([]ma.Multiaddr{addr}); err == nil || !d.verified.IsZero() {
		t.Fatal("closed TCP listener refreshed publication")
	}
	closed := httptest.NewRecorder()
	d.ServeHTTP(closed, r)
	if closed.Code != 503 {
		t.Fatalf("closed listener remained published: %d", closed.Code)
	}
}

func TestDiscoverySortsAndDeduplicatesCompleteSnapshot(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := ma.StringCast("/ip4/127.0.0.2/tcp/4001")
	b := ma.StringCast("/ip4/127.0.0.1/tcp/4001")
	d, err := NewDiscovery(s, wireAuthority{}, "relay.example.test", []ma.Multiaddr{a, b, a})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer authorized")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	var doc struct {
		Relay struct {
			Addresses []string `json:"addresses"`
		} `json:"relay"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Relay.Addresses) != 2 || !strings.Contains(doc.Relay.Addresses[0], "127.0.0.1") || !strings.Contains(doc.Relay.Addresses[1], "127.0.0.2") {
		t.Fatalf("addresses: %v", doc.Relay.Addresses)
	}
}

func TestDrainingDiscoveryWithdrawsBeforeBackendAuth(t *testing.T) {
	a := &countingAuthority{}
	s, err := New(a, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	address, err := s.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscovery(s, a, "relay.example.test", []ma.Multiaddr{address})
	if err != nil {
		t.Fatal(err)
	}
	s.StartDrain()
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 503 || a.calls.Load() != 0 {
		t.Fatalf("drain discovery %d, auth calls %d", w.Code, a.calls.Load())
	}
}

type zeroCapAuthority struct{}

func (zeroCapAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{AccountID: "zero", SessionLimit: 0, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestDiscoverySeparatesAccountQuotaFromPublicationAndGlobalCapacity(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", MaxSessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr, err := s.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscovery(s, zeroCapAuthority{}, "relay.example.test", []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		return w
	}
	if w := request(); w.Code != 200 {
		t.Fatalf("zero-account-quota discovery: %d %s", w.Code, w.Body.String())
	}
	if err = d.Publish(nil); err != nil {
		t.Fatal(err)
	}
	if w := request(); w.Code != 503 || w.Header().Get("Retry-After") != "5" || !strings.Contains(w.Body.String(), `"retryAfterMillis":5000`) {
		t.Fatalf("withdrawn publication: %d %s", w.Code, w.Body.String())
	}
	if err = d.Publish([]ma.Multiaddr{addr}); err != nil {
		t.Fatal(err)
	}
	// Global capacity is independent of this account's zero plan.
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	sendWireAuth(t, ctx, h, s.Host.ID())
	if w := request(); w.Code != 429 || w.Header().Get("Retry-After") != "5" || !strings.Contains(w.Body.String(), `"retryAfterMillis":5000`) {
		t.Fatalf("global ceiling: %d %s", w.Code, w.Body.String())
	}
}
