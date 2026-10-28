package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type deletionAdmissionTxFixture struct {
	persistence.DeletionTransaction
	ownerSubject string
	admitErr     error
	admission    persistence.DeletionAdmission
	commitErr    error
}

func (tx deletionAdmissionTxFixture) Commit(context.Context) error   { return tx.commitErr }
func (tx deletionAdmissionTxFixture) Rollback(context.Context) error { return nil }
func (tx deletionAdmissionTxFixture) DeletionOwner(context.Context, string) (persistence.DeletionOwner, error) {
	return persistence.DeletionOwner{Subject: tx.ownerSubject}, nil
}
func (tx deletionAdmissionTxFixture) AdmitDeletion(context.Context, persistence.DeletionAdmissionRequest) (persistence.DeletionAdmission, error) {
	return tx.admission, tx.admitErr
}

type deletionAdmissionRepositoryFixture struct {
	persistence.DeletionRepository
	tx persistence.DeletionTransaction
}

func (r deletionAdmissionRepositoryFixture) BeginDeletion(context.Context) (persistence.DeletionTransaction, error) {
	return r.tx, nil
}

type deletionAdmissionJournalFixture struct{ head JournalHead }

func (j deletionAdmissionJournalFixture) ReadHead(context.Context) (JournalHead, string, error) {
	return j.head, "etag", nil
}
func (deletionAdmissionJournalFixture) CreateEvent(context.Context, string, []byte) error { return nil }
func (deletionAdmissionJournalFixture) ReadEvent(context.Context, string) ([]byte, error) {
	return nil, errors.New("not found")
}
func (deletionAdmissionJournalFixture) ReplaceHead(context.Context, string, JournalHead) error {
	return nil
}
func (deletionAdmissionJournalFixture) ListEvents(context.Context, string) ([]string, string, error) {
	return nil, "", nil
}

type blockingDeletionWriter struct {
	header  http.Header
	entered chan struct{}
	release <-chan struct{}
}

func (w *blockingDeletionWriter) Header() http.Header { return w.header }
func (w *blockingDeletionWriter) WriteHeader(int)     {}
func (w *blockingDeletionWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return io.Discard.Write(p)
}

func deletionAdmissionServerFixture(subject string, admitErr error) (*Server, *http.Cookie, string) {
	const pepperVersion = uint64(1)
	const credential = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	pepper := []byte("ticket 07 test pepper with entropy")
	s := &Server{
		Origin:        "https://portal.example.test",
		Peppers:       map[uint64][]byte{pepperVersion: pepper},
		CurrentPepper: pepperVersion,
		accountRates:  make(map[string]rate),
		fences:        make(map[[32]byte]identityFence),
		flows:         make(map[string]flow),
		consents:      make(map[string]oauthConsent),
	}
	csrf := s.csrf(pepperVersion, credential)
	s.persistence = accountSessionWork{tx: &accountSessionTx{
		session: persistence.PortalSessionOwner{
			AccountID: "account-07", Status: "Active", Subject: subject,
			Generation: 1, PepperVersion: pepperVersion,
			CSRFHash: s.digest(pepperVersion, "csrf-store", csrf),
		},
		touched: true,
	}}
	s.SetAccountChangedForTest(func(AccountChange) string { return "" })
	s.deletions = deletionAdmissionRepositoryFixture{tx: deletionAdmissionTxFixture{
		ownerSubject: subject,
		admitErr:     admitErr,
		admission:    persistence.DeletionAdmission{Subject: subject, WeeklyBytes: 12, Sessions: 2},
	}}
	zero := strings.Repeat("0", 64)
	s.SetDeletionJournal(deletionAdmissionJournalFixture{head: JournalHead{
		RepositoryID: "fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0,
		Hash: zero, Format: 1,
	}}, "fixture", 0, zero)
	return s, &http.Cookie{Name: sessionCookie, Value: credential}, csrf
}

func TestDeletionRefusalResponseDoesNotHoldIdentityProtection(t *testing.T) {
	const subject = "deletion-owner"
	s, cookie, csrf := deletionAdmissionServerFixture(subject, persistence.ErrCapacityExceeded)

	form := url.Values{"csrf": {csrf}}
	r := httptest.NewRequest(http.MethodPost, "/auth/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", s.Origin)
	r.AddCookie(cookie)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	w := &blockingDeletionWriter{header: make(http.Header), entered: entered, release: release}
	handlerDone := make(chan struct{})
	go func() {
		s.deleteOwner(w, r)
		close(handlerDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		<-handlerDone
		t.Fatal("deletion refusal was not rendered")
	}

	mutationDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		mutationDone <- s.withIdentityGuard(ctx, subject, func(context.Context) error { return nil })
	}()
	var mutationErr error
	completedBeforeResponse := true
	select {
	case mutationErr = <-mutationDone:
	case <-time.After(100 * time.Millisecond):
		completedBeforeResponse = false
	}
	close(release)
	<-handlerDone
	if !completedBeforeResponse {
		t.Fatal("second protected mutation waited on refusal response")
	}
	if mutationErr != nil {
		t.Fatalf("second protected mutation waited on refusal response: %v", mutationErr)
	}
}

func TestDeletionAdmissionResponseFollowsProtectionRelease(t *testing.T) {
	const subject = "deletion-owner-success"
	s, cookie, csrf := deletionAdmissionServerFixture(subject, nil)
	s.SetAccountInvalidationCompleteForTest(func(string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		return s.withIdentityGuard(ctx, subject, func(context.Context) error { return nil })
	})
	form := url.Values{"csrf": {csrf}}
	r := httptest.NewRequest(http.MethodPost, "/auth/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", s.Origin)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.deleteOwner(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("admitted deletion status = %d, want %d: %s", w.Code, http.StatusAccepted, w.Body.String())
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/auth/deletion/") || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("pending deletion location/retry = %q/%q", location, w.Header().Get("Retry-After"))
	}
	if cookies := w.Result().Cookies(); len(cookies) == 0 || cookies[0].MaxAge >= 0 {
		t.Fatal("admitted deletion did not expire the Portal Session cookie")
	}
}

func TestDeletionAdmissionClassifiesUncertainAndIncompleteInvalidation(t *testing.T) {
	t.Run("uncertain commit", func(t *testing.T) {
		const subject = "deletion-owner-uncertain"
		s, cookie, csrf := deletionAdmissionServerFixture(subject, nil)
		s.commitDeletionAdmission = func(context.Context, persistence.DeletionTransaction) error {
			return persistence.ErrUncertainCommit
		}
		form := url.Values{"csrf": {csrf}}
		r := httptest.NewRequest(http.MethodPost, "/auth/delete", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", s.Origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.deleteOwner(w, r)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "could not be confirmed") {
			t.Fatalf("uncertain admission response = %d %q", w.Code, w.Body.String())
		}
		if cookies := w.Result().Cookies(); len(cookies) == 0 || cookies[0].MaxAge >= 0 {
			t.Fatal("uncertain admission did not expire the Portal Session cookie")
		}
	})

	t.Run("committed but incomplete invalidation", func(t *testing.T) {
		const subject = "deletion-owner-incomplete"
		s, cookie, csrf := deletionAdmissionServerFixture(subject, nil)
		s.SetAccountInvalidationCompleteForTest(func(string) error { return errors.New("physical cleanup delayed") })
		form := url.Values{"csrf": {csrf}}
		r := httptest.NewRequest(http.MethodPost, "/auth/delete", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", s.Origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.deleteOwner(w, r)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "invalidation could not be confirmed") {
			t.Fatalf("incomplete invalidation response = %d %q", w.Code, w.Body.String())
		}
		if cookies := w.Result().Cookies(); len(cookies) == 0 || cookies[0].MaxAge >= 0 {
			t.Fatal("incomplete invalidation did not expire the Portal Session cookie")
		}
	})
}
