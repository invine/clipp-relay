package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPortalSessionPolicyChecksUncertaintyCSRFAndRate(t *testing.T) {
	server := &Server{
		Peppers:       map[uint64][]byte{3: []byte("test pepper")},
		CurrentPepper: 3,
		accountRates:  make(map[string]rate),
	}
	credential, err := server.portalSessionCredential(strings.Repeat("a", 43), 3)
	if err != nil {
		t.Fatal(err)
	}
	csrf := server.csrf(3, credential.value)
	proof := portalSessionProof{
		accountID: "account-1", subject: "owner-1",
		version: 3, csrfHash: server.digest(3, "csrf-store", csrf),
	}
	if got, err := server.verifyPortalSessionProof(credential, proof, true); err != nil || got != csrf {
		t.Fatalf("valid session proof = %q, %v", got, err)
	}

	uncertain := &Server{Peppers: server.Peppers, CurrentPepper: server.CurrentPepper, accountRates: make(map[string]rate)}
	uncertain.uncertainAccounts.Store(proof.accountID, struct{}{})
	if _, err := uncertain.verifyPortalSessionProof(credential, proof, true); !errors.Is(err, errInvalidSession) {
		t.Fatalf("uncertain account proof error = %v", err)
	}

	proof.csrfHash = make([]byte, 32)
	if _, err := server.verifyPortalSessionProof(credential, proof, true); !errors.Is(err, errInvalidPortalSessionCSRF) {
		t.Fatalf("invalid stored CSRF proof error = %v", err)
	}

	proof.csrfHash = server.digest(3, "csrf-store", csrf)
	now := time.Now()
	rateKey := base64RateKey(server.digest(3, "account-rate", googleIssuer+"\x00"+proof.subject))
	server.accountRates[rateKey] = rate{tokens: 0, at: now}
	if _, err := server.verifyPortalSessionProof(credential, proof, true); !errors.Is(err, errRateLimit) {
		t.Fatalf("rate-limited account proof error = %v", err)
	}
}

func base64RateKey(digest []byte) string {
	return base64.RawURLEncoding.EncodeToString(digest)
}
