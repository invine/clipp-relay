package auth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestEstablishedCircuitRequiresHealthyNewWeekAuthorityDespiteSuccessfulWrites(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("authority-unavailable=%v", unavailable), func(t *testing.T) {
			var tick atomic.Int64
			var failed atomic.Bool
			base := time.Now()
			utc := base.UTC()
			monday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7-(int(utc.Weekday())+6)%7)
			shift := monday.Add(-time.Second).Sub(base)
			p := protectedPortalFixtureWithAccounting(t, func(q *quota.Quota) {
				q.SetAccountingBoundariesForTest(func(at time.Time) time.Time {
					if tick.Load() > 0 {
						return at.Add(shift + 2*time.Second)
					}
					return at.Add(shift)
				}, func() time.Time { return base.Add(time.Duration(tick.Load()) * time.Second) }, func(context.Context) error {
					if failed.Load() {
						return quota.ErrTemporary
					}
					return nil
				})
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			initial := p.relay.Host.Network().Conns()
			if len(initial) != 2 {
				t.Fatalf("expected both physical endpoints, got %d", len(initial))
			}
			ai := peer.AddrInfo{ID: p.relay.Host.ID(), Addrs: p.relay.Host.Addrs()}
			if _, err := circuitclient.Reserve(ctx, dest, ai); err != nil {
				t.Fatal(err)
			}
			route := ma.StringCast(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", p.relay.Host.ID(), dest.ID()))
			if err := src.Connect(ctx, peer.AddrInfo{ID: dest.ID(), Addrs: []ma.Multiaddr{route}}); err != nil {
				t.Fatal(err)
			}
			dest.SetStreamHandler("/clipp/rollover/1", func(st network.Stream) { defer st.Close(); _, _ = io.Copy(st, st) })
			st, err := src.NewStream(network.WithAllowLimitedConn(ctx, "existing circuit"), dest.ID(), "/clipp/rollover/1")
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if deadline, ok := ctx.Deadline(); ok {
				_ = st.SetDeadline(deadline)
			}
			transfer := func() {
				t.Helper()
				if _, err := st.Write([]byte("hello")); err != nil {
					t.Fatal(err)
				}
				var echo [5]byte
				if _, err := io.ReadFull(st, echo[:]); err != nil || string(echo[:]) != "hello" {
					t.Fatalf("existing circuit: %q %v", echo, err)
				}
			}
			transfer()
			failed.Store(unavailable)
			// Advance independently of database progress. Every observation is successfully
			// recorded; only authoritative new-week eligibility is unavailable.
			for second := int64(1); second <= 3; second++ {
				if second < 3 {
					transfer()
				}
				tick.Store(second)
				if err := p.quota.Observe(p.account, 100); err != nil {
					t.Fatal(err)
				}
				if err := p.quota.FlushAccounting(ctx, p.account); err != nil {
					t.Fatal(err)
				}
				_, err := p.quota.Eligibility(ctx, p.account)
				if unavailable && !errors.Is(err, quota.ErrTemporary) {
					t.Fatalf("authority=%v", err)
				}
				if !unavailable && err != nil {
					t.Fatal(err)
				}
				p.quota.CheckAccountingHealth()
			}
			usage, err := p.quota.RecordedUsage(ctx, p.account, monday)
			if err != nil || usage.RecordedBytes < 300 {
				t.Fatalf("writes did not progress: %+v %v", usage, err)
			}
			if !unavailable {
				transfer()
				if p.relay.AccountSessions(p.account) != 2 {
					t.Fatal("healthy rollover forced reauthentication")
				}
				return
			}
			until := time.Now().Add(time.Second)
			for p.relay.AccountSessions(p.account) != 0 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if p.relay.AccountSessions(p.account) != 0 {
				t.Fatal("successful writes concealed missing new-week authority")
			}
			for _, conn := range initial {
				for !conn.IsClosed() && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if !conn.IsClosed() {
					t.Fatal("authority cutoff left physical session open")
				}
			}
			_ = st.SetReadDeadline(time.Now().Add(time.Second))
			var b [1]byte
			_, err = io.ReadFull(st, b[:])
			var timeout net.Error
			if err == nil || errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatalf("circuit did not abort: %v", err)
			}
		})
	}
}
