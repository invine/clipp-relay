package relay

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

type validationOnlyAuthority struct{ calls atomic.Int64 }

func (a *validationOnlyAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	a.calls.Add(1)
	return auth.RelayCredential{AccountID: "relay-account", SessionLimit: 2, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func TestRelayAuthenticationNeedsOnlyCredentialValidation(t *testing.T) {
	authority := &validationOnlyAuthority{}
	s, err := New(authority, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	response := authResponse(t, ctx, h, s.Host.ID(), "authorized")
	if !strings.Contains(response, `"ok":true`) {
		t.Fatalf("relay authentication failed: %s", response)
	}
	if authority.calls.Load() != 1 || s.AccountSessions("relay-account") != 1 {
		t.Fatalf("validation calls=%d installed sessions=%d", authority.calls.Load(), s.AccountSessions("relay-account"))
	}
}
