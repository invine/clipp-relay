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

type portalSessionOnlyWork struct {
	tx    persistence.PortalSessionTransaction
	close func()
}

func (w portalSessionOnlyWork) StartPortalSession(context.Context) (persistence.PortalSessionOperation, error) {
	return portalSessionOnlyOperation{tx: w.tx, close: w.close}, nil
}

type portalSessionOnlyOperation struct {
	tx    persistence.PortalSessionTransaction
	close func()
}

func (o portalSessionOnlyOperation) Context() context.Context { return context.Background() }
func (o portalSessionOnlyOperation) Close() {
	if o.close != nil {
		o.close()
	}
}
func (o portalSessionOnlyOperation) BeginPortalSession(context.Context) (persistence.PortalSessionTransaction, error) {
	return o.tx, nil
}

func portalSessionWorkForTest(tx *accountSessionTx) portalSessionOnlyWork {
	return portalSessionOnlyWork{tx: tx, close: func() { tx.closed = true }}
}

type accountSessionTx struct {
	session    persistence.PortalSessionOwner
	lookupErr  error
	lookups    []uint64
	touched    bool
	touchCalls int
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
func (tx *accountSessionTx) PortalSessionForOwner(_ context.Context, _ [32]byte, version uint64) (persistence.PortalSessionOwner, error) {
	tx.lookups = append(tx.lookups, version)
	if tx.lookupErr != nil {
		return persistence.PortalSessionOwner{}, tx.lookupErr
	}
	if tx.session.PepperVersion != version {
		return persistence.PortalSessionOwner{}, persistence.ErrInvalidCredential
	}
	return tx.session, nil
}
func (tx *accountSessionTx) TouchPortalSession(context.Context, [32]byte, uint64, string, int64) (bool, error) {
	tx.touchCalls++
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
	s.portalSessions = portalSessionWorkForTest(tx)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})

	ownerSession, err := s.ownerPortalSession(r)
	if err != nil {
		t.Fatal(err)
	}
	if ownerSession.accountID != "account-9" || ownerSession.pepperVersion != pepperVersion || ownerSession.status != "Active" || ownerSession.email != "owner@example.test" || ownerSession.csrf != csrf {
		t.Fatalf("portal session projection = %q/%d/%q/%q/%q", ownerSession.accountID, ownerSession.pepperVersion, ownerSession.status, ownerSession.email, ownerSession.csrf)
	}
	if !tx.committed || tx.rolledBack || !tx.closed {
		t.Fatalf("session transaction lifecycle: committed=%t rolledBack=%t closed=%t", tx.committed, tx.rolledBack, tx.closed)
	}
}

func TestExpiredPortalSessionDoesNotRenewAndReleasesOperation(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tx := &accountSessionTx{lookupErr: persistence.ErrInvalidCredential}
	s := &Server{Peppers: map[uint64][]byte{1: []byte("test pepper")}, portalSessions: portalSessionWorkForTest(tx)}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})

	if _, err := s.ownerPortalSession(r); !errors.Is(err, errInvalidSession) {
		t.Fatalf("expired session result = %v, want invalid session", err)
	}
	if tx.touched || tx.committed || !tx.rolledBack || !tx.closed {
		t.Fatalf("expired session lifecycle: touched=%t committed=%t rolledBack=%t closed=%t", tx.touched, tx.committed, tx.rolledBack, tx.closed)
	}
}

func TestPortalSessionRefusalDoesNotRenew(t *testing.T) {
	const pepperVersion = uint64(4)
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	pepper := []byte("test pepper with enough entropy")
	csrf := (&Server{Peppers: map[uint64][]byte{pepperVersion: pepper}}).csrf(pepperVersion, credential)

	t.Run("stored CSRF proof", func(t *testing.T) {
		tx := &accountSessionTx{
			session: persistence.PortalSessionOwner{
				AccountID: "account-9", Subject: "subject", Generation: 7, PepperVersion: pepperVersion,
				CSRFHash: []byte("wrong proof"),
			},
			touched: true,
		}
		s := &Server{Peppers: map[uint64][]byte{pepperVersion: pepper}, portalSessions: portalSessionWorkForTest(tx), accountRates: make(map[string]rate)}
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		if _, err := s.ownerPortalSession(r); err == nil || err.Error() != "csrf" {
			t.Fatalf("stored CSRF refusal = %v, want csrf error", err)
		}
		if tx.touchCalls != 0 || tx.committed || !tx.rolledBack || !tx.closed {
			t.Fatalf("stored CSRF refusal lifecycle: touches=%d committed=%t rolledBack=%t closed=%t", tx.touchCalls, tx.committed, tx.rolledBack, tx.closed)
		}
	})

	t.Run("uncertain account", func(t *testing.T) {
		tx := &accountSessionTx{
			session: persistence.PortalSessionOwner{
				AccountID: "account-9", Subject: "subject", Generation: 7, PepperVersion: pepperVersion,
				CSRFHash: (&Server{Peppers: map[uint64][]byte{pepperVersion: pepper}}).digest(pepperVersion, "csrf-store", csrf),
			},
			touched: true,
		}
		s := &Server{Peppers: map[uint64][]byte{pepperVersion: pepper}, portalSessions: portalSessionWorkForTest(tx), accountRates: make(map[string]rate)}
		s.uncertainAccounts.Store("account-9", uncertainMutation{})
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		if _, err := s.ownerPortalSession(r); !errors.Is(err, errInvalidSession) {
			t.Fatalf("uncertain account result = %v, want invalid session", err)
		}
		if tx.touchCalls != 0 || tx.committed || !tx.rolledBack || !tx.closed {
			t.Fatalf("uncertain account lifecycle: touches=%d committed=%t rolledBack=%t closed=%t", tx.touchCalls, tx.committed, tx.rolledBack, tx.closed)
		}
	})
}

func TestOwnerPortalSessionKeepsRotatedPepperAndRateRefusal(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	oldPepper := []byte("old test pepper with enough entropy")
	currentPepper := []byte("current test pepper with enough entropy")

	t.Run("retained pepper", func(t *testing.T) {
		const oldVersion, currentVersion = uint64(2), uint64(5)
		s := &Server{Peppers: map[uint64][]byte{oldVersion: oldPepper, currentVersion: currentPepper}, CurrentPepper: currentVersion, accountRates: make(map[string]rate)}
		csrf := s.csrf(oldVersion, credential)
		tx := &accountSessionTx{session: persistence.PortalSessionOwner{
			AccountID: "account-9", Subject: "subject", Generation: 7, PepperVersion: oldVersion,
			CSRFHash: s.digest(oldVersion, "csrf-store", csrf),
		}, touched: true}
		s.portalSessions = portalSessionWorkForTest(tx)
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		verified, err := s.ownerPortalSession(r)
		if err != nil {
			t.Fatal(err)
		}
		if verified.pepperVersion != oldVersion || verified.csrf != csrf || len(tx.lookups) < 1 {
			t.Fatalf("rotated pepper projection/lookups = %#v / %v", verified, tx.lookups)
		}
		if !tx.committed || tx.touchCalls != 1 {
			t.Fatalf("rotated pepper transaction: committed=%t touches=%d", tx.committed, tx.touchCalls)
		}
	})

	t.Run("account rate limit", func(t *testing.T) {
		const version = uint64(5)
		s := &Server{Peppers: map[uint64][]byte{version: currentPepper}, CurrentPepper: version, accountRates: make(map[string]rate)}
		csrf := s.csrf(version, credential)
		tx := &accountSessionTx{session: persistence.PortalSessionOwner{
			AccountID: "account-9", Subject: "subject", Generation: 7, PepperVersion: version,
			CSRFHash: s.digest(version, "csrf-store", csrf),
		}, touched: true}
		s.portalSessions = portalSessionWorkForTest(tx)
		rateKey := base64.RawURLEncoding.EncodeToString(s.digest(version, "account-rate", googleIssuer+"\x00subject"))
		s.accountRates[rateKey] = rate{tokens: 0, at: time.Now()}
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		if _, err := s.ownerPortalSession(r); !errors.Is(err, errRateLimit) {
			t.Fatalf("rate-limited session = %v, want rate limit", err)
		}
		if tx.touchCalls != 0 || tx.committed || !tx.rolledBack || !tx.closed {
			t.Fatalf("rate refusal lifecycle: touches=%d committed=%t rolledBack=%t closed=%t", tx.touchCalls, tx.committed, tx.rolledBack, tx.closed)
		}
	})
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
	s.portalSessions = portalSessionWorkForTest(tx)
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
