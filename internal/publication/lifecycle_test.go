package publication

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/relay"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

type observedDocument struct {
	Relay struct {
		PeerID    string   `json:"peerId"`
		Addresses []string `json:"addresses"`
	} `json:"relay"`
}

type lifecycleAuthority struct{}

func (lifecycleAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	if token != "same-credential" {
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: "same-account", SessionLimit: 1, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func discoveryDocument(t *testing.T, d *relay.Discovery) observedDocument {
	t.Helper()
	r := httptest.NewRequest("GET", "https://relay.example.test/v1/relay", nil)
	r.Header.Set("Authorization", "Bearer same-credential")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("discovery %d %s", w.Code, w.Body.String())
	}
	var doc observedDocument
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func documentTCP(t *testing.T, doc observedDocument) peer.AddrInfo {
	t.Helper()
	values := append([]string{}, doc.Relay.Addresses...)
	for i, value := range values {
		if strings.HasPrefix(value, "/ip4/127.0.0.1/tcp/") {
			values[0], values[i] = values[i], values[0]
			break
		}
	}
	for _, value := range values {
		if strings.Contains(value, "/tcp/") && !strings.Contains(value, "/ws") {
			a, err := ma.NewMultiaddr(value)
			if err != nil {
				t.Fatal(err)
			}
			info, err := peer.AddrInfoFromP2pAddr(a)
			if err != nil {
				t.Fatal(err)
			}
			return *info
		}
	}
	t.Fatal("no TCP address in discovery")
	return peer.AddrInfo{}
}

func documentWebRTCHash(t *testing.T, doc observedDocument) string {
	t.Helper()
	for _, value := range doc.Relay.Addresses {
		if strings.Contains(value, "/webrtc-direct/certhash/") {
			return strings.Split(strings.Split(value, "/certhash/")[1], "/p2p/")[0]
		}
	}
	t.Fatal("no WebRTC certhash in discovery")
	return ""
}

type streamByteReader struct{ io.Reader }

func (r streamByteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r.Reader, b[:])
	return b[0], err
}

func authenticateClient(t *testing.T, ctx context.Context, h host.Host, id peer.ID) {
	t.Helper()
	st, err := h.NewStream(ctx, id, relay.AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	request := []byte(`{"accessToken":"same-credential"}`)
	var prefix [10]byte
	n := binary.PutUvarint(prefix[:], uint64(len(request)))
	if _, err = st.Write(append(prefix[:n], request...)); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	size, err := binary.ReadUvarint(streamByteReader{st})
	if err != nil {
		t.Fatal(err)
	}
	if size > 4096 {
		t.Fatalf("oversized auth response %d", size)
	}
	response := make([]byte, size)
	if _, err = io.ReadFull(st, response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `"ok":true`) {
		t.Fatalf("relay auth %s", response)
	}
}

func freePort(t *testing.T, network string) int {
	t.Helper()
	if network == "tcp" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func TestFakeServiceRealClientDrainRestartAndRediscovery(t *testing.T) {
	tcpPort, udpPort := freePort(t, "tcp"), freePort(t, "udp")
	var changed atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var protocol string
		var port int
		switch r.URL.Path {
		case "/api/v1/namespaces/clipp/services/relay-tcp":
			protocol, port = "TCP", tcpPort
		case "/api/v1/namespaces/clipp/services/relay-udp":
			protocol, port = "UDP", udpPort
		default:
			http.NotFound(w, r)
			return
		}
		ingress := []map[string]string{{"ip": "127.0.0.1"}}
		if changed.Load() {
			ingress = append(ingress, map[string]string{"hostname": "localhost"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "spec": map[string]any{"ports": []map[string]any{{"protocol": protocol, "port": port}}}, "status": map[string]any{"loadBalancer": map[string]any{"ingress": ingress}}})
	}))
	defer api.Close()
	cfg := Config{TCP: Transport{Enabled: true, PublicPort: tcpPort, Service: ServiceRef{Namespace: "clipp", Name: "relay-tcp"}}, WebRTC: Transport{Enabled: true, PublicPort: udpPort, Service: ServiceRef{Namespace: "clipp", Name: "relay-udp"}}}
	start := func() (*relay.Server, *relay.Discovery, *Controller) {
		t.Helper()
		s, err := relay.New(lifecycleAuthority{}, testCredit{}, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/" + strconv.Itoa(tcpPort), WebRTCListenAddress: "/ip4/127.0.0.1/udp/" + strconv.Itoa(udpPort) + "/webrtc-direct"})
		if err != nil {
			t.Fatal(err)
		}
		d, err := relay.NewDiscovery(s, lifecycleAuthority{}, "relay.example.test", nil)
		if err != nil {
			_ = s.Close()
			t.Fatal(err)
		}
		c, err := New(d, api.Client(), api.URL, "token", cfg, func(bool) {})
		if err != nil {
			_ = s.Close()
			t.Fatal(err)
		}
		if err = c.Sync(context.Background()); err != nil {
			_ = s.Close()
			t.Fatal(err)
		}
		return s, d, c
	}
	first, firstDiscovery, firstController := start()
	defer first.Close()
	old := discoveryDocument(t, firstDiscovery)
	oldHash := documentWebRTCHash(t, old)
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	deviceID := h.ID()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	oldInfo := documentTCP(t, old)
	if err = h.Connect(ctx, oldInfo); err != nil {
		t.Fatal(err)
	}
	authenticateClient(t, ctx, h, oldInfo.ID)
	if _, err = client.Reserve(ctx, h, oldInfo); err != nil {
		t.Fatalf("first reservation: %v", err)
	}
	changed.Store(true)
	if err = firstController.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	old = discoveryDocument(t, firstDiscovery)
	if len(old.Relay.Addresses) != 4 || !strings.Contains(strings.Join(old.Relay.Addresses, " "), "/dns/localhost/tcp/") {
		t.Fatalf("Service change did not republish all addresses: %v", old.Relay.Addresses)
	}
	oldInfo = documentTCP(t, old)
	first.StartDrain()
	_ = firstDiscovery.Publish(nil)
	if _, err = client.Reserve(ctx, h, oldInfo); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatalf("new reservation during drain: %v", err)
	}
	if err = first.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	// The logical Service slot remains the same, but the process has stopped.
	second, secondDiscovery, _ := start()
	defer second.Close()
	if second.ActiveSessions() != 0 {
		t.Fatal("new relay inherited a live session")
	}
	staleCtx, stopStale := context.WithTimeout(ctx, 2*time.Second)
	staleError := h.Connect(staleCtx, oldInfo)
	stopStale()
	if staleError == nil {
		t.Fatal("stale Peer ID connected to replacement process")
	}
	fresh := discoveryDocument(t, secondDiscovery)
	if fresh.Relay.PeerID == old.Relay.PeerID || documentWebRTCHash(t, fresh) == oldHash {
		t.Fatalf("restarted process reused identity or certificate: old=%s new=%s", old.Relay.PeerID, fresh.Relay.PeerID)
	}
	newInfo := documentTCP(t, fresh)
	if err = h.Connect(ctx, newInfo); err != nil {
		t.Fatalf("fresh discovery failed: %v", err)
	}
	authenticateClient(t, ctx, h, newInfo.ID)
	if _, err = client.Reserve(ctx, h, newInfo); err != nil {
		t.Fatalf("fresh reservation: %v", err)
	}
	if h.ID() != deviceID {
		t.Fatalf("device identity changed: %s -> %s", deviceID, h.ID())
	}
}
