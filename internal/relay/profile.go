package relay

import (
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
)

const mib = 1 << 20
const kib = 1 << 10

// profile starts from an empty fixed base. BlockAllLimit is required: a Go
// zero in ResourceLimits means inherit, whereas a zero in BaseLimit blocks.
func limits(cc, ci, co, sc, si, so, fd int, memory int64) rcmgr.ResourceLimits {
	value := func(v int) rcmgr.LimitVal {
		if v == 0 {
			return rcmgr.BlockAllLimit
		}
		return rcmgr.LimitVal(v)
	}
	mem := rcmgr.LimitVal64(memory)
	if memory == 0 {
		mem = rcmgr.BlockAllLimit64
	}
	return rcmgr.ResourceLimits{Conns: value(cc), ConnsInbound: value(ci), ConnsOutbound: value(co), Streams: value(sc), StreamsInbound: value(si), StreamsOutbound: value(so), FD: value(fd), Memory: mem}
}

func streamLimits(sc, si, so int, memory int64) rcmgr.ResourceLimits {
	return limits(0, 0, 0, sc, si, so, 0, memory)
}

// TCPProfile fixes every enabled scope, including unknown and allowlisted
// scopes. Additional transports may add protocols but may not inherit defaults.
func TCPProfile() rcmgr.Limiter {
	block := limits(0, 0, 0, 0, 0, 0, 0, 0)
	p := rcmgr.PartialLimitConfig{
		System:            limits(8192, 8192, 512, 32768, 32768, 16384, 8192, 512*mib),
		Transient:         limits(256, 256, 256, 1024, 1024, 512, 256, 512*mib),
		PeerDefault:       limits(4, 4, 4, 64, 64, 32, 4, 32*mib),
		Conn:              limits(1, 1, 1, 0, 0, 0, 1, 8*mib),
		Stream:            limits(0, 0, 0, 1, 1, 1, 0, mib),
		AllowlistedSystem: block, AllowlistedTransient: block,
		ServiceDefault: block, ServicePeerDefault: block,
		ProtocolDefault: block, ProtocolPeerDefault: block,
		Service: map[string]rcmgr.ResourceLimits{
			"libp2p.relay/v2":  streamLimits(24576, 12288, 12288, 128*mib),
			"libp2p.identify":  streamLimits(4096, 2048, 2048, 64*mib),
			"libp2p.ping":      streamLimits(1024, 1024, 512, 16*mib),
			"clipp.relay-auth": streamLimits(512, 512, 0, 8*mib),
		},
		ServicePeer: map[string]rcmgr.ResourceLimits{
			"libp2p.relay/v2":  streamLimits(32, 32, 32, 2*mib),
			"libp2p.identify":  streamLimits(8, 4, 4, mib),
			"libp2p.ping":      streamLimits(4, 4, 2, 256*kib),
			"clipp.relay-auth": streamLimits(8, 8, 0, 256*kib),
		},
		Protocol: map[protocol.ID]rcmgr.ResourceLimits{
			"/libp2p/circuit/relay/0.2.0/hop":  streamLimits(12288, 12288, 0, 64*mib),
			"/libp2p/circuit/relay/0.2.0/stop": streamLimits(12000, 0, 12000, 64*mib),
			"/ipfs/id/1.0.0":                   streamLimits(2048, 1024, 1024, 32*mib),
			"/ipfs/id/push/1.0.0":              streamLimits(2048, 1024, 1024, 32*mib),
			"/ipfs/ping/1.0.0":                 streamLimits(1024, 1024, 512, 16*mib),
			"/clipp/relay-auth/1.0.0":          streamLimits(512, 512, 0, 8*mib),
		},
		ProtocolPeer: map[protocol.ID]rcmgr.ResourceLimits{
			"/libp2p/circuit/relay/0.2.0/hop":  streamLimits(32, 32, 0, mib),
			"/libp2p/circuit/relay/0.2.0/stop": streamLimits(32, 0, 32, mib),
			"/ipfs/id/1.0.0":                   streamLimits(4, 2, 2, 512*kib),
			"/ipfs/id/push/1.0.0":              streamLimits(4, 2, 2, 512*kib),
			"/ipfs/ping/1.0.0":                 streamLimits(4, 4, 2, 256*kib),
			"/clipp/relay-auth/1.0.0":          streamLimits(8, 8, 0, 256*kib),
		},
	}
	return rcmgr.NewFixedLimiter(p.Build(rcmgr.ConcreteLimitConfig{}))
}
