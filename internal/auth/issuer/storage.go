package issuer

import (
	"context"
	"errors"
	"net/url"
	"time"

	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
	oauth2 "github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/pkce"
)

type ClientType string

const (
	Electron  ClientType = "electron"
	Android   ClientType = "android"
	Extension ClientType = "extension"
)

type Principal struct {
	AccountID  string
	Generation int64
	Client     ClientType
	GrantID    string
}

func NewSession(p Principal) fosite.Session {
	session := &fosite.DefaultSession{}
	setPrincipal(session, p)
	return session
}

type persistenceTxKey struct{}
type authorizationTxKey struct{}
type refreshTxKey struct{}
type clientKey struct{}
type rawTokenKey struct{}
type operationKey struct{}
type operation struct{ replay, replayComplete bool }

// InPersistenceTransaction attaches the narrow access-token repository used
// during Discovery and Relay Authentication.
func InPersistenceTransaction(ctx context.Context, tx persistence.Transaction, client ClientType, token string) context.Context {
	ctx = context.WithValue(ctx, persistenceTxKey{}, tx)
	ctx = context.WithValue(ctx, clientKey{}, client)
	ctx = context.WithValue(ctx, rawTokenKey{}, token)
	return context.WithValue(ctx, operationKey{}, &operation{})
}

// InAuthorizationTransaction attaches the complete-operation repository set
// used by native authorization-code issuance and redemption.
func InAuthorizationTransaction(ctx context.Context, tx persistence.AuthorizationTransaction, client ClientType, token string) context.Context {
	ctx = context.WithValue(ctx, authorizationTxKey{}, tx)
	ctx = context.WithValue(ctx, persistenceTxKey{}, tx)
	ctx = context.WithValue(ctx, clientKey{}, client)
	ctx = context.WithValue(ctx, rawTokenKey{}, token)
	return context.WithValue(ctx, operationKey{}, &operation{})
}

// InRefreshTransaction attaches the complete refresh repository scope. The
// transaction remains owned and committed by the HTTP coordinator.
func InRefreshTransaction(ctx context.Context, tx persistence.RefreshTransaction, client ClientType, token string) context.Context {
	ctx = InAuthorizationTransaction(ctx, tx, client, token)
	return context.WithValue(ctx, refreshTxKey{}, tx)
}

func refreshTxFrom(ctx context.Context) (persistence.RefreshTransaction, bool) {
	tx, ok := ctx.Value(refreshTxKey{}).(persistence.RefreshTransaction)
	return tx, ok && tx != nil
}

func authorizationTxFrom(ctx context.Context) (persistence.AuthorizationTransaction, bool) {
	tx, ok := ctx.Value(authorizationTxKey{}).(persistence.AuthorizationTransaction)
	return tx, ok && tx != nil
}

func ReplayCanCommit(ctx context.Context) bool {
	op, _ := ctx.Value(operationKey{}).(*operation)
	return op != nil && op.replay && op.replayComplete
}

type Storage struct {
	clients map[string]*fosite.DefaultOpenIDConnectClient
	refresh oauth2.RefreshTokenStrategy
}

func NewStorage(redirects map[ClientType]string) *Storage {
	clients := make(map[string]*fosite.DefaultOpenIDConnectClient, 3)
	for _, id := range []ClientType{Electron, Android, Extension} {
		redirect := redirects[id]
		if id == Electron {
			// Fosite implements RFC 8252 dynamic loopback-port matching for this
			// registered IP-literal URI.
			redirect = "http://127.0.0.1/oauth/callback"
		}
		clients[string(id)] = &fosite.DefaultOpenIDConnectClient{
			DefaultClient: &fosite.DefaultClient{
				ID: string(id), Public: true,
				RedirectURIs:  []string{redirect},
				GrantTypes:    []string{"authorization_code", "refresh_token"},
				ResponseTypes: []string{"code"},
			},
			TokenEndpointAuthMethod: "none",
		}
	}
	return &Storage{clients: clients}
}

func (s *Storage) client(id string) (*fosite.DefaultOpenIDConnectClient, error) {
	client := s.clients[id]
	if client == nil {
		return nil, fosite.ErrNotFound
	}
	return client, nil
}

func (s *Storage) GetClient(_ context.Context, id string) (fosite.Client, error) {
	return s.client(id)
}

func (s *Storage) ClientAssertionJWTValid(context.Context, string) error {
	return errors.New("client assertions are disabled")
}

func (s *Storage) SetClientAssertionJWT(context.Context, string, time.Time) error {
	return errors.New("client assertions are disabled")
}

func expectedClient(ctx context.Context) ClientType {
	client, _ := ctx.Value(clientKey{}).(ClientType)
	return client
}

func (s *Storage) CreateAuthorizeCodeSession(ctx context.Context, signature string, req fosite.Requester) error {
	if tx, ok := authorizationTxFrom(ctx); ok {
		digest, err := LookupDigest(AuthorizationCode, signature)
		if err != nil {
			return err
		}
		p, err := principalFrom(req)
		if err != nil {
			return err
		}
		if p.GrantID == "" {
			p.GrantID = req.GetID()
		}
		if _, err = url.ParseRequestURI(req.GetRequestForm().Get("redirect_uri")); err != nil {
			return err
		}
		challenge := req.GetRequestForm().Get("code_challenge")
		if len(challenge) != 43 || p.AccountID == "" || p.Generation < 0 || req.GetClient().GetID() != string(p.Client) {
			return errors.New("incomplete native authorization request")
		}
		expires := req.GetSession().GetExpiresAt(fosite.AuthorizeCode)
		if expires.IsZero() {
			return errors.New("Fosite authorization-code expiry is missing")
		}
		return tx.StoreAuthorizationCode(ctx, digest, persistence.AuthorizationCode{AccountID: p.AccountID, Generation: p.Generation, ClientType: string(p.Client), RedirectURI: req.GetRequestForm().Get("redirect_uri"), Challenge: challenge, GrantID: p.GrantID, ExpiresAt: expires})
	}
	return errors.New("Fosite authorization-code operations require authorization repositories")
}
func (s *Storage) GetAuthorizeCodeSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	if tx, ok := authorizationTxFrom(ctx); ok {
		digest, err := LookupDigest(AuthorizationCode, signature)
		if err != nil {
			return nil, fosite.ErrNotFound
		}
		code, err := tx.LoadAuthorizationCode(ctx, digest)
		if errors.Is(err, persistence.ErrInvalidCredential) || code.Consumed {
			return nil, fosite.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		client, err := s.client(code.ClientType)
		expected := expectedClient(ctx)
		if err != nil || expected != "" && expected != ClientType(code.ClientType) {
			return nil, fosite.ErrNotFound
		}
		if session == nil {
			session = &fosite.DefaultSession{}
		}
		setPrincipal(session, Principal{AccountID: code.AccountID, Generation: code.Generation, Client: ClientType(code.ClientType), GrantID: code.GrantID})
		session.SetExpiresAt(fosite.AuthorizeCode, code.ExpiresAt)
		req := fosite.NewRequest()
		req.SetID(code.GrantID)
		req.Client = client
		req.Form = url.Values{"redirect_uri": {code.RedirectURI}, "code_challenge": {code.Challenge}, "code_challenge_method": {"S256"}}
		req.SetSession(session)
		req.SetRequestedScopes(nil)
		return req, nil
	}
	return nil, errors.New("Fosite authorization-code operations require authorization repositories")
}
func (s *Storage) InvalidateAuthorizeCodeSession(ctx context.Context, signature string) error {
	if tx, ok := authorizationTxFrom(ctx); ok {
		digest, err := LookupDigest(AuthorizationCode, signature)
		if err != nil {
			return fosite.ErrNotFound
		}
		if err = tx.ConsumeAuthorizationCode(ctx, digest); errors.Is(err, persistence.ErrInvalidCredential) {
			return fosite.ErrNotFound
		}
		return err
	}
	return errors.New("Fosite authorization-code operations require authorization repositories")
}
func (s *Storage) GetPKCERequestSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	if tx, ok := authorizationTxFrom(ctx); ok {
		digest, err := LookupDigest(AuthorizationCode, signature)
		if err != nil {
			return nil, fosite.ErrNotFound
		}
		code, err := tx.LoadAuthorizationCode(ctx, digest)
		if errors.Is(err, persistence.ErrInvalidCredential) || code.Consumed {
			return nil, fosite.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		client, err := s.client(code.ClientType)
		if err != nil {
			return nil, fosite.ErrNotFound
		}
		req := fosite.NewRequest()
		req.SetID(code.GrantID)
		req.Client = client
		req.Form = url.Values{"redirect_uri": {code.RedirectURI}, "code_challenge": {code.Challenge}, "code_challenge_method": {"S256"}}
		return req, nil
	}
	return nil, errors.New("Fosite authorization-code operations require authorization repositories")
}
func (s *Storage) CreatePKCERequestSession(context.Context, string, fosite.Requester) error {
	return nil
}
func (s *Storage) DeletePKCERequestSession(context.Context, string) error { return nil }

func (s *Storage) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	if tx, ok := refreshTxFrom(ctx); ok {
		digest, err := LookupDigest(AccessToken, signature)
		if err != nil {
			return err
		}
		p, err := principalFrom(req)
		if err != nil {
			return err
		}
		// Refresh rotation issues a replacement access token for the existing
		// Login Grant. Only authorization-code redemption creates a grant.
		expires := req.GetSession().GetExpiresAt(fosite.AccessToken)
		if expires.IsZero() {
			expires = time.Now().UTC().Add(15 * time.Minute)
		}
		return tx.StoreRelayAccessToken(ctx, digest, p.GrantID, expires)
	}
	if tx, ok := authorizationTxFrom(ctx); ok {
		digest, err := LookupDigest(AccessToken, signature)
		if err != nil {
			return err
		}
		p, err := principalFrom(req)
		if err != nil {
			return err
		}
		if err = tx.CreateLoginGrant(ctx, persistence.LoginGrant{ID: p.GrantID, AccountID: p.AccountID, Generation: p.Generation, ClientType: string(p.Client)}); err != nil {
			return err
		}
		expires := req.GetSession().GetExpiresAt(fosite.AccessToken)
		if expires.IsZero() {
			expires = time.Now().UTC().Add(15 * time.Minute)
		}
		return tx.StoreRelayAccessToken(ctx, digest, p.GrantID, expires)
	}
	return errors.New("Fosite access-token creation requires an authorization transaction")
}

func (s *Storage) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	tx, ok := ctx.Value(persistenceTxKey{}).(persistence.Transaction)
	if !ok || tx == nil {
		return nil, errors.New("Fosite access-token reads require a persistence transaction")
	}
	digest, err := LookupDigest(AccessToken, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	token, err := tx.AccessToken(ctx, digest)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return accessTokenRequest(s, token, session)
}

func accessTokenRequest(s *Storage, token persistence.AccessToken, session fosite.Session) (fosite.Requester, error) {
	client, err := s.client(token.ClientType)
	if err != nil {
		return nil, err
	}
	if session == nil {
		session = &fosite.DefaultSession{}
	}
	setPrincipal(session, Principal{AccountID: token.AccountID, GrantID: token.GrantID, Generation: token.Generation, Client: ClientType(token.ClientType)})
	session.SetExpiresAt(fosite.AccessToken, token.ExpiresAt)
	req := fosite.NewRequest()
	req.SetID(token.GrantID)
	req.Client = client
	req.SetSession(session)
	return req, nil
}

func (s *Storage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	digest, err := LookupDigest(AccessToken, signature)
	if err != nil {
		return nil
	}
	if tx, ok := authorizationTxFrom(ctx); ok {
		return tx.DeleteRelayAccessToken(ctx, digest)
	}
	return errors.New("Fosite access-token deletion requires an authorization transaction")
}

func (s *Storage) CreateRefreshTokenSession(ctx context.Context, signature, _ string, req fosite.Requester) error {
	if tx, ok := refreshTxFrom(ctx); ok {
		digest, err := LookupDigest(RefreshToken, signature)
		if err != nil {
			return err
		}
		p, err := principalFrom(req)
		if err != nil {
			return err
		}
		generation, err := tx.CurrentRefreshGeneration(ctx, p.GrantID)
		if err != nil {
			return err
		}
		return tx.StoreRefreshGeneration(ctx, digest, p.GrantID, generation)
	}
	tx, ok := authorizationTxFrom(ctx)
	if !ok {
		return errors.New("Fosite refresh-token creation requires an authorization transaction")
	}
	digest, err := LookupDigest(RefreshToken, signature)
	if err != nil {
		return err
	}
	p, err := principalFrom(req)
	if err != nil {
		return err
	}
	generation, err := tx.CurrentRefreshGeneration(ctx, p.GrantID)
	if err != nil {
		return err
	}
	return tx.StoreRefreshGeneration(ctx, digest, p.GrantID, generation)
}

func (s *Storage) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	if tx, ok := refreshTxFrom(ctx); ok {
		return s.getRefreshTokenSession(ctx, tx, signature, session)
	}
	return nil, errors.New("Fosite refresh-token reads require a refresh transaction")
}

func (s *Storage) getRefreshTokenSession(ctx context.Context, tx persistence.RefreshTransaction, signature string, session fosite.Session) (fosite.Requester, error) {
	digest, err := LookupDigest(RefreshToken, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	c, err := tx.LoadRefreshCredential(ctx, digest)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if expected := expectedClient(ctx); expected == "" || string(expected) != c.ClientType {
		return nil, fosite.ErrNotFound
	}
	client, err := s.client(c.ClientType)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	if session == nil {
		session = &fosite.DefaultSession{}
	}
	p := Principal{AccountID: c.AccountID, Generation: c.AccountGeneration, Client: ClientType(c.ClientType), GrantID: c.GrantID}
	setPrincipal(session, p)
	expiry := c.IdleExpiresAt
	if c.AbsoluteExpiresAt.Before(expiry) {
		expiry = c.AbsoluteExpiresAt
	}
	session.SetExpiresAt(fosite.RefreshToken, expiry)
	req := fosite.NewRequest()
	req.SetID(c.GrantID)
	req.Client = client
	req.SetSession(session)
	if c.Consumed {
		if !c.Active {
			return nil, fosite.ErrNotFound
		}
		if op, ok := ctx.Value(operationKey{}).(*operation); ok {
			op.replay = true
		}
		raw, _ := ctx.Value(rawTokenKey{}).(string)
		if raw == "" || s.refresh == nil || s.refresh.ValidateRefreshToken(ctx, req, raw) != nil {
			return nil, fosite.ErrNotFound
		}
		return req, fosite.ErrInactiveToken
	}
	current, err := tx.CurrentRefreshGeneration(ctx, c.GrantID)
	if err != nil {
		return nil, err
	}
	if !c.Active || current != c.Generation {
		return nil, fosite.ErrNotFound
	}
	return req, nil
}

func (s *Storage) DeleteRefreshTokenSession(context.Context, string) error { return nil }

func (s *Storage) RotateRefreshToken(ctx context.Context, requestID, signature string) error {
	if tx, ok := refreshTxFrom(ctx); ok {
		digest, err := LookupDigest(RefreshToken, signature)
		if err != nil {
			return fosite.ErrNotFound
		}
		return tx.RotateRefreshCredential(ctx, digest, requestID)
	}
	return errors.New("Fosite refresh rotation requires a refresh transaction")
}

func (s *Storage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	if tx, ok := refreshTxFrom(ctx); ok {
		_, err := tx.RevokeRefreshGrant(ctx, requestID)
		return err
	}
	return errors.New("Fosite refresh revocation requires a refresh transaction")
}

func (s *Storage) RevokeAccessToken(ctx context.Context, requestID string) error {
	if tx, ok := refreshTxFrom(ctx); ok {
		err := tx.DeleteGrantAccessTokens(ctx, requestID)
		if err == nil {
			if op, ok := ctx.Value(operationKey{}).(*operation); ok && op.replay {
				op.replayComplete = true
			}
		}
		return err
	}
	return errors.New("Fosite refresh replay invalidation requires a refresh transaction")
}

func principalFrom(req fosite.Requester) (Principal, error) {
	if req == nil || req.GetSession() == nil {
		return Principal{}, errors.New("native request session missing")
	}
	extra, ok := req.GetSession().(fosite.ExtraClaimsSession)
	if !ok {
		return Principal{}, errors.New("native request session has no typed extra fields")
	}
	m := extra.GetExtraClaims()
	account, _ := m["clipp_account_id"].(string)
	generation, ok := m["clipp_credential_generation"].(int64)
	if !ok {
		if n, ok2 := m["clipp_credential_generation"].(float64); ok2 {
			generation = int64(n)
		}
	}
	client := ClientType(req.GetClient().GetID())
	grantID, _ := m["clipp_grant_id"].(string)
	if grantID == "" {
		grantID = req.GetID()
	}
	if account == "" || generation < 0 || client != Electron && client != Android && client != Extension || grantID == "" {
		return Principal{}, errors.New("native request session fields invalid")
	}
	return Principal{AccountID: account, Generation: generation, Client: client, GrantID: grantID}, nil
}
func setPrincipal(session fosite.Session, p Principal) {
	if extra, ok := session.(fosite.ExtraClaimsSession); ok {
		m := extra.GetExtraClaims()
		m["clipp_account_id"] = p.AccountID
		m["clipp_credential_generation"] = p.Generation
		m["clipp_grant_id"] = p.GrantID
	}
}

var _ fosite.Storage = (*Storage)(nil)
var _ oauth2.CoreStorage = (*Storage)(nil)
var _ oauth2.TokenRevocationStorage = (*Storage)(nil)
var _ pkce.PKCERequestStorage = (*Storage)(nil)
