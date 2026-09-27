package relay

import (
	"strings"
	"testing"

	ma "github.com/multiformats/go-multiaddr"
)

func TestDiscoveryPublishesCompleteWSSAndCurrentWebRTCCerthash(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct", WebSocketHostname: "relay.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wss := ma.StringCast("/dns4/relay.example.test/tcp/443/tls/ws")
	webrtc := ma.StringCast("/dns4/relay.example.test/udp/18083/webrtc-direct")
	d, err := NewDiscovery(s, wireAuthority{}, "portal.example.test", []ma.Multiaddr{wss, webrtc})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.addresses) != 2 {
		t.Fatalf("published %v", d.addresses)
	}
	for _, a := range d.addresses {
		if !strings.HasSuffix(a, "/p2p/"+s.Host.ID().String()) {
			t.Fatalf("missing peer ID: %s", a)
		}
		if strings.Contains(a, "/webrtc-direct") && !strings.Contains(a, "/certhash/") {
			t.Fatalf("missing process certhash: %s", a)
		}
	}
	if err := d.Publish([]ma.Multiaddr{wss}); err == nil || len(d.addresses) != 0 {
		t.Fatal("incomplete enabled transport set remained published")
	}
	if err := d.Publish([]ma.Multiaddr{ma.StringCast("/dns4/wrong.example.test/tcp/443/tls/ws"), webrtc}); err == nil {
		t.Fatal("published wrong WSS host")
	}
	previous, err := New(wireAuthority{}, wireCredit{}, Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	otherAddress, err := previous.ListenAddressFor("webrtc-direct")
	if err != nil {
		t.Fatal(err)
	}
	otherHash, err := otherAddress.ValueForProtocol(ma.P_CERTHASH)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Publish([]ma.Multiaddr{wss, ma.StringCast(webrtc.String() + "/certhash/" + otherHash)}); err == nil {
		t.Fatal("published stale WebRTC certhash")
	}
}
