package relay

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestCommittedLimitReconciliationRetiresPermissionBeforeDeferredClose(t *testing.T) {
	accounting := &admissionWireAccounting{counts: make(map[string]int), limit: 5}
	s, err := New(&validationOnlyAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	clientHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer clientHost.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := clientHost.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, clientHost, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("initial Relay Authentication=%s", response)
	}

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	var closeOnce sync.Once
	release := func() { closeOnce.Do(func() { close(releaseClose) }) }
	defer release()
	s.sessionManager.physicalClose = func(conn network.Conn) {
		close(closeStarted)
		<-releaseClose
		_ = conn.Close()
	}

	var exactCount int
	if err := s.ReconcileAccountSessions("relay-account", func(count int) (bool, string) {
		exactCount = count
		return count > 0, "policy_changed"
	}); err != nil {
		t.Fatal(err)
	}
	if exactCount != 1 {
		t.Fatalf("reconciliation observed %d sessions, want exact registry count 1", exactCount)
	}
	if got := s.AccountSessions("relay-account"); got != 0 {
		t.Fatalf("logical retirement left %d authorized sessions", got)
	}
	select {
	case <-closeStarted:
		t.Fatal("physical close began before Auth released protection")
	default:
	}
	if response := authResponse(t, ctx, clientHost, s.Host.ID(), "authorized"); strings.Contains(response, `"ok":true`) {
		t.Fatalf("retired connection regained permission before close: %s", response)
	}
	if err := s.CompleteAccountInvalidation("relay-account"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
	case <-ctx.Done():
		t.Fatal("physical close did not start after Auth released protection")
	}
	release()
}
