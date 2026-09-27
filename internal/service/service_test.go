package service_test

import (
	"context"
	"errors"
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

func TestDeletionMetricsExposeOnlyBoundedAggregateSignals(t *testing.T) {
	s := service.New()
	s.SetDeletionMetrics(func(context.Context) (service.DeletionSample, error) {
		return service.DeletionSample{Pending: 820, OldestSeconds: 3600, Warning: true, Critical: true, Completed: 4, Retried: 3}, nil
	})
	out := httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := out.Body.String()
	for _, want := range []string{"clipp_relay_deletion_pending 820", "clipp_relay_deletion_oldest_seconds 3600.000", "clipp_relay_deletion_critical 1", `clipp_relay_deletion_outcomes_total{result="retry"} 3`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "account=") || strings.Contains(body, "journal=") {
		t.Fatal("identifying metric label")
	}
	s.SetDeletionMetrics(func(context.Context) (service.DeletionSample, error) {
		return service.DeletionSample{}, errors.New("private outage")
	})
	out = httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(out.Body.String(), "clipp_relay_deletion_observation_available 0") || strings.Contains(out.Body.String(), "clipp_relay_deletion_pending 0") {
		t.Fatal("missing observation reported healthy zero")
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

func TestReadyRequiresLocalRoutingAndPublication(t *testing.T) {
	s := service.New()
	s.SetRouting(false)
	s.SetReady(true)
	r := httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 503 {
		t.Fatalf("unbound readiness: %d", r.Code)
	}
	s.SetRouting(true)
	r = httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 200 {
		t.Fatalf("bound readiness: %d", r.Code)
	}
}

func TestReadyChecksPublicationFreshnessAtProbeTime(t *testing.T) {
	s := service.New()
	s.SetReady(true)
	fresh := false
	s.SetReadinessCheck(func() bool { return fresh })
	r := httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 503 {
		t.Fatalf("stale readiness: %d", r.Code)
	}
	fresh = true
	r = httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 200 {
		t.Fatalf("fresh readiness: %d", r.Code)
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

func TestRendezvousMetricsHaveOnlyFixedVersionLabels(t *testing.T) {
	s := service.New()
	s.SetRendezvousMetrics(func() uint64 { return 7 }, func() uint64 { return 11 })
	metric := httptest.NewRecorder()
	s.PrivateHandler().ServeHTTP(metric, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metric.Body.String()
	if metric.Code != 200 || !strings.Contains(body, `clipp_relay_rendezvous_requests_total{version="1"} 7`) || !strings.Contains(body, `clipp_relay_rendezvous_requests_total{version="2"} 11`) || strings.Contains(body, "peer=") || strings.Contains(body, "account=") {
		t.Fatalf("metrics: %d %q", metric.Code, body)
	}
}
