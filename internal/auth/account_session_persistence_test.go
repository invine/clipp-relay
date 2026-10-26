package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type accountSessionWork struct{ tx *accountSessionTx }

func (w accountSessionWork) Start(context.Context) (persistence.Operation, error) {
	return accountSessionOperation{tx: w.tx}, nil
}

type accountSessionOperation struct{ tx *accountSessionTx }

func (o accountSessionOperation) Context() context.Context { return context.Background() }
func (o accountSessionOperation) Close()                   { o.tx.closed = true }
func (o accountSessionOperation) Begin(context.Context) (persistence.Transaction, error) {
	return o.tx, nil
}

type accountSessionTx struct {
	session    persistence.PortalSessionOwner
	lookupErr  error
	touched    bool
	deleted    bool
	committed  bool
	rolledBack bool
	closed     bool
}

func (*accountSessionTx) AccessToken(context.Context, [32]byte) (persistence.AccessToken, error) {
	return persistence.AccessToken{}, persistence.ErrInvalidCredential
}
func (*accountSessionTx) RelayCredential(context.Context, [32]byte, string, string) (persistence.RelayCredential, error) {
	return persistence.RelayCredential{}, persistence.ErrInvalidCredential
}
func (tx *accountSessionTx) Commit(context.Context) error   { tx.committed = true; return nil }
func (tx *accountSessionTx) Rollback(context.Context) error { tx.rolledBack = true; return nil }
func (*accountSessionTx) UpsertRelayAccount(context.Context, persistence.AccountRegistration) (persistence.RegisteredAccount, error) {
	return persistence.RegisteredAccount{}, nil
}
func (*accountSessionTx) AccountStateAfterUncertain(context.Context, string, string) (persistence.AccountStateAfterUncertain, error) {
	return persistence.AccountStateAfterUncertain{}, nil
}
func (*accountSessionTx) RecordAccountCreated(context.Context, string, string) error { return nil }
func (*accountSessionTx) CurrentRetainedUsageWeek(context.Context) (time.Time, error) {
	return time.Time{}, nil
}
func (*accountSessionTx) RetainedUsageVersions(context.Context) ([]uint64, error) { return nil, nil }
func (*accountSessionTx) RetainedUsageForIdentity(context.Context, uint64, [32]byte, time.Time) (persistence.RetainedUsageRecord, bool, error) {
	return persistence.RetainedUsageRecord{}, false, nil
}
func (*accountSessionTx) ImportRetainedUsage(context.Context, string, persistence.RetainedUsageRecord) error {
	return nil
}
func (*accountSessionTx) CreatePortalSession(context.Context, [32]byte, uint64, string, int64, [32]byte) error {
	return nil
}
func (tx *accountSessionTx) PortalSessionForOwner(context.Context, [32]byte, uint64) (persistence.PortalSessionOwner, error) {
	return tx.session, tx.lookupErr
}
func (tx *accountSessionTx) TouchPortalSession(context.Context, [32]byte, uint64, string, int64) (bool, error) {
	return tx.touched, nil
}
func (tx *accountSessionTx) DeletePortalSession(context.Context, [32]byte, uint64) (bool, error) {
	tx.deleted = true
	return true, nil
}

func TestPortalSessionValidationUsesRepositoryAndPreservesSessionIdentity(t *testing.T) {
	const pepperVersion = uint64(4)
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	pepper := []byte("test pepper with enough entropy")
	s := &Server{Peppers: map[uint64][]byte{pepperVersion: pepper}, accountRates: make(map[string]rate)}
	csrf := s.csrf(pepperVersion, credential)
	tx := &accountSessionTx{
		session: persistence.PortalSessionOwner{
			AccountID: "account-9", Status: "Active", Email: "owner@example.test", Subject: "provider-subject",
			Generation: 7, PepperVersion: pepperVersion,
			CSRFHash: s.digest(pepperVersion, "csrf-store", csrf),
		},
		touched: true,
	}
	s.persistence = accountSessionWork{tx: tx}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})

	accountID, gotVersion, status, email, gotCSRF, err := s.session(r)
	if err != nil {
		t.Fatal(err)
	}
	if accountID != "account-9" || gotVersion != pepperVersion || status != "Active" || email != "owner@example.test" || gotCSRF != csrf {
		t.Fatalf("portal session projection = %q/%d/%q/%q/%q", accountID, gotVersion, status, email, gotCSRF)
	}
	if !tx.committed || tx.rolledBack || !tx.closed {
		t.Fatalf("session transaction lifecycle: committed=%t rolledBack=%t closed=%t", tx.committed, tx.rolledBack, tx.closed)
	}
}

func TestExpiredPortalSessionDoesNotRenewAndReleasesOperation(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tx := &accountSessionTx{lookupErr: persistence.ErrInvalidCredential}
	s := &Server{Peppers: map[uint64][]byte{1: []byte("test pepper")}, persistence: accountSessionWork{tx: tx}}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})

	if _, _, _, _, _, err := s.session(r); !errors.Is(err, errInvalidSession) {
		t.Fatalf("expired session result = %v, want invalid session", err)
	}
	if tx.touched || tx.committed || !tx.rolledBack || !tx.closed {
		t.Fatalf("expired session lifecycle: touched=%t committed=%t rolledBack=%t closed=%t", tx.touched, tx.committed, tx.rolledBack, tx.closed)
	}
}

func TestPortalLogoutDeletesOnlyItsSessionThroughRepository(t *testing.T) {
	const pepperVersion = uint64(4)
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	pepper := []byte("test pepper with enough entropy")
	s := &Server{Origin: "https://portal.example.test", Peppers: map[uint64][]byte{pepperVersion: pepper}, accountRates: make(map[string]rate)}
	csrf := s.csrf(pepperVersion, credential)
	tx := &accountSessionTx{
		session: persistence.PortalSessionOwner{
			AccountID: "account-9", Status: "Active", Email: "owner@example.test", Subject: "provider-subject",
			Generation: 7, PepperVersion: pepperVersion,
			CSRFHash: s.digest(pepperVersion, "csrf-store", csrf),
		},
		touched: true,
	}
	s.persistence = accountSessionWork{tx: tx}
	r := httptest.NewRequest("POST", "/auth/logout", strings.NewReader("csrf="+url.QueryEscape(csrf)))
	r.Header.Set("Origin", s.Origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
	w := httptest.NewRecorder()

	s.logout(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if !tx.deleted || !tx.committed {
		t.Fatalf("portal-only logout persistence: deleted=%t committed=%t", tx.deleted, tx.committed)
	}
	if len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("logout did not expire the Portal Session cookie")
	}
}
