package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

type lockedOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.Buffer.Write(p)
}
func (o *lockedOutput) rows() []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(o.Bytes()), []byte("\n")) {
		var row map[string]any
		if json.Unmarshal(line, &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows
}
func waitRow(t *testing.T, out *lockedOutput, event, outcome, reason string) map[string]any {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		for _, row := range out.rows() {
			if row["event"] == event && row["outcome"] == outcome && row["reason"] == reason {
				return row
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %s/%s/%s in %v", event, outcome, reason, out.rows())
	return nil
}

func TestRelayAuthDiagnosticJourney(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	admitted := waitRow(t, out, "relay.connection", "success", "none")
	if admitted["transport"] != "tcp" || admitted["session_id"] == nil {
		t.Fatalf("admission %v", admitted)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	authenticated := waitRow(t, out, "relay.authentication", "success", "none")
	started := waitRow(t, out, "relay.session", "started", "none")
	if authenticated["request_id"] == nil || authenticated["request_id"] == admitted["session_id"] || authenticated["session_id"] != admitted["session_id"] || started["session_id"] != admitted["session_id"] {
		t.Fatalf("correlation %v %v %v", admitted, authenticated, started)
	}
	time.Sleep(time.Second)
	if r := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	renewed := waitRow(t, out, "relay.session", "success", "renewed")
	if renewed["session_id"] != admitted["session_id"] {
		t.Fatalf("renewal %v", renewed)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "b"); !strings.Contains(r, "account_change_requires_new_connection") {
		t.Fatal(r)
	}
	mismatch := waitRow(t, out, "relay.authentication", "refused", "account_mismatch")
	if mismatch["session_id"] != admitted["session_id"] {
		t.Fatalf("mismatch %v", mismatch)
	}
	_ = h.Network().ClosePeer(s.Host.ID())
	ended := waitRow(t, out, "relay.session", "ended", "connection_closed")
	if ended["session_id"] != admitted["session_id"] {
		t.Fatalf("end %v", ended)
	}
}

func TestPolicyCloseEmitsSessionEndWithoutIdentity(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	s.CloseAccount("a")
	row := waitRow(t, out, "relay.session", "ended", "policy_changed")
	if row["session_id"] == nil {
		t.Fatal(row)
	}
	for _, canary := range []string{h.ID().String(), "/ip4/127.0.0.1"} {
		if strings.Contains(out.text(), canary) {
			t.Fatalf("leaked %q", canary)
		}
	}
}

type expiringAuthority struct{}

func (expiringAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{AccountID: "private-account-canary", SessionLimit: 2, ExpiresAt: time.Now().Add(300 * time.Millisecond)}, nil
}

func TestRelaySessionExpiryIsDistinctFromDisconnect(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(expiringAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "expiry-token-canary"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	row := waitRow(t, out, "relay.session", "ended", "session_expired")
	if row["session_id"] == nil {
		t.Fatal(row)
	}
	if strings.Contains(out.text(), "private-account-canary") || strings.Contains(out.text(), "expiry-token-canary") || strings.Contains(out.text(), h.ID().String()) {
		t.Fatal("identity or credential leaked")
	}
}

func (o *lockedOutput) text() string { o.mu.Lock(); defer o.mu.Unlock(); return o.Buffer.String() }

func TestRelayAuthRefusalIsBoundedAndPrivate(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	for i := 0; i < 25; i++ {
		_ = authResponse(t, ctx, h, s.Host.ID(), "secret-token-canary")
	}
	waitRow(t, out, "relay.authentication", "refused", "invalid_credentials")
	count := 0
	for _, row := range out.rows() {
		if row["event"] == "relay.authentication" && row["outcome"] == "refused" {
			count++
		}
	}
	if count > 10 {
		t.Fatalf("unbounded refusal rows: %d", count)
	}
	if strings.Contains(out.text(), "secret-token-canary") || strings.Contains(out.text(), h.ID().String()) {
		t.Fatal("credential or peer leaked")
	}
}

func TestPreauthenticationExpiresWithoutRelaySession(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	admitted := waitRow(t, out, "relay.connection", "success", "none")
	until := time.Now().Add(12 * time.Second)
	var ended map[string]any
	for time.Now().Before(until) {
		for _, r := range out.rows() {
			if r["event"] == "relay.connection" && r["reason"] == "preauth_expired" {
				ended = r
				break
			}
		}
		if ended != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ended == nil || ended["session_id"] != admitted["session_id"] {
		t.Fatalf("preauth outcome %v", out.rows())
	}
	for _, r := range out.rows() {
		if r["event"] == "relay.authentication" || r["event"] == "relay.session" {
			t.Fatalf("invented auth/session: %v", r)
		}
	}
}

func TestMalformedAuthFrameDoesNotLeakPayload(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	st, err := h.NewStream(ctx, s.Host.ID(), AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Write(frame(`{"accessToken":"malformed-frame-canary","peer":"peer-id-canary"}`))
	_ = st.CloseWrite()
	waitRow(t, out, "relay.authentication", "refused", "invalid_request")
	if strings.Contains(out.text(), "malformed-frame-canary") || strings.Contains(out.text(), "peer-id-canary") || strings.Contains(out.text(), h.ID().String()) {
		t.Fatal("request or identity leaked")
	}
}

func TestDrainEndsRelaySessionWithDrainReason(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	s.StartDrain()
	if err = s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	waitRow(t, out, "relay.session", "ended", "draining")
}

func TestQuotaReporterClosesSessionsWithReportingReason(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	runAccountCutoff(t, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log}, "tcp")
	row := waitRow(t, out, "relay.session", "ended", "reporting_failed")
	if row["session_id"] == nil {
		t.Fatal(row)
	}
	report := waitRow(t, out, "quota.reporting", "failed", "reporting_failed")
	if report["request_id"] != nil || report["session_id"] != nil {
		t.Fatalf("aggregate reporter invented correlation: %v", report)
	}
	for _, r := range out.rows() {
		if r["event"] == "quota.reporting" && r["outcome"] == "success" {
			t.Fatalf("routine byte callback logged success: %v", r)
		}
	}
	if strings.Contains(out.text(), "private-relayed-payload-canary") {
		t.Fatal("opaque relay payload leaked into diagnostics")
	}
}

func TestQuotaExhaustionKeepsAuthenticationReasonHonest(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, failingCredit{quota.ErrExhausted}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(r, "quota_exhausted") {
		t.Fatal(r)
	}
	time.Sleep(20 * time.Millisecond)
	for _, row := range out.rows() {
		if row["event"] == "relay.authentication" && row["reason"] == "quota_unavailable" {
			t.Fatalf("exhaustion mislabeled as outage: %v", row)
		}
	}
}

func TestPhysicalTransportDiagnosticIDsNeverCarryAcrossConnections(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	f := newTransportClients(t, wireAuthority{}, 1, log)
	seen := map[string]bool{}
	for _, tc := range []struct{ family, transport string }{{"tcp", "tcp"}, {"websocket", "wss"}, {"webrtc-direct", "webrtc_direct"}} {
		h := f.connect(t, tc.family, nil)
		f.auth(t, h, "authorized", `"ok":true`)
		var id string
		until := time.Now().Add(2 * time.Second)
		for time.Now().Before(until) {
			for _, row := range out.rows() {
				if row["event"] == "relay.session" && row["outcome"] == "started" && row["transport"] == tc.transport {
					id, _ = row["session_id"].(string)
				}
			}
			if id != "" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if id == "" || seen[id] {
			t.Fatalf("missing or reused physical ID for %s: %v remote=%v", tc.family, out.rows(), f.server.Host.Network().ConnsToPeer(h.ID()))
		}
		seen[id] = true
	}
}

func TestClientDisconnectDuringDrainKeepsConnectionReason(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if r := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	s.StartDrain()
	_ = h.Network().ClosePeer(s.Host.ID())
	waitRow(t, out, "relay.session", "ended", "connection_closed")
}
