package relay

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
)

type failingAuthority struct{ err error }

type countingAuthority struct{ calls atomic.Int64 }

func (a *countingAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	a.calls.Add(1)
	return auth.RelayCredential{AccountID: "a", SessionLimit: 2, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (a failingAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{}, a.err
}

type failingCredit struct{ err error }

func (c failingCredit) Ensure(context.Context, string, int64) (quota.Result, error) {
	return quota.Result{}, c.err
}
func (c failingCredit) Take(context.Context, string, int64, int64) (quota.Result, error) {
	return quota.Result{}, c.err
}

func authResponse(t *testing.T, ctx context.Context, h host.Host, id peer.ID, token string) string {
	t.Helper()
	st, err := h.NewStream(ctx, id, AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.Write(frame(fmt.Sprintf(`{"accessToken":%q}`, token))); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	n, err := readSize(st)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, n)
	if _, err = io.ReadFull(st, data); err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func readSize(r io.Reader) (uint64, error) { return binary.ReadUvarint(byteReader{r}) }

func TestDrainRejectsAuthBeforeAuthorityCall(t *testing.T) {
	a := &countingAuthority{}
	s, err := New(a, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatal(response)
	}
	before := a.calls.Load()
	s.StartDrain()
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"code":"temporarily_unavailable"`) {
		t.Fatal(response)
	}
	if a.calls.Load() != before {
		t.Fatalf("authority called during drain: %d -> %d", before, a.calls.Load())
	}
}

func TestDelayedExtraAuthFrameIsRejected(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	st, err := h.NewStream(ctx, s.Host.ID(), AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.Write(frame(`{"accessToken":"authorized"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err = st.Write(frame(`{"accessToken":"authorized"}`)); err != nil {
		t.Fatal(err)
	}
	_ = st.CloseWrite()
	_, err = readSize(st)
	if err == nil || s.ActiveSessions() != 0 {
		t.Fatalf("extra frame accepted: read=%v sessions=%d", err, s.ActiveSessions())
	}
}

func TestInitialAuthErrorsAreDistinctAndCloseConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Authority
		c    Credit
		code string
	}{
		{"invalid token", failingAuthority{auth.ErrInvalidAccess}, wireCredit{}, "authentication_failed"},
		{"database outage", failingAuthority{errors.New("database unavailable")}, wireCredit{}, "temporarily_unavailable"},
		{"exhausted allowance", wireAuthority{}, failingCredit{quota.ErrExhausted}, "quota_exhausted"},
		{"uncertain funding", wireAuthority{}, failingCredit{quota.ErrTemporary}, "temporarily_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.a, tc.c, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
			ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
			if err = h.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			resp := authResponse(t, ctx, h, s.Host.ID(), "authorized")
			if !strings.Contains(resp, `"ok":false`) || !strings.Contains(resp, tc.code) {
				t.Fatalf("response %s", resp)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && len(h.Network().ConnsToPeer(s.Host.ID())) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			if n := len(h.Network().ConnsToPeer(s.Host.ID())); n != 0 {
				t.Fatalf("failed initial auth retained %d connections", n)
			}
		})
	}
}

func TestFailedRenewalAndAccountChangePreservePriorSession(t *testing.T) {
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err = h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if resp := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(resp, `"ok":true`) {
		t.Fatal(resp)
	}
	if resp := authResponse(t, ctx, h, s.Host.ID(), "invalid"); !strings.Contains(resp, "authentication_failed") {
		t.Fatal(resp)
	}
	time.Sleep(time.Second)
	if resp := authResponse(t, ctx, h, s.Host.ID(), "b"); !strings.Contains(resp, "account_change_requires_new_connection") {
		t.Fatal(resp)
	}
	if s.AccountSessions("a") != 1 || s.AccountSessions("b") != 0 {
		t.Fatal("failed renewal changed ownership")
	}
	if _, err = client.Reserve(ctx, h, ai); err != nil {
		t.Fatalf("old permission lost after failed renewal: %v", err)
	}
}
