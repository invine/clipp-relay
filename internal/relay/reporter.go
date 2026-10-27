package relay

import (
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type trafficSession interface {
	ObserveTraffic(peer.ID, int64) bool
}

// endpointReporter receives the stock swarm's HOP and STOP byte callbacks.
// It retains only aggregate counts; association with an account is sampled at
// report time, so an old connection's tail may charge a replacement session.
type endpointReporter struct {
	sessions       trafficSession
	sent, received atomic.Int64
	unattributed   atomic.Int64
}

var _ metrics.Reporter = (*endpointReporter)(nil)

func (r *endpointReporter) LogSentMessage(n int64) { r.sent.Add(n) }
func (r *endpointReporter) LogRecvMessage(n int64) { r.received.Add(n) }
func (r *endpointReporter) LogSentMessageStream(n int64, proto protocol.ID, p peer.ID) {
	r.report(n, proto, p)
}
func (r *endpointReporter) LogRecvMessageStream(n int64, proto protocol.ID, p peer.ID) {
	r.report(n, proto, p)
}
func (r *endpointReporter) report(n int64, proto protocol.ID, p peer.ID) {
	if n <= 0 || proto != hopProtocol && proto != "/libp2p/circuit/relay/0.2.0/stop" {
		return
	}
	if !r.sessions.ObserveTraffic(p, n) {
		r.unattributed.Add(n)
	}
}
func (r *endpointReporter) GetBandwidthForPeer(peer.ID) metrics.Stats         { return metrics.Stats{} }
func (r *endpointReporter) GetBandwidthForProtocol(protocol.ID) metrics.Stats { return metrics.Stats{} }
func (r *endpointReporter) GetBandwidthTotals() metrics.Stats {
	return metrics.Stats{TotalIn: r.received.Load(), TotalOut: r.sent.Load()}
}
func (r *endpointReporter) GetBandwidthByPeer() map[peer.ID]metrics.Stats {
	return map[peer.ID]metrics.Stats{}
}
func (r *endpointReporter) GetBandwidthByProtocol() map[protocol.ID]metrics.Stats {
	return map[protocol.ID]metrics.Stats{}
}
func (r *endpointReporter) Reset()             { r.sent.Store(0); r.received.Store(0); r.unattributed.Store(0) }
func (r *endpointReporter) TrimIdle(time.Time) {}

// CloseAccountWithReason retires an account's sessions through the shared
// manager-owned close queue. It is used for asynchronous policy and quota
// cutoffs that do not hold Auth's account protection.
func (s *Server) CloseAccountWithReason(account, reason string) {
	if err := s.RetireAccount(account, reason); err != nil {
		return
	}
	_ = s.CompleteAccountInvalidation(account)
}

// CloseAccount invalidates all live sessions after an account policy change.
func (s *Server) CloseAccount(account string) { s.CloseAccountWithReason(account, "policy_changed") }
