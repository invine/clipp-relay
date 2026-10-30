package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
)

type accountDeletionResult uint8

const (
	accountDeletionFailed accountDeletionResult = iota
	accountDeletionRefused
	accountDeletionUncertain
	accountDeletionInvalidationIncomplete
	accountDeletionAdmittedPending
)

type accountDeletionOutcome struct {
	result            accountDeletionResult
	status            int
	operationID       string
	diagnosticOutcome string
	reason            string
	event             string
	handled           bool
}

func failedAccountDeletion(status int, reason string) accountDeletionOutcome {
	return accountDeletionOutcome{result: accountDeletionFailed, status: status, diagnosticOutcome: "failed", reason: reason}
}

func refusedAccountDeletion(status int, reason string) accountDeletionOutcome {
	return accountDeletionOutcome{result: accountDeletionRefused, status: status, diagnosticOutcome: "refused", reason: reason}
}

// admitAccountDeletion owns the complete owner operation. Admission, fencing,
// and logical invalidation run under Auth protection. Relay starts physical
// cleanup only after both guards have been released.
func (s *Server) admitAccountDeletion(r *http.Request) accountDeletionOutcome {
	if s.deletionJournal == nil || s.deletionRepository == "" {
		return failedAccountDeletion(http.StatusServiceUnavailable, "journal_unavailable")
	}
	ownerSession, err := s.ownerPortalSession(r)
	if err != nil {
		switch {
		case errors.Is(err, errRateLimit):
			return accountDeletionOutcome{result: accountDeletionRefused, status: http.StatusUnauthorized, handled: true}
		case errors.Is(err, errInvalidSession):
			return refusedAccountDeletion(http.StatusUnauthorized, "invalid_credentials")
		default:
			return failedAccountDeletion(http.StatusUnauthorized, "database_unavailable")
		}
	}
	id, csrf, version := ownerSession.accountID, ownerSession.csrf, ownerSession.pepperVersion
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		return refusedAccountDeletion(http.StatusForbidden, "csrf_rejected")
	}
	if _, _, err := s.readHead(r.Context()); err != nil {
		return failedAccountDeletion(http.StatusServiceUnavailable, "journal_unavailable")
	}
	if s.beforeOwnerSubjectRead != nil {
		s.beforeOwnerSubjectRead()
	}
	ownerTx, err := s.deletions.BeginDeletion(r.Context())
	if err != nil {
		return failedAccountDeletion(http.StatusServiceUnavailable, "database_unavailable")
	}
	owner, err := ownerTx.DeletionOwner(r.Context(), id)
	_ = ownerTx.Rollback(r.Context())
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrInvalidCredential) {
			return refusedAccountDeletion(http.StatusConflict, "invalid_credentials")
		}
		return failedAccountDeletion(http.StatusConflict, "database_unavailable")
	}
	operationID, err := uuid()
	if err != nil {
		return failedAccountDeletion(http.StatusServiceUnavailable, "database_unavailable")
	}
	if s.beforeAccountActionGuard != nil {
		s.beforeAccountActionGuard()
	}

	subject := owner.Subject
	var admitted, uncertain, invalidationFailed bool
	outcome := failedAccountDeletion(http.StatusServiceUnavailable, "database_unavailable")
	progress, guardErr := s.completeAccountMutation(r.Context(), accountMutation{
		subject:  subject,
		accounts: []string{id},
		work: func(ctx context.Context) bool {
			tx, operationErr := s.deletions.BeginDeletion(ctx)
			if operationErr != nil {
				return false
			}
			defer tx.Rollback(ctx)
			if s.beforeOwnerDeletionRecheck != nil {
				if operationErr = s.beforeOwnerDeletionRecheck(ctx); operationErr != nil {
					return false
				}
			}
			admission, operationErr := tx.AdmitDeletion(ctx, persistence.DeletionAdmissionRequest{
				AccountID: id, OperationID: operationID, ExpectedSubject: subject,
				SessionDigest: s.sessionDigest(r, version), PepperVersion: version,
			})
			if errors.Is(operationErr, persistence.ErrInvalidCredential) {
				outcome = refusedAccountDeletion(http.StatusUnauthorized, "invalid_credentials")
				return false
			}
			if errors.Is(operationErr, persistence.ErrRecentAuthentication) {
				outcome = refusedAccountDeletion(http.StatusForbidden, "recent_auth_required")
				return false
			}
			if errors.Is(operationErr, persistence.ErrCapacityExceeded) {
				outcome = refusedAccountDeletion(http.StatusServiceUnavailable, "deletion_capacity")
				return false
			}
			if operationErr != nil {
				return false
			}
			if s.commitDeletionAdmission != nil {
				operationErr = s.commitDeletionAdmission(ctx, tx)
			} else {
				operationErr = tx.Commit(ctx)
			}
			s.installIdentityFence(subject)
			if operationErr != nil {
				s.uncertainAccounts.Store(id, uncertainDeletion{operationID: operationID})
			}
			reason := s.accountChanged(AccountChange{
				AccountID: id, WeeklyBytes: admission.WeeklyBytes,
				SessionLimit: admission.Sessions, CloseAll: true,
			})
			if reason == "" {
				reason = "account_access_changed"
			}
			invalidationFailed = s.deliverAccountInvalidation(id, reason) != nil
			admitted, uncertain = true, operationErr != nil
			return admitted
		},
		complete: func() error {
			if s.deletionAccounting != nil {
				s.deletionAccounting.BeginAccountingDeletion(id)
			}
			return s.accountInvalidationComplete(id)
		},
	})
	if guardErr != nil && !progress.identityEntered {
		return accountDeletionGuardOutcome(guardErr)
	}
	if guardErr != nil && progress.identityEntered && !admitted {
		outcome = accountDeletionGuardOutcome(guardErr)
	}
	if !admitted {
		return outcome
	}

	// Logical deletion accounting and physical Relay cleanup are independent of
	// HTTP lifetime and run after all Auth protection has ended.
	if progress.completionErr != nil {
		invalidationFailed = true
	}
	if uncertain {
		return accountDeletionOutcome{
			result: accountDeletionUncertain, status: http.StatusServiceUnavailable,
			operationID: operationID, diagnosticOutcome: "uncertain", reason: "commit_unconfirmed",
		}
	}
	if invalidationFailed {
		return accountDeletionOutcome{
			result: accountDeletionInvalidationIncomplete, status: http.StatusServiceUnavailable,
			operationID: operationID, diagnosticOutcome: "failed", reason: "invalidation_incomplete",
		}
	}
	return accountDeletionOutcome{result: accountDeletionAdmittedPending, status: http.StatusAccepted, operationID: operationID}
}

func accountDeletionGuardOutcome(err error) accountDeletionOutcome {
	failure := classifyAccountMutationGuardFailure(err)
	if failure.refused {
		return accountDeletionOutcome{
			result: accountDeletionRefused, status: failure.status,
			diagnosticOutcome: "refused", reason: failure.reason, event: failure.event, handled: failure.handled,
		}
	}
	if failure.canceled {
		return accountDeletionOutcome{result: accountDeletionFailed, status: failure.status, handled: failure.handled}
	}
	return failedAccountDeletion(failure.status, failure.reason)
}

func (s *Server) renderAccountDeletion(w http.ResponseWriter, r *http.Request, outcome accountDeletionOutcome) {
	if outcome.handled {
		markAction(r, outcome.event)
	}
	if outcome.diagnosticOutcome != "" {
		markDiagnostic(r, outcome.diagnosticOutcome, outcome.reason)
	}
	switch outcome.result {
	case accountDeletionAdmittedPending:
		cookie(w, sessionCookie, "", -1)
		location := "/auth/deletion/" + outcome.operationID
		w.Header().Set("Location", location)
		w.Header().Set("Retry-After", "5")
		renderDeletion(w, true, location)
	case accountDeletionUncertain:
		cookie(w, sessionCookie, "", -1)
		uncertainResponse(w)
	case accountDeletionInvalidationIncomplete:
		cookie(w, sessionCookie, "", -1)
		http.Error(w, "Deletion admission committed, but relay session invalidation could not be confirmed.", http.StatusServiceUnavailable)
	case accountDeletionRefused, accountDeletionFailed:
		if outcome.reason == "recent_auth_required" {
			portal.RecentAuthenticationRequired(w)
		} else {
			fail(w, outcome.status)
		}
	}
}
