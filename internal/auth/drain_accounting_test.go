package auth_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/relay"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestObservedRealCircuitSettlesDuringDrainAndFinalFlush(t *testing.T) {
	for _, mode := range []string{"complete", "blocked-final", "forced-active"} {
		t.Run(mode, func(t *testing.T) {
			p := protectedPortalFixtureWithRelay(t, relay.NewObservedProtected)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			create := func() host.Host {
				h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { h.Close() })
				if err = h.Connect(ctx, peer.AddrInfo{ID: p.relay.Host.ID(), Addrs: p.relay.Host.Addrs()}); err != nil {
					t.Fatal(err)
				}
				requireProtectedAuthentication(t, h, p, `"ok":true`)
				return h
			}
			dest, src := create(), create()
			ai := peer.AddrInfo{ID: p.relay.Host.ID(), Addrs: p.relay.Host.Addrs()}
			if _, err := circuitclient.Reserve(ctx, dest, ai); err != nil {
				t.Fatal(err)
			}
			route := ma.StringCast(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", p.relay.Host.ID(), dest.ID()))
			if err := src.Connect(ctx, peer.AddrInfo{ID: dest.ID(), Addrs: []ma.Multiaddr{route}}); err != nil {
				t.Fatal(err)
			}
			received := make(chan struct{}, 1)
			dest.SetStreamHandler("/clipp/accounting-drain/1.0.0", func(st network.Stream) { defer st.Close(); _, _ = io.Copy(io.Discard, st); received <- struct{}{} })
			st, err := src.NewStream(network.WithAllowLimitedConn(ctx, "existing circuit"), dest.ID(), "/clipp/accounting-drain/1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			drainCtx, stop := context.WithCancel(ctx)
			defer stop()
			finished := make(chan error, 1)
			go func() { finished <- p.relay.Drain(drainCtx) }()
			until := time.Now().Add(time.Second)
			for p.relay.Serving() && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if p.relay.Serving() {
				t.Fatal("drain did not start")
			}
			if _, err = st.Write([]byte(strings.Repeat("a", 4096))); err != nil {
				t.Fatal(err)
			}
			// The real circuit remains admitted while periodic settlement progresses.
			week := time.Now().UTC()
			week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -((int(week.Weekday()) + 6) % 7))
			var before int64
			until = time.Now().Add(2 * time.Second)
			for time.Now().Before(until) {
				usage, e := p.quota.RecordedUsage(ctx, p.account, week)
				if e == nil && usage.RecordedBytes >= 8192 {
					before = usage.RecordedBytes
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if before == 0 {
				t.Fatal("active drain did not record both endpoints")
			}
			select {
			case err := <-finished:
				t.Fatalf("drain ended with open circuit: %v", err)
			default:
			}
			if mode == "forced-active" {
				stop()
				select {
				case err := <-finished:
					if err != context.Canceled {
						t.Fatalf("forced=%v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("forced drain blocked")
				}
				until = time.Now().Add(time.Second)
				for len(src.Network().ConnsToPeer(p.relay.Host.ID())) != 0 && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if len(src.Network().ConnsToPeer(p.relay.Host.ID())) != 0 {
					t.Fatal("forced drain left physical session")
				}
				return
			}
			if mode == "blocked-final" {
				p.quota.SetSettlementHooksForTest(nil, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
				timer := time.AfterFunc(250*time.Millisecond, stop)
				defer timer.Stop()
			}
			if _, err = st.Write([]byte(strings.Repeat("b", 4096))); err != nil {
				t.Fatal(err)
			}
			st.Close()
			select {
			case <-received:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			src.Network().ClosePeer(dest.ID())
			select {
			case err := <-finished:
				if mode == "complete" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("final accounting exceeded drain budget")
			}
			if mode == "complete" {
				usage, e := p.quota.RecordedUsage(ctx, p.account, week)
				if e != nil || usage.RecordedBytes < before+8192 {
					t.Fatalf("final=%+v before=%d %v", usage, before, e)
				}
			}
		})
	}
}
