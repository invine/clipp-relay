package publication

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/relay"
)

type publicationRoundTrip func(*http.Request) (*http.Response, error)

func (f publicationRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuietServiceWatchDoesNotReportPublicationAPIOutage(t *testing.T) {
	var watches atomic.Int32
	secondWatch := make(chan struct{}, 1)
	malformedWatch := make(chan struct{})
	client := &http.Client{Transport: publicationRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("watch") == "1" {
			if watches.Add(1) > 1 {
				secondWatch <- struct{}{}
				select {
				case <-malformedWatch:
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"type":`)), Header: make(http.Header)}, nil
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			// A quiet Kubernetes watch can close at its timeout with no event.
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		body := `{"metadata":{"resourceVersion":"1"},"spec":{"ports":[{"protocol":"TCP","port":4001}]},"status":{"loadBalancer":{"ingress":[{"ip":"127.0.0.1"}]}}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	s, err := relay.New(testAuthority{}, testAccounting{}, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := relay.NewDiscovery(s, testAuthority{}, "relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(d, client, "https://kubernetes.example.test", "token", Config{TCP: Transport{Enabled: true, PublicPort: 4001, Service: ServiceRef{Namespace: "clipp", Name: "relay-tcp"}}}, func(bool) {})
	if err != nil {
		t.Fatal(err)
	}
	rows := make(chan diagnostic.Event, 4)
	c.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { rows <- e }))
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { c.watch(ctx, c.sources["tcp"], make(chan struct{}, 1)); close(done) }()
	select {
	case row := <-rows:
		if err := c.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case recovery := <-rows:
			t.Fatalf("quiet watch caused false publication API transitions: %s/%s then %s/%s", row.Outcome, row.Reason, recovery.Outcome, recovery.Reason)
		default:
			t.Fatalf("quiet watch caused false publication API transition: %s/%s", row.Outcome, row.Reason)
		}
	case <-secondWatch:
		select {
		case row := <-rows:
			t.Fatalf("quiet watch logged publication API %s/%s", row.Outcome, row.Reason)
		default:
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not reconnect after quiet close")
	}
	close(malformedWatch)
	select {
	case row := <-rows:
		if row.Outcome != "failed" || row.Reason != "invalid_response" {
			t.Fatalf("malformed watch logged %s/%s", row.Outcome, row.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("malformed watch did not report a failure")
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case row := <-rows:
		if row.Outcome != "recovered" || row.Reason != "none" {
			t.Fatalf("valid Service read logged %s/%s", row.Outcome, row.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid Service read did not report recovery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop")
	}
	select {
	case row := <-rows:
		t.Fatalf("quiet watch logged publication API %s/%s", row.Outcome, row.Reason)
	default:
	}
}
