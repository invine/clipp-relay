package auth

import (
	"net/http/httptest"
	"testing"
)

func TestAdminListScopeStrictRouteAndSafeReturnLink(t *testing.T) {
	valid := httptest.NewRequest("GET", "/admin?accounts_after=6dd09395-51a0-451c-96b3-716e6038e870&status=Pending&email=%20owner%40example.test%20&limit=1", nil)
	scope, ok := adminListScopeFromQuery(valid, true)
	if !ok || scope.limit != 1 || scope.email != "owner@example.test" || scope.query.Get("accounts_after") == "" {
		t.Fatalf("valid strict list scope: %+v, ok=%v", scope, ok)
	}
	defaultLimit := httptest.NewRequest("GET", "/admin?limit=50", nil)
	scope, ok = adminListScopeFromQuery(defaultLimit, false)
	if !ok || scope.limit != 50 || scope.query.Get("limit") != "50" {
		t.Fatalf("safe return context changed explicit default limit: %+v, ok=%v", scope, ok)
	}
	invalid := httptest.NewRequest("GET", "/admin?accounts_after=bad&status=unknown&email=%20owner%40example.test%20&limit=1000", nil)
	if _, ok := adminListScopeFromQuery(invalid, true); ok {
		t.Fatal("strict list route accepted malformed scope")
	}
	safe, ok := adminListScopeFromQuery(invalid, false)
	if !ok || safe.query.Get("accounts_after") != "" || safe.query.Get("status") != "" || safe.query.Get("limit") != "" || safe.query.Get("email") != "owner@example.test" {
		t.Fatalf("return-link scope did not retain only safe valid values: %+v, ok=%v", safe, ok)
	}
}
