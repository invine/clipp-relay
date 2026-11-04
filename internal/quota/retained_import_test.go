package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type retainedImportFixture struct {
	week     time.Time
	versions []uint64
	records  map[retainedImportKey]persistence.RetainedUsageRecord
	imported []persistence.RetainedUsageRecord
}

type retainedImportKey struct {
	version uint64
	digest  [32]byte
	week    time.Time
}

func (f *retainedImportFixture) CurrentRetainedUsageWeek(context.Context) (time.Time, error) {
	return f.week, nil
}

func (f *retainedImportFixture) RetainedUsageVersions(context.Context) ([]uint64, error) {
	return append([]uint64(nil), f.versions...), nil
}

func (f *retainedImportFixture) RetainedUsageForIdentity(_ context.Context, version uint64, digest [32]byte, week time.Time) (persistence.RetainedUsageRecord, bool, error) {
	record, ok := f.records[retainedImportKey{version: version, digest: digest, week: week}]
	return record, ok, nil
}

func (f *retainedImportFixture) ImportRetainedUsage(_ context.Context, accountID string, record persistence.RetainedUsageRecord) error {
	if accountID == "" {
		return errors.New("missing account")
	}
	f.imported = append(f.imported, record)
	return nil
}

func TestImportRetainedUsageImportsOnlyCurrentWeekMatch(t *testing.T) {
	q := New(nil)
	defer q.Close()
	peppers := map[uint64][]byte{
		2: []byte("retiring retained usage test key with enough entropy"),
		3: []byte("current retained usage test key with enough entropy"),
	}
	if err := q.ConfigureRetainedUsageKeys(3, peppers); err != nil {
		t.Fatal(err)
	}
	week := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	oldWeek := week.AddDate(0, 0, -7)
	oldKey, err := q.retainedUsageKey(2, "https://accounts.google.com", "subject")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &retainedImportFixture{
		week: week, versions: []uint64{2},
		records: map[retainedImportKey]persistence.RetainedUsageRecord{
			{version: 2, digest: oldKey.IdentityDigest, week: oldWeek}: {PepperVersion: 2, IdentityDigest: oldKey.IdentityDigest, Week: oldWeek, RecordedBytes: 111},
			{version: 2, digest: oldKey.IdentityDigest, week: week}:    {PepperVersion: 2, IdentityDigest: oldKey.IdentityDigest, Week: week, RecordedBytes: 701},
		},
	}
	if err := q.ImportRetainedUsage(context.Background(), fixture, "fresh-account", "https://accounts.google.com", "subject"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.imported) != 1 || fixture.imported[0].RecordedBytes != 701 || !fixture.imported[0].Week.Equal(week) {
		t.Fatalf("imported records=%+v, want exactly current-week 701-byte opening total", fixture.imported)
	}
}

func TestImportRetainedUsageRejectsDuplicateLogicalRecordAndMissingPepper(t *testing.T) {
	q := New(nil)
	defer q.Close()
	peppers := map[uint64][]byte{
		2: []byte("retiring retained usage test key with enough entropy"),
		3: []byte("current retained usage test key with enough entropy"),
	}
	if err := q.ConfigureRetainedUsageKeys(3, peppers); err != nil {
		t.Fatal(err)
	}
	week := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	oldKey, _ := q.retainedUsageKey(2, "https://accounts.google.com", "subject")
	currentKey, _ := q.retainedUsageKey(3, "https://accounts.google.com", "subject")
	fixture := &retainedImportFixture{
		week: week, versions: []uint64{2},
		records: map[retainedImportKey]persistence.RetainedUsageRecord{
			{version: 2, digest: oldKey.IdentityDigest, week: week}:     {PepperVersion: 2, IdentityDigest: oldKey.IdentityDigest, Week: week, RecordedBytes: 701},
			{version: 3, digest: currentKey.IdentityDigest, week: week}: {PepperVersion: 3, IdentityDigest: currentKey.IdentityDigest, Week: week, RecordedBytes: 701},
		},
	}
	if err := q.ImportRetainedUsage(context.Background(), fixture, "fresh-account", "https://accounts.google.com", "subject"); err == nil {
		t.Fatal("duplicate logical retained usage was imported")
	}
	if len(fixture.imported) != 0 {
		t.Fatalf("duplicate import mutated account: %+v", fixture.imported)
	}
	fixture.versions = []uint64{9}
	if err := q.ImportRetainedUsage(context.Background(), fixture, "fresh-account", "https://accounts.google.com", "subject"); err == nil {
		t.Fatal("missing required retained-usage pepper was accepted")
	}
	if len(fixture.imported) != 0 {
		t.Fatalf("missing-key import mutated account: %+v", fixture.imported)
	}
}
