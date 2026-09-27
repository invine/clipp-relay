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

type finiteCredit struct {
	mu        sync.Mutex
	remaining int64
	funded    bool
}

func (c *finiteCredit) Ensure(_ context.Context, id string, _ int64) (quota.Result, error) {
	if id != "a" {
		return quota.Result{Committed: quota.BlockBytes, Usable: quota.BlockBytes}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.funded {
		c.remaining = quota.BlockBytes
		c.funded = true
	}
	if c.remaining == 0 {
		return quota.Result{}, quota.ErrExhausted
	}
	return quota.Result{Committed: quota.BlockBytes, Usable: c.remaining}, nil
}
func (c *finiteCredit) Take(_ context.Context, id string, _ int64, n int64) (quota.Result, error) {
	if id != "a" {
		return quota.Result{Committed: quota.BlockBytes, Usable: quota.BlockBytes}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > c.remaining {
		return quota.Result{}, quota.ErrTemporary
	}
	c.remaining -= n
	return quota.Result{Committed: quota.BlockBytes, Usable: c.remaining}, nil
}

func TestConfirmedPartialCreditClosesAllAccountSessionsOnExhaustion(t *testing.T) {
	credit := &finiteCredit{}
	s, err := New(twoAuthority{}, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
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
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
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
