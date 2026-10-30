package auth

import (
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
	email                  string
	emailVerified          bool
	version                uint64
}

// authorizationPortalSession performs the small Portal Session read used by
// native authorization through an admitted, transaction-scoped projection.
// It validates CSRF before extending the idle deadline, matching session().
func (s *Server) authorizationPortalSession(r *http.Request) (authorizationPortalSession, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return authorizationPortalSession{}, errInvalidSession
	}
	credentials, err := s.portalSessionCredentials(cookie.Value)
	if err != nil {
		return authorizationPortalSession{}, err
	}
	for _, credential := range credentials {
		op, err := s.authorization.StartAuthorization(r.Context())
		if err != nil {
			return authorizationPortalSession{}, err
		}
		tx, err := op.BeginAuthorization(op.Context())
		if err != nil {
			op.Close()
			return authorizationPortalSession{}, err
		}
		defer tx.Rollback(op.Context())
		stored, err := tx.PortalSessionForAuthorization(op.Context(), credential.digest, credential.version)
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
		csrf, err := s.verifyPortalSessionProof(credential, portalSessionProof{
			accountID: stored.AccountID, subject: stored.Subject,
			version: stored.PepperVersion, csrfHash: stored.CSRFHash,
		}, true)
		if err != nil {
			_ = tx.Rollback(op.Context())
			op.Close()
			return authorizationPortalSession{}, err
		}
		if err = tx.Commit(op.Context()); err != nil {
			op.Close()
			return authorizationPortalSession{}, err
		}
		op.Close()
		return authorizationPortalSession{
			account: stored.AccountID,
			subject: stored.Subject, csrf: csrf, version: credential.version,
			email: stored.Email, emailVerified: stored.EmailVerified,
		}, nil
	}
	return authorizationPortalSession{}, errInvalidSession
}

func beginAuthorizationTransaction(op persistence.AuthorizationOperation) (persistence.AuthorizationTransaction, error) {
	tx, err := op.BeginAuthorization(op.Context())
	if err != nil {
		return nil, err
	}
	return tx, nil
}
