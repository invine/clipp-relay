package relay

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
)

// Set CLIPP_JS_NODE_MODULES to a read-only Clipp installation to run the
// real JS/libp2p interoperability check. No files are written to that checkout.
func TestRealJSLibp2pTCPInterop(t *testing.T) {
	modules := os.Getenv("CLIPP_JS_NODE_MODULES")
	if modules == "" {
		t.Skip("Clipp JS dependencies not configured")
	}
	if _, err := os.Stat(filepath.Join(modules, "libp2p")); err != nil {
		t.Fatal(err)
	}
	credit := &recordingCredit{counts: map[string]int64{}}
	s, err := New(twoAuthority{}, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr, err := s.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	var discovery *Discovery
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { discovery.ServeHTTP(w, r) }))
	ts.StartTLS()
	defer ts.Close()
	discovery, err = NewDiscovery(s, twoAuthority{}, strings.TrimPrefix(ts.URL, "https://"), []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.Symlink(modules, filepath.Join(dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/js-wire.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "js-wire.mjs")
	if err = os.WriteFile(script, data, 0600); err != nil {
		t.Fatal(err)
	}
	cert := ts.Certificate()
	if _, err = x509.ParseCertificate(cert.Raw); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", script, ts.URL+"/v1/relay", "a", "b")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS libp2p interop: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("missing JS success evidence: %s", out)
	}
	if credit.count("a") == 0 || credit.count("b") == 0 {
		t.Fatalf("JS endpoint charges missing: a=%d b=%d", credit.count("a"), credit.count("b"))
	}
	t.Logf("JS wire evidence: %s", strings.TrimSpace(string(out)))
}

// The opt-in harness exercises Clipp's real single-host controller against all
// relay families. Module and source paths are read-only; generated TLS trust and
// harness artifacts remain in this test's disposable directory.
func TestRealJSConcurrentRelayTransports(t *testing.T) {
	harness := os.Getenv("CLIPP_JS_TRANSPORT_HARNESS")
	modules := os.Getenv("CLIPP_JS_NODE_MODULES")
	if harness == "" || modules == "" {
		t.Skip("Clipp concurrent transport harness and dependencies not configured")
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
	credit := &recordingCredit{counts: map[string]int64{}}
	s, err := New(twoAuthority{}, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct", WebSocketHostname: "relay.example.test", MaxSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addresses := make([]ma.Multiaddr, 0, 3)
	var proxyCertificate []byte
	for _, family := range []string{"tcp", "websocket", "webrtc-direct"} {
		address, err := s.ListenAddressFor(family)
		if err != nil {
			t.Fatal(err)
		}
		if family == "websocket" {
			address, _, proxyCertificate = localTLSProxyCertificate(t, address, "relay.example.test")
		}
		addresses = append(addresses, address)
	}
	var discovery *Discovery
	front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { discovery.ServeHTTP(w, r) }))
	front.StartTLS()
	defer front.Close()
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
	cmd := exec.CommandContext(ctx, "node", "--import", loader, harness, front.URL+"/v1/relay", "a", "b")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS concurrent relay transport interop: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("missing concurrent JS success evidence: %s", out)
	}
	if credit.count("a") == 0 || credit.count("b") == 0 {
		t.Fatalf("JS endpoint charges missing: a=%d b=%d", credit.count("a"), credit.count("b"))
	}
	t.Logf("JS concurrent transport evidence: %s", strings.TrimSpace(string(out)))
}
