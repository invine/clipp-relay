package relay

import (
	"sync/atomic"
	"time"

	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
)

// circuitTracer counts only stock relay circuits, without identities or bytes.
type circuitTracer struct{ active atomic.Int64 }

func (*circuitTracer) RelayStatus(bool)                      {}
func (t *circuitTracer) ConnectionOpened()                   { t.active.Add(1) }
func (t *circuitTracer) ConnectionClosed(time.Duration)      { t.active.Add(-1) }
func (*circuitTracer) ConnectionRequestHandled(pbv2.Status)  {}
func (*circuitTracer) ReservationAllowed(bool)               {}
func (*circuitTracer) ReservationClosed(int)                 {}
func (*circuitTracer) ReservationRequestHandled(pbv2.Status) {}
func (*circuitTracer) BytesTransferred(int)                  {}
