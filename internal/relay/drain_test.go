package relay

import (
	"context"
	"fmt"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestDrainWaitsForStockCircuitAndForceCloses(t *testing.T) {
	for _, force := range []bool{false, true} {
		s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatal(err)
		}
		s.tracer.ConnectionOpened()
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() { finished <- s.Drain(ctx) }()
		deadline := time.Now().Add(time.Second)
		for s.Serving() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if s.Serving() {
			t.Fatal("drain did not reject new traffic")
		}
		select {
		case err := <-finished:
			t.Fatalf("closed active circuit early: %v", err)
		case <-time.After(40 * time.Millisecond):
		}
		if force {
			cancel()
		} else {
			s.tracer.ConnectionClosed(0)
		}
		select {
		case err := <-finished:
			if force && err != context.Canceled || !force && err != nil {
				t.Fatalf("drain result: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("drain did not finish")
		}
		cancel()
		_ = s.Close()
	}
}

func TestExistingTCPCircuitCarriesBytesDuringDrain(t *testing.T) {
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dest, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	src, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	for _, h := range []interface {
		Connect(context.Context, peer.AddrInfo) error
	}{dest, src} {
		if err = h.Connect(ctx, ai); err != nil {
			t.Fatal(err)
		}
	}
	sendWireToken(t, ctx, dest, s.Host.ID(), "a")
	sendWireToken(t, ctx, src, s.Host.ID(), "b")
	if _, err = client.Reserve(ctx, dest, ai); err != nil {
		t.Fatal(err)
	}
	route, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", s.Host.ID(), dest.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if err = src.Connect(ctx, peer.AddrInfo{ID: dest.ID(), Addrs: []ma.Multiaddr{route}}); err != nil {
		t.Fatal(err)
	}
	if n := s.tracer.active.Load(); n == 0 {
		t.Fatal("stock relay did not report an active circuit")
	}
	received := make(chan string, 1)
	dest.SetStreamHandler("/clipp/drain-test/1.0.0", func(st network.Stream) {
		defer st.Close()
		buf := make([]byte, 16)
		n, _ := st.Read(buf)
		received <- string(buf[:n])
	})
	finished := make(chan error, 1)
	go func() { finished <- s.Drain(ctx) }()
	deadline := time.Now().Add(time.Second)
	for s.Serving() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Serving() {
		t.Fatal("drain did not start")
	}
	st, err := src.NewStream(network.WithAllowLimitedConn(ctx, "existing circuit"), dest.ID(), "/clipp/drain-test/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Write([]byte("during drain")); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	select {
	case got := <-received:
		if got != "during drain" {
			t.Fatalf("received %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err = <-finished:
		t.Fatalf("drain closed active circuit: %v", err)
	default:
	}
	_ = src.Network().ClosePeer(dest.ID())
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("drain did not finish after circuit closed")
	}
}
