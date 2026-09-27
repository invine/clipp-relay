package service_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"clipp-relay/internal/service"
)

func TestConfiguredPublicHandlerReceivesLoginRouteWithoutExposingOperations(t *testing.T) {
	s := service.New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusFound) })
	s.SetPublicHandler(mux)
	for path, want := range map[string]int{"/auth/login": http.StatusFound, "/livez": http.StatusNotFound} {
		w := httptest.NewRecorder()
		s.PublicHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Fatalf("%s: got %d, want %d", path, w.Code, want)
		}
	}
}
