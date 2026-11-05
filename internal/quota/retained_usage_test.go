package quota

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

func TestRetainedUsageKeyUsesCurrentPepperAndDedicatedPurpose(t *testing.T) {
	q := New(nil)
	defer q.Close()
	peppers := map[uint64][]byte{3: []byte("retention key with enough test entropy"), 2: []byte("retiring retention key with test entropy")}
	if err := q.ConfigureRetainedUsageKeys(3, peppers); err != nil {
		t.Fatal(err)
	}
	key, err := q.RetainedUsageKey("https://accounts.google.com", "subject-123")
	if err != nil {
		t.Fatal(err)
	}
	if key.PepperVersion != 3 {
		t.Fatalf("pepper version=%d, want 3", key.PepperVersion)
	}
	h := hmac.New(sha256.New, peppers[3])
	_, _ = h.Write([]byte("clipp-relay/v1/retained-quota\x00https://accounts.google.com\x00subject-123"))
	var want [32]byte
	copy(want[:], h.Sum(nil))
	if key.IdentityDigest != want {
		t.Fatalf("retained identity key=%x, want %x", key.IdentityDigest, want)
	}
	if key == (persistence.RetainedUsageKey{}) {
		t.Fatal("retained identity key was empty")
	}
}

func TestRetainedUsageKeyFailsClosedWithoutCurrentPepper(t *testing.T) {
	q := New(nil)
	defer q.Close()
	if err := q.ConfigureRetainedUsageKeys(9, map[uint64][]byte{8: []byte("retiring retention key with test entropy")}); err == nil {
		t.Fatal("configuration without current pepper succeeded")
	}
	if err := q.ConfigureRetainedUsageKeys(9, map[uint64][]byte{9: []byte("current key with enough test entropy")}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RetainedUsageKey("issuer", "subject"); err != nil {
		t.Fatalf("current pepper should produce the key: %v", err)
	}
}

func TestRecoveryRetainedUsageCoverageAndDuplicateResolution(t *testing.T) {
	q := New(nil)
	defer q.Close()
	peppers := map[uint64][]byte{
		2: []byte("retiring recovery key with test entropy"),
		3: []byte("current recovery key with test entropy"),
	}
	if err := q.ConfigureRetainedUsageKeys(3, peppers); err != nil {
		t.Fatal(err)
	}
	if err := q.ValidateRetainedUsageKeyCoverage([]uint64{2, 3}); err != nil {
		t.Fatalf("configured retained keys were not covered: %v", err)
	}
	if err := q.ValidateRetainedUsageKeyCoverage([]uint64{2, 9}); err == nil {
		t.Fatal("missing retained-usage key was accepted")
	}

	ctx := context.Background()
	oldMatch := &recoveryAccountingFixture{week: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), recorded: 77, hasUsage: true, matches: map[uint64]bool{2: true}}
	if err := q.RetainRecoveredUsage(ctx, oldMatch, "restored-account", "https://accounts.google.com", "restore-subject"); err != nil {
		t.Fatalf("retain through old key: %v", err)
	}
	if oldMatch.storedVersion != 2 || oldMatch.storedBytes != 77 || len(oldMatch.queriedVersions) != 2 {
		t.Fatalf("retained old key version=%d bytes=%d queries=%v", oldMatch.storedVersion, oldMatch.storedBytes, oldMatch.queriedVersions)
	}

	noMatch := &recoveryAccountingFixture{week: oldMatch.week, recorded: 91, hasUsage: true, matches: map[uint64]bool{}}
	if err := q.RetainRecoveredUsage(ctx, noMatch, "restored-account", "https://accounts.google.com", "restore-subject"); err != nil {
		t.Fatalf("retain through current key: %v", err)
	}
	if noMatch.storedVersion != 3 || noMatch.storedBytes != 91 {
		t.Fatalf("no prior match stored version=%d bytes=%d, want current key and confirmed usage", noMatch.storedVersion, noMatch.storedBytes)
	}

	duplicate := &recoveryAccountingFixture{week: oldMatch.week, recorded: 91, hasUsage: true, matches: map[uint64]bool{2: true, 3: true}}
	if err := q.RetainRecoveredUsage(ctx, duplicate, "restored-account", "https://accounts.google.com", "restore-subject"); err == nil {
		t.Fatal("duplicate logical retained usage was accepted")
	}
	if duplicate.storedVersion != 0 {
		t.Fatal("duplicate retained usage was written")
	}
}

type recoveryAccountingFixture struct {
	week            time.Time
	recorded        int64
	hasUsage        bool
	matches         map[uint64]bool
	queriedVersions []uint64
	storedVersion   uint64
	storedBytes     int64
}

func (f *recoveryAccountingFixture) CurrentQuotaWeek(context.Context) (time.Time, error) {
	return f.week, nil
}
func (f *recoveryAccountingFixture) RecordedUsage(context.Context, string, time.Time) (int64, bool, error) {
	return f.recorded, f.hasUsage, nil
}
func (f *recoveryAccountingFixture) RecoveredRetainedUsage(_ context.Context, version uint64, _ []byte, _ time.Time) (int64, bool, error) {
	f.queriedVersions = append(f.queriedVersions, version)
	return 0, f.matches[version], nil
}
func (f *recoveryAccountingFixture) StoreRecoveredRetainedUsage(_ context.Context, version uint64, _ []byte, _ time.Time, recorded int64) error {
	f.storedVersion, f.storedBytes = version, recorded
	return nil
}
