package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestReservationDiagnosticsDistinguishAuthenticationAndStockRenewal(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err := h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reserve(ctx, h, ai); err == nil {
		t.Fatal("unauthenticated reservation accepted")
	}
	waitRow(t, out, "relay.reservation", "refused", "authentication_required")
	sendWireAuth(t, ctx, h, s.Host.ID())
	waitRow(t, out, "relay.authentication", "success", "none")
	if _, err := client.Reserve(ctx, h, ai); err != nil {
		t.Fatal(err)
	}
	waitRow(t, out, "relay.reservation", "success", "none")
	if _, err := client.Reserve(ctx, h, ai); err != nil {
		t.Fatal(err)
	}
	waitRow(t, out, "relay.reservation", "success", "renewed")
	s.StartDrain()
	if _, err := client.Reserve(ctx, h, ai); err == nil {
		t.Fatal("reservation accepted during drain")
	}
	denied := waitRow(t, out, "relay.reservation", "refused", "draining")
	if denied["session_id"] == nil || denied["transport"] != "unknown" {
		t.Fatalf("gate evidence %v", denied)
	}
}

func TestCircuitDiagnosticsUseOnlyStockAggregateEvidence(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	for _, h := range []host.Host{dest, src} {
		if err := h.Connect(ctx, ai); err != nil {
			t.Fatal(err)
		}
	}
	sendWireToken(t, ctx, dest, s.Host.ID(), "a")
	sendWireToken(t, ctx, src, s.Host.ID(), "b")
	if _, err := client.Reserve(ctx, dest, ai); err != nil {
		t.Fatal(err)
	}
	route, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", s.Host.ID(), dest.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Connect(ctx, peer.AddrInfo{ID: dest.ID(), Addrs: []ma.Multiaddr{route}}); err != nil {
		t.Fatal(err)
	}
	started := waitRow(t, out, "relay.circuit", "started", "none")
	success := waitRow(t, out, "relay.circuit", "success", "none")
	for _, row := range []map[string]any{started, success} {
		if row["transport"] != "unknown" || row["session_id"] != nil || row["request_id"] != nil {
			t.Fatalf("invented correlation: %v", row)
		}
	}
	// Forward a canary through the opaque circuit. Diagnostics must not interpret it.
	dest.SetStreamHandler("/clipp/diagnostic-canary/1.0.0", func(st network.Stream) { defer st.Close(); _, _ = io.Copy(io.Discard, st) })
	st, err := src.NewStream(network.WithAllowLimitedConn(ctx, "diagnostic canary"), dest.ID(), "/clipp/diagnostic-canary/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Write([]byte("secret-content-canary"))
	_ = st.Close()
	_ = src.Network().ClosePeer(dest.ID())
	ended := waitRow(t, out, "relay.circuit", "ended", "none")
	if ended["duration_ms"] == nil || ended["transport"] != "unknown" || ended["session_id"] != nil || ended["request_id"] != nil {
		t.Fatalf("close evidence %v", ended)
	}
	for _, row := range out.rows() {
		b, _ := json.Marshal(row)
		if strings.Contains(string(b), "secret-content-canary") || strings.Contains(string(b), dest.ID().String()) || strings.Contains(string(b), src.ID().String()) || strings.Contains(string(b), "/ip4/") {
			t.Fatalf("private material in row: %s", b)
		}
	}
}

func TestRendezvousDiagnosticsFollowVersionAndLeaseOutcome(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "a")
	finder := rvClient(t, ctx, s, "b")
	register, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(rvEnvelope(t, owner, s.Host.ID()))})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), register); string(got["code"]) != `"reservation_required"` {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v2.register", "refused", "lease_unavailable")
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if _, err := client.Reserve(ctx, owner, ai); err != nil {
		t.Fatal(err)
	}
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), register); string(got["ok"]) != "true" {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v2.register", "success", "none")
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	if got := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV1Protocol), lookup); got["record"] == nil {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v1.find", "success", "none")
	unregister, _ := json.Marshal(map[string]any{"action": "unregister", "topic": "clipp"})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV1Protocol), unregister); string(got["ok"]) != "true" {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v1.unregister", "success", "none")
	if got := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] != nil {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v2.find", "refused", "lease_unavailable")
	badRecord, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString([]byte("secret-error-canary"))})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), badRecord); string(got["code"]) != `"invalid_peer_record"` {
		t.Fatal(got)
	}
	waitRow(t, out, "relay.rendezvous.v2.register", "refused", "invalid_request")
	for _, row := range out.rows() {
		b, _ := json.Marshal(row)
		for _, secret := range []string{"secret-error-canary", owner.ID().String(), finder.ID().String(), "/ip4/"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("private material in row: %s", b)
			}
		}
	}
}

func TestRendezvousRepeatedMissesRemainBounded(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": h.ID().String()})
	start := time.Now()
	for i := 0; i < 30; i++ {
		if got := rvExchange(t, ctx, h, s.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] != nil {
			t.Fatal(got)
		}
	}
	time.Sleep(20 * time.Millisecond)
	count := 0
	for _, row := range out.rows() {
		if row["event"] == "relay.rendezvous.v2.find" && row["outcome"] == "refused" && row["reason"] == "lease_unavailable" {
			count++
		}
	}
	max := 5 + int(time.Since(start).Seconds()) + 1
	if count == 0 || count > max {
		t.Fatalf("bounded tuple rows=%d max=%d", count, max)
	}
}
