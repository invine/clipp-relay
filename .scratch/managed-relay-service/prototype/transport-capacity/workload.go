// PROTOTYPE: bounded traffic patterns; no changes to libp2p relay behavior.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	pb "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
)

var receiverBytes atomic.Int64

type workView struct {
	BusyControlOK, BusyControlFailed, IdleControlOK, IdleControlFailed                 int64
	Kind                                                                               string
	Done                                                                               bool
	Opened, OpenFailed, IOFailed, DeadlineWrites, SlowWrites, ControlOK, ControlFailed int64
	AcceptedBytes, VerifiedEchoBytes, DestinationReadBytes                             int64
}
type workload struct {
	busyOK, busyFailed, idleOK, idleFailed                                             atomic.Int64
	kind                                                                               string
	done                                                                               atomic.Bool
	opened, openFailed, ioFailed, deadlineWrites, slowWrites, controlOK, controlFailed atomic.Int64
	accepted, verified, remaining                                                      atomic.Int64
	receiverStart                                                                      int64
}

func (w *workload) view() workView {
	return workView{
		BusyControlOK: w.busyOK.Load(), BusyControlFailed: w.busyFailed.Load(), IdleControlOK: w.idleOK.Load(), IdleControlFailed: w.idleFailed.Load(),
		Kind: w.kind, Done: w.done.Load(), Opened: w.opened.Load(), OpenFailed: w.openFailed.Load(),
		IOFailed: w.ioFailed.Load(), DeadlineWrites: w.deadlineWrites.Load(), SlowWrites: w.slowWrites.Load(),
		ControlOK: w.controlOK.Load(), ControlFailed: w.controlFailed.Load(), AcceptedBytes: w.accepted.Load(),
		VerifiedEchoBytes: w.verified.Load(), DestinationReadBytes: receiverBytes.Load() - w.receiverStart,
	}
}
func prelude(s network.Stream, mode byte) error {
	if _, err := s.Write([]byte{mode}); err != nil {
		return err
	}
	var ack [1]byte
	if _, err := io.ReadFull(s, ack[:]); err != nil {
		return err
	}
	if ack[0] != mode {
		return fmt.Errorf("wrong prototype acknowledgement")
	}
	return nil
}
func (w *workload) budget(n int64) bool {
	for {
		left := w.remaining.Load()
		if left < n {
			return false
		}
		if w.remaining.CompareAndSwap(left, left-n) {
			return true
		}
	}
}
func startWork(clients []client, kind string, seconds int) *workload {
	if seconds < 1 || seconds > 12 {
		die(fmt.Errorf("workload duration must be 1..12 seconds"))
	}
	w := &workload{kind: kind, receiverStart: receiverBytes.Load()}
	w.remaining.Store(128 * MiB)
	go func() {
		defer w.done.Store(true)
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		var wg sync.WaitGroup
		// Four circuits per pair, at most sixteen pairs: <=64 concurrent circuits.
		for i := 0; i+1 < len(clients) && i < 32; i += 2 {
			a, b := clients[i], clients[i+1]
			reservation, err := hop(b.h, b.relay, pb.HopMessage_RESERVE, "")
			if err != nil {
				w.openFailed.Add(4)
				continue
			}
			reservation.Close()
			for lane := 0; lane < 4; lane++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					payload := bytes.Repeat([]byte{0x57}, 16<<10)
					received := make([]byte, len(payload))
					for ctx.Err() == nil {
						s, err := hop(a.h, a.relay, pb.HopMessage_CONNECT, b.h.ID())
						if err != nil {
							w.openFailed.Add(1)
							return
						}
						s.SetDeadline(deadline)
						mode := byte(0)
						if kind == "slow-readers" {
							mode = 1
						}
						if err = prelude(s, mode); err != nil {
							w.openFailed.Add(1)
							s.Reset()
							return
						}
						w.opened.Add(1)
						// Leave prelude room below the stock 2-MiB directional circuit limit.
						chunks := 64
						if mode == 1 {
							chunks = 127
						}
						failed := false
						for chunk := 0; chunk < chunks && ctx.Err() == nil; chunk++ {
							if !w.budget(int64(len(payload))) {
								break
							}
							before := time.Now()
							n, err := s.Write(payload)
							w.accepted.Add(int64(n))
							if time.Since(before) > 250*time.Millisecond {
								w.slowWrites.Add(1)
							}
							if err != nil {
								if ctx.Err() != nil || !time.Now().Before(deadline) {
									w.deadlineWrites.Add(1)
								} else {
									w.ioFailed.Add(1)
								}
								failed = true
								break
							}
							if mode == 0 {
								if _, err = io.ReadFull(s, received); err != nil {
									if ctx.Err() == nil {
										w.ioFailed.Add(1)
									}
									failed = true
									break
								}
								if !bytes.Equal(payload, received) {
									w.ioFailed.Add(1)
									failed = true
									break
								}
								w.verified.Add(int64(len(received)))
								select {
								case <-time.After(100 * time.Millisecond):
								case <-ctx.Done():
								}
							}
						}
						if mode == 1 && !failed {
							// Half-close, then wait for target drain/EOF. Do not tear down buffers
							// immediately just because a local Write accepted the full payload.
							s.CloseWrite()
							_, err = io.Copy(io.Discard, s)
							if err != nil && ctx.Err() == nil {
								w.ioFailed.Add(1)
							}
						}
						s.Reset()
						if mode == 1 || failed {
							return
						}
					}
				}()
			}
		}
		// One real control exchange every 200ms during traffic, never unbounded.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if len(clients) == 0 {
				return
			}
			i := 0
			for ctx.Err() == nil {
				index := i % min(32, len(clients))
				busy := true
				if len(clients) > 32 && i%2 == 1 {
					index = 32 + (i/2)%(len(clients)-32)
					busy = false
				}
				c := clients[index]
				i++
				op, cancel := context.WithTimeout(ctx, time.Second)
				s, err := c.h.NewStream(network.WithNoDial(op, "concurrent control"), c.relay, control)
				if err == nil {
					d := time.Now().Add(time.Second)
					if deadline.Before(d) {
						d = deadline
					}
					s.SetDeadline(d)
					_, err = s.Write([]byte("probe"))
					if err == nil {
						var b [5]byte
						_, err = io.ReadFull(s, b[:])
						if err == nil && string(b[:]) != "probe" {
							err = fmt.Errorf("wrong control echo")
						}
					}
					s.Reset()
				}
				cancel()
				if err != nil {
					if ctx.Err() == nil {
						w.controlFailed.Add(1)
						if busy {
							w.busyFailed.Add(1)
						} else {
							w.idleFailed.Add(1)
						}
					}
				} else {
					w.controlOK.Add(1)
					if busy {
						w.busyOK.Add(1)
					} else {
						w.idleOK.Add(1)
					}
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
				}
			}
		}()
		wg.Wait()
	}()
	return w
}
