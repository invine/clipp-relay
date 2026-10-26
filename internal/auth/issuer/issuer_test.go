package issuer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
)

type accessRepository struct {
	token persistence.AccessToken
	err   error
}

func (r accessRepository) AccessToken(context.Context, [32]byte) (persistence.AccessToken, error) {
	return r.token, r.err
}
func (accessRepository) RelayCredential(context.Context, [32]byte, string, string) (persistence.RelayCredential, error) {
	return persistence.RelayCredential{}, nil
}
func (accessRepository) Commit(context.Context) error   { return nil }
func (accessRepository) Rollback(context.Context) error { return nil }

func TestFositeAccessSessionUsesTypedRepositoryProjection(t *testing.T) {
	storage := issuer.NewStorage(issuer.Redirects{issuer.Android: "clipp-relay://oauth/android"})
	tx := accessRepository{token: persistence.AccessToken{
		AccountID: "account-7", GrantID: "grant-9", Generation: 4,
		ClientType: string(issuer.Android), ExpiresAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}}
	ctx := issuer.InPersistenceTransaction(context.Background(), tx, issuer.Android, "opaque")
	request, err := storage.GetAccessTokenSession(ctx, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), &fosite.DefaultSession{})
	if err != nil {
		t.Fatal(err)
	}
	if request.GetID() != "grant-9" || request.GetClient().GetID() != "android" {
		t.Fatalf("repository projection produced request id/client %q/%q", request.GetID(), request.GetClient().GetID())
	}
	claims := request.GetSession().(fosite.ExtraClaimsSession).GetExtraClaims()
	if claims["clipp_account_id"] != "account-7" || claims["clipp_credential_generation"] != int64(4) || claims["clipp_grant_id"] != "grant-9" {
		t.Fatalf("repository identity was not retained in the Fosite session: %#v", claims)
	}
	if got := request.GetSession().GetExpiresAt(fosite.AccessToken); !got.Equal(tx.token.ExpiresAt) {
		t.Fatalf("access expiry = %s, want repository expiry %s", got, tx.token.ExpiresAt)
	}
}

func TestFositeAccessSessionDistinguishesInvalidFromUnavailable(t *testing.T) {
	storage := issuer.NewStorage(nil)
	signature := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	ctx := issuer.InPersistenceTransaction(context.Background(), accessRepository{err: persistence.ErrInvalidCredential}, "", "")
	if _, err := storage.GetAccessTokenSession(ctx, signature, &fosite.DefaultSession{}); !errors.Is(err, fosite.ErrNotFound) {
		t.Fatalf("invalid credential error = %v, want Fosite not-found", err)
	}
	ctx = issuer.InPersistenceTransaction(context.Background(), accessRepository{err: persistence.ErrUnavailable}, "", "")
	if _, err := storage.GetAccessTokenSession(ctx, signature, &fosite.DefaultSession{}); !errors.Is(err, persistence.ErrUnavailable) || errors.Is(err, fosite.ErrNotFound) {
		t.Fatalf("unavailable repository error = %v, want distinct temporary failure", err)
	}
}

func TestPublicClientsRejectJWTClientAssertionsBeforeJWKLookup(t *testing.T) {
	provider := issuer.NewProvider(bytes.Repeat([]byte{0x42}, 32), nil, nil)
	request := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(url.Values{
		"grant_type":            {"refresh_token"},
		"client_id":             {"android"},
		"refresh_token":         {"ory_rt_" + strings.Repeat("a", 87)},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbmRyb2lkIn0.signature"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	_, err := provider.NewAccessRequest(context.Background(), request, &fosite.DefaultSession{})
	if err == nil || !errors.Is(err, fosite.ErrInvalidClient) {
		t.Fatalf("public client assertion was not rejected before JWK lookup: %v", err)
	}
}

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
