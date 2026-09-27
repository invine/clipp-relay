package service_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clipp-relay/internal/service"
)

func TestPublicNeverExposesOperations(t *testing.T) {
	s := service.New()
	for _, path := range []string{"/livez", "/readyz", "/metrics", "/debug/pprof"} {
		r := httptest.NewRecorder()
		s.PublicHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusNotFound {
			t.Fatalf("public %s: %d", path, r.Code)
		}
	}
}

func TestPrivateHealthDoesNotClaimRelayReadiness(t *testing.T) {
	s := service.New()
	r := httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if r.Code != 200 || r.Body.String() != "ok\n" {
		t.Fatalf("livez: %d %q", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 503 || r.Body.String() != "unavailable\n" {
		t.Fatalf("readyz: %d %q", r.Code, r.Body.String())
	}
}

func TestReadinessMetricTracksHealth(t *testing.T) {
	s := service.New()
	for _, ready := range []bool{false, true, false} {
		s.SetReady(ready)
		metric := httptest.NewRecorder()
		s.PrivateHandler().ServeHTTP(metric, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		want := "clipp_relay_ready 0\n"
		if ready {
			want = "clipp_relay_ready 1\n"
		}
		if metric.Code != 200 || !strings.Contains(metric.Body.String(), want) {
			t.Fatalf("ready=%t metric=%d %q", ready, metric.Code, metric.Body.String())
		}
	}
}
