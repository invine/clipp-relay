package relay

import (
	"sync/atomic"
	"time"

	"clipp-relay/internal/diagnostic"
	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
)

// Stock callbacks are aggregate observations: they carry no physical connection,
// request, peer or transport identity. Never attach one from neighboring callbacks.
type circuitTracer struct {
	active      atomic.Int64
	diagnostics DiagnosticSink
}

func (*circuitTracer) RelayStatus(bool) {}
func (t *circuitTracer) emit(event, outcome, reason string, duration time.Duration) {
	if t.diagnostics != nil {
		t.diagnostics.Emit(diagnostic.Event{Event: event, Outcome: outcome, Reason: reason, Transport: "unknown", Duration: duration})
	}
}
func (t *circuitTracer) ConnectionOpened() {
	t.active.Add(1)
	t.emit("relay.circuit", "started", "none", 0)
}
func (t *circuitTracer) ConnectionClosed(d time.Duration) {
	t.active.Add(-1)
	t.emit("relay.circuit", "ended", "none", d)
}
func (t *circuitTracer) ConnectionRequestHandled(status pbv2.Status) {
	switch status {
	case pbv2.Status_OK:
		t.emit("relay.circuit", "success", "none", 0)
	case pbv2.Status_RESOURCE_LIMIT_EXCEEDED:
		t.emit("relay.circuit", "refused", "safety_limit", 0)
	case pbv2.Status_CONNECTION_FAILED:
		t.emit("relay.circuit", "failed", "connection_failed", 0)
	default:
		t.emit("relay.circuit", "refused", "protocol_refused", 0)
	}
}
func (t *circuitTracer) ReservationAllowed(renewed bool) {
	reason := "none"
	if renewed {
		reason = "renewed"
	}
	t.emit("relay.reservation", "success", reason, 0)
}
func (*circuitTracer) ReservationClosed(int) {}
func (t *circuitTracer) ReservationRequestHandled(status pbv2.Status) {
	if status == pbv2.Status_OK {
		return
	}
	switch status {
	case pbv2.Status_RESOURCE_LIMIT_EXCEEDED, pbv2.Status_RESERVATION_REFUSED:
		t.emit("relay.reservation", "refused", "safety_limit", 0)
	case pbv2.Status_CONNECTION_FAILED:
		t.emit("relay.reservation", "failed", "internal_failure", 0)
	default:
		t.emit("relay.reservation", "refused", "protocol_refused", 0)
	}
}
func (*circuitTracer) BytesTransferred(int) {}
