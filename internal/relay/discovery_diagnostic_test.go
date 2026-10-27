package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/service"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

type blockedIntegratedOutput struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (o *blockedIntegratedOutput) Write(p []byte) (int, error) {
	o.once.Do(func() { close(o.entered) })
	<-o.release
	return len(p), nil
}

type failedIntegratedOutput struct{}

func (failedIntegratedOutput) Write([]byte) (int, error) {
	return 0, errors.New("SECRET_OUTPUT_ERROR_CANARY")
}

type discoveryErrorAuthority struct{}

func (discoveryErrorAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{}, errors.New("SECRET_AUTHORITY_ERROR_CANARY")
}

func TestSharedHTTPActivityAndShutdownIgnoreBlockedOrFailedOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writer func() (interface{ Write([]byte) (int, error) }, func())
	}{
		{"blocked", func() (interface{ Write([]byte) (int, error) }, func()) {
			out := &blockedIntegratedOutput{entered: make(chan struct{}), release: make(chan struct{})}
			return out, func() { close(out.release) }
		}},
		{"failed", func() (interface{ Write([]byte) (int, error) }, func()) { return failedIntegratedOutput{}, func() {} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer, release := tc.writer()
			defer release()
			log := diagnostic.New(writer)
			portal := auth.New(auth.Dependencies{}, config.Config{PortalOrigin: "https://portal.example.test"}, config.Material{}, auth.Provider{})
			portal.SetDiagnostics(log)
			app := service.New()
			app.SetDiagnostics(log.Emit)
			app.SetPublicHandler(portal.Handler())
			app.SetReady(true)
			if blocked, ok := writer.(*blockedIntegratedOutput); ok {
				select {
				case <-blocked.entered:
				case <-time.After(time.Second):
					t.Fatal("writer did not block")
				}
			}
			var wg sync.WaitGroup
			for range 100 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := httptest.NewRequest(http.MethodPost, "https://portal.example.test/auth/logout", nil)
					r.Header.Set("Origin", "https://wrong-origin.test")
					response := httptest.NewRecorder()
					app.PublicHandler().ServeHTTP(response, r)
					if response.Code != http.StatusForbidden {
						t.Errorf("refusal status = %d", response.Code)
					}
				}()
			}
			completed := make(chan struct{})
			go func() { wg.Wait(); close(completed) }()
			select {
			case <-completed:
			case <-time.After(2 * time.Second):
				t.Fatal("public HTTP activity waited for diagnostics")
			}
			drained := make(chan struct{})
			go func() { app.BeginDrain(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(2 * time.Second):
				t.Fatal("drain waited for diagnostics")
			}
			closed := make(chan struct{})
			go func() { log.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("logger shutdown waited for output")
			}
		})
	}
}

func TestDiscoveryHTTPOutcomesShareBoundedDiagnostics(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	s, err := New(wireAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr, err := s.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDiscovery(s, wireAuthority{}, "relay.example.test", []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, token string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "https://relay.example.test"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Request-Id", "SECRET_HEADER_CANARY")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("discovery status = %d, want %d: %s", w.Code, want, w.Body.String())
		}
	}
	request("/v1/relay", "authorized", http.StatusOK)
	request("/v1/relay", "SECRET_TOKEN_CANARY", http.StatusUnauthorized)
	request("/v1/relay?SECRET_QUERY_CANARY=1", "authorized", http.StatusBadRequest)
	unavailable, err := NewDiscovery(s, discoveryErrorAuthority{}, "relay.example.test", []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	unavailableRequest := httptest.NewRequest(http.MethodGet, "https://relay.example.test/v1/relay", nil)
	unavailableRequest.Header.Set("Authorization", "Bearer authorized")
	unavailableResponse := httptest.NewRecorder()
	unavailable.ServeHTTP(unavailableResponse, unavailableRequest)
	if unavailableResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("authority failure status = %d", unavailableResponse.Code)
	}
	if err := d.Publish(nil); err != nil {
		t.Fatal(err)
	}
	request("/v1/relay", "authorized", http.StatusServiceUnavailable)
	if err := d.Publish([]ma.Multiaddr{addr}); err != nil {
		t.Fatal(err)
	}
	s.StartDrain()
	request("/v1/relay", "authorized", http.StatusServiceUnavailable)
	for _, pair := range [][3]string{{"relay.discovery", "success", "none"}, {"relay.discovery", "refused", "invalid_credentials"}, {"relay.discovery", "refused", "not_published"}, {"relay.discovery", "refused", "draining"}} {
		row := waitRow(t, out, pair[0], pair[1], pair[2])
		if id, ok := row["request_id"].(string); !ok || len(id) != 32 {
			t.Fatalf("missing request correlation: %v", row)
		}
	}
	for _, canary := range []string{"SECRET_", s.Host.ID().String(), addr.String()} {
		if strings.Contains(out.text(), canary) {
			t.Fatalf("discovery diagnostics leaked %q: %s", canary, out.text())
		}
	}
}

func TestIntegratedHTTPRelayAndReadinessRowsMatchCatalog(t *testing.T) {
	out := &lockedOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	portal := auth.New(auth.Dependencies{}, config.Config{PortalOrigin: "https://portal.example.test"}, config.Material{}, auth.Provider{})
	portal.SetDiagnostics(log)
	app := service.New()
	app.SetPublicHandler(portal.Handler())
	app.SetDiagnostics(log.Emit)
	r, err := New(wireAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", MaxSessions: 1, Diagnostics: log})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	addr, err := r.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := NewDiscovery(r, wireAuthority{}, "relay.example.test", []ma.Multiaddr{addr})
	if err != nil {
		t.Fatal(err)
	}
	app.SetReadinessCheck(discovery.Published)
	app.SetReady(true)
	if got := probeReadiness(app); got != http.StatusOK {
		t.Fatalf("published readiness = %d", got)
	}
	discoveryRequest := httptest.NewRequest(http.MethodGet, "https://relay.example.test/v1/relay", nil)
	discoveryRequest.Header.Set("Authorization", "Bearer authorized")
	discoveryResponse := httptest.NewRecorder()
	discovery.ServeHTTP(discoveryResponse, discoveryRequest)
	if discoveryResponse.Code != http.StatusOK {
		t.Fatalf("discovery = %d", discoveryResponse.Code)
	}
	waitRow(t, out, "service.readiness", "success", "none")
	waitRow(t, out, "relay.discovery", "success", "none")
	beforePage := len(out.rows())
	page := httptest.NewRecorder()
	app.PublicHandler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "https://portal.example.test/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("portal page = %d", page.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(out.rows()); got != beforePage {
		t.Fatalf("routine page or idle interval emitted %d rows", got-beforePage)
	}
	refuse := func() {
		req := httptest.NewRequest(http.MethodPost, "https://portal.example.test/auth/logout?SECRET_QUERY_CANARY=1", strings.NewReader("SECRET_BODY_CANARY"))
		req.Header.Set("Origin", "https://SECRET_ORIGIN_CANARY.test")
		req.Header.Set("Authorization", "Bearer SECRET_CREDENTIAL_CANARY")
		req.Header.Set("X-Request-Id", "SECRET_HEADER_CANARY")
		response := httptest.NewRecorder()
		app.PublicHandler().ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("owner refusal = %d", response.Code)
		}
	}
	refuse()
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: r.Host.ID(), Addrs: r.Host.Addrs()}
	if err := h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, r.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatal(response)
	}
	capacityResponse := httptest.NewRecorder()
	discovery.ServeHTTP(capacityResponse, discoveryRequest)
	if capacityResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("global session ceiling status = %d", capacityResponse.Code)
	}
	if _, err := client.Reserve(ctx, h, ai); err != nil {
		t.Fatal(err)
	}
	if err := discovery.Publish(nil); err != nil {
		t.Fatal(err)
	}
	if got := probeReadiness(app); got != http.StatusServiceUnavailable {
		t.Fatalf("withdrawn publication readiness = %d", got)
	}
	if err := discovery.Publish([]ma.Multiaddr{addr}); err != nil {
		t.Fatal(err)
	}
	if got := probeReadiness(app); got != http.StatusOK {
		t.Fatalf("recovered publication readiness = %d", got)
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); refuse() }()
	}
	wg.Wait()
	r.StartDrain()
	app.BeginDrain()
	log.Close()
	text := out.text()
	for _, canary := range []string{"SECRET_", r.Host.ID().String(), h.ID().String(), addr.String()} {
		if strings.Contains(text, canary) {
			t.Fatalf("private material in shared output: %q", canary)
		}
	}
	rows := out.rows()
	for _, pair := range [][3]string{{"service.readiness", "success", "none"}, {"service.readiness", "failed", "publication_stale"}, {"service.readiness", "failed", "draining"}, {"portal.logout", "refused", "origin_rejected"}, {"relay.discovery", "success", "none"}, {"relay.discovery", "refused", "safety_limit"}, {"relay.connection", "success", "none"}, {"relay.authentication", "success", "none"}, {"relay.session", "started", "none"}, {"relay.reservation", "success", "none"}, {"log.suppressed", "observed", "diagnostic_loss"}} {
		if !containsDiagnosticRow(rows, pair) {
			t.Fatalf("missing %s/%s/%s in %s", pair[0], pair[1], pair[2], text)
		}
	}
	if strings.Contains(text, `"event":"portal.page"`) || strings.Contains(text, `"event":"heartbeat"`) {
		t.Fatalf("routine page or heartbeat row: %s", text)
	}
	assertRowsMatchCatalog(t, text)
	for _, event := range []string{"portal.logout", "relay.discovery", "relay.authentication", "relay.reservation", "service.readiness", "log.suppressed"} {
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			if strings.Contains(line, `"event":"`+event+`"`) {
				t.Logf("captured %s: %s", event, line)
				break
			}
		}
	}
}

func probeReadiness(app *service.Service) int {
	response := httptest.NewRecorder()
	app.PrivateHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return response.Code
}

func containsDiagnosticRow(rows []map[string]any, pair [3]string) bool {
	for _, row := range rows {
		if row["event"] == pair[0] && row["outcome"] == pair[1] && row["reason"] == pair[2] {
			return true
		}
	}
	return false
}

func assertRowsMatchCatalog(t *testing.T, output string) {
	t.Helper()
	data, err := os.ReadFile("../diagnostic/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Events []struct {
			Event string      `json:"event"`
			Pairs [][2]string `json:"pairs"`
		} `json:"events"`
		ControlEvents []struct {
			Event string      `json:"event"`
			Pairs [][2]string `json:"pairs"`
		} `json:"control_events"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	allowed := make(map[[3]string]bool)
	for _, event := range append(catalog.Events, catalog.ControlEvents...) {
		for _, pair := range event.Pairs {
			allowed[[3]string{event.Event, pair[0], pair[1]}] = true
		}
	}
	fields := map[string]bool{"time": true, "level": true, "msg": true, "event": true, "outcome": true, "reason": true, "request_id": true, "session_id": true, "transport": true, "client_type": true, "gate": true, "schema_revision": true, "duration_ms": true, "signal": true, "state": true, "rate_limited_count": true, "queue_full_count": true, "window_seconds": true}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if len(line) > 2048 {
			t.Fatalf("diagnostic row exceeds 2 KiB: %d", len(line))
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		key := [3]string{row["event"].(string), row["outcome"].(string), row["reason"].(string)}
		if !allowed[key] {
			t.Fatalf("row outside fixed catalog: %v", row)
		}
		for field := range row {
			if !fields[field] {
				t.Fatalf("unauthorized diagnostic field %q in %v", field, row)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, row["time"].(string)); err != nil {
			t.Fatalf("invalid row time: %v", row)
		}
	}
}
