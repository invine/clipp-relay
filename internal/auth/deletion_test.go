package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	"github.com/jackc/pgx/v5"
)

type deletionJournalFixture struct {
	mu                 sync.Mutex
	head               auth.JournalHead
	etag               string
	events             map[string][]byte
	failHeadOnce       bool
	failBeforeHeadOnce bool
	blockHead          <-chan struct{}
	headEntered        chan<- struct{}
}

func (j *deletionJournalFixture) ReadHead(context.Context) (auth.JournalHead, string, error) {
	j.mu.Lock()
	head, etag, block, entered := j.head, j.etag, j.blockHead, j.headEntered
	j.mu.Unlock()
	if block != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
	}
	return head, etag, nil
}
func (j *deletionJournalFixture) CreateEvent(_ context.Context, key string, data []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.events[key]; ok {
		return errors.New("exists")
	}
	j.events[key] = append([]byte(nil), data...)
	return nil
}
func (j *deletionJournalFixture) ReadEvent(_ context.Context, key string) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	b, ok := j.events[key]
	if !ok {
		return nil, errors.New("missing")
	}
	return append([]byte(nil), b...), nil
}
func (j *deletionJournalFixture) ListEvents(_ context.Context, cursor string) ([]string, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	names := make([]string, 0, len(j.events))
	for key := range j.events {
		name := "journal/" + key
		if cursor == "" || name >= cursor {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 250 {
		return names[:250], names[250], nil
	}
	return names, "", nil
}
func (j *deletionJournalFixture) ReplaceHead(_ context.Context, old string, head auth.JournalHead) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if old != j.etag {
		return errors.New("condition failed")
	}
	if j.failBeforeHeadOnce {
		j.failBeforeHeadOnce = false
		return errors.New("head unavailable")
	}
	j.head = head
	j.etag = strconv.Itoa(int(head.Sequence))
	if j.failHeadOnce {
		j.failHeadOnce = false
		return errors.New("lost acknowledgment")
	}
	return nil
}

func TestDeletionUploadAloneCannotCompleteOrFreeIdentity(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}, failBeforeHeadOnce: true}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	subject := "upload-only-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "upload@example.test", "")
	csrf := csrfFrom(t, s, owner)
	out := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin)
	if out.Code != http.StatusAccepted {
		t.Fatalf("admission %d", out.Code)
	}
	if err := s.ReconcileOneDeletion(context.Background()); err == nil {
		t.Fatal("uncommitted head accepted")
	}
	if got := portalRequest(s, "GET", out.Header().Get("Location"), owner, nil, ""); got.Code != http.StatusAccepted {
		t.Fatal("upload alone reported completion")
	}
	state, binding := start(t, s, nil)
	if got := complete(s, state, binding); got.Code == http.StatusSeeOther {
		t.Fatal("upload alone freed identity")
	}
	j.mu.Lock()
	stoppedAfterUpload := j.head.Sequence == 0 && len(j.events) == 1
	var eventKey string
	var original []byte
	for key, data := range j.events {
		eventKey = key
		original = append([]byte(nil), data...)
		if strings.Contains(string(data), subject) || strings.Contains(string(data), "upload@example.test") {
			stoppedAfterUpload = false
		}
	}
	j.events[eventKey] = []byte("conflicting event")
	j.mu.Unlock()
	if !stoppedAfterUpload {
		t.Fatal("upload-only fixture did not stop or leaked identity")
	}
	time.Sleep(time.Second)
	if err := s.ReconcileOneDeletion(context.Background()); err == nil {
		t.Fatal("conflicting event accepted")
	}
	if got := portalRequest(s, "GET", out.Header().Get("Location"), owner, nil, ""); got.Code != http.StatusAccepted {
		t.Fatal("conflict freed identity")
	}
	j.mu.Lock()
	j.events[eventKey] = original
	j.mu.Unlock()
	time.Sleep(2 * time.Second)
	if err := s.ReconcileOneDeletion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := portalRequest(s, "GET", out.Header().Get("Location"), owner, nil, ""); got.Code != http.StatusOK {
		t.Fatal("retry did not finalize")
	}
}

func TestDeletionRejectsWrongJournalAndStaleGoogleAuth(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "unexpected", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "expected", 0, zero)
	subject := "wrong-journal-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "wrong@example.test", "")
	csrf := csrfFrom(t, s, owner)
	form := url.Values{"csrf": {csrf}}
	if got := portalRequest(s, "POST", "/auth/delete", owner, form, c.PortalOrigin); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("wrong journal %d", got.Code)
	}
	j.mu.Lock()
	j.head.RepositoryID = "expected"
	j.mu.Unlock()
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, subject); err != nil {
		t.Fatal(err)
	}
	if got := portalRequest(s, "POST", "/auth/delete", owner, form, c.PortalOrigin); got.Code != http.StatusForbidden {
		t.Fatalf("stale auth %d", got.Code)
	}
	var pending int
	if err := db.Pool.QueryRow(context.Background(), `SELECT pending FROM public.deletion_capacity`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("stale action consumed capacity %d %v", pending, err)
	}
}

func TestOwnerDeletionAllAccountStatesAndCapacityGate(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	for _, state := range []string{"Pending", "Active", "Suspended", "Denied"} {
		t.Run(state, func(t *testing.T) {
			subject := "state-" + state + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			owner := loginAs(t, s, d, subject, "state@example.test", "")
			if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status=$2,plan_id=CASE WHEN $2='Active' THEN '6dd09395-51a0-451c-96b3-716e6038e870'::uuid ELSE NULL END WHERE subject=$1`, subject, state); err != nil {
				t.Fatal(err)
			}
			csrf := csrfFrom(t, s, owner)
			if state == "Pending" {
				if _, err := db.Pool.Exec(context.Background(), `UPDATE public.deletion_capacity SET pending=1024 WHERE singleton=true`); err != nil {
					t.Fatal(err)
				}
				if got := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin); got.Code != http.StatusServiceUnavailable {
					t.Fatalf("capacity gate: %d", got.Code)
				}
				if _, err := db.Pool.Exec(context.Background(), `UPDATE public.deletion_capacity SET pending=0 WHERE singleton=true`); err != nil {
					t.Fatal(err)
				}
			}
			out := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin)
			if out.Code != http.StatusAccepted {
				t.Fatalf("admission: %d", out.Code)
			}
			if err := s.ReconcileOneDeletion(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := portalRequest(s, "GET", out.Header().Get("Location"), owner, nil, ""); got.Code != http.StatusOK {
				t.Fatalf("completion: %d", got.Code)
			}
			if state == "Denied" {
				fresh := loginAs(t, s, d, subject, "state-new@example.test", "")
				if got := portalRequest(s, "GET", "/", fresh, nil, ""); got.Code != http.StatusOK || !containsPending(got.Body.String()) {
					t.Fatal("Denied account did not re-register Pending")
				}
			}
		})
	}
}

func TestAmbiguousDeletionAdmissionRollbackAllowsFreshLogin(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	subject := "rollback-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "rollback@example.test", "")
	s.SetAccountCommitHookForTest(func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
		return errors.New("admission response uncertain")
	})
	csrf := csrfFrom(t, s, owner)
	if got := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin); got.Code == http.StatusAccepted {
		t.Fatal("rolled-back admission accepted")
	}
	s.SetAccountCommitHookForTest(nil)
	if fresh := loginAs(t, s, d, subject, "rollback@example.test", ""); fresh == nil {
		t.Fatal("rolled-back admission fenced fresh login")
	}
	var pending int
	if err := db.Pool.QueryRow(context.Background(), `SELECT pending FROM public.deletion_capacity WHERE singleton=true`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("rolled-back admission changed capacity: %d %v", pending, err)
	}
}

func TestDeletionReleasesDatabaseLockDuringJournalIO(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	subject := "unlocked-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "unlocked@example.test", "")
	csrf := csrfFrom(t, s, owner)
	if got := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin); got.Code != http.StatusAccepted {
		t.Fatalf("admission: %d", got.Code)
	}
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	j.mu.Lock()
	j.blockHead, j.headEntered = block, entered
	j.mu.Unlock()
	finished := make(chan error, 1)
	go func() { finished <- s.ReconcileOneDeletion(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(block)
		t.Fatal("worker did not enter journal IO")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := db.Pool.Begin(ctx)
	if err == nil {
		var id string
		err = tx.QueryRow(ctx, `SELECT id FROM public.accounts WHERE subject=$1 FOR UPDATE`, subject).Scan(&id)
		_ = tx.Rollback(ctx)
	}
	close(block)
	if err != nil {
		t.Fatalf("database account lock held across journal IO: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestCommittedDeletionRecoversAfterHeadAdvancesAndValidatesCoverage(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	first := true
	s.SetDeletionFinalizeCommitHookForTest(func(ctx context.Context, tx pgx.Tx) error {
		if first {
			first = false
			return errors.New("finalization interrupted")
		}
		return tx.Commit(ctx)
	})
	for i := 0; i < 2; i++ {
		subject := "advanced-head-" + strconv.Itoa(i) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		owner := loginAs(t, s, d, subject, "advanced@example.test", "")
		csrf := csrfFrom(t, s, owner)
		if got := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin); got.Code != http.StatusAccepted {
			t.Fatalf("admission %d: %d", i, got.Code)
		}
		if err := s.ReconcileOneDeletion(context.Background()); (i == 0 && err == nil) || (i == 1 && err != nil) {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	j.mu.Lock()
	advanced := j.head.Sequence == 2
	j.mu.Unlock()
	if !advanced {
		t.Fatal("second deletion did not advance head")
	}
	time.Sleep(time.Second)
	if err := s.ReconcileOneDeletion(context.Background()); err != nil {
		t.Fatalf("earlier committed deletion did not recover: %v", err)
	}
	if err := s.ValidateDeletionJournal(context.Background()); err != nil {
		t.Fatalf("complete committed chain rejected: %v", err)
	}
	j.mu.Lock()
	var removedKey string
	var original []byte
	for key, data := range j.events {
		removedKey = key
		original = append([]byte(nil), data...)
		delete(j.events, key)
		break
	}
	j.mu.Unlock()
	if err := s.ValidateDeletionJournal(context.Background()); err == nil {
		t.Fatal("startup accepted missing committed event")
	}
	j.mu.Lock()
	j.events[removedKey] = []byte("conflicting event")
	j.mu.Unlock()
	if err := s.ValidateDeletionJournal(context.Background()); err == nil {
		t.Fatal("startup accepted conflicting committed event")
	}
	j.mu.Lock()
	j.events[removedKey] = original
	j.mu.Unlock()
}

func TestOwnerDeletionJournalReconciliationAndFreshPendingAccount(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: "0000000000000000000000000000000000000000000000000000000000000000", Sequence: 0, Hash: "0000000000000000000000000000000000000000000000000000000000000000", Format: 1}, etag: "initial", events: map[string][]byte{}, failHeadOnce: true}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, j.head.CoverageHash)
	s.SetDeletionFinalizeCommitHookForTest(func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost finalization acknowledgement")
	})
	subject := "delete-owner-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "delete@example.test", "")
	var oldID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes) VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,12345,1,'11111111-1111-4111-8111-111111111111',12345)`, oldID); err != nil {
		t.Fatal(err)
	}
	csrf := csrfFrom(t, s, owner)
	out := portalRequest(s, "POST", "/auth/delete", owner, url.Values{"csrf": {csrf}}, c.PortalOrigin)
	if out.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", out.Code, out.Body.String())
	}
	if signals, err := s.SampleDeletionSignals(context.Background()); err != nil || signals.Pending != 1 {
		t.Fatalf("pending signals %+v %v", signals, err)
	}
	statusURL := out.Header().Get("Location")
	if got := portalRequest(s, "GET", statusURL, owner, nil, ""); got.Code != http.StatusAccepted {
		t.Fatalf("pending status %d", got.Code)
	}
	if got := portalRequest(s, "GET", "/", owner, nil, ""); got.Code != 200 || !containsSignedOut(got.Body.String()) {
		t.Fatal("old portal session survived deletion")
	}
	state, binding := start(t, s, nil)
	if got := complete(s, state, binding); got.Code == http.StatusSeeOther {
		t.Fatal("pending deletion permitted re-registration")
	}
	if err := s.ReconcileOneDeletion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if signals, err := s.SampleDeletionSignals(context.Background()); err != nil || signals.Pending != 0 || signals.Completed != 1 {
		t.Fatalf("completed signals %+v %v", signals, err)
	}
	if got := portalRequest(s, "GET", statusURL, owner, nil, ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "complete") {
		t.Fatal("completion status missing")
	}
	fresh := loginAs(t, s, d, subject, "fresh@example.test", "")
	if got := portalRequest(s, "GET", "/", fresh, nil, ""); got.Code != 200 || !containsPending(got.Body.String()) {
		t.Fatal("fresh account was not Pending")
	}
	var newID string
	var committed int64
	var sequence int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT a.id,u.committed_bytes,u.sequence FROM public.accounts a JOIN public.weekly_quota_usage u ON u.account_id=a.id AND u.week_start=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date WHERE a.subject=$1`, subject).Scan(&newID, &committed, &sequence); err != nil || newID == oldID || committed != 12345 || sequence != 0 {
		t.Fatalf("retained usage import: %s %d %d %v", newID, committed, sequence, err)
	}
	var remaining int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.retained_quota_usage WHERE week_start=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("retained usage not consumed: %d %v", remaining, err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, newID); err != nil {
		t.Fatal(err)
	}
	credit := quota.New(db.Pool)
	defer credit.Close()
	result, err := credit.Ensure(context.Background(), newID, 0)
	if err != nil || result.Committed != 12345+quota.BlockBytes {
		t.Fatalf("new plan funding after import: %+v %v", result, err)
	}
}

func containsSignedOut(body string) bool { return strings.Contains(body, "Sign in with Google") }
func containsPending(body string) bool   { return strings.Contains(body, "waiting for approval") }
