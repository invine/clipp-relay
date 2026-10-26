package auth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/portal"
	"clipp-relay/internal/relay"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestObservedRealCircuitSettlesDuringDrainAndFinalFlush(t *testing.T) {
	for _, mode := range []string{"complete", "blocked-final", "forced-active", "forced-after-accounting-cutoff"} {
		t.Run(mode, func(t *testing.T) {
			p := protectedPortalFixture(t)
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
			// Capture the exact admitted physical connections at the Relay boundary.
			// Remote swarm registries may retain a half after its streams have reset.
			initialRelay := p.relay.Host.Network().Conns()
			if len(initialRelay) != 2 {
				t.Fatalf("expected both admitted physical endpoints, got %d", len(initialRelay))
			}
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
			if mode == "forced-active" || mode == "forced-after-accounting-cutoff" {
				if mode == "forced-after-accounting-cutoff" {
					p.quota.AccountingFailed(p.account)
				}
				stop()
				select {
				case err := <-finished:
					// Accounting cutoff can finish the circuit just before the
					// forced context is observed. Either completion must
					// leave the same exact transports closed below.
					if err != context.Canceled && !(mode == "forced-after-accounting-cutoff" && err == nil) {
						t.Fatalf("forced=%v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("forced drain blocked")
				}
				for _, conn := range initialRelay {
					if !conn.IsClosed() {
						t.Fatalf("forced drain left admitted physical connection %s open", conn.ID())
					}
				}
				if p.relay.AccountSessions(p.account) != 0 || len(p.relay.Host.Network().Conns()) != 0 {
					t.Fatal("forced drain retained local session authority")
				}
				// The actual admitted circuit must abort too; a timeout is not
				// evidence of closure. Checking only the remote swarm's Conn
				// registry races its independent transport cleanup.
				_ = st.SetReadDeadline(time.Now().Add(time.Second))
				var byteAfterForce [1]byte
				_, readErr := io.ReadFull(st, byteAfterForce[:])
				if readErr == nil {
					t.Fatal("forced circuit still carried traffic")
				}
				var timeout net.Error
				if errors.As(readErr, &timeout) && timeout.Timeout() {
					t.Fatalf("forced circuit failed to abort: %v", readErr)
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
				// Fresh production components load the confirmed total and receipt without
				// reconstructing process-local traffic or inheriting physical sessions.
				material, e := p.config.ReadMaterial()
				if e != nil {
					t.Fatal(e)
				}
				restartedAuth := auth.New(p.auth.Pool, p.config, material, p.provider.endpoints())
				if err := restartedAuth.EnableNativeOAuth(material.NativeSigningKey); err != nil {
					t.Fatal(err)
				}
				restartedQuota := newQuota(p.auth.Pool)
				defer restartedQuota.Close()
				restartedRelay, e := relay.NewProtected(restartedAuth, restartedQuota, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
				if e != nil {
					t.Fatal(e)
				}
				defer restartedRelay.Close()
				if restartedRelay.Host.ID() == p.relay.Host.ID() || restartedRelay.AccountSessions(p.account) != 0 {
					t.Fatal("restart inherited relay identity or sessions")
				}
				got, e := restartedQuota.Eligibility(ctx, p.account)
				if e != nil || got.RecordedBytes != usage.RecordedBytes || got.EffectiveBytes != usage.RecordedBytes {
					t.Fatalf("restart accounting %+v %v", got, e)
				}
				receipt, e := restartedQuota.RecordedUsage(ctx, p.account, week)
				if e != nil || receipt != usage {
					t.Fatalf("restart altered receipt: %+v %+v %v", receipt, usage, e)
				}
				view := portalRequest(restartedAuth, "GET", "/", p.owner, nil, "")
				if view.Code != 200 || !strings.Contains(view.Body.String(), "Recorded Relay Usage: "+portal.FormatBytes(usage.RecordedBytes)) {
					t.Fatalf("restart portal: %d %s", view.Code, view.Body.String())
				}
				restarted := p
				restarted.auth = restartedAuth
				restarted.quota = restartedQuota
				restarted.relay = restartedRelay
				requireProtectedAuthentication(t, protectedClient(t, restarted), restarted, `"ok":true`)

			}
		})
	}
}
