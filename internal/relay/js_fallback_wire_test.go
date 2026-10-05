package relay

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	multistream "github.com/multiformats/go-multistream"
)

// The optional harness uses Clipp's public controller and host adapter. It must
// report the selected family after a relayed round trip, clean up both clients,
// and exit normally. Source and dependency checkouts remain read-only.
func TestRealJSRelayTransportFallback(t *testing.T) {
	harness := os.Getenv("CLIPP_JS_TRANSPORT_HARNESS")
	modules := os.Getenv("CLIPP_JS_NODE_MODULES")
	if harness == "" || modules == "" {
		t.Skip("Clipp transport fallback harness and dependencies not configured")
	}
	if !filepath.IsAbs(harness) || !filepath.IsAbs(modules) {
		t.Fatal("Clipp harness and module paths must be absolute")
	}
	loader := filepath.Join(modules, "tsx", "dist", "loader.mjs")
	for _, path := range []string{harness, loader} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range []string{"tcp", "websocket", "webrtc-direct"} {
		t.Run(expected, func(t *testing.T) {
			credit := &recordingCredit{counts: map[string]int64{}}
			s, err := New(twoAuthority{}, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct", WebSocketHostname: "localhost", MaxSessions: 2})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			productionAuth := captureWireHandler(t, s.Host, AuthProtocol)
			var attemptsMu sync.Mutex
			attempts := map[peer.ID]map[string]int{}
			s.Host.SetStreamHandler(AuthProtocol, func(st network.Stream) {
				family := "tcp"
				address := st.Conn().LocalMultiaddr()
				if _, err := address.ValueForProtocol(ma.P_WEBRTC_DIRECT); err == nil {
					family = "webrtc-direct"
				} else if _, err := address.ValueForProtocol(ma.P_WS); err == nil {
					family = "websocket"
				}
				blocked := family == "tcp" && expected != "tcp" || family == "websocket" && expected == "webrtc-direct"
				if blocked && !consumeWireAuthRequest(st) {
					_ = st.Reset()
					return
				}
				attemptsMu.Lock()
				if attempts[st.Conn().RemotePeer()] == nil {
					attempts[st.Conn().RemotePeer()] = map[string]int{}
				}
				attempts[st.Conn().RemotePeer()][family]++
				attemptsMu.Unlock()
				if blocked {
					// No semantic policy response: this is transport-local failure
					// after verified identity and a complete bounded request.
					_ = st.Reset()
					return
				}
				_ = productionAuth(AuthProtocol, st)
			})
			addresses := make([]ma.Multiaddr, 0, 3)
			var proxyCertificate []byte
			for _, family := range []string{"tcp", "websocket", "webrtc-direct"} {
				address, err := s.ListenAddressFor(family)
				if err != nil {
					t.Fatal(err)
				}
				if family == "websocket" {
					address, _, proxyCertificate = localTLSProxyCertificate(t, address, "localhost")
					port, err := address.ValueForProtocol(ma.P_TCP)
					if err != nil {
						t.Fatal(err)
					}
					address = ma.StringCast("/dns4/localhost/tcp/" + port + "/tls/ws")
				}
				addresses = append(addresses, address)
			}
			var discovery *Discovery
			front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { discovery.ServeHTTP(w, r) }))
			front.StartTLS()
			t.Cleanup(front.Close)
			discovery, err = NewDiscovery(s, twoAuthority{}, strings.TrimPrefix(front.URL, "https://"), addresses)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			certificates := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw})
			certificates = append(certificates, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxyCertificate})...)
			ca := filepath.Join(dir, "ca.pem")
			if err := os.WriteFile(ca, certificates, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "--import", loader, harness, front.URL+"/v1/relay", "a", "b", expected)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("JS relay fallback interop: %v\n%s", err, out)
			}
			var receipt struct {
				OK             bool   `json:"ok"`
				SelectedFamily string `json:"selectedFamily"`
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &receipt); err != nil || !receipt.OK || receipt.SelectedFamily != expected {
				t.Fatalf("missing %s JS fallback success receipt: %s", expected, out)
			}
			if credit.count("a") == 0 || credit.count("b") == 0 {
				t.Fatalf("JS endpoint charges missing: a=%d b=%d", credit.count("a"), credit.count("b"))
			}
			attemptsMu.Lock()
			observed := attempts
			attempts = map[peer.ID]map[string]int{}
			attemptsMu.Unlock()
			if len(observed) != 2 {
				t.Fatalf("AUTH attempts from %d Device Identities, want 2", len(observed))
			}
			for _, byFamily := range observed {
				for index, family := range []string{"tcp", "websocket", "webrtc-direct"} {
					if byFamily[family] == 0 {
						t.Fatalf("required %s AUTH wire attempt absent: %v", family, byFamily)
					}
					if family == expected {
						if len(byFamily) != index+1 {
							t.Fatalf("unneeded AUTH family after %s selection: %v", expected, byFamily)
						}
						break
					}
				}
			}
			waitTransportSessions(t, s, 0)
			t.Logf("JS fallback wire evidence: %s", lines[len(lines)-1])
		})
	}
}

// Capture through the public protocol negotiator so the test delegates to the
// same handler production registered, without calling a private auth method.
func captureWireHandler(t *testing.T, h host.Host, id protocol.ID) protocol.HandlerFunc {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	type result struct {
		id      protocol.ID
		handler protocol.HandlerFunc
		err     error
	}
	negotiated := make(chan result, 1)
	go func() {
		selected, handler, err := h.Mux().Negotiate(server)
		negotiated <- result{selected, handler, err}
	}()
	if err := multistream.SelectProtoOrFail(id, client); err != nil {
		t.Fatal(err)
	}
	chosen := <-negotiated
	if chosen.err != nil || chosen.id != id || chosen.handler == nil {
		t.Fatalf("capture production protocol handler: %s, %v", chosen.id, chosen.err)
	}
	return chosen.handler
}

func consumeWireAuthRequest(st network.Stream) bool {
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(st, 4096)
	size, err := binary.ReadUvarint(reader)
	if err != nil || size == 0 || size > 4096 {
		return false
	}
	if _, err := io.CopyN(io.Discard, reader, int64(size)); err != nil {
		return false
	}
	_, err = reader.ReadByte()
	return err == io.EOF
}
