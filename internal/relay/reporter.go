package relay

import (
	"context"
	"sync/atomic"
	"time"

	"clipp-relay/internal/quota"
	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// endpointReporter receives the stock swarm's HOP and STOP byte callbacks.
// It retains only aggregate counts; association with an account is sampled at
// report time, so an old connection's tail may charge a replacement session.
type endpointReporter struct {
	server         *Server
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
	s := r.server
	s.mu.Lock()
	active := s.peerSessionLocked(p)
	s.mu.Unlock()
	if active == nil || !time.Now().Before(active.deadline) {
		r.unattributed.Add(n)
		return
	}
	remaining := n
	for remaining > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		balance, err := s.credit.Take(ctx, active.account, active.generation, 0)
		if err == nil && balance.Usable == 0 {
			balance, err = s.credit.Ensure(ctx, active.account, active.generation)
		}
		if err == nil {
			chunk := remaining
			if chunk > quota.BlockBytes {
				chunk = quota.BlockBytes
			}
			if chunk > balance.Usable {
				chunk = balance.Usable
			}
			if chunk <= 0 {
				err = quota.ErrTemporary
			} else {
				_, err = s.credit.Take(ctx, active.account, active.generation, chunk)
				remaining -= chunk
			}
		}
		cancel()
		if err != nil {
			s.closeAccount(active.account)
			return
		}
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

// DetachAccount removes every session while the caller still holds its account
// guard. The returned connection closes perform I/O after that guard is released.
func (s *Server) DetachAccount(account string) func() {
	s.mu.Lock()
	connections := make([]*session, 0, len(s.byAccount[account]))
	for v := range s.byAccount[account] {
		connections = append(connections, v)
		s.removeLocked(v)
	}
	s.mu.Unlock()
	return func() {
		for _, v := range connections {
			_ = v.conn.Close()
		}
	}
}

// CloseAccount invalidates all live sessions after an account policy change.
func (s *Server) CloseAccount(account string) { s.DetachAccount(account)() }

func (s *Server) closeAccount(account string) { s.CloseAccount(account) }
