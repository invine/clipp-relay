package relay

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

// Controlled accounting adapter records full callbacks and closes on exhaustion.
type finiteAccounting struct {
	wireAccounting
	mu       sync.Mutex
	observed int64
	server   *Server
}

func (c *finiteAccounting) Eligibility(context.Context, string) (quota.AccountingEligibility, error) {
	return quota.AccountingEligibility{SessionLimit: 5}, nil
}
func (c *finiteAccounting) Observe(id string, n int64) error {
	if id != "a" {
		return nil
	}
	c.mu.Lock()
	c.observed += n
	exhausted := c.observed >= 65536
	c.mu.Unlock()
	if exhausted {
		return quota.ErrExhausted
	}
	return nil
}
func (c *finiteAccounting) AccountingFailed(id string) {
	c.server.CloseAccountWithReason(id, "quota_exhausted")
}

func TestObservedUsageClosesAllAccountSessionsOnExhaustion(t *testing.T) {
	runAccountCutoff(t, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"}, "tcp")
}

func runAccountCutoff(t *testing.T, opts Options, transport string) {
	t.Helper()
	accounting := &finiteAccounting{}
	s, err := New(twoAuthority{}, accounting, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	accounting.server = s
	clients := make([]host.Host, 3)
	for i := range clients {
		clients[i], err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	addr, err := s.ListenAddressFor(transport)
	if err != nil {
		t.Fatal(err)
	}
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{addr}}
	for i, h := range clients {
		if err = h.Connect(ctx, ai); err != nil {
			t.Fatal(err)
		}
		token := "a"
		if i == 2 {
			token = "b"
		}
		sendWireToken(t, ctx, h, s.Host.ID(), token)
	}
	if got := s.AccountSessions("a"); got != 2 {
		t.Fatalf("initial account sessions %d", got)
	}
	if _, err = client.Reserve(ctx, clients[0], ai); err != nil {
		t.Fatal(err)
	}
	clients[0].SetStreamHandler("/clipp/quota-test/1.0.0", func(st network.Stream) {
		defer st.Close()
		buffer := make([]byte, 4096)
		for {
			if _, e := st.Read(buffer); e != nil {
				return
			}
		}
	})
	raddr, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", s.Host.ID(), clients[0].ID()))
	if err != nil {
		t.Fatal(err)
	}
	if err = clients[2].Connect(ctx, peer.AddrInfo{ID: clients[0].ID(), Addrs: []ma.Multiaddr{raddr}}); err != nil {
		t.Fatal(err)
	}
	st, err := clients[2].NewStream(network.WithAllowLimitedConn(ctx, "quota"), clients[0].ID(), "/clipp/quota-test/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 128<<10)
	copy(data, []byte("private-relayed-payload-canary"))
	for off := 0; off < len(data); off += 4096 {
		if _, err = st.Write(data[off : off+4096]); err != nil {
			break
		}
	}
	_ = st.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.AccountSessions("a") != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("exhausted account retained %d sessions", got)
	}
}

func TestWSSAndWebRTCAccountWideCutoff(t *testing.T) {
	// The stock transport may deliver an already-buffered tail after cutoff.
	// This probe asserts account-wide closure, not an unproven exact byte cap.
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) { runAccountCutoff(t, tc.opts, tc.name) })
	}
}
