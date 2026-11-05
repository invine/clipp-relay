package portal

import (
	"context"
	"encoding/json"
	"time"

	"clipp-relay/internal/persistence"
)

// OwnerQuotaSource is the protected, durable Portal snapshot. The adapter
// verifies the supplied Portal Session and reads account policy plus usage in
// one database operation.
type OwnerQuotaSource interface {
	OwnerProfile(context.Context, string, []byte, uint64) (persistence.OwnerProfile, error)
}

// AdminQuotaSource supplies the selected account's durable Portal projection.
// The caller must authenticate administrator access before requesting it.
type AdminQuotaSource interface {
	AdminAccount(context.Context, string) (persistence.AdminAccount, error)
}

// LiveSessionCounts samples only Quota's process-local aggregate. A missing
// sample is unavailable; a present zero is a known empty Relay registry.
type LiveSessionCounts interface {
	LiveSessionCount(string) (int, bool)
}

// OwnerQuotaView is the Portal's owner-facing quota projection. Authorization
// and active Login Grant facts remain with Auth and are not part of this view.
type OwnerQuotaView struct {
	Status, Email          string
	WeekStart              time.Time
	Verified, RecoveryHeld bool
	HostedDomain           *string
	WeeklyBytes            *int64
	Sessions               *int
	Recorded               int64
	History                []QuotaHistoryEntry
	LiveSessions           *int
}

// ReadOwnerQuotaView keeps the session recheck and durable account/usage
// snapshot in the persistence adapter's single operation, then samples live
// sessions independently from Quota.
func ReadOwnerQuotaView(ctx context.Context, source OwnerQuotaSource, counts LiveSessionCounts, id string, digest []byte, version uint64) (OwnerQuotaView, error) {
	var view OwnerQuotaView
	profile, err := source.OwnerProfile(ctx, id, digest, version)
	if err != nil {
		return view, err
	}
	view = OwnerQuotaView{
		Status: profile.Status, Email: profile.Email, WeekStart: profile.WeekStart,
		Verified: profile.Verified, RecoveryHeld: profile.RecoveryHeld, HostedDomain: profile.HostedDomain,
		WeeklyBytes: profile.WeeklyBytes, Sessions: profile.Sessions, Recorded: profile.Usage,
	}
	if len(profile.HistoryJSON) > 0 {
		if err := json.Unmarshal(profile.HistoryJSON, &view.History); err != nil {
			return OwnerQuotaView{}, err
		}
	}
	if counts != nil {
		if count, known := counts.LiveSessionCount(id); known && count >= 0 {
			view.LiveSessions = &count
		}
	}
	return view, nil
}

// AdminQuotaView contains confirmed usage and immutable-history projection for
// one account, separate from authorization and mutation decisions.
type AdminQuotaView struct {
	Account   persistence.AdminAccount
	Recorded  int64
	Remaining int64
	History   []QuotaHistoryEntry
}

// ReadAdminQuotaView decodes the durable usage projection after the caller has
// authorized the administrator request.
func ReadAdminQuotaView(ctx context.Context, source AdminQuotaSource, id string) (AdminQuotaView, error) {
	var view AdminQuotaView
	account, err := source.AdminAccount(ctx, id)
	if err != nil {
		return view, err
	}
	view.Account = account
	view.Recorded, view.Remaining = account.Recorded, account.Remaining
	if len(account.HistoryJSON) > 0 {
		if err := json.Unmarshal(account.HistoryJSON, &view.History); err != nil {
			return AdminQuotaView{}, err
		}
	}
	return view, nil
}
