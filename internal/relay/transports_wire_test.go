package relay

import (
	"context"
	"strings"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

func TestTransportOnlyListenersRequireRelayAuth(t *testing.T) {
	for _, tc := range []struct {
		name        string
		opts        Options
		marker      string
		boundMarker string
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}, "/ws", "/ws"},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}, "/webrtc-direct", "/webrtc-direct/certhash/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(wireAuthority{}, wireCredit{}, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			addr, err := s.ListenAddressFor(tc.name)
			if err != nil || !strings.Contains(addr.String(), tc.boundMarker) {
				t.Fatalf("address = %v, %v", addr, err)
			}
			h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{addr}}
			if err := h.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			for _, c := range h.Network().ConnsToPeer(s.Host.ID()) {
				if !strings.Contains(c.RemoteMultiaddr().String(), tc.marker) {
					t.Fatalf("unexpected transport %s", c.RemoteMultiaddr())
				}
			}
			if _, err := client.Reserve(ctx, h, ai); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
				t.Fatalf("unauthenticated reserve: %v", err)
			}
			sendWireAuth(t, ctx, h, s.Host.ID())
			if _, err := client.Reserve(ctx, h, ai); err != nil {
				t.Fatalf("authenticated reserve: %v", err)
			}
		})
	}
}

func TestTransportOnlyCircuitsTransferAndChargeBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credit := runOpaqueCircuit(t, twoAuthority{}, tc.opts, tc.name)
			if credit.count("a") < int64(len("opaque relayed bytes")+len("return bytes")) || credit.count("b") < int64(len("opaque relayed bytes")+len("return bytes")) {
				t.Fatalf("missing endpoint charges: a=%d b=%d", credit.count("a"), credit.count("b"))
			}
		})
	}
}
