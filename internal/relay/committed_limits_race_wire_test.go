package relay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	relayclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
)

type committedPolicyFixture struct {
	mu           sync.Mutex
	weeklyBytes  int64
	sessionLimit int
}

func (p *committedPolicyFixture) ProbeDatabaseTime(context.Context) (time.Time, error) {
	return time.Now(), nil
}
func (p *committedPolicyFixture) ReadAccountingEligibility(context.Context, string) (persistence.AccountingEligibility, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return persistence.AccountingEligibility{DatabaseNow: time.Now(), CapBytes: p.weeklyBytes, SessionLimit: p.sessionLimit}, nil
}
func (*committedPolicyFixture) ReadRecordedUsage(context.Context, string, time.Time) (persistence.RecordedUsage, error) {
	return persistence.RecordedUsage{}, nil
}
func (*committedPolicyFixture) StartSettlement(context.Context) (persistence.AccountingOperation, error) {
	return nil, errors.New("settlement is not used in this test")
}
func (p *committedPolicyFixture) commitLimits(weeklyBytes int64, sessionLimit int) {
	p.mu.Lock()
	p.weeklyBytes, p.sessionLimit = weeklyBytes, sessionLimit
	p.mu.Unlock()
}

func TestCommittedCapRacingFinalAdmissionRetiresAllBeforeAcknowledgment(t *testing.T) {
	store := &committedPolicyFixture{weeklyBytes: 1_000, sessionLimit: 5}
	accounting := quota.New(store)
	defer accounting.Close()
	server, err := New(&validationOnlyAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	reconcileAttempted := make(chan struct{})
	accounting.SetSessionReconciler(func(id string, decide func(int) (bool, string)) error {
		close(reconcileAttempted)
		return server.ReconcileAccountSessions(id, decide)
	})
	clients := make([]host.Host, 4)
	for i := range clients {
		clients[i], err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, clientHost := range clients {
		if err := clientHost.Connect(ctx, peer.AddrInfo{ID: server.Host.ID(), Addrs: server.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if response := authResponse(t, ctx, clients[i], server.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
			t.Fatalf("initial Relay Authentication %d=%s", i, response)
		}
	}
	closeStarted, releaseClose := make(chan struct{}), make(chan struct{})
	var releaseOnce, closeStartedOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer release()
	server.sessionManager.physicalClose = func(conn network.Conn) {
		closeStartedOnce.Do(func() { close(closeStarted) })
		<-releaseClose
		_ = conn.Close()
	}

	admissionValidated, finishAdmission := make(chan struct{}), make(chan struct{})
	var pauseOnce sync.Once
	accounting.SetAdmissionInstallBoundaryForTest(func() {
		pauseOnce.Do(func() { close(admissionValidated); <-finishAdmission })
	})
	admissionResponse := make(chan string, 1)
	go func() { admissionResponse <- authResponse(t, ctx, clients[3], server.Host.ID(), "authorized") }()
	select {
	case <-admissionValidated:
	case <-ctx.Done():
		t.Fatal("fourth authentication did not reach final admission")
	}

	// The administrator transaction has committed before local reconciliation
	// starts; changing this fixture models that durable commit boundary.
	store.commitLimits(1_000, 3)
	application := make(chan error, 1)
	go func() {
		_, applyErr := accounting.ApplyCommittedLimits("relay-account", 1_000, 3)
		application <- applyErr
	}()
	select {
	case <-reconcileAttempted:
	case <-ctx.Done():
		t.Fatal("cap edit did not reach Relay reconciliation")
	}

	close(finishAdmission)
	select {
	case response := <-admissionResponse:
		if !strings.Contains(response, `"ok":true`) {
			t.Fatalf("admission that held the registry lock should install first: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("winning admission did not finish")
	}
	select {
	case err := <-application:
		if err != nil {
			t.Fatalf("committed cap application: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cap application did not finish after admission released the registry lock")
	}
	if got := server.AccountSessions("relay-account"); got != 0 {
		t.Fatalf("cap application acknowledged with %d authorized sessions, want all four retired", got)
	}
	if got, known := accounting.LiveSessionCount("relay-account"); !known || got != 0 {
		t.Fatalf("Quota count after retirement=(%d,%v), want known zero", got, known)
	}
	select {
	case <-closeStarted:
		t.Fatal("physical close began before the Auth completion phase")
	default:
	}
	if _, err := relayclient.Reserve(ctx, clients[0], peer.AddrInfo{ID: server.Host.ID(), Addrs: server.Host.Addrs()}); err == nil {
		t.Fatal("Relay accepted a reservation after logical retirement while physical close was deferred")
	}
	if err := server.CompleteAccountInvalidation("relay-account"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
	case <-ctx.Done():
		t.Fatal("physical close did not start after Auth completion")
	}
}

func TestCommittedCapWinsAgainstCompletedAssessmentBeforeFinalAdmission(t *testing.T) {
	store := &committedPolicyFixture{weeklyBytes: 1_000, sessionLimit: 5}
	accounting := quota.New(store)
	defer accounting.Close()
	server, err := New(&validationOnlyAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	accounting.SetSessionReconciler(server.ReconcileAccountSessions)
	clients := make([]host.Host, 5)
	for i := range clients {
		clients[i], err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, clientHost := range clients {
		if err := clientHost.Connect(ctx, peer.AddrInfo{ID: server.Host.ID(), Addrs: server.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if response := authResponse(t, ctx, clients[i], server.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
			t.Fatalf("initial Relay Authentication %d=%s", i, response)
		}
	}

	assessmentCaptured, releaseAssessment := make(chan struct{}), make(chan struct{})
	var pauseOnce sync.Once
	accounting.SetAssessmentReturnBoundaryForTest(func() {
		pauseOnce.Do(func() { close(assessmentCaptured); <-releaseAssessment })
	})
	staleResponse := make(chan string, 1)
	go func() { staleResponse <- authResponse(t, ctx, clients[3], server.Host.ID(), "authorized") }()
	select {
	case <-assessmentCaptured:
	case <-ctx.Done():
		t.Fatal("fourth authentication did not capture its cap-five assessment")
	}

	store.commitLimits(1_000, 3)
	if reason, err := accounting.ApplyCommittedLimits("relay-account", 1_000, 3); err != nil || reason != "" {
		t.Fatalf("cap equal to occupancy reason=%q err=%v", reason, err)
	}
	close(releaseAssessment)
	select {
	case response := <-staleResponse:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("stale cap-five assessment response=%s", response)
		}
	case <-ctx.Done():
		t.Fatal("stale assessment did not finish after cap application")
	}
	if got := server.AccountSessions("relay-account"); got != 3 {
		t.Fatalf("stale assessment changed the three existing sessions: count=%d", got)
	}
	if _, err := relayclient.Reserve(ctx, clients[0], peer.AddrInfo{ID: server.Host.ID(), Addrs: server.Host.Addrs()}); err != nil {
		t.Fatalf("existing Relay Session lost permission at exact cap: %v", err)
	}
	if response := authResponse(t, ctx, clients[4], server.Host.ID(), "authorized"); !strings.Contains(response, `"code":"session_limit_exceeded"`) {
		t.Fatalf("fresh assessment at cap response=%s", response)
	}
}
