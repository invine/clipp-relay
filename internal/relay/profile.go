package relay

import (
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
)

const mib = 1 << 20
const kib = 1 << 10

// A zero in ResourceLimits means inherit, so a named profile is converted to
// explicit BlockAllLimit values before the empty fixed base is built.
type limitSpec struct {
	conns, connsInbound, connsOutbound       int
	streams, streamsInbound, streamsOutbound int
	fd                                       int
	memory                                   int64
}

func limits(spec limitSpec) rcmgr.ResourceLimits {
	value := func(v int) rcmgr.LimitVal {
		if v == 0 {
			return rcmgr.BlockAllLimit
		}
		return rcmgr.LimitVal(v)
	}
	mem := rcmgr.LimitVal64(spec.memory)
	if spec.memory == 0 {
		mem = rcmgr.BlockAllLimit64
	}
	return rcmgr.ResourceLimits{
		Conns: value(spec.conns), ConnsInbound: value(spec.connsInbound), ConnsOutbound: value(spec.connsOutbound),
		Streams: value(spec.streams), StreamsInbound: value(spec.streamsInbound), StreamsOutbound: value(spec.streamsOutbound),
		FD: value(spec.fd), Memory: mem,
	}
}

// TCPProfile fixes every enabled scope, including unknown and allowlisted
// scopes. TCP, WSS and WebRTC Direct use the same service and protocol limits.
func TCPProfile() rcmgr.Limiter {
	block := limits(limitSpec{})
	p := rcmgr.PartialLimitConfig{
		System:            limits(limitSpec{conns: 8192, connsInbound: 8192, connsOutbound: 512, streams: 32768, streamsInbound: 32768, streamsOutbound: 16384, fd: 8192, memory: 512 * mib}),
		Transient:         limits(limitSpec{conns: 256, connsInbound: 256, connsOutbound: 256, streams: 1024, streamsInbound: 1024, streamsOutbound: 512, fd: 256, memory: 512 * mib}),
		PeerDefault:       limits(limitSpec{conns: 4, connsInbound: 4, connsOutbound: 4, streams: 64, streamsInbound: 64, streamsOutbound: 32, fd: 4, memory: 32 * mib}),
		Conn:              limits(limitSpec{conns: 1, connsInbound: 1, connsOutbound: 1, fd: 1, memory: 8 * mib}),
		Stream:            limits(limitSpec{streams: 1, streamsInbound: 1, streamsOutbound: 1, memory: mib}),
		AllowlistedSystem: block, AllowlistedTransient: block,
		ServiceDefault: block, ServicePeerDefault: block,
		ProtocolDefault: block, ProtocolPeerDefault: block,
		Service: map[string]rcmgr.ResourceLimits{
			"libp2p.relay/v2":  limits(limitSpec{streams: 24576, streamsInbound: 12288, streamsOutbound: 12288, memory: 128 * mib}),
			"libp2p.identify":  limits(limitSpec{streams: 4096, streamsInbound: 2048, streamsOutbound: 2048, memory: 64 * mib}),
			"libp2p.ping":      limits(limitSpec{streams: 1024, streamsInbound: 1024, streamsOutbound: 512, memory: 16 * mib}),
			"clipp.relay-auth": limits(limitSpec{streams: 512, streamsInbound: 512, memory: 8 * mib}),
			"clipp.rendezvous": limits(limitSpec{streams: 2048, streamsInbound: 2048, memory: 64 * mib}),
		},
		ServicePeer: map[string]rcmgr.ResourceLimits{
			"libp2p.relay/v2":  limits(limitSpec{streams: 32, streamsInbound: 32, streamsOutbound: 32, memory: 2 * mib}),
			"libp2p.identify":  limits(limitSpec{streams: 8, streamsInbound: 4, streamsOutbound: 4, memory: mib}),
			"libp2p.ping":      limits(limitSpec{streams: 4, streamsInbound: 4, streamsOutbound: 2, memory: 256 * kib}),
			"clipp.relay-auth": limits(limitSpec{streams: 8, streamsInbound: 8, memory: 256 * kib}),
			"clipp.rendezvous": limits(limitSpec{streams: 8, streamsInbound: 8, memory: 2 * mib}),
		},
		Protocol: map[protocol.ID]rcmgr.ResourceLimits{
			"/libp2p/circuit/relay/0.2.0/hop":  limits(limitSpec{streams: 12288, streamsInbound: 12288, memory: 64 * mib}),
			"/libp2p/circuit/relay/0.2.0/stop": limits(limitSpec{streams: 12000, streamsOutbound: 12000, memory: 64 * mib}),
			"/ipfs/id/1.0.0":                   limits(limitSpec{streams: 2048, streamsInbound: 1024, streamsOutbound: 1024, memory: 32 * mib}),
			"/ipfs/id/push/1.0.0":              limits(limitSpec{streams: 2048, streamsInbound: 1024, streamsOutbound: 1024, memory: 32 * mib}),
			"/ipfs/ping/1.0.0":                 limits(limitSpec{streams: 1024, streamsInbound: 1024, streamsOutbound: 512, memory: 16 * mib}),
			"/clipp/relay-auth/1.0.0":          limits(limitSpec{streams: 512, streamsInbound: 512, memory: 8 * mib}),
			RendezvousV1Protocol:               limits(limitSpec{streams: 1024, streamsInbound: 1024, memory: 32 * mib}),
			RendezvousV2Protocol:               limits(limitSpec{streams: 2048, streamsInbound: 2048, memory: 64 * mib}),
		},
		ProtocolPeer: map[protocol.ID]rcmgr.ResourceLimits{
			"/libp2p/circuit/relay/0.2.0/hop":  limits(limitSpec{streams: 32, streamsInbound: 32, memory: mib}),
			"/libp2p/circuit/relay/0.2.0/stop": limits(limitSpec{streams: 32, streamsOutbound: 32, memory: mib}),
			"/ipfs/id/1.0.0":                   limits(limitSpec{streams: 4, streamsInbound: 2, streamsOutbound: 2, memory: 512 * kib}),
			"/ipfs/id/push/1.0.0":              limits(limitSpec{streams: 4, streamsInbound: 2, streamsOutbound: 2, memory: 512 * kib}),
			"/ipfs/ping/1.0.0":                 limits(limitSpec{streams: 4, streamsInbound: 4, streamsOutbound: 2, memory: 256 * kib}),
			"/clipp/relay-auth/1.0.0":          limits(limitSpec{streams: 8, streamsInbound: 8, memory: 256 * kib}),
			RendezvousV1Protocol:               limits(limitSpec{streams: 4, streamsInbound: 4, memory: mib}),
			RendezvousV2Protocol:               limits(limitSpec{streams: 8, streamsInbound: 8, memory: 2 * mib}),
		},
	}
	return rcmgr.NewFixedLimiter(p.Build(rcmgr.ConcreteLimitConfig{}))
}
