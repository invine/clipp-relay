package service_test

import (
	"net/http"
	"net/http/httptest"
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
