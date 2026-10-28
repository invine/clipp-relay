package auth

import (
	"context"
	"errors"
	"net/http"

	"clipp-relay/internal/persistence"
)

type accountMutation struct {
	subject  string
	accounts []string
	work     func(context.Context) bool
	complete func() error
}

type accountMutationProgress struct {
	identityEntered bool
	completionReady bool
	completionErr   error
}

// completeAccountMutation orders common Auth protection around an operation
// and starts request-independent physical cleanup only after all guards release.
// The operation itself retains its action-specific transaction and outcome rules.
func (s *Server) completeAccountMutation(ctx context.Context, mutation accountMutation) (accountMutationProgress, error) {
	var progress accountMutationProgress
	protected := func(ctx context.Context) error {
		return s.withAccountGuards(ctx, mutation.accounts, func(ctx context.Context) {
			progress.completionReady = mutation.work(ctx)
		})
	}
	var guardErr error
	if mutation.subject == "" {
		guardErr = protected(ctx)
	} else {
		guardErr = s.withIdentityGuard(ctx, mutation.subject, func(ctx context.Context) error {
			progress.identityEntered = true
			return protected(ctx)
		})
	}
	if progress.completionReady && mutation.complete != nil {
		progress.completionErr = mutation.complete()
	}
	return progress, guardErr
}

type accountMutationGuardFailure struct {
	refused  bool
	canceled bool
	status   int
	reason   string
	event    string
	handled  bool
}

func classifyAccountMutationGuardFailure(err error) accountMutationGuardFailure {
	if errors.Is(err, persistence.ErrWorkUnavailable) {
		return accountMutationGuardFailure{refused: true, status: http.StatusServiceUnavailable, reason: "overloaded", event: "http.request_refused", handled: true}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return accountMutationGuardFailure{canceled: true, status: http.StatusServiceUnavailable, handled: true}
	}
	return accountMutationGuardFailure{status: http.StatusServiceUnavailable, reason: "database_unavailable"}
}
