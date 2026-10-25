package issuer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"clipp-relay/internal/auth/issuer"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
)

func TestCredentialLookupDigestSeparatesCredentialPurpose(t *testing.T) {
	const signature = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	code, err := issuer.LookupDigest(issuer.AuthorizationCode, signature)
	if err != nil {
		t.Fatal(err)
	}
	access, err := issuer.LookupDigest(issuer.AccessToken, signature)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := issuer.LookupDigest(issuer.RefreshToken, signature)
	if err != nil {
		t.Fatal(err)
	}
	if code == access || code == refresh || access == refresh {
		t.Fatal("credential lookup digest reused across purposes")
	}
	want := sha256.Sum256([]byte("clipp-relay/fosite/v1/authorization_code\x00" + signature))
	if code != want {
		t.Fatalf("authorization-code digest does not follow the storage contract")
	}
	if _, err := issuer.LookupDigest(issuer.AccessToken, ""); err == nil {
		t.Fatal("empty native signature accepted")
	}
}

func TestPinnedFositeNativeCredentialsUseTheExpectedOpaquePrefixes(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	config := &fosite.Config{GlobalSecret: secret, TokenEntropy: 32}
	strategy := compose.NewOAuth2HMACStrategy(config)
	request := fosite.NewRequest()
	request.SetSession(&fosite.DefaultSession{})
	ctx := context.Background()

	cases := []struct {
		name   string
		prefix string
		issue  func() (string, string, error)
	}{
		{name: "authorization code", prefix: "ory_ac_", issue: func() (string, string, error) { return strategy.GenerateAuthorizeCode(ctx, request) }},
		{name: "access token", prefix: "ory_at_", issue: func() (string, string, error) { return strategy.GenerateAccessToken(ctx, request) }},
		{name: "refresh token", prefix: "ory_rt_", issue: func() (string, string, error) { return strategy.GenerateRefreshToken(ctx, request) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, signature, err := tc.issue()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(token, tc.prefix) || len(token) != 94 {
				t.Fatalf("unexpected native credential shape: prefix=%q length=%d", tc.prefix, len(token))
			}
			if len(signature) != 43 {
				t.Fatalf("native signature length = %d, want 43", len(signature))
			}
			if _, err := base64.RawURLEncoding.DecodeString(signature); err != nil {
				t.Fatalf("native signature is not base64url: %v", err)
			}
		})
	}
}
