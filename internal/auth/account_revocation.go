package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
)

type accountRevocationResult uint8

const (
	accountRevocationFailed accountRevocationResult = iota
	accountRevocationRefused
	accountRevocationCompleted
	accountRevocationUncertain
	accountRevocationInvalidationIncomplete
)

type accountRevocationOutcome struct {
	result            accountRevocationResult
	status            int
	diagnosticOutcome string
	reason            string
	event             string
	handled           bool
}

func refusedAccountRevocation(status int, reason string) accountRevocationOutcome {
	return accountRevocationOutcome{result: accountRevocationRefused, status: status, diagnosticOutcome: "refused", reason: reason}
}

func failedAccountRevocation(status int, reason string) accountRevocationOutcome {
	return accountRevocationOutcome{result: accountRevocationFailed, status: status, diagnosticOutcome: "failed", reason: reason}
}

// revokeAccountAccess owns the complete account-wide mutation. The operation
// retires logical session permission under Auth protection, releases every
// guard, and only then asks Relay to start physical cleanup.
func (s *Server) revokeAccountAccess(r *http.Request) accountRevocationOutcome {
	id, version, _, _, csrf, err := s.session(r)
	if err != nil {
		switch {
		case errors.Is(err, errRateLimit):
			outcome := refusedAccountRevocation(http.StatusUnauthorized, "rate_limited")
			outcome.handled = true
			return outcome
		case errors.Is(err, errInvalidSession):
			return refusedAccountRevocation(http.StatusUnauthorized, "invalid_credentials")
		default:
			return failedAccountRevocation(http.StatusUnauthorized, "database_unavailable")
		}
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		return refusedAccountRevocation(http.StatusForbidden, "csrf_rejected")
	}
	if s.beforeOwnerSubjectRead != nil {
		s.beforeOwnerSubjectRead()
	}
	readOp, err := s.persistence.Start(r.Context())
	if err != nil {
		return failedAccountRevocation(http.StatusServiceUnavailable, "database_unavailable")
	}
	readTx, err := beginAdminAccessTransaction(readOp)
	if err != nil {
		readOp.Close()
		return failedAccountRevocation(http.StatusServiceUnavailable, "database_unavailable")
	}
	ownerState, err := readTx.AccountAccessState(readOp.Context(), id, false)
	_ = readTx.Rollback(readOp.Context())
	readOp.Close()
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return refusedAccountRevocation(http.StatusServiceUnavailable, "invalid_credentials")
		}
		return failedAccountRevocation(http.StatusServiceUnavailable, "database_unavailable")
	}

	subject := ownerState.Subject
	if s.beforeAccountActionGuard != nil {
		s.beforeAccountActionGuard()
	}
	committed := false
	uncertain := false
	invalidationFailed := false
	outcome := failedAccountRevocation(http.StatusServiceUnavailable, "database_unavailable")
	progress, guardErr := s.completeAccountMutation(r.Context(), accountMutation{
		subject:  subject,
		accounts: []string{id},
		work: func(ctx context.Context) bool {
			op, operationErr := s.persistence.Start(ctx)
			if operationErr != nil {
				return false
			}
			defer op.Close()
			tx, operationErr := beginAdminAccessTransaction(op)
			if operationErr != nil {
				return false
			}
			defer tx.Rollback(op.Context())
			if s.beforeOwnerAccessRecheck != nil {
				if operationErr = s.beforeOwnerAccessRecheck(ctx); operationErr != nil {
					outcome = failedAccountRevocation(http.StatusUnauthorized, "database_unavailable")
					return false
				}
			}
			auditID, operationErr := uuid()
			if operationErr != nil {
				return false
			}
			state, operationErr := tx.RevokeAccountAccess(op.Context(), id, s.sessionDigest(r, version), version, auditID)
			if errors.Is(operationErr, persistence.ErrInvalidCredential) {
				outcome = refusedAccountRevocation(http.StatusUnauthorized, "invalid_credentials")
				return false
			}
			if operationErr != nil {
				return false
			}
			if s.commitAccountAccess != nil {
				operationErr = s.commitAccountAccess(op.Context(), tx)
			} else {
				operationErr = tx.Commit(op.Context())
			}
			s.installIdentityFence(subject)
			if operationErr != nil {
				s.uncertainAccounts.Store(id, uncertainMutation{revision: state.Revision, generation: state.Generation + 1, status: state.Status})
			}
			reason := s.accountChanged(AccountChange{AccountID: id, WeeklyBytes: state.WeeklyBytes, SessionLimit: state.Sessions, CloseAll: true})
			if reason == "" {
				reason = "account_access_changed"
			}
			invalidationFailed = s.deliverAccountInvalidation(id, reason) != nil
			committed = true
			uncertain = operationErr != nil
			return committed
		},
		complete: func() error { return s.accountInvalidationComplete(id) },
	})
	if guardErr != nil && !progress.identityEntered {
		return accountRevocationGuardOutcome(guardErr)
	}
	if guardErr != nil && progress.identityEntered {
		outcome = accountRevocationGuardOutcome(guardErr)
	}
	if !committed {
		if outcome.result == accountRevocationRefused || outcome.status == http.StatusUnauthorized {
			return outcome
		}
		return failedAccountRevocation(http.StatusServiceUnavailable, "database_unavailable")
	}

	// Completion is request-independent and happens after both Auth guards and
	// their operation resources have been released, even if the request context
	// was canceled after the database commit.
	if progress.completionErr != nil {
		invalidationFailed = true
	}
	if uncertain {
		return accountRevocationOutcome{result: accountRevocationUncertain, status: http.StatusServiceUnavailable, diagnosticOutcome: "uncertain", reason: "commit_unconfirmed"}
	}
	if invalidationFailed {
		return accountRevocationOutcome{result: accountRevocationInvalidationIncomplete, status: http.StatusServiceUnavailable, diagnosticOutcome: "failed", reason: "invalidation_incomplete"}
	}
	return accountRevocationOutcome{result: accountRevocationCompleted, status: http.StatusSeeOther, diagnosticOutcome: "success", reason: "none"}
}

func accountRevocationGuardOutcome(err error) accountRevocationOutcome {
	failure := classifyAccountMutationGuardFailure(err)
	if failure.refused {
		return accountRevocationOutcome{result: accountRevocationRefused, status: failure.status, diagnosticOutcome: "refused", reason: failure.reason, event: failure.event, handled: failure.handled}
	}
	if failure.canceled {
		return accountRevocationOutcome{result: accountRevocationFailed, status: failure.status, handled: failure.handled}
	}
	return failedAccountRevocation(failure.status, failure.reason)
}

func (s *Server) renderAccountRevocation(w http.ResponseWriter, r *http.Request, outcome accountRevocationOutcome) {
	if outcome.handled {
		markAction(r, outcome.event)
	}
	if outcome.diagnosticOutcome != "" {
		markDiagnostic(r, outcome.diagnosticOutcome, outcome.reason)
	}
	switch outcome.result {
	case accountRevocationCompleted:
		cookie(w, sessionCookie, "", -1)
		http.Redirect(w, r, "/?notice=revoked", http.StatusSeeOther)
	case accountRevocationUncertain:
		cookie(w, sessionCookie, "", -1)
		uncertainResponse(w)
	case accountRevocationInvalidationIncomplete:
		cookie(w, sessionCookie, "", -1)
		http.Error(w, "Account revocation committed, but relay session invalidation could not be confirmed.", http.StatusServiceUnavailable)
	case accountRevocationRefused, accountRevocationFailed:
		fail(w, outcome.status)
	}
}
