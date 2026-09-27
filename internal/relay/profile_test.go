package relay

import (
	"slices"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
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
	want := []protocol.ID{AuthProtocol, hopProtocol, "/ipfs/id/1.0.0", "/ipfs/id/push/1.0.0", "/ipfs/ping/1.0.0"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("enabled protocol inventory = %v; want %v", got, want)
	}
}
