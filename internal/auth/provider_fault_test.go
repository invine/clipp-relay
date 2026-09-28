package auth_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

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
