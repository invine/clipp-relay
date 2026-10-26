package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
)

func digest32(value []byte) [32]byte {
	var digest [32]byte
	copy(digest[:], value)
	return digest
}

type authorizationPortalSession struct {
	account, subject, csrf string
	version                uint64
}

func authorizationTransaction(tx persistence.Transaction) (persistence.AuthorizationTransaction, error) {
	authorizationTx, ok := tx.(persistence.AuthorizationTransaction)
	if !ok {
		return nil, errors.New("authorization repositories are unavailable")
	}
	return authorizationTx, nil
}

// authorizationPortalSession performs the small Portal Session read used by
// native authorization through an admitted, transaction-scoped projection.
// It validates CSRF before extending the idle deadline, matching session().
func (s *Server) authorizationPortalSession(r *http.Request) (authorizationPortalSession, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != 43 {
		return authorizationPortalSession{}, errInvalidSession
	}
	for version := range s.Peppers {
		digest := digest32(s.digest(version, "portal-session", cookie.Value))
		op, err := s.persistence.Start(r.Context())
		if err != nil {
			return authorizationPortalSession{}, err
		}
		tx, err := op.Begin(op.Context())
		if err != nil {
			op.Close()
			return authorizationPortalSession{}, err
		}
		defer tx.Rollback(op.Context())
		authorizationTx, err := authorizationTransaction(tx)
		if err != nil {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, err
		}
		stored, err := authorizationTx.PortalSessionForAuthorization(op.Context(), digest, version)
		if errors.Is(err, persistence.ErrInvalidCredential) {
			_ = tx.Rollback(op.Context())
			op.Close()
			continue
		}
		if err != nil {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, err
		}
		if _, uncertain := s.uncertainAccounts.Load(stored.AccountID); uncertain {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, errInvalidSession
		}
		if !s.allowAccount(stored.Subject) {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, errRateLimit
		}
		csrf := s.csrf(stored.PepperVersion, cookie.Value)
		if subtle.ConstantTimeCompare(stored.CSRFHash, s.digest(stored.PepperVersion, "csrf-store", csrf)) != 1 {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, errors.New("csrf")
		}
		if err = tx.Commit(op.Context()); err != nil {
			op.Close()
			return authorizationPortalSession{}, err
		}
		op.Close()
		return authorizationPortalSession{
			account: stored.AccountID,
			subject: stored.Subject, csrf: csrf, version: stored.PepperVersion,
		}, nil
	}
	return authorizationPortalSession{}, errInvalidSession
}

func beginAuthorizationTransaction(op persistence.Operation) (persistence.Transaction, persistence.AuthorizationTransaction, error) {
	tx, err := op.Begin(op.Context())
	if err != nil {
		return nil, nil, err
	}
	authorizationTx, err := authorizationTransaction(tx)
	if err != nil {
		_ = tx.Rollback(op.Context())
		return nil, nil, err
	}
	return tx, authorizationTx, nil
}
