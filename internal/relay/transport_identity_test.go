package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/transport/websocket"
	ma "github.com/multiformats/go-multiaddr"
)

type observedAuthority struct{ calls atomic.Int32 }

func (a *observedAuthority) AuthenticateRelay(ctx context.Context, token string) (auth.RelayCredential, error) {
	a.calls.Add(1)
	return wireAuthority{}.AuthenticateRelay(ctx, token)
}

func localTLSProxy(t *testing.T, internal ma.Multiaddr, name string) (ma.Multiaddr, *x509.CertPool) {
	t.Helper()
	address, roots, _ := localTLSProxyCertificate(t, internal, name)
	return address, roots
}

func localTLSProxyCertificate(t *testing.T, internal ma.Multiaddr, name string) (ma.Multiaddr, *x509.CertPool, []byte) {
	t.Helper()
	port, err := internal.ValueForProtocol(ma.P_TCP)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key}
	parsed, _ := url.Parse("http://127.0.0.1:" + port)
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "proxy unavailable", 502) }
	front := httptest.NewUnstartedServer(proxy)
	front.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	front.StartTLS()
	t.Cleanup(front.Close)
	_, publicPort, err := net.SplitHostPort(front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strconv.Atoi(publicPort); err != nil {
		t.Fatal(err)
	}
	address := ma.StringCast("/ip4/127.0.0.1/tcp/" + publicPort + "/tls/sni/relay.example.test/ws")
	roots := x509.NewCertPool()
	xcert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(xcert)
	return address, roots, certDER
}

func TestWSSCertificateAndNoiseIdentityBeforeToken(t *testing.T) {
	a := &observedAuthority{}
	s, err := New(a, wireCredit{}, Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebSocketHostname: "relay.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	internal, err := s.ListenAddressFor("websocket")
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	for _, tc := range []struct {
		name      string
		certName  string
		wrongPeer bool
		success   bool
	}{
		{"valid", "relay.example.test", false, true},
		{"wrong TLS name", "other.example.test", false, false},
		{"wrong Noise Peer ID", "relay.example.test", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public, roots := localTLSProxy(t, internal, tc.certName)
			h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.NoTransports, libp2p.Transport(websocket.New, websocket.WithTLSClientConfig(&tls.Config{RootCAs: roots})), libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			id := s.Host.ID()
			if tc.wrongPeer {
				id = wrong.ID()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			before := a.calls.Load()
			err = h.Connect(ctx, peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{public}})
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				sendWireAuth(t, ctx, h, s.Host.ID())
				// Admission may recheck the credential under the account guard.
				if a.calls.Load() <= before {
					t.Fatal("successful authentication did not validate the token")
				}
			} else if err == nil || a.calls.Load() != before {
				t.Fatalf("expected connection failure before token: %v; calls %d", err, a.calls.Load()-before)
			}
		})
	}
}

func TestWebRTCStaleCerthashAndPeerIDBeforeToken(t *testing.T) {
	a := &observedAuthority{}
	s, err := New(a, wireCredit{}, Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr, err := s.ListenAddressFor("webrtc-direct")
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(wireAuthority{}, wireCredit{}, Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stale, err := other.ListenAddressFor("webrtc-direct")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := stale.ValueForProtocol(ma.P_CERTHASH)
	if err != nil {
		t.Fatal(err)
	}
	currentHash, err := addr.ValueForProtocol(ma.P_CERTHASH)
	if err != nil {
		t.Fatal(err)
	}
	if currentHash == hash || s.Host.ID() == other.Host.ID() {
		t.Fatal("process restart reused WebRTC certificate or Peer ID")
	}
	base := strings.Split(addr.String(), "/certhash/")[0]
	for _, tc := range []struct {
		name    string
		address ma.Multiaddr
		id      peer.ID
	}{
		{"stale certhash", ma.StringCast(base + "/certhash/" + hash), s.Host.ID()},
		{"wrong Peer ID", addr, other.Host.ID()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			before := a.calls.Load()
			if err := h.Connect(ctx, peer.AddrInfo{ID: tc.id, Addrs: []ma.Multiaddr{tc.address}}); err == nil || a.calls.Load() != before {
				t.Fatalf("connected or sent token before identity check: %v, %d", err, a.calls.Load()-before)
			}
		})
	}
}

func TestWSSOnlyCircuitTransfersAndChargesBothDirections(t *testing.T) {
	credit := &recordingCredit{counts: map[string]int64{}}
	s, err := New(twoAuthority{}, credit, Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebSocketHostname: "relay.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	internal, err := s.ListenAddressFor("websocket")
	if err != nil {
		t.Fatal(err)
	}
	public, roots := localTLSProxy(t, internal, "relay.example.test")
	publicPort, _ := public.ValueForProtocol(ma.P_TCP)
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "relay.example.test"}}}
	for _, path := range []string{"/", "/v1/relay", "/readyz", "/metrics", "/admin"} {
		response, err := httpClient.Get("https://127.0.0.1:" + publicPort + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("WSS exposed %s: HTTP %d", path, response.StatusCode)
		}
	}
	newClient := func() (host.Host, error) {
		return libp2p.New(libp2p.NoListenAddrs, libp2p.Transport(websocket.New, websocket.WithTLSClientConfig(&tls.Config{RootCAs: roots})), libp2p.EnableRelay())
	}
	a, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{public}}
	for _, h := range []host.Host{a, b} {
		if err := h.Connect(ctx, ai); err != nil {
			t.Fatal(err)
		}
		conns := h.Network().ConnsToPeer(s.Host.ID())
		if len(conns) != 1 || !strings.Contains(conns[0].RemoteMultiaddr().String(), "/wss") {
			t.Fatalf("not WSS-only: %v", conns)
		}
	}
	sendWireToken(t, ctx, a, s.Host.ID(), "a")
	sendWireToken(t, ctx, b, s.Host.ID(), "b")
	if _, err := client.Reserve(ctx, a, ai); err != nil {
		t.Fatal(err)
	}
	// Ordinary portal writes time out after 15s; an upgraded WSS connection must
	// remain usable past that boundary because it owns a separate listener.
	time.Sleep(16 * time.Second)
	received := make(chan string, 1)
	a.SetStreamHandler("/clipp/wss-probe/1", func(st network.Stream) {
		defer st.Close()
		buf := make([]byte, 64)
		n, err := st.Read(buf)
		if err == nil {
			received <- string(buf[:n])
			_, _ = st.Write([]byte("back over WSS"))
		}
	})
	raddr := ma.StringCast(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", s.Host.ID(), a.ID()))
	if err := b.Connect(ctx, peer.AddrInfo{ID: a.ID(), Addrs: []ma.Multiaddr{raddr}}); err != nil {
		t.Fatal(err)
	}
	st, err := b.NewStream(network.WithAllowLimitedConn(ctx, "probe"), a.ID(), "/clipp/wss-probe/1")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Write([]byte("through WSS")); err != nil {
		t.Fatal(err)
	}
	_ = st.CloseWrite()
	select {
	case got := <-received:
		if got != "through WSS" {
			t.Fatalf("received %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reply := make([]byte, len("back over WSS"))
	if _, err := io.ReadFull(st, reply); err != nil || string(reply) != "back over WSS" {
		t.Fatalf("reply %q: %v", reply, err)
	}
	for _, conn := range s.Host.Network().Conns() {
		if conn.Stat().Direction != network.DirInbound {
			t.Fatalf("relay initiated outbound peer connection: %s", conn.RemoteMultiaddr())
		}
	}
	if credit.count("a") < int64(len("through WSS")+len("back over WSS")) || credit.count("b") < int64(len("through WSS")+len("back over WSS")) {
		t.Fatalf("charges a=%d b=%d", credit.count("a"), credit.count("b"))
	}
}
