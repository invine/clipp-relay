package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
)

func TestUncomposedProtectedMutationsAreUnavailable(t *testing.T) {
	s := auth.New(auth.Dependencies{}, config.Config{}, config.Material{}, auth.Provider{})
	for _, path := range []string{"/auth/revoke", "/auth/delete", "/admin/accounts/00000000-0000-0000-0000-000000000001"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("unprotected mutation returned %d", w.Code)
			}
		})
	}
}
