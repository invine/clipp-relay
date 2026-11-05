package portal

import (
	"context"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type usageReadViews struct {
	owner persistence.OwnerProfile
	admin persistence.AdminAccount
}

func (v usageReadViews) OwnerProfile(context.Context, string, []byte, uint64) (persistence.OwnerProfile, error) {
	return v.owner, nil
}
func (v usageReadViews) AdminAccount(context.Context, string) (persistence.AdminAccount, error) {
	return v.admin, nil
}

type usageLiveCounts struct {
	count int
	known bool
}

func (v usageLiveCounts) LiveSessionCount(string) (int, bool) { return v.count, v.known }

func TestReadOwnerQuotaViewKeepsUsageAndKnownZeroFromQuota(t *testing.T) {
	week := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	views := usageReadViews{owner: persistence.OwnerProfile{
		Status: "Active", WeekStart: week, WeeklyBytes: int64Pointer(200), Sessions: intPointer(3), Usage: 125,
		HistoryJSON: []byte(`[{"week":"2026-11-02","recorded":125,"observed":true}]`),
	}}
	owner, err := ReadOwnerQuotaView(context.Background(), views, usageLiveCounts{count: 0, known: true}, "account", []byte("session"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Recorded != 125 || owner.WeeklyBytes == nil || *owner.WeeklyBytes != 200 || owner.WeekStart != week {
		t.Fatalf("owner quota view=%+v", owner)
	}
	if len(owner.History) != 1 || owner.History[0].Recorded != 125 {
		t.Fatalf("owner history=%+v", owner.History)
	}
	if owner.LiveSessions == nil || *owner.LiveSessions != 0 {
		t.Fatalf("known zero was lost: %+v", owner.LiveSessions)
	}
}

func TestReadOwnerQuotaViewKeepsUnavailableDistinctFromZero(t *testing.T) {
	owner, err := ReadOwnerQuotaView(context.Background(), usageReadViews{owner: persistence.OwnerProfile{Status: "Active", HistoryJSON: []byte(`[]`)}}, usageLiveCounts{}, "account", []byte("session"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if owner.LiveSessions != nil {
		t.Fatalf("unavailable live sample became %d", *owner.LiveSessions)
	}
}

func TestReadAdminQuotaViewDecodesConfirmedHistory(t *testing.T) {
	views := usageReadViews{admin: persistence.AdminAccount{ID: "target", Recorded: 80, Remaining: 20, HistoryJSON: []byte(`[{"week":"2026-11-02","recorded":80}]`)}}
	admin, err := ReadAdminQuotaView(context.Background(), views, "target")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Account.ID != "target" || admin.Recorded != 80 || admin.Remaining != 20 || len(admin.History) != 1 || admin.History[0].Recorded != 80 {
		t.Fatalf("admin quota view=%+v", admin)
	}
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }
