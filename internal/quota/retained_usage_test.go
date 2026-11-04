package quota

import (
	"crypto/hmac"
	"crypto/sha256"
	"testing"

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
