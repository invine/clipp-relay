package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
)

type controlledPersistence struct {
	token      persistence.AccessToken
	credential persistence.RelayCredential
	err        error
	closed     bool
}

func (p *controlledPersistence) Start(context.Context) (persistence.Operation, error) {
	return &controlledOperation{parent: p}, nil
}

type controlledOperation struct{ parent *controlledPersistence }

func (o *controlledOperation) Context() context.Context { return context.Background() }
func (o *controlledOperation) Close()                   { o.parent.closed = true }
func (o *controlledOperation) Begin(context.Context) (persistence.Transaction, error) {
	return &controlledTransaction{parent: o.parent}, nil
}

type controlledTransaction struct{ parent *controlledPersistence }

func (t *controlledTransaction) AccessToken(context.Context, [32]byte) (persistence.AccessToken, error) {
	return t.parent.token, t.parent.err
}
func (t *controlledTransaction) RelayCredential(context.Context, [32]byte, string, string) (persistence.RelayCredential, error) {
	return t.parent.credential, t.parent.err
}
func (*controlledTransaction) Commit(context.Context) error   { return nil }
func (*controlledTransaction) Rollback(context.Context) error { return nil }

func relayCredentialFixture(t *testing.T) (string, persistence.AccessToken, persistence.RelayCredential) {
	t.Helper()
	secret := []byte("01234567890123456789012345678901")
	issuerServer := issuer.NewProvider(secret, nil, nil)
	client, err := issuerServer.Store().GetClient(context.Background(), string(issuer.Android))
	if err != nil {
		t.Fatal(err)
	}
	session := &fosite.DefaultSession{}
	claims := session.GetExtraClaims()
	claims["clipp_account_id"] = "account-1"
	claims["clipp_credential_generation"] = int64(3)
	claims["clipp_grant_id"] = "grant-2"
	session.SetExpiresAt(fosite.AccessToken, time.Now().Add(5*time.Minute))
	request := fosite.NewRequest()
	request.Client = client
	request.SetID("grant-2")
	request.SetSession(session)
	strategy := compose.NewOAuth2HMACStrategy(&fosite.Config{GlobalSecret: secret, TokenEntropy: 32})
	token, _, err := strategy.GenerateAccessToken(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	stored := persistence.AccessToken{AccountID: "account-1", GrantID: "grant-2", Generation: 3, ClientType: string(issuer.Android), ExpiresAt: session.GetExpiresAt(fosite.AccessToken)}
	credential := persistence.RelayCredential{AccessToken: stored, AccountStatus: "Active", SessionLimit: 5, DatabaseNow: time.Now()}
	return token, stored, credential
}

func TestAuthenticateRelayUsesControlledRepositoryAndClassifiesOutcomes(t *testing.T) {
	token, stored, credential := relayCredentialFixture(t)
	p := &controlledPersistence{token: stored, credential: credential}
	s := &Server{persistence: p, nativeIssuer: issuer.NewProvider([]byte("01234567890123456789012345678901"), nil, nil)}
	got, err := s.AuthenticateRelay(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "account-1" || got.GrantID != "grant-2" || got.Generation != 3 || got.SessionLimit != 5 {
		t.Fatalf("unexpected credential projection: %+v", got)
	}
	if !p.closed {
		t.Fatal("repository operation was not released after validation")
	}

	p.err = persistence.ErrInvalidCredential
	if _, err = s.AuthenticateRelay(context.Background(), token); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("invalid repository credential error = %v", err)
	}
	p.err = persistence.ErrUnavailable
	if _, err = s.AuthenticateRelay(context.Background(), token); !errors.Is(err, persistence.ErrUnavailable) || errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("unavailable repository error = %v", err)
	}
}
