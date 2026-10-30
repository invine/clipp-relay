package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
)

type portalSessionCredential struct {
	value   string
	version uint64
	digest  [32]byte
}

type portalSessionProof struct {
	accountID, subject string
	version            uint64
	csrfHash           []byte
}

var errInvalidPortalSessionCSRF = errors.New("csrf")

type portalSessionOwnerReader interface {
	PortalSessionForOwner(context.Context, [32]byte, uint64) (persistence.PortalSessionOwner, error)
}

type portalSessionConsentReader interface {
	LockPortalSessionForConsent(context.Context, [32]byte, uint64, string, int64) (persistence.PortalSessionForConsent, error)
}

// verifyConsentPortalSessionInTransaction applies shared Portal Session policy
// to the row locked by the protected authorization transaction. The operation
// that owns this transaction still decides whether and when to renew it.
func (s *Server) verifyConsentPortalSessionInTransaction(ctx context.Context, tx portalSessionConsentReader, raw, accountID string, version uint64, generation int64) (string, error) {
	credential, err := s.portalSessionCredential(raw, version)
	if err != nil {
		return "", err
	}
	stored, err := tx.LockPortalSessionForConsent(ctx, credential.digest, credential.version, accountID, generation)
	if err != nil {
		return "", err
	}
	if stored.AccountID != accountID || stored.Generation != generation {
		return "", errInvalidSession
	}
	return s.verifyPortalSessionProof(credential, portalSessionProof{
		accountID: stored.AccountID, subject: stored.Subject,
		version: credential.version, csrfHash: stored.CSRFHash,
	}, false)
}

func (s *Server) portalSessionCredentials(value string) ([]portalSessionCredential, error) {
	if len(value) != 43 {
		return nil, errInvalidSession
	}
	credentials := make([]portalSessionCredential, 0, len(s.Peppers))
	for version := range s.Peppers {
		credentials = append(credentials, portalSessionCredential{
			value: value, version: version,
			digest: digest32(s.digest(version, "portal-session", value)),
		})
	}
	return credentials, nil
}

func (s *Server) portalSessionCredential(value string, version uint64) (portalSessionCredential, error) {
	if len(value) != 43 {
		return portalSessionCredential{}, errInvalidSession
	}
	if _, ok := s.Peppers[version]; !ok {
		return portalSessionCredential{}, errInvalidSession
	}
	return portalSessionCredential{value: value, version: version, digest: digest32(s.digest(version, "portal-session", value))}, nil
}

// verifyPortalSessionProof owns the shared credential policy used by ordinary
// owner requests and protected operations. Callers retain the account,
// administrator, consent-generation and transaction decisions around it.
func (s *Server) verifyPortalSessionProof(credential portalSessionCredential, stored portalSessionProof, checkRate bool) (string, error) {
	if stored.accountID == "" || stored.version != credential.version {
		return "", errInvalidSession
	}
	if _, uncertain := s.uncertainAccounts.Load(stored.accountID); uncertain {
		return "", errInvalidSession
	}
	if checkRate && !s.allowAccount(stored.subject) {
		return "", errRateLimit
	}
	csrf := s.csrf(stored.version, credential.value)
	if subtle.ConstantTimeCompare(stored.csrfHash, s.digest(stored.version, "csrf-store", csrf)) != 1 {
		return "", errInvalidPortalSessionCSRF
	}
	return csrf, nil
}

// verifyOwnerPortalSessionInTransaction reuses the caller's existing
// transaction to recheck Portal Session policy after a protected wait. It does
// not renew the session; only the ordinary owner operation owns renewal.
func (s *Server) verifyOwnerPortalSessionInTransaction(ctx context.Context, tx portalSessionOwnerReader, raw, accountID string, version uint64, generation int64) (string, error) {
	credential, err := s.portalSessionCredential(raw, version)
	if err != nil {
		return "", err
	}
	stored, err := tx.PortalSessionForOwner(ctx, credential.digest, credential.version)
	if err != nil {
		return "", err
	}
	if stored.AccountID != accountID || generation != 0 && stored.Generation != generation {
		return "", errInvalidSession
	}
	return s.verifyPortalSessionProof(credential, portalSessionProof{
		accountID: stored.AccountID, subject: stored.Subject,
		version: stored.PepperVersion, csrfHash: stored.CSRFHash,
	}, false)
}

// verifiedOwnerPortalSession is the complete projection ordinary owner portal
// requests need after the Portal Session operation has validated and renewed
// the credential.
type verifiedOwnerPortalSession struct {
	accountID, status, email, csrf string
	pepperVersion                  uint64
	generation                     int64
}

// ownerPortalSession owns ordinary browser credential parsing, pepper lookup,
// refusal policy, stored CSRF proof and idle renewal in one admitted operation.
func (s *Server) ownerPortalSession(r *http.Request) (verifiedOwnerPortalSession, error) {
	c, e := r.Cookie(sessionCookie)
	if e != nil {
		return verifiedOwnerPortalSession{}, errInvalidSession
	}
	op, e := s.portalSessions.StartPortalSession(r.Context())
	if e != nil {
		return verifiedOwnerPortalSession{}, e
	}
	defer op.Close()
	tx, e := op.BeginPortalSession(op.Context())
	if e != nil {
		return verifiedOwnerPortalSession{}, e
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(op.Context())
		}
	}()
	credentials, e := s.portalSessionCredentials(c.Value)
	if e != nil {
		return verifiedOwnerPortalSession{}, e
	}
	for _, credential := range credentials {
		stored, err := tx.PortalSessionForOwner(op.Context(), credential.digest, credential.version)
		if errors.Is(err, persistence.ErrInvalidCredential) {
			continue
		}
		if err != nil {
			return verifiedOwnerPortalSession{}, err
		}
		token, err := s.verifyPortalSessionProof(credential, portalSessionProof{
			accountID: stored.AccountID, subject: stored.Subject,
			version: stored.PepperVersion, csrfHash: stored.CSRFHash,
		}, true)
		if err != nil {
			return verifiedOwnerPortalSession{}, err
		}
		touched, err := tx.TouchPortalSession(op.Context(), credential.digest, credential.version, stored.AccountID, stored.Generation)
		if err != nil {
			return verifiedOwnerPortalSession{}, err
		}
		if !touched {
			return verifiedOwnerPortalSession{}, errInvalidSession
		}
		if err = tx.Commit(op.Context()); err != nil {
			finished = true
			return verifiedOwnerPortalSession{}, err
		}
		finished = true
		return verifiedOwnerPortalSession{
			accountID: stored.AccountID, pepperVersion: credential.version, status: stored.Status, email: stored.Email, csrf: token,
			generation: stored.Generation,
		}, nil
	}
	if e = tx.Rollback(op.Context()); e != nil {
		finished = true
		return verifiedOwnerPortalSession{}, e
	}
	finished = true
	return verifiedOwnerPortalSession{}, errInvalidSession
}
