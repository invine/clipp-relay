package relay

import (
	"clipp-relay/internal/auth"
	"context"
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
