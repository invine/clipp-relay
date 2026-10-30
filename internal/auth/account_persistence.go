package auth

import (
	"context"
	"errors"

	"clipp-relay/internal/persistence"
)

func beginAccountTransaction(op persistence.AccountRegistrationOperation) (persistence.AccountRegistrationTransaction, error) {
	tx, err := op.BeginAccountRegistration(op.Context())
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (s *Server) importRetainedUsage(ctx context.Context, tx persistence.AccountRegistrationTransaction, accountID, subject string) error {
	week, err := tx.CurrentRetainedUsageWeek(ctx)
	if err != nil {
		return err
	}
	versions, err := tx.RetainedUsageVersions(ctx)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if len(s.Peppers[version]) == 0 {
			return errors.New("retained usage key unavailable")
		}
	}
	var match persistence.RetainedUsageRecord
	matches := 0
	for version := range s.Peppers {
		digest := digest32(s.digest(version, "retained-quota", googleIssuer+"\x00"+subject))
		record, found, err := tx.RetainedUsageForIdentity(ctx, version, digest, week)
		if err != nil {
			return err
		}
		if found {
			matches++
			match = record
		}
	}
	if matches > 1 {
		return errors.New("duplicate logical retained usage")
	}
	if matches == 0 {
		return nil
	}
	return tx.ImportRetainedUsage(ctx, accountID, match)
}

func (s *Server) commitPortalTransaction(ctx context.Context, tx persistence.Transaction) error {
	if s.commitPortal != nil {
		return s.commitPortal(ctx, tx)
	}
	return tx.Commit(ctx)
}

func (s *Server) persistGoogleLogin(op persistence.AccountRegistrationOperation, claims identity, candidateID, auditID, credential string) (string, int64, error) {
	ctx := op.Context()
	tx, err := beginAccountTransaction(op)
	if err != nil {
		return "", 0, err
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(ctx)
		}
	}()
	registered, err := tx.UpsertRelayAccount(ctx, persistence.AccountRegistration{
		ID: candidateID, Issuer: googleIssuer, Subject: claims.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, HostedDomain: claims.HostedDomain,
	})
	if err != nil {
		return "", 0, err
	}
	if pending, uncertain := s.uncertainAccounts.Load(registered.ID); uncertain {
		var deletionOperationID string
		if expected, ok := pending.(uncertainDeletion); ok {
			deletionOperationID = expected.operationID
		}
		state, err := tx.AccountStateAfterUncertain(ctx, registered.ID, deletionOperationID)
		if err != nil {
			return "", 0, err
		}
		switch expected := pending.(type) {
		case uncertainMutation:
			if state.Revision != expected.revision || state.Generation != expected.generation || state.Status != expected.status {
				return "", 0, persistence.ErrUncertainCommit
			}
		case uncertainDeletion:
			if state.DeletionStarted || state.DeletionOperationPresent {
				return "", 0, persistence.ErrUncertainCommit
			}
		default:
			return "", 0, persistence.ErrUncertainCommit
		}
		s.uncertainAccounts.Delete(registered.ID)
	}
	if registered.Created {
		if err = s.importRetainedUsage(ctx, tx, registered.ID, claims.Subject); err != nil {
			return "", 0, err
		}
		if err = tx.RecordAccountCreated(ctx, auditID, registered.ID); err != nil {
			return "", 0, err
		}
	}
	csrf := s.csrf(s.CurrentPepper, credential)
	if err = tx.CreatePortalSession(ctx, digest32(s.digest(s.CurrentPepper, "portal-session", credential)), s.CurrentPepper, registered.ID, registered.Generation, digest32(s.digest(s.CurrentPepper, "csrf-store", csrf))); err != nil {
		return "", 0, err
	}
	err = s.commitPortalTransaction(ctx, tx)
	finished = true
	if err != nil {
		return "", 0, err
	}
	return registered.ID, registered.Generation, nil
}
