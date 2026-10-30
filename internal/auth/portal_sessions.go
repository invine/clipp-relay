package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
)

// verifiedOwnerPortalSession is the complete projection ordinary owner portal
// requests need after the Portal Session operation has validated and renewed
// the credential.
type verifiedOwnerPortalSession struct {
	accountID, status, email, csrf string
	pepperVersion                  uint64
}

// ownerPortalSession owns ordinary browser credential parsing, pepper lookup,
// refusal policy, stored CSRF proof and idle renewal in one admitted operation.
func (s *Server) ownerPortalSession(r *http.Request) (verifiedOwnerPortalSession, error) {
	c, e := r.Cookie(sessionCookie)
	if e != nil || len(c.Value) != 43 {
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
	for version := range s.Peppers {
		digest := digest32(s.digest(version, "portal-session", c.Value))
		stored, err := tx.PortalSessionForOwner(op.Context(), digest, version)
		if errors.Is(err, persistence.ErrInvalidCredential) {
			continue
		}
		if err != nil {
			return verifiedOwnerPortalSession{}, err
		}
		if _, uncertain := s.uncertainAccounts.Load(stored.AccountID); uncertain {
			return verifiedOwnerPortalSession{}, errInvalidSession
		}
		if !s.allowAccount(stored.Subject) {
			return verifiedOwnerPortalSession{}, errRateLimit
		}
		token := s.csrf(stored.PepperVersion, c.Value)
		if subtle.ConstantTimeCompare(stored.CSRFHash, s.digest(stored.PepperVersion, "csrf-store", token)) != 1 {
			return verifiedOwnerPortalSession{}, errors.New("csrf")
		}
		touched, err := tx.TouchPortalSession(op.Context(), digest, version, stored.AccountID, stored.Generation)
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
			accountID: stored.AccountID, pepperVersion: version, status: stored.Status, email: stored.Email, csrf: token,
		}, nil
	}
	if e = tx.Rollback(op.Context()); e != nil {
		finished = true
		return verifiedOwnerPortalSession{}, e
	}
	finished = true
	return verifiedOwnerPortalSession{}, errInvalidSession
}
