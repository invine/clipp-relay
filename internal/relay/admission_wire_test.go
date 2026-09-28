package relay

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/database"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

type budgetAuthority struct {
	db                   *database.Runtime
	calls                atomic.Int64
	failAfterWork        atomic.Bool
	failAccountAfterWork atomic.Bool
}

func (a *budgetAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	err := a.db.WithUnit(ctx, func(unitCtx context.Context) error {
		work(unitCtx)
		return nil
	})
	return err == nil && !a.failAfterWork.Load()
}

func TestRelayRemovesSessionWhenOuterUnitFailsAfterInstall(t *testing.T) {
	for _, stage := range []string{"outer", "account"} {
		t.Run(stage, func(t *testing.T) {
			testRelayRemovesLateAdmission(t, stage)
		})
	}
}

func testRelayRemovesLateAdmission(t *testing.T, stage string) {
	db := database.NewRuntime(nil)
	a := &budgetAuthority{db: db}
	a.failAfterWork.Store(stage == "outer")
	a.failAccountAfterWork.Store(stage == "account")
	s, err := New(a, budgetCredit{db: db}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	st, err := h.NewStream(ctx, s.Host.ID(), AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Write(frame(`{"accessToken":"authorized"}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = st.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.Copy(io.Discard, st)
	until := time.Now().Add(time.Second)
	for a.calls.Load() < 2 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.calls.Load() != 2 || s.AccountSessions("relay-account") != 0 {
		t.Fatalf("failed outer unit left a relay session: calls=%d sessions=%d", a.calls.Load(), s.AccountSessions("relay-account"))
	}
}

func (a *budgetAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	err := a.db.WithUnit(ctx, func(unitCtx context.Context) error {
		work(unitCtx)
		return nil
	})
	return err == nil && !a.failAccountAfterWork.Load()
}

func (a *budgetAuthority) AuthenticateRelay(ctx context.Context, raw string) (auth.RelayCredential, error) {
	var credential auth.RelayCredential
	err := a.db.WithUnit(ctx, func(context.Context) error {
		a.calls.Add(1)
		if raw != "authorized" {
			return auth.ErrInvalidAccess
		}
		credential = auth.RelayCredential{AccountID: "relay-account", SessionLimit: 2, ExpiresAt: time.Now().Add(5 * time.Minute)}
		return nil
	})
	return credential, err
}

type budgetCredit struct{ db *database.Runtime }

func (c budgetCredit) Ensure(ctx context.Context, _ string, _ int64) (quota.Result, error) {
	return quota.Result{}, c.db.WithUnit(ctx, func(context.Context) error { return nil })
}

func (c budgetCredit) Take(ctx context.Context, _ string, _, _ int64) (quota.Result, error) {
	return c.Ensure(ctx, "", 0)
}

func TestRelayAuthAdmitsBeforePeerGuardAndReusesOneUnit(t *testing.T) {
	db := database.NewRuntime(nil)
	a := &budgetAuthority{db: db}
	s, err := New(a, budgetCredit{db: db}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	var workers sync.WaitGroup
	startUnit := func() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_ = db.WithUnit(context.Background(), func(context.Context) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}
	releaseUnits := sync.OnceFunc(func() { close(release) })
	defer func() { releaseUnits(); workers.Wait() }()
	for range 63 {
		startUnit()
	}
	for range 63 {
		<-entered
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("relay path reacquired a second unit: %s", response)
	}
	startUnit()
	<-entered
	before := a.calls.Load()
	guard := &s.peerGuards[peerGuardIndex(h.ID())]
	guard.Lock()
	unlocked := make(chan struct{})
	time.AfterFunc(700*time.Millisecond, func() { guard.Unlock(); close(unlocked) })
	start := time.Now()
	response := authResponse(t, ctx, h, s.Host.ID(), "authorized")
	if !strings.Contains(response, "temporarily_unavailable") || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("overload queued on the peer guard: %s after %s", response, time.Since(start))
	}
	if a.calls.Load() != before {
		t.Fatal("credential read ran after budget overflow")
	}
	<-unlocked
	releaseUnits()
	workers.Wait()
	time.Sleep(1100 * time.Millisecond) // replenish the per-connection auth rate bucket
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("relay admission did not recover: %s", response)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("relay admission exceeded test deadline")
	}
}
