package relay

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/transport/webrtc/udpmux"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/stun/v3"
)

// A valid first ICE check allocates the stock WebRTC candidate. The client
// deliberately stops before DTLS/Noise, so the listener must release that
// incomplete setup at its stock ten-second deadline.
func TestIncompleteWebRTCSetupReleasesResourcesWithinTenSeconds(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.ListenAddressFor("webrtc-direct")
	if err != nil {
		t.Fatal(err)
	}
	portText, err := a.ValueForProtocol(ma.P_UDP)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	memory := func() int64 {
		var used int64
		if err := s.manager.ViewSystem(func(scope network.ResourceScope) error { used = scope.Stat().Memory; return nil }); err != nil {
			t.Fatal(err)
		}
		return used
	}
	baseline := memory()
	ufrag := udpmux.UfragPrefixV1 + "incompleteSetupProbe"
	msg := stun.New()
	msg.SetType(stun.BindingRequest)
	if err := (stun.RawAttribute{Type: stun.AttrUsername, Value: []byte(ufrag + ":" + ufrag)}).AddTo(msg); err != nil {
		t.Fatal(err)
	}
	msg.Encode()
	started := time.Now()
	if _, err := client.Write(msg.Raw); err != nil {
		t.Fatal(err)
	}

	// The reservation proves the real listener accepted the candidate; merely
	// sending malformed UDP would otherwise give a false timeout pass.
	acceptedBy := started.Add(2 * time.Second)
	for time.Now().Before(acceptedBy) && memory() < baseline+2<<20 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := memory(); got < baseline+2<<20 {
		t.Fatalf("WebRTC candidate never reserved setup memory: before=%d after=%d", baseline, got)
	}
	deadline := started.Add(12 * time.Second)
	for time.Now().Before(deadline) && memory() >= baseline+2<<20 {
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if got := memory(); got >= baseline+2<<20 {
		t.Fatalf("incomplete WebRTC setup retained reservation after %s: %d bytes", elapsed, got)
	}
	if elapsed < 9*time.Second || elapsed > 12*time.Second {
		t.Fatalf("incomplete WebRTC setup released after %s, want stock 10s deadline", elapsed)
	}
	if s.ActiveSessions() != 0 {
		t.Fatal("incomplete WebRTC candidate became an authenticated Relay Session")
	}
}
