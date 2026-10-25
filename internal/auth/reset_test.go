package auth_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/relay"
	"github.com/jackc/pgx/v5"
	ma "github.com/multiformats/go-multiaddr"
)

func TestZZNativeOAuthResetIsIdempotentAndPreservesAccountState(t *testing.T) {
	_, serving, runtime := fixture(t)
	migrationPath := os.Getenv("CLIPP_TEST_MIGRATION_CONFIG")
	if migrationPath == "" {
		t.Skip("migration credentials not configured")
	}
	migrationConfig, err := config.Load(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	migrationMaterial, err := migrationConfig.ReadMaterial()
	if err != nil {
		t.Fatal(err)
	}
	migrationPool, err := database.NewPool(context.Background(), migrationConfig, migrationMaterial, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrationPool.Close()
	ctx := context.Background()
	account := "9e4120b7-1662-4b95-92c9-978c503d3501"
	grant := "e05b0e50-df52-4b63-b77b-b49f1d53b145"
	_, err = runtime.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id)
VALUES($1,'https://accounts.google.com','reset-test','reset@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, account)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Pool.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1; DELETE FROM public.weekly_relay_usage WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, account)
	_, err = runtime.Pool.Exec(ctx, `INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES(decode(repeat('aa',32),'hex'),$1,$2,0,decode(repeat('bb',32),'hex'),clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`, serving.CurrentPepper, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Pool.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes)
VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,73,1,'f2153fa4-b7d6-4d53-bf61-26898a6b6103',73)`, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrationPool.Exec(ctx, `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES($1,$2,0,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 day',clock_timestamp()+interval '2 days')`, grant, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrationPool.Exec(ctx, `INSERT INTO public.native_access_tokens(credential_digest,grant_id,issued_at,expires_at)
VALUES(decode(repeat('cc',32),'hex'),$1,clock_timestamp(),clock_timestamp()+interval '15 minutes')`, grant)
	if err != nil {
		t.Fatal(err)
	}
	legacyGrant := "0b824e69-2f4f-4e91-88ce-9e334bec9ae1"
	_, err = migrationPool.Exec(ctx, `INSERT INTO public.login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES($1,$2,0,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 day',clock_timestamp()+interval '2 days')`, legacyGrant, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrationPool.Exec(ctx, `INSERT INTO public.refresh_generations(credential_digest,pepper_version,grant_id,generation,issued_at)
VALUES(decode(repeat('dd',32),'hex'),$1,$2,1,clock_timestamp())`, serving.CurrentPepper, legacyGrant)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, key); !errors.Is(err, auth.ErrNativeIssuerResetRequired) {
		t.Fatalf("startup accepted key without a completed reset: %v", err)
	}
	var auditBaseline int
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE event='credential_reset'`).Scan(&auditBaseline); err != nil {
		t.Fatal(err)
	}
	commitAckLost := errors.New("simulated lost reset commit acknowledgement")
	reset, err := auth.ResetNativeOAuthCredentialsWithCommitHookForTest(ctx, migrationPool, key, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return commitAckLost
	})
	if reset || !errors.Is(err, commitAckLost) {
		t.Fatalf("uncertain reset result=%v err=%v", reset, err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, key); err != nil {
		t.Fatalf("serving rejected confirmed key: %v", err)
	}
	var auditCount int
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE event='credential_reset'`).Scan(&auditCount); err != nil || auditCount != auditBaseline+1 {
		t.Fatalf("initial reset audit rows=%d want=%d err=%v", auditCount, auditBaseline+1, err)
	}
	var preserved bool
	if err := runtime.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.accounts WHERE id=$1 AND status='Active')
AND EXISTS(SELECT 1 FROM public.portal_sessions WHERE account_id=$1)
AND EXISTS(SELECT 1 FROM public.weekly_relay_usage WHERE account_id=$1 AND recorded_bytes=73)
AND NOT EXISTS(SELECT 1 FROM public.native_login_grants WHERE account_id=$1)
AND NOT EXISTS(SELECT 1 FROM public.native_access_tokens)
AND NOT EXISTS(SELECT 1 FROM public.login_grants WHERE account_id=$1)
AND NOT EXISTS(SELECT 1 FROM public.refresh_generations)`, account).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("reset preservation/invalidation=%v err=%v", preserved, err)
	}
	if _, err = migrationPool.Exec(ctx, `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES('2496a9b9-6948-4c07-bc58-c2e14c2fd16b',$1,0,'electron',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 day',clock_timestamp()+interval '2 days')`, account); err != nil {
		t.Fatal(err)
	}
	reset, err = auth.ResetNativeOAuthCredentials(ctx, migrationPool, key)
	if err != nil || reset {
		t.Fatalf("same-key recovery reset=%v err=%v", reset, err)
	}
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE event='credential_reset'`).Scan(&auditCount); err != nil || auditCount != auditBaseline+1 {
		t.Fatalf("no-op retry changed reset audit rows=%d want=%d err=%v", auditCount, auditBaseline+1, err)
	}
	var grants int
	if err := migrationPool.QueryRow(ctx, `SELECT count(*) FROM public.native_login_grants WHERE account_id=$1`, account).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("same-key recovery deleted newly issued grant: %d %v", grants, err)
	}
	otherKey := make([]byte, 32)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatal(err)
	}
	reset, err = auth.ResetNativeOAuthCredentials(ctx, migrationPool, otherKey)
	if err != nil || !reset {
		t.Fatalf("key replacement reset=%v err=%v", reset, err)
	}
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE event='credential_reset'`).Scan(&auditCount); err != nil || auditCount != auditBaseline+2 {
		t.Fatalf("replacement reset audit rows=%d want=%d err=%v", auditCount, auditBaseline+2, err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, key); !errors.Is(err, auth.ErrNativeIssuerResetRequired) {
		t.Fatalf("old key identity accepted after replacement: %v", err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, otherKey); err != nil {
		t.Fatalf("new key identity rejected: %v", err)
	}
	if _, err := migrationPool.Exec(ctx, `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES('8670c193-8f45-49f3-a1d9-6e5dbe4ea82e',$1,0,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 day',clock_timestamp()+interval '2 days')`, account); err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, key); !errors.Is(err, auth.ErrNativeIssuerResetRequired) {
		t.Fatalf("old key was accepted after replacement: %v", err)
	}
	if err := migrationPool.QueryRow(ctx, `SELECT count(*) FROM public.native_login_grants WHERE id='8670c193-8f45-49f3-a1d9-6e5dbe4ea82e'`).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("old-key startup check changed new-key credential: %d %v", grants, err)
	}
	reset, err = auth.ResetNativeOAuthCredentials(ctx, migrationPool, key)
	if err != nil || !reset {
		t.Fatalf("restored old-key reset=%v err=%v", reset, err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, runtime.Pool, key); err != nil {
		t.Fatalf("restored key was not durably selected: %v", err)
	}
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE event='credential_reset'`).Scan(&auditCount); err != nil || auditCount != auditBaseline+3 {
		t.Fatalf("restored-key reset audit rows=%d want=%d err=%v", auditCount, auditBaseline+3, err)
	}
	if err := runtime.Pool.QueryRow(ctx, `SELECT count(*) FROM public.native_login_grants WHERE account_id=$1`, account).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("restoring old key resurrected grants: %d %v", grants, err)
	}
}

func TestZZZNativeResetPreservesPortalSessionForReauthorization(t *testing.T) {
	c, serving, runtime := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	migrationConfig, err := config.Load(os.Getenv("CLIPP_TEST_MIGRATION_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	migrationMaterial, err := migrationConfig.ReadMaterial()
	if err != nil {
		t.Fatal(err)
	}
	migrationPool, err := database.NewPool(context.Background(), migrationConfig, migrationMaterial, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrationPool.Close()

	provider := newProvider(t)
	subject := fmt.Sprintf("reset-reauth-%d", time.Now().UnixNano())
	provider.subject = subject
	provider.email = subject + "@example.test"
	beforeReset := newTestServer(t, runtime.Pool, c, serving, provider.endpoints())
	portalSession := loginAs(t, beforeReset, provider, subject, provider.email, "")
	if _, err := runtime.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, subject); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	loginTokenCalls := provider.tokenCalls
	provider.mu.Unlock()

	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		t.Fatal(err)
	}
	if reset, err := auth.ResetNativeOAuthCredentials(context.Background(), migrationPool, newKey); err != nil || !reset {
		t.Fatalf("deliberate signing-key reset=%v err=%v", reset, err)
	}

	serving.NativeSigningKey = newKey
	afterReset := newTestServer(t, runtime.Pool, c, serving, provider.endpoints())
	code := authorizeClient(t, afterReset, portalSession, "android", c.PublicClients.AndroidRedirect)
	access, _ := redeemClient(t, afterReset, "android", c.PublicClients.AndroidRedirect, code)
	provider.mu.Lock()
	postResetTokenCalls := provider.tokenCalls
	provider.mu.Unlock()
	if postResetTokenCalls != loginTokenCalls {
		t.Fatalf("native reauthorization performed another Google token exchange: before=%d after=%d", loginTokenCalls, postResetTokenCalls)
	}

	if _, err := afterReset.AuthenticateRelay(context.Background(), access); err != nil {
		t.Fatalf("new native access did not authenticate after key reset: %v", err)
	}
	relayServer, err := relay.New(afterReset, nil, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer relayServer.Close()
	address, err := relayServer.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := relay.NewDiscovery(relayServer, afterReset, "relay.example.test", []ma.Multiaddr{address})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://relay.example.test/v1/relay", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	response := httptest.NewRecorder()
	discovery.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Discovery rejected reauthorized native token: %d %s", response.Code, response.Body.String())
	}
}
