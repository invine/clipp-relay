package issuer

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
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

type txKey struct{}
type clientKey struct{}
type rawTokenKey struct{}
type operationKey struct{}
type operation struct{ replay, replayComplete bool }

// InTransaction attaches the caller-owned transaction. This storage deliberately
// does not implement Fosite's Transactional hook: grant operations commit once,
// in the HTTP coordinator, after all policy and audit writes have succeeded.
func InTransaction(ctx context.Context, tx pgx.Tx, client ClientType, token string) context.Context {
	op := &operation{}
	ctx = context.WithValue(ctx, txKey{}, tx)
	ctx = context.WithValue(ctx, clientKey{}, client)
	ctx = context.WithValue(ctx, rawTokenKey{}, token)
	return context.WithValue(ctx, operationKey{}, op)
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

func txFrom(ctx context.Context) (pgx.Tx, error) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	if !ok || tx == nil {
		return nil, errors.New("native issuer operation has no caller-owned transaction")
	}
	return tx, nil
}

func expectedClient(ctx context.Context) ClientType {
	client, _ := ctx.Value(clientKey{}).(ClientType)
	return client
}

func (s *Storage) CreateAuthorizeCodeSession(ctx context.Context, signature string, req fosite.Requester) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
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
	_, err = tx.Exec(ctx, `INSERT INTO public.native_authorization_codes(credential_digest,account_id,credential_generation,client_type,redirect_uri,code_challenge,grant_id,issued_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp(),$8)`, digest[:], p.AccountID, p.Generation, p.Client, req.GetRequestForm().Get("redirect_uri"), challenge, p.GrantID, expires)
	return err
}

func (s *Storage) GetAuthorizeCodeSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	tx, err := txFrom(ctx)
	if err != nil {
		return nil, err
	}
	digest, err := LookupDigest(AuthorizationCode, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	var p Principal
	var redirect, challenge string
	var expires time.Time
	var consumed bool
	err = tx.QueryRow(ctx, `SELECT account_id::text,credential_generation,client_type,redirect_uri,code_challenge,grant_id::text,consumed_at IS NOT NULL,expires_at FROM public.native_authorization_codes WHERE credential_digest=$1 AND expires_at>clock_timestamp() FOR UPDATE`, digest[:]).Scan(&p.AccountID, &p.Generation, &p.Client, &redirect, &challenge, &p.GrantID, &consumed, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if consumed {
		return nil, fosite.ErrNotFound
	}
	client, err := s.client(string(p.Client))
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	if expected := expectedClient(ctx); expected != "" && expected != p.Client {
		return nil, fosite.ErrNotFound
	}
	if session == nil {
		session = &fosite.DefaultSession{}
	}
	setPrincipal(session, p)
	session.SetExpiresAt(fosite.AuthorizeCode, expires)
	req := fosite.NewRequest()
	req.SetID(p.GrantID)
	req.Client = client
	req.Form = url.Values{"redirect_uri": {redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	req.SetSession(session)
	req.SetRequestedScopes(nil)
	return req, nil
}

func (s *Storage) InvalidateAuthorizeCodeSession(ctx context.Context, signature string) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	digest, err := LookupDigest(AuthorizationCode, signature)
	if err != nil {
		return fosite.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `UPDATE public.native_authorization_codes SET consumed_at=clock_timestamp() WHERE credential_digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, digest[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fosite.ErrNotFound
	}
	return nil
}

func (s *Storage) GetPKCERequestSession(ctx context.Context, signature string, _ fosite.Session) (fosite.Requester, error) {
	tx, err := txFrom(ctx)
	if err != nil {
		return nil, err
	}
	digest, err := LookupDigest(AuthorizationCode, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	var p Principal
	var challenge, redirect string
	err = tx.QueryRow(ctx, `SELECT account_id::text,credential_generation,client_type,grant_id::text,code_challenge,redirect_uri FROM public.native_authorization_codes WHERE credential_digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`, digest[:]).Scan(&p.AccountID, &p.Generation, &p.Client, &p.GrantID, &challenge, &redirect)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	client, err := s.client(string(p.Client))
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	req := fosite.NewRequest()
	req.SetID(p.GrantID)
	req.Client = client
	req.Form = url.Values{"redirect_uri": {redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return req, nil
}

// PKCE storage shares the typed challenge on the authorization-code record.
// Fosite calls Delete before validating the verifier; leaving it in place is
// necessary so an invalid verifier cannot consume an otherwise valid code.
func (s *Storage) CreatePKCERequestSession(context.Context, string, fosite.Requester) error {
	return nil
}
func (s *Storage) DeletePKCERequestSession(context.Context, string) error { return nil }

func (s *Storage) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	digest, err := LookupDigest(AccessToken, signature)
	if err != nil {
		return err
	}
	p, err := principalFrom(req)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,current_refresh_generation,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,$3,$4,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days') ON CONFLICT(id) DO NOTHING`, p.GrantID, p.AccountID, p.Generation, p.Client); err != nil {
		return err
	}
	expires := req.GetSession().GetExpiresAt(fosite.AccessToken)
	if expires.IsZero() {
		expires = time.Now().UTC().Add(15 * time.Minute)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO public.native_access_tokens(credential_digest,grant_id,issued_at,expires_at) SELECT $1,$2,clock_timestamp(),$3 FROM public.native_login_grants g JOIN public.accounts a ON a.id=g.account_id WHERE g.id=$2 AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp() AND a.status='Active' AND NOT a.recovery_review_hold AND a.credential_generation=g.credential_generation`, digest[:], p.GrantID, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fosite.ErrNotFound
	}
	return nil
}

func (s *Storage) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	tx, err := txFrom(ctx)
	if err != nil {
		return nil, err
	}
	digest, err := LookupDigest(AccessToken, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	var p Principal
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT g.account_id::text,g.credential_generation,g.client_type,g.id::text,a.expires_at FROM public.native_access_tokens a JOIN public.native_login_grants g ON g.id=a.grant_id JOIN public.accounts c ON c.id=g.account_id WHERE a.credential_digest=$1 AND a.expires_at>clock_timestamp() AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp() AND c.status='Active' AND NOT c.recovery_review_hold AND c.credential_generation=g.credential_generation`, digest[:]).Scan(&p.AccountID, &p.Generation, &p.Client, &p.GrantID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	client, err := s.client(string(p.Client))
	if err != nil {
		return nil, err
	}
	if session == nil {
		session = &fosite.DefaultSession{}
	}
	setPrincipal(session, p)
	session.SetExpiresAt(fosite.AccessToken, expires)
	req := fosite.NewRequest()
	req.SetID(p.GrantID)
	req.Client = client
	req.SetSession(session)
	return req, nil
}

func (s *Storage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	digest, err := LookupDigest(AccessToken, signature)
	if err != nil {
		return nil
	}
	_, err = tx.Exec(ctx, `DELETE FROM public.native_access_tokens WHERE credential_digest=$1`, digest[:])
	return err
}

func (s *Storage) CreateRefreshTokenSession(ctx context.Context, signature, _ string, req fosite.Requester) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	digest, err := LookupDigest(RefreshToken, signature)
	if err != nil {
		return err
	}
	p, err := principalFrom(req)
	if err != nil {
		return err
	}
	var generation int64
	err = tx.QueryRow(ctx, `SELECT current_refresh_generation FROM public.native_login_grants WHERE id=$1 AND terminated_at IS NULL FOR UPDATE`, p.GrantID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return fosite.ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.native_refresh_generations(credential_digest,grant_id,generation,issued_at) VALUES($1,$2,$3,clock_timestamp())`, digest[:], p.GrantID, generation)
	return err
}

func (s *Storage) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	tx, err := txFrom(ctx)
	if err != nil {
		return nil, err
	}
	digest, err := LookupDigest(RefreshToken, signature)
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	var p Principal
	var generation int64
	var consumed bool
	var active bool
	var idleExpiry, absoluteExpiry time.Time
	err = tx.QueryRow(ctx, `SELECT g.account_id::text,g.credential_generation,g.client_type,g.id::text,r.generation,r.consumed_at IS NOT NULL,(g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp() AND a.status='Active' AND NOT a.recovery_review_hold AND a.credential_generation=g.credential_generation),g.idle_expires_at,g.absolute_expires_at FROM public.native_refresh_generations r JOIN public.native_login_grants g ON g.id=r.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE r.credential_digest=$1 FOR UPDATE OF r,g`, digest[:]).Scan(&p.AccountID, &p.Generation, &p.Client, &p.GrantID, &generation, &consumed, &active, &idleExpiry, &absoluteExpiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if expected := expectedClient(ctx); expected == "" || expected != p.Client {
		return nil, fosite.ErrNotFound
	}
	client, err := s.client(string(p.Client))
	if err != nil {
		return nil, fosite.ErrNotFound
	}
	if session == nil {
		session = &fosite.DefaultSession{}
	}
	setPrincipal(session, p)
	refreshExpiry := idleExpiry
	if absoluteExpiry.Before(refreshExpiry) {
		refreshExpiry = absoluteExpiry
	}
	session.SetExpiresAt(fosite.RefreshToken, refreshExpiry)
	req := fosite.NewRequest()
	req.SetID(p.GrantID)
	req.Client = client
	req.SetSession(session)
	if consumed {
		if !active {
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
	var current int64
	if err = tx.QueryRow(ctx, `SELECT current_refresh_generation FROM public.native_login_grants WHERE id=$1`, p.GrantID).Scan(&current); err != nil {
		return nil, err
	}
	if !active || current != generation {
		return nil, fosite.ErrNotFound
	}
	return req, nil
}

func (s *Storage) DeleteRefreshTokenSession(context.Context, string) error { return nil }

func (s *Storage) RotateRefreshToken(ctx context.Context, requestID, signature string) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	digest, err := LookupDigest(RefreshToken, signature)
	if err != nil {
		return fosite.ErrNotFound
	}
	var grantID string
	err = tx.QueryRow(ctx, `UPDATE public.native_refresh_generations SET consumed_at=clock_timestamp() WHERE credential_digest=$1 AND consumed_at IS NULL RETURNING grant_id::text`, digest[:]).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fosite.ErrNotFound
	}
	if err != nil {
		return err
	}
	if grantID != requestID {
		return fosite.ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE public.native_login_grants SET current_refresh_generation=current_refresh_generation+1,last_used_at=clock_timestamp(),idle_expires_at=LEAST(absolute_expires_at,clock_timestamp()+interval '30 days') WHERE id=$1 AND terminated_at IS NULL`, grantID)
	return err
}

func (s *Storage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	var account, client string
	err = tx.QueryRow(ctx, `UPDATE public.native_login_grants SET terminated_at=COALESCE(terminated_at,clock_timestamp()) WHERE id=$1 RETURNING account_id::text,client_type`, requestID).Scan(&account, &client)
	if errors.Is(err, pgx.ErrNoRows) {
		return fosite.ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,client_type) VALUES(gen_random_uuid(),clock_timestamp(),'refresh_reuse',$1,$2)`, account, client)
	return err
}

func (s *Storage) RevokeAccessToken(ctx context.Context, requestID string) error {
	tx, err := txFrom(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM public.native_access_tokens WHERE grant_id=$1`, requestID)
	if err == nil {
		if op, ok := ctx.Value(operationKey{}).(*operation); ok && op.replay {
			op.replayComplete = true
		}
	}
	return err
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
