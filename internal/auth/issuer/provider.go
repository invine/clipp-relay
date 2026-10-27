package issuer

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	oauth2 "github.com/ory/fosite/handler/oauth2"
)

type Redirects map[ClientType]string

// Issuer holds Fosite behind the private auth boundary. Its composition exposes
// only authorization_code and refresh_token grants, public clients, and S256.
type Issuer struct {
	provider     fosite.OAuth2Provider
	storage      *Storage
	strategy     *oauth2.HMACSHAStrategy
	validationMu sync.Mutex
	validations  map[*accessValidation]struct{}
}

type accessValidation struct{ cancel context.CancelFunc }

func NewProvider(secret []byte, redirects Redirects, storage *Storage) *Issuer {
	if storage == nil {
		storage = NewStorage(redirects)
	}
	config := &fosite.Config{
		GlobalSecret:                   append([]byte(nil), secret...),
		TokenEntropy:                   32,
		AuthorizeCodeLifespan:          5 * time.Minute,
		AccessTokenLifespan:            15 * time.Minute,
		RefreshTokenLifespan:           180 * 24 * time.Hour,
		RefreshTokenScopes:             []string{},
		OmitRedirectScopeParam:         true,
		SanitationWhiteList:            []string{"code", "redirect_uri", "code_challenge", "code_challenge_method"},
		EnforcePKCEForPublicClients:    true,
		EnablePKCEPlainChallengeMethod: false,
		RedirectSecureChecker: func(_ context.Context, u *url.URL) bool {
			return u.Scheme == "https" || u.Scheme == "http" && u.Hostname() == "127.0.0.1" || u.Scheme == "clipp-relay"
		},
		ClientAuthenticationStrategy: publicClientAuthentication(storage),
	}
	strategy := compose.NewOAuth2HMACStrategy(config)
	storage.refresh = strategy
	provider := compose.Compose(config, storage, strategy,
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2PKCEFactory,
	)
	return &Issuer{provider: provider, storage: storage, strategy: strategy, validations: make(map[*accessValidation]struct{})}
}

func (i *Issuer) TokenDigest(purpose Purpose, token string) ([32]byte, error) {
	prefix := map[Purpose]string{AuthorizationCode: "ory_ac_", AccessToken: "ory_at_", RefreshToken: "ory_rt_"}[purpose]
	if prefix == "" || len(token) != 94 || token[:7] != prefix || token[50] != '.' {
		return [32]byte{}, errInvalidSignature
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return [32]byte{}, errInvalidSignature
		}
	}
	if strings.Count(token, ".") != 1 {
		return [32]byte{}, errInvalidSignature
	}
	message, err := base64.RawURLEncoding.DecodeString(token[7:50])
	if err != nil || len(message) != 32 {
		return [32]byte{}, errInvalidSignature
	}
	var signature string
	switch purpose {
	case AuthorizationCode:
		signature = i.strategy.AuthorizeCodeSignature(context.Background(), token)
	case AccessToken:
		signature = i.strategy.AccessTokenSignature(context.Background(), token)
	case RefreshToken:
		signature = i.strategy.RefreshTokenSignature(context.Background(), token)
	}
	return LookupDigest(purpose, signature)
}

func publicClientAuthentication(storage *Storage) fosite.ClientAuthenticationStrategy {
	return func(ctx context.Context, r *http.Request, form url.Values) (fosite.Client, error) {
		if r.Header.Get("Authorization") != "" || form.Get("client_secret") != "" || form.Get("client_assertion") != "" || form.Get("client_assertion_type") != "" {
			return nil, fosite.ErrInvalidClient.WithHint("Only registered public clients are accepted.")
		}
		clientID := form.Get("client_id")
		if clientID == "" {
			return nil, fosite.ErrInvalidClient
		}
		client, err := storage.GetClient(ctx, clientID)
		if err != nil {
			return nil, fosite.ErrInvalidClient
		}
		if !client.IsPublic() {
			return nil, errors.New("native issuer client is not public")
		}
		return client, nil
	}
}

func (i *Issuer) NewAuthorizeRequest(ctx context.Context, r *http.Request) (fosite.AuthorizeRequester, error) {
	return i.provider.NewAuthorizeRequest(ctx, r)
}

func (i *Issuer) NewAuthorizeResponse(ctx context.Context, req fosite.AuthorizeRequester, session fosite.Session) (fosite.AuthorizeResponder, error) {
	return i.provider.NewAuthorizeResponse(ctx, req, session)
}

func (i *Issuer) WriteAuthorizeError(ctx context.Context, w http.ResponseWriter, req fosite.AuthorizeRequester, err error) {
	i.provider.WriteAuthorizeError(ctx, w, req, err)
}

func (i *Issuer) NewAccessRequest(ctx context.Context, r *http.Request, session fosite.Session) (fosite.AccessRequester, error) {
	return i.provider.NewAccessRequest(ctx, r, session)
}

func (i *Issuer) NewAccessResponse(ctx context.Context, req fosite.AccessRequester) (fosite.AccessResponder, error) {
	return i.provider.NewAccessResponse(ctx, req)
}

func (i *Issuer) AuthenticateAccess(ctx context.Context, raw string) (Principal, time.Time, error) {
	validationCtx, cancel := context.WithCancel(ctx)
	validation := &accessValidation{cancel: cancel}
	i.validationMu.Lock()
	i.validations[validation] = struct{}{}
	i.validationMu.Unlock()
	defer func() {
		cancel()
		i.validationMu.Lock()
		delete(i.validations, validation)
		i.validationMu.Unlock()
	}()
	ctx = validationCtx
	if _, err := i.TokenDigest(AccessToken, raw); err != nil {
		return Principal{}, time.Time{}, fosite.ErrNotFound
	}
	signature := i.strategy.AccessTokenSignature(ctx, raw)
	request, err := i.storage.GetAccessTokenSession(ctx, signature, &fosite.DefaultSession{})
	if err != nil {
		return Principal{}, time.Time{}, err
	}
	if err = i.strategy.ValidateAccessToken(ctx, request, raw); err != nil {
		return Principal{}, time.Time{}, fosite.ErrNotFound
	}
	principal, err := principalFrom(request)
	if err != nil {
		return Principal{}, time.Time{}, fosite.ErrNotFound
	}
	return principal, request.GetSession().GetExpiresAt(fosite.AccessToken), nil
}

// CancelPendingValidations stops only active access-token validation calls.
// Each call owns a child context; no issuance or refresh operation is canceled.
func (i *Issuer) CancelPendingValidations() {
	i.validationMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(i.validations))
	for validation := range i.validations {
		cancels = append(cancels, validation.cancel)
	}
	i.validationMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (i *Issuer) WriteAccessResponse(ctx context.Context, w http.ResponseWriter, req fosite.AccessRequester, response fosite.AccessResponder) {
	i.provider.WriteAccessResponse(ctx, w, req, response)
}

func (i *Issuer) Store() *Storage { return i.storage }

var _ oauth2.RefreshTokenStrategy = compose.NewOAuth2HMACStrategy(&fosite.Config{})
