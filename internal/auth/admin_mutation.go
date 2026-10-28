package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"clipp-relay/internal/config"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
)

type adminAccountResult uint8

const (
	adminAccountFailed adminAccountResult = iota
	adminAccountRefused
	adminAccountCompleted
	adminAccountUncertain
	adminAccountInvalidationIncomplete
)

type adminAccountOutcome struct {
	result               adminAccountResult
	status               int
	reason               string
	policyRevision       uint64
	policyRevisionSet    bool
	change               *AccountChange
	invalidationRequired bool
	invalidationFailed   bool
	actionHandled        bool
	event                string
}

func refusedAdminAccount(status int, reason string) adminAccountOutcome {
	return adminAccountOutcome{result: adminAccountRefused, status: status, reason: reason}
}

func failedAdminAccount(status int, reason string) adminAccountOutcome {
	return adminAccountOutcome{result: adminAccountFailed, status: status, reason: reason}
}

// changeAdminAccount owns the complete administrator mutation. It classifies
// protected work first, applies logical Relay effects while account protection
// is held, releases identity/account guards, then starts physical cleanup.
func (s *Server) changeAdminAccount(r *http.Request, actor adminIdentity, target string, expected int64, action string, spec accountActionSpec, plan string) adminAccountOutcome {
	var securitySubject string
	if s.beforeAccountActionGuard != nil {
		s.beforeAccountActionGuard()
	}
	if spec.security {
		preflight, err := s.persistence.Start(r.Context())
		if err != nil {
			return failedAdminAccount(http.StatusServiceUnavailable, "database_unavailable")
		}
		preflightTx, err := beginAdminAccessTransaction(preflight)
		if err != nil {
			preflight.Close()
			return failedAdminAccount(http.StatusServiceUnavailable, "database_unavailable")
		}
		state, err := preflightTx.AccountAccessState(preflight.Context(), target, false)
		_ = preflightTx.Rollback(preflight.Context())
		preflight.Close()
		if err != nil {
			if errors.Is(err, persistence.ErrNotFound) {
				return failedAdminAccount(http.StatusServiceUnavailable, "invalid_request")
			}
			return failedAdminAccount(http.StatusServiceUnavailable, "database_unavailable")
		}
		if state.Deleting {
			return refusedAdminAccount(http.StatusServiceUnavailable, "invalid_request")
		}
		securitySubject = state.Subject
	}

	var outcome adminAccountOutcome
	progress, guardErr := s.completeAccountMutation(r.Context(), accountMutation{
		subject:  securitySubject,
		accounts: []string{actor.id, target},
		work: func(ctx context.Context) bool {
			outcome = s.applyAdminAccountMutation(ctx, r, actor, target, expected, action, spec, plan)
			if outcome.change == nil {
				return false
			}
			reason := s.accountChanged(*outcome.change)
			if reason == "" && outcome.change.CloseAll {
				reason = "account_access_changed"
			}
			if reason != "" {
				outcome.invalidationRequired = true
				if s.deliverAccountInvalidation(outcome.change.AccountID, reason) != nil {
					outcome.invalidationFailed = true
				}
				if reason == "account_access_changed" {
					outcome.change.CloseAll = true
				}
			}
			return outcome.invalidationRequired
		},
		complete: func() error { return s.accountInvalidationComplete(outcome.change.AccountID) },
	})
	if guardErr != nil && !progress.identityEntered && outcome.status == 0 {
		return adminGuardFailure(guardErr)
	}
	if outcome.change == nil {
		if guardErr != nil && outcome.status == 0 {
			return adminGuardFailure(guardErr)
		}
		if outcome.status != 0 {
			return outcome
		}
		return failedAdminAccount(http.StatusServiceUnavailable, "database_unavailable")
	}

	// The request context may be canceled after commit; cleanup still runs once
	// the guarded logical effects have completed.
	if progress.completionErr != nil {
		outcome.invalidationFailed = true
	}
	if outcome.invalidationFailed && outcome.result != adminAccountUncertain {
		outcome.result = adminAccountInvalidationIncomplete
		outcome.status = http.StatusServiceUnavailable
		outcome.reason = "invalidation_incomplete"
	}
	return outcome
}

func adminGuardFailure(err error) adminAccountOutcome {
	failure := classifyAccountMutationGuardFailure(err)
	if failure.refused {
		outcome := refusedAdminAccount(failure.status, failure.reason)
		outcome.actionHandled = failure.handled
		outcome.event = failure.event
		return outcome
	}
	if failure.canceled {
		return adminAccountOutcome{result: adminAccountFailed, status: failure.status, actionHandled: failure.handled}
	}
	return failedAdminAccount(failure.status, failure.reason)
}

func (s *Server) applyAdminAccountMutation(ctx context.Context, r *http.Request, actor adminIdentity, target string, expected int64, action string, spec accountActionSpec, plan string) (outcome adminAccountOutcome) {
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	defer op.Close()
	tx, err := beginAdminAccessTransaction(op)
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	defer tx.Rollback(op.Context())
	if err = tx.LockAdministratorAccounts(op.Context(), actor.id, target); errors.Is(err, persistence.ErrNotFound) {
		return refusedAdminAccount(http.StatusNotFound, "")
	} else if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	if s.beforeAdminRecheck != nil {
		s.beforeAdminRecheck()
	}
	actorSession, err := tx.AdministratorSession(op.Context(), actor.id, s.sessionDigest(r, actor.version), actor.version)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		return refusedAdminAccount(http.StatusUnauthorized, "not_authorized")
	}
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "database_unavailable")
	}
	if !actorSession.Recent {
		return refusedAdminAccount(http.StatusUnauthorized, "recent_auth_required")
	}
	policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile)
	if err != nil {
		outcome = refusedAdminAccount(http.StatusForbidden, "not_authorized")
		outcome.policyRevisionSet = true
		return outcome
	}
	defer func() {
		outcome.policyRevision = policy.Revision
		outcome.policyRevisionSet = true
	}()
	if !policy.Allows(actorSession.Email, actorSession.Verified, deref(actorSession.HostedDomain)) {
		return refusedAdminAccount(http.StatusForbidden, "not_authorized")
	}
	actor.email = normalizedEmail(actorSession.Email)
	outcome = adminAccountOutcome{policyRevision: policy.Revision}
	state, err := tx.AccountAccessState(op.Context(), target, false)
	if errors.Is(err, persistence.ErrNotFound) {
		return refusedAdminAccount(http.StatusNotFound, "")
	}
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	if state.Deleting || state.Revision != expected || !spec.states[state.Status] {
		return refusedAdminAccount(http.StatusConflict, "revision_conflict")
	}
	weekly, sessions := state.WeeklyOverride, state.SessionOverride
	if action == "override" {
		if values, present := r.PostForm["weekly_bytes_override"]; present {
			if len(values) == 1 && values[0] != "" {
				n, parseErr := parseQuotaBytes(values[0], r.PostForm.Get("weekly_bytes_override_unit"))
				if parseErr != nil || n < 0 {
					return refusedAdminAccount(http.StatusBadRequest, "")
				}
				weekly = &n
			} else {
				weekly = nil
			}
		}
		if values, present := r.PostForm["sessions_override"]; present {
			if len(values) == 1 && values[0] != "" {
				n, parseErr := strconv.ParseInt(values[0], 10, 32)
				if parseErr != nil || n < 0 {
					return refusedAdminAccount(http.StatusBadRequest, "")
				}
				value := int(n)
				sessions = &value
			} else {
				sessions = nil
			}
		}
	}
	auditID, err := uuid()
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	result, err := tx.ApplyAdminAccountMutation(op.Context(), persistence.AdminAccountMutation{AccountID: target, PlanID: plan, Action: action, NextStatus: spec.nextStatus, Event: spec.event, Reason: r.PostForm.Get("reason"), ActorEmail: actor.email, ExpectedRevision: expected, Security: spec.security, WeeklyOverride: weekly, SessionOverride: sessions, AuditID: auditID})
	if errors.Is(err, persistence.ErrConflict) {
		return refusedAdminAccount(http.StatusConflict, "revision_conflict")
	}
	if err != nil {
		return failedAdminAccount(http.StatusServiceUnavailable, "")
	}
	if spec.security {
		s.installIdentityFence(state.Subject)
	}
	if s.commitAccountAccess != nil {
		err = s.commitAccountAccess(op.Context(), tx)
	} else {
		err = tx.Commit(op.Context())
	}
	if err != nil {
		s.uncertainAccounts.Store(target, uncertainMutation{revision: result.NewRevision, generation: result.NewGeneration, status: result.NewStatus})
	}
	outcome.change = &AccountChange{AccountID: target, WeeklyBytes: result.WeeklyBytes, SessionLimit: result.Sessions, CloseAll: spec.security || err != nil}
	if err != nil {
		outcome.result = adminAccountUncertain
		outcome.status = http.StatusServiceUnavailable
		outcome.reason = "commit_unconfirmed"
	} else {
		outcome.result = adminAccountCompleted
		outcome.status = http.StatusSeeOther
	}
	return outcome
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Server) renderAdminAccountOutcome(w http.ResponseWriter, r *http.Request, target string, outcome adminAccountOutcome) {
	if outcome.policyRevisionSet {
		w.Header().Set("X-Admin-Policy-Revision", strconv.FormatUint(outcome.policyRevision, 10))
	}
	if outcome.actionHandled {
		markAction(r, outcome.event)
	}
	if outcome.reason != "" {
		diagnosticOutcome := "failed"
		if outcome.result == adminAccountRefused {
			diagnosticOutcome = "refused"
		}
		if outcome.result == adminAccountUncertain {
			diagnosticOutcome = "uncertain"
		}
		markDiagnostic(r, diagnosticOutcome, outcome.reason)
	}
	switch outcome.result {
	case adminAccountCompleted:
		http.Redirect(w, r, adminReturnHref(r, target), http.StatusSeeOther)
	case adminAccountUncertain:
		uncertainResponse(w)
	case adminAccountInvalidationIncomplete:
		http.Error(w, "Account change committed, but relay session invalidation could not be confirmed.", http.StatusServiceUnavailable)
	case adminAccountRefused:
		if outcome.status == http.StatusConflict {
			portal.Conflict(w, adminReturnHref(r, target), "Reload this account")
		} else {
			fail(w, outcome.status)
		}
	default:
		fail(w, outcome.status)
	}
}
