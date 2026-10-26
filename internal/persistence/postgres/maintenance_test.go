package postgres

import (
	"context"
	"strings"
	"testing"

	"clipp-relay/internal/persistence"
)

func TestCleanupOperationQueriesAreBoundedAndComplete(t *testing.T) {
	want := []persistence.CleanupOperation{
		persistence.CleanupRetainedUsage,
		persistence.CleanupWeeklyUsage,
		persistence.CleanupAuthorizationCodes,
		persistence.CleanupAccessTokens,
		persistence.CleanupTerminatedAccessTokens,
		persistence.CleanupRefreshGenerations,
		persistence.CleanupLoginGrants,
		persistence.CleanupIdlePortalSessions,
		persistence.CleanupAbsolutePortalSessions,
		persistence.CleanupHiddenAuthorizationCodes,
		persistence.CleanupHiddenAccessTokens,
		persistence.CleanupHiddenRefreshGenerations,
		persistence.CleanupHiddenLoginGrants,
		persistence.CleanupHiddenPortalSessions,
		persistence.CleanupHiddenWeeklyUsage,
		persistence.CleanupDeletionOperations,
		persistence.CleanupAuditEvents,
		persistence.CleanupDeletedAccounts,
		persistence.CleanupExpiredPendingAccounts,
	}
	if len(cleanupQueries) != len(want) {
		t.Fatalf("registered cleanup query count = %d, want %d", len(cleanupQueries), len(want))
	}
	for _, operation := range want {
		query, ok := cleanupQueries[operation]
		if !ok || query.sql == "" {
			t.Errorf("cleanup operation %v has no PostgreSQL implementation", operation)
		}
		if query.fair && operation < persistence.CleanupHiddenAuthorizationCodes {
			t.Errorf("non-hidden cleanup operation %v unexpectedly uses account fairness", operation)
		}
	}
	if q := cleanupQueries[persistence.CleanupExpiredPendingAccounts].sql; !containsAll(q, "WITH removed AS", "INSERT INTO public.audit_events", "pending_expired") {
		t.Fatalf("pending expiry no longer audits within its atomic statement: %s", q)
	}
	if q := cleanupQueries[persistence.CleanupRetainedUsage].sql; !containsAll(q, "week_start", "pepper_version", "identity_digest", "LIMIT $1") {
		t.Fatalf("retained usage expiry lost its key/week co-retirement bound: %s", q)
	}
}

func TestCleanupBatchRejectsUnknownAndUnboundedRequests(t *testing.T) {
	adapter := NewMaintenance(nil)
	for _, batch := range []persistence.CleanupBatch{
		{Operation: 255, Limit: 250},
		{Operation: persistence.CleanupRetainedUsage, Limit: 0},
		{Operation: persistence.CleanupHiddenPortalSessions, Limit: 250},
	} {
		if _, err := adapter.CleanupBatch(context.Background(), batch); err != persistence.ErrInvalidCleanupOperation {
			t.Errorf("invalid batch %+v error = %v", batch, err)
		}
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
