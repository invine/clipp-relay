package relay

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	ma "github.com/multiformats/go-multiaddr"
)

func TestFixedTCPProfile(t *testing.T) {
	l := TCPProfile()
	assert := func(name string, got network.Direction, total, direction int) {
		t.Helper()
		var limit int
		switch name {
		case "system":
			limit = l.GetSystemLimits().GetStreamLimit(got)
		case "hop":
			limit = l.GetProtocolLimits(protocol.ID("/libp2p/circuit/relay/0.2.0/hop")).GetStreamLimit(got)
		case "unknown":
			limit = l.GetProtocolLimits(protocol.ID("/unknown")).GetStreamLimit(got)
		}
		if limit != direction {
			t.Fatalf("%s direction limit = %d, want %d", name, limit, direction)
		}
		_ = total
	}
	assert("system", network.DirInbound, 32768, 32768)
	assert("hop", network.DirInbound, 12288, 12288)
	assert("hop", network.DirOutbound, 12288, 0)
	assert("unknown", network.DirInbound, 0, 0)
	if l.GetConnLimits().GetStreamTotalLimit() != 0 || l.GetSystemLimits().GetConnTotalLimit() != 8192 {
		t.Fatal("connection scopes drifted")
	}
}

func TestEveryEnabledScopeMatchesAcceptedFixedLimits(t *testing.T) {
	l := TCPProfile()
	type expected struct {
		cc, ci, co, sc, si, so, fd int
		memory                     int64
	}
	check := func(name string, limit rcmgr.Limit, w expected) {
		t.Helper()
		got := expected{limit.GetConnTotalLimit(), limit.GetConnLimit(network.DirInbound), limit.GetConnLimit(network.DirOutbound), limit.GetStreamTotalLimit(), limit.GetStreamLimit(network.DirInbound), limit.GetStreamLimit(network.DirOutbound), limit.GetFDLimit(), limit.GetMemoryLimit()}
		if got != w {
			t.Errorf("%s = %+v, want %+v", name, got, w)
		}
	}
	check("system", l.GetSystemLimits(), expected{8192, 8192, 512, 32768, 32768, 16384, 8192, 512 * mib})
	check("transient", l.GetTransientLimits(), expected{256, 256, 256, 1024, 1024, 512, 256, 512 * mib})
	check("peer", l.GetPeerLimits("unused"), expected{4, 4, 4, 64, 64, 32, 4, 32 * mib})
	check("connection", l.GetConnLimits(), expected{1, 1, 1, 0, 0, 0, 1, 8 * mib})
	check("stream", l.GetStreamLimits("unused"), expected{0, 0, 0, 1, 1, 1, 0, mib})
	for name, limit := range map[string]rcmgr.Limit{
		"allowlisted system": l.GetAllowlistedSystemLimits(), "allowlisted transient": l.GetAllowlistedTransientLimits(),
		"unknown service": l.GetServiceLimits("other"), "unknown service peer": l.GetServicePeerLimits("other"),
		"unknown protocol": l.GetProtocolLimits("/other"), "unknown protocol peer": l.GetProtocolPeerLimits("/other"),
	} {
		check(name, limit, expected{})
	}
	for _, row := range []struct {
		name       string
		sc, si, so int
		memory     int64
		pc, pi, po int
		pm         int64
	}{
		{"libp2p.relay/v2", 24576, 12288, 12288, 128 * mib, 32, 32, 32, 2 * mib},
		{"libp2p.identify", 4096, 2048, 2048, 64 * mib, 8, 4, 4, mib},
		{"libp2p.ping", 1024, 1024, 512, 16 * mib, 4, 4, 2, 256 * kib},
		{"clipp.relay-auth", 512, 512, 0, 8 * mib, 8, 8, 0, 256 * kib},
		{"clipp.rendezvous", 2048, 2048, 0, 64 * mib, 8, 8, 0, 2 * mib},
	} {
		check(row.name, l.GetServiceLimits(row.name), expected{sc: row.sc, si: row.si, so: row.so, memory: row.memory})
		check(row.name+" peer", l.GetServicePeerLimits(row.name), expected{sc: row.pc, si: row.pi, so: row.po, memory: row.pm})
	}
	for _, row := range []struct {
		name       protocol.ID
		sc, si, so int
		memory     int64
		pc, pi, po int
		pm         int64
	}{
		{"/libp2p/circuit/relay/0.2.0/hop", 12288, 12288, 0, 64 * mib, 32, 32, 0, mib},
		{"/libp2p/circuit/relay/0.2.0/stop", 12000, 0, 12000, 64 * mib, 32, 0, 32, mib},
		{"/ipfs/id/1.0.0", 2048, 1024, 1024, 32 * mib, 4, 2, 2, 512 * kib},
		{"/ipfs/id/push/1.0.0", 2048, 1024, 1024, 32 * mib, 4, 2, 2, 512 * kib},
		{"/ipfs/ping/1.0.0", 1024, 1024, 512, 16 * mib, 4, 4, 2, 256 * kib},
		{AuthProtocol, 512, 512, 0, 8 * mib, 8, 8, 0, 256 * kib},
		{RendezvousV1Protocol, 1024, 1024, 0, 32 * mib, 4, 4, 0, mib},
		{RendezvousV2Protocol, 2048, 2048, 0, 64 * mib, 8, 8, 0, 2 * mib},
	} {
		check(string(row.name), l.GetProtocolLimits(row.name), expected{sc: row.sc, si: row.si, so: row.so, memory: row.memory})
		check(string(row.name)+" peer", l.GetProtocolPeerLimits(row.name), expected{sc: row.pc, si: row.pi, so: row.po, memory: row.pm})
	}
}

func TestRelayEnablesOnlyRequiredTCPProtocols(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := s.Host.Mux().Protocols()
	slices.Sort(got)
	want := []protocol.ID{AuthProtocol, RendezvousV1Protocol, RendezvousV2Protocol, hopProtocol, "/ipfs/id/1.0.0", "/ipfs/id/push/1.0.0", "/ipfs/ping/1.0.0"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("enabled protocol inventory = %v; want %v", got, want)
	}
}

func TestCustomStreamsChargeAndReleaseServiceBuffers(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	state := s.manager.(rcmgr.ResourceManagerState)
	await := func(service string, want int64) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			stat := state.Stat().Services[service]
			if stat.Memory == want && (want == 0 || stat.NumStreamsInbound == 1) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("service %s did not account %d bytes: %+v", service, want, state.Stat().Services[service])
	}
	for _, tc := range []struct {
		name     string
		protocol protocol.ID
		memory   int64
	}{
		{"clipp.rendezvous", RendezvousV2Protocol, 2 * rvV2Limit},
		{"clipp.relay-auth", AuthProtocol, 4096},
	} {
		stream, err := h.NewStream(ctx, s.Host.ID(), tc.protocol)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte{0x80}); err != nil {
			t.Fatal(err)
		}
		await(tc.name, tc.memory)
		_ = stream.Reset()
		await(tc.name, 0)
	}
}

func TestResourceManagerDoesNotApplyStockIPBuckets(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Thirty-two in-flight connections from one non-loopback address exceed
	// the stock subnet concurrency gate (8) and rate burst (16). This checks
	// the resolved manager rather than only its configuration literals.
	endpoint := ma.StringCast("/ip4/203.0.113.7/tcp/4001")
	var scopes []network.ConnManagementScope
	defer func() {
		for _, scope := range scopes {
			scope.Done()
		}
	}()
	for range 32 {
		scope, err := s.manager.OpenConnection(network.DirInbound, true, endpoint)
		if err != nil {
			t.Fatalf("unexpected IP/subnet gate after %d connections: %v", len(scopes), err)
		}
		scopes = append(scopes, scope)
	}
}

func TestTransientConnectionScopeRejectsAtLimitAndRecovers(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	endpoint := ma.StringCast("/ip4/203.0.113.7/tcp/4001")
	var scopes []network.ConnManagementScope
	defer func() {
		for _, scope := range scopes {
			scope.Done()
		}
	}()
	for range 256 {
		scope, err := s.manager.OpenConnection(network.DirInbound, true, endpoint)
		if err != nil {
			t.Fatalf("transient gate rejected before 256 connections: %d: %v", len(scopes), err)
		}
		scopes = append(scopes, scope)
	}
	if scope, err := s.manager.OpenConnection(network.DirInbound, true, endpoint); err == nil {
		scope.Done()
		t.Fatal("257th transient connection bypassed fixed ceiling")
	}
	scopes[0].Done()
	scopes = scopes[1:]
	scope, err := s.manager.OpenConnection(network.DirInbound, true, endpoint)
	if err != nil {
		t.Fatalf("transient admission did not recover after release: %v", err)
	}
	scopes = append(scopes, scope)
}

func TestUnknownResourceScopesBlockAtRuntime(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stream, err := s.manager.OpenStream(s.Host.ID(), network.DirInbound)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SetProtocol("/clipp/unknown/1.0.0"); err == nil {
		stream.Done()
		t.Fatal("unknown protocol inherited an active limit")
	}
	stream.Done()
	stream, err = s.manager.OpenStream(s.Host.ID(), network.DirInbound)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Done()
	if err := stream.SetProtocol(AuthProtocol); err != nil {
		t.Fatal(err)
	}
	if err := stream.SetService("clipp.unknown"); err == nil {
		t.Fatal("unknown service inherited an active limit")
	}
}
