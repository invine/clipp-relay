package auth_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

func TestProviderOutagePreservesIndependentGrant(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	session := loginAs(t, s, d, "provider-outage-grant", "grant@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	access, refresh := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, session, "android", c.PublicClients.AndroidRedirect))
	state, binding := start(t, s, nil)
	d.server.Close()
	if response := complete(s, state, binding); response.Code == 303 {
		t.Fatal("new provider login succeeded during outage")
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), access); err != nil {
		t.Fatalf("existing independent access lost during provider outage: %v", err)
	}
	response := tokenPost(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}})
	if response.Code != 200 {
		t.Fatalf("independent grant could not refresh during provider outage: %d %s", response.Code, response.Body.String())
	}
	var renewed struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &renewed); err != nil || renewed.Access == "" {
		t.Fatalf("renewed access missing: %v", err)
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), renewed.Access); err != nil {
		t.Fatalf("renewed access invalid during provider outage: %v", err)
	}
}

func TestCancelledSlowTokenExchangeIsNotRetried(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "cancelled-token-exchange"
	d.tokenGate = make(chan struct{})
	d.tokenStarted = make(chan struct{}, 1)
	var release sync.Once
	releaseToken := func() { release.Do(func() { close(d.tokenGate) }) }
	defer releaseToken()
	s := auth.New(db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	parts := strings.Split(state, "|")
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/auth/callback?state="+url.QueryEscape(parts[0])+"&code="+url.QueryEscape(parts[1]), nil).WithContext(ctx)
	request.AddCookie(binding)
	done := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		done <- response.Code
	}()
	select {
	case <-d.tokenStarted:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("provider token exchange did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code == 303 {
			t.Fatal("cancelled provider exchange completed login")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled provider exchange retained callback worker")
	}
	releaseToken()
	if response := complete(s, state, binding); response.Code == 303 {
		t.Fatal("one-use callback was accepted after an ambiguous exchange")
	}
	d.mu.Lock()
	calls := d.tokenCalls
	d.mu.Unlock()
	if calls != 1 {
		t.Fatalf("ambiguous token exchange was retried %d times", calls)
	}
}
