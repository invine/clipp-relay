package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/relay"
	"github.com/jackc/pgx/v5"
	ma "github.com/multiformats/go-multiaddr"
)

func TestNativeFositeCodePKCERefreshReplayLifecycle(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	issueCode := func(state string) string {
		return issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, state)
	}
	tokenPostNative := func(form url.Values) *httptest.ResponseRecorder { return nativeTokenPost(t, handler, form) }
	decode := func(out *httptest.ResponseRecorder) (string, string) { return decodeNativeTokens(t, out) }
	codeA := issueCode("native-state-0000000001")
	wrongClient := tokenPostNative(url.Values{"grant_type": {"authorization_code"}, "client_id": {"electron"}, "redirect_uri": {"http://127.0.0.1:34567/oauth/callback"}, "code": {codeA}, "code_verifier": {verifier}})
	if wrongClient.Code == 200 {
		t.Fatal("a different public client redeemed the native code")
	}
	badVerifier := tokenPostNative(url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {codeA}, "code_verifier": {strings.Repeat("b", 43)}})
	if badVerifier.Code == 200 {
		t.Fatal("invalid S256 verifier was accepted")
	}
	first := tokenPostNative(url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {codeA}, "code_verifier": {verifier}})
	if first.Code != 200 {
		t.Fatalf("native code redemption: %d %s", first.Code, first.Body.String())
	}
	accessA, refreshA := decode(first)
	if !strings.HasPrefix(accessA, "ory_at_") || len(accessA) != 94 || !strings.HasPrefix(refreshA, "ory_rt_") || len(refreshA) != 94 {
		t.Fatal("Fosite native token format not returned")
	}
	usedCode := tokenPostNative(url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {codeA}, "code_verifier": {verifier}})
	if usedCode.Code == 200 {
		t.Fatal("consumed native code was accepted")
	}
	wrongRefreshClient := tokenPostNative(url.Values{"grant_type": {"refresh_token"}, "client_id": {"electron"}, "refresh_token": {refreshA}})
	if wrongRefreshClient.Code == 200 {
		t.Fatal("a different public client used the refresh token")
	}
	forgedRefresh := []byte(refreshA)
	if forgedRefresh[7] == 'A' {
		forgedRefresh[7] = 'B'
	} else {
		forgedRefresh[7] = 'A'
	}
	forged := tokenPostNative(url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {string(forgedRefresh)}})
	if forged.Code == 200 {
		t.Fatal("a refresh token with an invalid HMAC was accepted")
	}
	_, refreshA2 := decode(tokenPostNative(url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshA}}))
	if _, _, err := s.AuthenticateAccess(context.Background(), accessA); err != nil {
		t.Fatalf("ordinary refresh revoked prior access token: %v", err)
	}
	codeB := issueCode("native-state-0000000002")
	second := tokenPostNative(url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {codeB}, "code_verifier": {verifier}})
	if second.Code != 200 {
		t.Fatalf("second native grant exchange: %d %s", second.Code, second.Body.String())
	}
	accessB, refreshB := decode(second)
	if _, _, err := s.AuthenticateAccess(context.Background(), accessB); err != nil {
		t.Fatalf("second grant access invalid: %v", err)
	}
	concurrentRefresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshA2}}
	refreshResults := make([]*httptest.ResponseRecorder, 2)
	var refreshes sync.WaitGroup
	for i := range refreshResults {
		refreshes.Add(1)
		go func(i int) { defer refreshes.Done(); refreshResults[i] = tokenPostNative(concurrentRefresh) }(i)
	}
	refreshes.Wait()
	refreshWinners := 0
	var replayedAccess string
	for _, out := range refreshResults {
		if out.Code == 200 {
			refreshWinners++
			replayedAccess, _ = decode(out)
		}
	}
	if refreshWinners != 1 {
		t.Fatalf("concurrent refresh redemption successes=%d, want one", refreshWinners)
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), accessA); err == nil {
		t.Fatal("authenticated concurrent refresh replay did not revoke the owning grant's old access token")
	}
	if _, err := s.AuthenticateRelay(context.Background(), accessA); !errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("Relay Authentication accepted the revoked grant access token: %v", err)
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), replayedAccess); err == nil {
		t.Fatal("authenticated concurrent refresh replay did not revoke the winning response's access token")
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), accessB); err != nil {
		t.Fatalf("refresh replay revoked an unrelated grant: %v", err)
	}
	if continued := tokenPostNative(url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshB}}); continued.Code != 200 {
		t.Fatalf("unrelated grant refresh failed: %d %s", continued.Code, continued.Body.String())
	}
	var replayAudits int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events a JOIN public.accounts c ON c.id=a.account_id WHERE c.subject=$1 AND a.event='refresh_reuse'`, d.subject).Scan(&replayAudits); err != nil || replayAudits != 1 {
		t.Fatalf("concurrent refresh replay audit rows=%d err=%v", replayAudits, err)
	}
	concurrentCode := issueCode("native-state-0000000003")
	concurrentForm := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {concurrentCode}, "code_verifier": {verifier}}
	concurrentResults := make([]*httptest.ResponseRecorder, 2)
	var redemptions sync.WaitGroup
	for i := range concurrentResults {
		redemptions.Add(1)
		go func(i int) { defer redemptions.Done(); concurrentResults[i] = tokenPostNative(concurrentForm) }(i)
	}
	redemptions.Wait()
	winners := 0
	for _, out := range concurrentResults {
		if out.Code == 200 {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent code redemption successes=%d, want one", winners)
	}
	if refreshA2 == refreshA {
		t.Fatal("refresh rotation did not issue a successor")
	}
}

func TestNativeRelayAuthenticationAndDiscoveryAuthority(t *testing.T) {
	c, db, s, _, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "relay-auth-code-purpose-0001")
	grant := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}})
	access, refresh := decodeNativeTokens(t, grant)
	credential, err := s.AuthenticateRelay(context.Background(), access)
	if err != nil {
		t.Fatalf("native access rejected by exported Relay Authentication: %v", err)
	}
	if credential.AccountID == "" || credential.SessionLimit <= 0 || !credential.ExpiresAt.After(time.Now()) {
		t.Fatalf("native Relay Credential omitted current account/plan/expiry authority: %+v", credential)
	}
	relayServer, err := relay.New(s, nil, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relayServer.Close() })
	address, err := relayServer.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := relay.NewDiscovery(relayServer, s, "relay.example.test", []ma.Multiaddr{address})
	if err != nil {
		t.Fatal(err)
	}
	discoveryRequest := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://relay.example.test/v1/relay", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		discovery.ServeHTTP(out, req)
		return out
	}
	if out := discoveryRequest(access); out.Code != http.StatusOK {
		t.Fatalf("Discovery rejected native access: %d %s", out.Code, out.Body.String())
	}
	tampered := []byte(access)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	for name, token := range map[string]string{"authorization code": code, "refresh token": refresh, "malformed token": access + "x", "tampered native MAC": string(tampered)} {
		if _, err := s.AuthenticateRelay(context.Background(), token); !errors.Is(err, auth.ErrInvalidAccess) {
			t.Fatalf("Relay Authentication accepted %s: %v", name, err)
		}
		if out := discoveryRequest(token); out.Code != http.StatusUnauthorized {
			t.Fatalf("Discovery accepted %s: %d %s", name, out.Code, out.Body.String())
		}
	}
	// The failed access-purpose probes above must not consume the refresh token.
	refreshed := nativeTokenPost(t, handler, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}})
	if refreshed.Code != http.StatusOK {
		t.Fatalf("purpose probes caused refresh replay side effects: %d %s", refreshed.Code, refreshed.Body.String())
	}
	if _, err := s.AuthenticateRelay(context.Background(), access); err != nil {
		t.Fatalf("ordinary refresh invalidated older unexpired access: %v", err)
	}
	var accountID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE id=$1`, credential.AccountID).Scan(&accountID); err != nil || accountID != credential.AccountID {
		t.Fatalf("native account lookup: id=%s err=%v", accountID, err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET sessions_override=2 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if current, err := s.AuthenticateRelay(context.Background(), access); err != nil || current.SessionLimit != 2 {
		t.Fatalf("Relay Authentication did not observe current quota plan override: %+v err=%v", current, err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET sessions_override=NULL,recovery_review_hold=true WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateRelay(context.Background(), access); !errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("Relay Authentication accepted a recovery review hold: %v", err)
	}
	if out := discoveryRequest(access); out.Code != http.StatusUnauthorized {
		t.Fatalf("Discovery accepted a recovery review hold: %d %s", out.Code, out.Body.String())
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET recovery_review_hold=false,status='Suspended' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateRelay(context.Background(), access); !errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("Relay Authentication accepted suspended account: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateRelay(context.Background(), access); !errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("Relay Authentication accepted stale credential generation: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation-1 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRelayUncertainAccountReconciliationAndOutage(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "relay-auth-uncertain-00001")
	grant := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}})
	access, _ := decodeNativeTokens(t, grant)
	credential, err := s.AuthenticateRelay(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["native-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.AdminAllowlistFile = c.Secrets.AdminAllowlistFile
	relayServer, err := relay.New(s, nil, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relayServer.Close() })
	address, err := relayServer.ListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := relay.NewDiscovery(relayServer, s, "relay.example.test", []ma.Multiaddr{address})
	if err != nil {
		t.Fatal(err)
	}
	discoveryRequest := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://relay.example.test/v1/relay", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		out := httptest.NewRecorder()
		discovery.ServeHTTP(out, req)
		return out
	}
	if out := discoveryRequest(); out.Code != http.StatusOK {
		t.Fatalf("Discovery rejected active native token: %d %s", out.Code, out.Body.String())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetRelayCredentialHookForTest(func() { close(entered); <-release })
	result := make(chan error, 1)
	go func() {
		_, err := s.AuthenticateRelay(context.Background(), access)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native Relay Authentication did not reach the post-observation wait")
	}
	adminCookie := loginAs(t, s, d, "native-admin-"+fmt.Sprint(time.Now().UnixNano()), "native-admin@gmail.com", "")
	adminCSRF := csrfFrom(t, s, adminCookie)
	var revision int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT revision FROM public.accounts WHERE id=$1`, credential.AccountID).Scan(&revision); err != nil {
		close(release)
		t.Fatal(err)
	}
	s.SetAccountCommitHookForTest(func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("simulated account commit acknowledgment loss")
	})
	form := url.Values{"csrf": {adminCSRF}, "reason": {"security_response"}, "action": {"suspend"}, "revision": {fmt.Sprint(revision)}}
	mutation := portalRequest(s, http.MethodPost, "/admin/accounts/"+credential.AccountID, adminCookie, form, c.PortalOrigin)
	s.SetAccountCommitHookForTest(nil)
	if mutation.Code != http.StatusServiceUnavailable {
		close(release)
		t.Fatalf("ambiguous suspension response=%d %s", mutation.Code, mutation.Body.String())
	}
	close(release)
	select {
	case err := <-result:
		s.SetRelayCredentialHookForTest(nil)
		if !errors.Is(err, auth.ErrInvalidAccess) {
			t.Fatalf("native access after uncertain account suspension was accepted or misclassified: %v", err)
		}
	case <-time.After(5 * time.Second):
		s.SetRelayCredentialHookForTest(nil)
		t.Fatal("native Relay Authentication did not finish after account uncertainty resolved")
	}
	if out := discoveryRequest(); out.Code != http.StatusUnauthorized {
		t.Fatalf("Discovery accepted token after uncertain account suspension: %d %s", out.Code, out.Body.String())
	}
	db.Pool.Close()
	if _, err := s.AuthenticateRelay(context.Background(), access); err == nil || errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("database outage was not preserved distinctly: %v", err)
	}
	if out := discoveryRequest(); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("Discovery mapped database outage to credential refusal: %d %s", out.Code, out.Body.String())
	}
}

func TestNativeFositeUncertainCommitAcknowledgements(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	d := newProvider(t)
	d.subject = fmt.Sprintf("native-fosite-uncertain-%d", time.Now().UnixNano())
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, d.subject, d.subject+"@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	handler := s.NativeHandlerForTest(bytes.Repeat([]byte{0x52}, 32))
	issueCode := func(state string) string {
		t.Helper()
		verifier := strings.Repeat("a", 43)
		hash := sha256.Sum256([]byte(verifier))
		query := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}}
		begin := httptest.NewRequest("GET", "/oauth/authorize?"+query.Encode(), nil)
		begin.AddCookie(cookie)
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, begin)
		if page.Code != http.StatusOK {
			t.Fatalf("native authorize page: %d %s", page.Code, page.Body.String())
		}
		flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
		if len(flow) != 2 || len(csrf) != 2 {
			t.Fatal("native consent form missing")
		}
		confirm := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}.Encode()))
		confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		confirm.Header.Set("Origin", s.Origin)
		confirm.AddCookie(cookie)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, confirm)
		if out.Code != http.StatusFound {
			t.Fatalf("native consent: %d %s", out.Code, out.Body.String())
		}
		callback, err := url.Parse(out.Header().Get("Location"))
		if err != nil || callback.Query().Get("state") != state {
			t.Fatalf("native callback mismatch: %s %v", out.Header().Get("Location"), err)
		}
		return callback.Query().Get("code")
	}
	tokenPost := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	assertNoCredentials := func(out *httptest.ResponseRecorder) {
		t.Helper()
		if strings.Contains(out.Body.String(), "access_token") || strings.Contains(out.Body.String(), "refresh_token") {
			t.Fatalf("uncertain token response exposed credentials: %s", out.Body.String())
		}
	}
	decode := func(out *httptest.ResponseRecorder) (string, string) {
		t.Helper()
		var body struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil || body.Access == "" || body.Refresh == "" {
			t.Fatalf("native token response: %d %s (%v)", out.Code, out.Body.String(), err)
		}
		return body.Access, body.Refresh
	}
	verifier := strings.Repeat("a", 43)
	code := issueCode("uncertain-code-state-00000001")
	codeForm := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}}
	codeCommitCalls := 0
	s.SetAuthorizationCommitHookForTest(func(ctx context.Context, tx persistence.Transaction) error {
		codeCommitCalls++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return persistence.ErrUncertainCommit
	})
	uncertainCodeResponse := tokenPost(codeForm)
	if uncertainCodeResponse.Code != http.StatusServiceUnavailable || codeCommitCalls != 1 {
		t.Fatalf("uncertain code exchange status=%d commit attempts=%d", uncertainCodeResponse.Code, codeCommitCalls)
	}
	assertNoCredentials(uncertainCodeResponse)
	s.SetAuthorizationCommitHookForTest(nil)
	if retry := tokenPost(codeForm); retry.Code != http.StatusUnauthorized {
		t.Fatalf("committed authorization code retry status=%d, want unauthorized", retry.Code)
	} else {
		assertNoCredentials(retry)
	}
	var consumedCodes, grants, accesses, refreshes int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes x JOIN public.accounts a ON a.id=x.account_id WHERE a.subject=$1 AND x.consumed_at IS NOT NULL`, d.subject).Scan(&consumedCodes); err != nil || consumedCodes != 1 {
		t.Fatalf("uncertain code commit consumed rows=%d err=%v", consumedCodes, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_login_grants g JOIN public.accounts a ON a.id=g.account_id WHERE a.subject=$1`, d.subject).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("uncertain code commit grant rows=%d err=%v", grants, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_access_tokens x JOIN public.native_login_grants g ON g.id=x.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE a.subject=$1`, d.subject).Scan(&accesses); err != nil || accesses != 1 {
		t.Fatalf("uncertain code commit access rows=%d err=%v", accesses, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_refresh_generations x JOIN public.native_login_grants g ON g.id=x.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE a.subject=$1`, d.subject).Scan(&refreshes); err != nil || refreshes != 1 {
		t.Fatalf("uncertain code commit refresh rows=%d err=%v", refreshes, err)
	}

	refreshCode := issueCode("uncertain-refresh-state-00001")
	grantResponse := tokenPost(url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {refreshCode}, "code_verifier": {verifier}})
	if grantResponse.Code != http.StatusOK {
		t.Fatalf("uncertain-refresh fixture grant exchange: %d %s", grantResponse.Code, grantResponse.Body.String())
	}
	oldAccess, oldRefresh := decode(grantResponse)
	refreshCommitCalls := 0
	s.SetCredentialCommitHookForTest(func(ctx context.Context, tx persistence.Transaction) error {
		refreshCommitCalls++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("simulated refresh commit acknowledgment loss")
	})
	uncertainRefreshResponse := tokenPost(url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {oldRefresh}})
	if uncertainRefreshResponse.Code != http.StatusServiceUnavailable || refreshCommitCalls != 1 {
		t.Fatalf("uncertain refresh status=%d commit attempts=%d", uncertainRefreshResponse.Code, refreshCommitCalls)
	}
	assertNoCredentials(uncertainRefreshResponse)
	s.SetCredentialCommitHookForTest(nil)
	var currentGeneration, generationRows, consumedGenerations int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT g.current_refresh_generation,count(r.credential_digest),count(r.consumed_at) FROM public.native_login_grants g JOIN public.accounts a ON a.id=g.account_id JOIN public.native_refresh_generations r ON r.grant_id=g.id WHERE g.id=(SELECT newest.id FROM public.native_login_grants newest JOIN public.accounts owner ON owner.id=newest.account_id WHERE owner.subject=$1 ORDER BY newest.created_at DESC,newest.id DESC LIMIT 1) GROUP BY g.id`, d.subject).Scan(&currentGeneration, &generationRows, &consumedGenerations); err != nil || currentGeneration != 2 || generationRows != 2 || consumedGenerations != 1 {
		t.Fatalf("uncertain refresh commit generation=%d rows=%d consumed=%d err=%v", currentGeneration, generationRows, consumedGenerations, err)
	}
	if retry := tokenPost(url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {oldRefresh}}); retry.Code != http.StatusUnauthorized {
		t.Fatalf("explicit old refresh generation retry status=%d, want unauthorized", retry.Code)
	} else {
		assertNoCredentials(retry)
	}
	if refreshCommitCalls != 1 {
		t.Fatalf("uncertain refresh automatically retried commit hook %d times", refreshCommitCalls)
	}
	if _, _, err := s.AuthenticateAccess(context.Background(), oldAccess); err == nil {
		t.Fatal("old refresh generation replay did not revoke its grant's access token")
	}
	var replayAudits int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events a JOIN public.accounts c ON c.id=a.account_id WHERE c.subject=$1 AND a.event='refresh_reuse'`, d.subject).Scan(&replayAudits); err != nil || replayAudits != 1 {
		t.Fatalf("uncertain refresh replay audit rows=%d err=%v", replayAudits, err)
	}
}

func TestConsentSubmissionIsAtomicOneUse(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	d.subject = "one-use-consent"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	session := loginAs(t, s, d, d.subject, "consent@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {"android"},
		"redirect_uri":          {c.PublicClients.AndroidRedirect},
		"code_challenge_method": {"S256"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"state":                 {"duplicate-consent-state"},
	}
	begin := httptest.NewRequest("GET", "/oauth/authorize?"+query.Encode(), nil)
	begin.AddCookie(session)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, begin)
	if response.Code != http.StatusOK {
		t.Fatalf("consent page: %d %s", response.Code, response.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(response.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(response.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("consent form missing flow or csrf")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", s.Origin)
		r.AddCookie(session)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	first := confirm()
	if first.Code != http.StatusFound {
		t.Fatalf("first consent submission: %d %s", first.Code, first.Body.String())
	}
	second := confirm()
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate consent submission was not refused: %d", second.Code)
	}
	var codes int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes c JOIN public.accounts a ON a.id=c.account_id WHERE a.subject=$1`, d.subject).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 1 {
		t.Fatalf("one consent submission issued %d codes", codes)
	}
}

func TestConsentUsesCapturedCredentialGeneration(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	d.subject = "stale-consent-generation"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	session := loginAs(t, s, d, d.subject, "stale-consent@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "state": {"stale-consent-state"}}
	begin := httptest.NewRequest("GET", "/oauth/authorize?"+query.Encode(), nil)
	begin.AddCookie(session)
	page := httptest.NewRecorder()
	s.Handler().ServeHTTP(page, begin)
	if page.Code != http.StatusOK {
		t.Fatalf("consent page: %d %s", page.Code, page.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("consent form missing flow or csrf")
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", s.Origin)
	confirm.AddCookie(session)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, confirm)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("stale consent generation issued authorization: %d %s", response.Code, response.Body.String())
	}
	var codes int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes c JOIN public.accounts a ON a.id=c.account_id WHERE a.subject=$1`, d.subject).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 0 {
		t.Fatalf("stale consent issued %d authorization codes", codes)
	}
}

func TestRestartCancelsUnfinishedConsent(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	d.subject = "restart-consent"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	session := loginAs(t, s, d, d.subject, "restart@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "state": {"restart-consent-state"}}
	begin := httptest.NewRequest("GET", "/oauth/authorize?"+query.Encode(), nil)
	begin.AddCookie(session)
	page := httptest.NewRecorder()
	s.Handler().ServeHTTP(page, begin)
	if page.Code != http.StatusOK {
		t.Fatalf("consent page: %d %s", page.Code, page.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("consent form missing flow or csrf")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	restarted := newTestServer(t, db.Pool, c, m, d.endpoints())
	confirm := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", s.Origin)
	confirm.AddCookie(session)
	response := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(response, confirm)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("consent survived process restart: %d", response.Code)
	}
	var codes int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes c JOIN public.accounts a ON a.id=c.account_id WHERE a.subject=$1`, d.subject).Scan(&codes); err != nil || codes != 0 {
		t.Fatalf("restart issued code from unfinished consent: %d %v", codes, err)
	}
}

func authorizeClient(t *testing.T, s *auth.Server, cookie *http.Cookie, client, redirect string) string {
	t.Helper()
	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "state": {"independent-client-state"}}
	r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%s consent: %d %s", client, w.Code, w.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("consent form missing")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", s.Origin)
	confirm.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, confirm)
	if w.Code != 302 {
		t.Fatalf("%s confirm: %d %s", client, w.Code, w.Body.String())
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil || u.Query().Get("state") != "independent-client-state" || u.Query().Get("code") == "" {
		t.Fatalf("%s callback: %s %v", client, w.Header().Get("Location"), e)
	}
	return u.Query().Get("code")
}
func tokenPost(s *auth.Server, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func redeemClient(t *testing.T, s *auth.Server, client, redirect, code string) (string, string) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "redirect_uri": {redirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	w := tokenPost(s, form)
	if w.Code != 200 {
		t.Fatalf("%s exchange: %d %s", client, w.Code, w.Body.String())
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &token); e != nil || token.Access == "" || token.Refresh == "" {
		t.Fatalf("tokens: %v", e)
	}
	return token.Access, token.Refresh
}
func TestAllRegisteredClientsAndBlockedStates(t *testing.T) {
	for _, status := range []string{"Active", "Pending", "Suspended", "Denied"} {
		t.Run(status, func(t *testing.T) {
			c, m, db := fixture(t)
			c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
			c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
			d := newProvider(t)
			s := newTestServer(t, db.Pool, c, m, d.endpoints())
			cookie := loginAs(t, s, d, "states-"+status, "states@example.test", "")
			if status != "Pending" {
				_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status=$1,plan_id=CASE WHEN $1='Active' THEN '6dd09395-51a0-451c-96b3-716e6038e870'::uuid ELSE NULL END WHERE subject=$2`, status, "states-"+status)
				if e != nil {
					t.Fatal(e)
				}
			}
			clients := []struct{ client, redirect string }{{"electron", "http://127.0.0.1:34567/oauth/callback"}, {"android", c.PublicClients.AndroidRedirect}, {"extension", c.PublicClients.ExtensionRedirect}}
			for _, entry := range clients {
				if status == "Active" {
					access, _ := redeemClient(t, s, entry.client, entry.redirect, authorizeClient(t, s, cookie, entry.client, entry.redirect))
					if _, _, e := s.AuthenticateAccess(context.Background(), access); e != nil {
						t.Fatalf("%s access: %v", entry.client, e)
					}
				} else {
					verifier := strings.Repeat("a", 43)
					hash := sha256.Sum256([]byte(verifier))
					q := url.Values{"response_type": {"code"}, "client_id": {entry.client}, "redirect_uri": {entry.redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"independent-client-state"}}
					r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
					r.AddCookie(cookie)
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					u, e := url.Parse(w.Header().Get("Location"))
					if e != nil || u.Query().Get("code") != "" || u.Query().Get("error") != "access_denied" {
						t.Fatalf("%s %s: %d %s", status, entry.client, w.Code, w.Header().Get("Location"))
					}
				}
			}
		})
	}
}
func TestRefreshReplayInvalidatesOnlyOwningGrant(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "replay-account", "replay@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='replay-account'`)
	if e != nil {
		t.Fatal(e)
	}
	accessA, refreshA := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect))
	accessB, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect))
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshA}}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- tokenPost(s, form).Code }()
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent refresh success count %d", success)
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), accessA); e == nil {
		t.Fatal("replay left own access valid")
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), accessB); e != nil {
		t.Fatalf("replay revoked unrelated grant: %v", e)
	}
}
func TestIdentityFenceCancelsUnknownGoogleFlow(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.mu.Lock()
	d.subject = "fenced-unknown-subject"
	d.mu.Unlock()
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	e := s.WithIdentityFence("fenced-unknown-subject", func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	w := complete(s, state, binding)
	if w.Code == 303 {
		t.Fatal("pre-fence unknown flow completed")
	}
	var count int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject='fenced-unknown-subject'`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("fenced flow registered account: %d %v", count, e)
	}
	state, binding = start(t, s, nil)
	w = complete(s, state, binding)
	if w.Code != 303 {
		t.Fatalf("new flow blocked: %d", w.Code)
	}
}

func TestIdentityFenceDuringProviderExchangeCancelsClaimedCallback(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "fenced-inflight-provider"
	d.tokenGate = make(chan struct{})
	d.tokenStarted = make(chan struct{}, 1)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	result := make(chan int, 1)
	go func() { result <- complete(s, state, binding).Code }()
	select {
	case <-d.tokenStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not enter provider exchange")
	}
	if err := s.WithIdentityFence(d.subject, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	close(d.tokenGate)
	if status := <-result; status == http.StatusSeeOther {
		t.Fatal("claimed callback completed across an account fence")
	}
	var count int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fenced callback created account: %d %v", count, err)
	}
}

func TestIdentityFenceOverflowCancelsUnfinishedFlows(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	for i := 0; i < 4097; i++ {
		subject := fmt.Sprintf("fenced-%04d", i)
		if e := s.WithIdentityFence(subject, func() error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if w := complete(s, state, binding); w.Code == 303 {
		t.Fatal("overflow preserved an unfinished flow")
	}
	if w := complete(newTestServer(t, db.Pool, c, m, d.endpoints()), state, binding); w.Code == 303 {
		t.Fatal("restart revived a flow")
	}
	state, binding = start(t, s, nil)
	if w := complete(s, state, binding); w.Code != 303 {
		t.Fatalf("new flow after overflow: %d", w.Code)
	}
}

func TestCodeBindingExpiryAndGeneration(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "binding-account", "binding@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='binding-account'`)
	if e != nil {
		t.Fatal(e)
	}
	code := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	for _, change := range []func(){func() { form.Set("code_verifier", strings.Repeat("b", 43)) }, func() { form.Set("code_verifier", strings.Repeat("a", 43)); form.Set("client_id", "electron") }, func() { form.Set("client_id", "android"); form.Set("redirect_uri", "clipp-relay://other/callback") }} {
		change()
		if w := tokenPost(s, form); w.Code == 200 {
			t.Fatal("wrong code binding issued tokens")
		}
	}
	form.Set("redirect_uri", c.PublicClients.AndroidRedirect)
	if w := tokenPost(s, form); w.Code != 200 {
		t.Fatalf("valid code rejected after wrong attempts: %d", w.Code)
	}
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("code replay succeeded")
	}
	parallelCode := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	form.Set("code", parallelCode)
	var group sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); results <- tokenPost(s, form).Code }()
	}
	group.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == 200 {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("parallel code redemption succeeded %d times", successes)
	}
	expired := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.native_authorization_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=(SELECT id FROM public.accounts WHERE subject='binding-account') AND consumed_at IS NULL`)
	if e != nil {
		t.Fatal(e)
	}
	form.Set("code", expired)
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("expired code succeeded")
	}
	changed := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE subject='binding-account'`)
	if e != nil {
		t.Fatal(e)
	}
	form.Set("code", changed)
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("generation-stale code succeeded")
	}
}
func TestDeletedIdentityCannotCompleteOldGoogleFlow(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.mu.Lock()
	d.subject = "deletion-fence-subject"
	d.mu.Unlock()
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "deletion-fence-subject", "delete@example.test", "")
	_ = cookie
	state, binding := start(t, s, nil)
	e := s.WithIdentityFence("deletion-fence-subject", func() error {
		_, err := db.Pool.Exec(context.Background(), `DELETE FROM public.portal_sessions WHERE account_id=(SELECT id FROM public.accounts WHERE subject='deletion-fence-subject'); DELETE FROM public.accounts WHERE subject='deletion-fence-subject'`)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	if w := complete(s, state, binding); w.Code == 303 {
		t.Fatal("old flow re-registered deleted identity")
	}
	state, binding = start(t, s, nil)
	if w := complete(s, state, binding); w.Code != 303 {
		t.Fatalf("fresh flow after deletion: %d", w.Code)
	}
}

func TestUnknownClientBrowserFlowRequiresConsent(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	// The account already has approval, but this browser has no Portal Session.
	cookie := loginAs(t, s, d, "browser-oauth-subject", "browser@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='browser-oauth-subject'`)
	if e != nil {
		t.Fatal(e)
	}
	_ = cookie
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"independent-client-state"}}
	r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), d.server.URL+"/authorize?") {
		t.Fatalf("Google start: %d %s", w.Code, w.Header().Get("Location"))
	}
	google, _ := url.Parse(w.Header().Get("Location"))
	var binding *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-clipp-binding" {
			binding = c
		}
	}
	if binding == nil {
		t.Fatal("browser binding missing")
	}
	callback := httptest.NewRequest("GET", "/auth/callback?state="+url.QueryEscape(google.Query().Get("state"))+"&code="+url.QueryEscape(google.Query().Get("nonce")), nil)
	callback.AddCookie(binding)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, callback)
	if out.Code != 200 || strings.Contains(out.Body.String(), "SECRET_CANARY") || out.Header().Get("Location") != "" {
		t.Fatalf("consent: %d %s", out.Code, out.Body.String())
	}
	portalCookie := sessionFrom(out)
	if portalCookie == nil {
		t.Fatal("Portal Session missing")
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("form missing")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(portalCookie)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := confirm("https://attacker.example"); w.Code == 302 {
		t.Fatal("cross-origin consent succeeded")
	}
	authorized := confirm(c.PortalOrigin)
	if authorized.Code != 302 {
		t.Fatalf("authorized consent: %d %s", authorized.Code, authorized.Body.String())
	}
	target, e := url.Parse(authorized.Header().Get("Location"))
	if e != nil || target.Query().Get("code") == "" || target.Query().Get("state") != "independent-client-state" || strings.Contains(authorized.Header().Get("Location"), "SECRET_CANARY") {
		t.Fatal("bad app callback")
	}
	if w := confirm(c.PortalOrigin); w.Code == 302 {
		t.Fatal("consent replay succeeded")
	}
	redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, target.Query().Get("code"))
}

func TestGrantCapAndDeadlines(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "cap-account", "cap@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='cap-account'`)
	if e != nil {
		t.Fatal(e)
	}
	code := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at) SELECT gen_random_uuid(),a.id,a.credential_generation,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days' FROM public.accounts a,generate_series(1,20) WHERE a.subject='cap-account'`)
	if e != nil {
		t.Fatal(e)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	if w := tokenPost(s, form); w.Code != 429 {
		t.Fatalf("grant cap: %d", w.Code)
	}
	var audits int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events e JOIN public.accounts a ON a.id=e.account_id WHERE a.subject='cap-account' AND e.event='grant_cap'`).Scan(&audits); e != nil || audits != 1 {
		t.Fatalf("cap audit: %d %v", audits, e)
	}
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE account_id=(SELECT id FROM public.accounts WHERE subject='cap-account')`)
	if e != nil {
		t.Fatal(e)
	}
	access, refresh := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, code)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE account_id=(SELECT id FROM public.accounts WHERE subject='cap-account') AND terminated_at IS NULL`)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), access); e == nil {
		t.Fatal("idle-expired grant kept access")
	}
	if w := tokenPost(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}}); w.Code == 200 {
		t.Fatal("idle-expired grant renewed")
	}
}

func TestHTTPSPublicClientStateContract(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "https-client-subject", "https@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='https-client-subject'`)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewTLSServer(s.Handler())
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	expectedState := "initiator-owned-state"
	authorize := func(clientType, redirect, state string) (string, string) {
		q := url.Values{"response_type": {"code"}, "client_id": {clientType}, "redirect_uri": {redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}}
		req, _ := http.NewRequest("GET", server.URL+"/oauth/authorize?"+q.Encode(), nil)
		req.AddCookie(cookie)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("authorize HTTPS status %d", response.StatusCode)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(string(body))
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(body))
		if len(flow) != 2 || len(csrf) != 2 {
			t.Fatal("consent form missing")
		}
		form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
		post, _ := http.NewRequest("POST", server.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		post.Header.Set("Origin", c.PortalOrigin)
		post.AddCookie(cookie)
		confirmed, err := client.Do(post)
		if err != nil {
			t.Fatal(err)
		}
		defer confirmed.Body.Close()
		if confirmed.StatusCode != 302 {
			t.Fatalf("confirm HTTPS status %d", confirmed.StatusCode)
		}
		callback, err := url.Parse(confirmed.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return callback.Query().Get("state"), callback.Query().Get("code")
	}
	actual, wrongCode := authorize("android", c.PublicClients.AndroidRedirect, "attacker-replaced-state")
	if actual == expectedState || wrongCode == "" {
		t.Fatal("bad test callback")
	}
	// A public client accepts only the exact state it generated; it never redeems this code.
	actual, code := authorize("android", c.PublicClients.AndroidRedirect, expectedState)
	if actual != expectedState || code == "" {
		t.Fatal("legitimate callback failed state check")
	}
	redeemTLS := func(clientType, redirect, code string) {
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientType}, "redirect_uri": {redirect}, "code": {code}, "code_verifier": {verifier}}
		req, _ := http.NewRequest("POST", server.URL+"/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s HTTPS token status %d", clientType, response.StatusCode)
		}
		var tokens struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		}
		if e := json.NewDecoder(response.Body).Decode(&tokens); e != nil || tokens.Access == "" || tokens.Refresh == "" {
			t.Fatal("body credentials missing")
		}
	}
	redeemTLS("android", c.PublicClients.AndroidRedirect, code)
	for _, entry := range []struct{ clientType, redirect string }{{"electron", "http://127.0.0.1:34567/oauth/callback"}, {"extension", c.PublicClients.ExtensionRedirect}} {
		actual, code := authorize(entry.clientType, entry.redirect, expectedState)
		if actual != expectedState || code == "" {
			t.Fatalf("%s client state/callback invalid", entry.clientType)
		}
		redeemTLS(entry.clientType, entry.redirect, code)
	}

}

func TestNativeIssuerRegisteredClientsAndRedirects(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	clients := []struct {
		client, redirect string
	}{
		{"electron", "http://127.0.0.1:34567/oauth/callback"},
		{"android", c.PublicClients.AndroidRedirect},
		{"extension", c.PublicClients.ExtensionRedirect},
	}
	var androidRefresh string
	for i, client := range clients {
		code := issueNativeCode(t, handler, s.Origin, cookie, client.client, client.redirect, verifier, fmt.Sprintf("native-policy-state-%02d", i))
		invalidRedirect := client.redirect + "/other"
		if client.client == "electron" {
			invalidRedirect = "http://localhost:34567/oauth/callback"
		}
		bad := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.client}, "redirect_uri": {invalidRedirect}, "code": {code}, "code_verifier": {verifier}})
		if bad.Code == http.StatusOK {
			t.Fatalf("%s code accepted a non-exact redirect", client.client)
		}
		out := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.client}, "redirect_uri": {client.redirect}, "code": {code}, "code_verifier": {verifier}})
		access, refresh := decodeNativeTokens(t, out)
		if out.Code != http.StatusOK {
			t.Fatalf("%s code exchange: %d %s", client.client, out.Code, out.Body.String())
		}
		var response struct {
			TokenType string `json:"token_type"`
			ExpiresIn int    `json:"expires_in"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.TokenType != "Bearer" || response.ExpiresIn < 895 || response.ExpiresIn > 900 || response.Scope != "" {
			t.Fatalf("%s token response type=%q lifetime=%d scope=%q, want Bearer, a conservatively floored lifetime no greater than 900, and empty scope", client.client, response.TokenType, response.ExpiresIn, response.Scope)
		}
		if !strings.HasPrefix(access, "ory_at_") || !strings.HasPrefix(refresh, "ory_rt_") {
			t.Fatalf("%s did not receive native access and refresh credentials", client.client)
		}
		refreshed := nativeTokenPost(t, handler, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.client}, "refresh_token": {refresh}})
		if refreshed.Code != http.StatusOK {
			t.Fatalf("%s refresh: %d %s", client.client, refreshed.Code, refreshed.Body.String())
		}
		var refreshResponse struct {
			TokenType string `json:"token_type"`
			ExpiresIn int    `json:"expires_in"`
			Scope     string `json:"scope"`
		}
		if err := json.Unmarshal(refreshed.Body.Bytes(), &refreshResponse); err != nil || refreshResponse.TokenType != "Bearer" || refreshResponse.ExpiresIn < 895 || refreshResponse.ExpiresIn > 900 || refreshResponse.Scope != "" {
			t.Fatalf("%s refresh response type=%q lifetime=%d scope=%q err=%v", client.client, refreshResponse.TokenType, refreshResponse.ExpiresIn, refreshResponse.Scope, err)
		}
		_, nextRefresh := decodeNativeTokens(t, refreshed)
		if client.client == "android" {
			androidRefresh = nextRefresh
		}
		if _, _, err := s.AuthenticateAccess(context.Background(), access); err != nil {
			t.Fatalf("ordinary %s refresh invalidated the preceding access token: %v", client.client, err)
		}
	}

	for _, invalid := range []struct{ client, redirect string }{
		{"electron", "http://localhost:34567/oauth/callback"},
		{"electron", "http://127.0.0.1:34567/other"},
		{"electron", "http://user@127.0.0.1:34567/oauth/callback"},
		{"electron", "http://127.0.0.1:34567/oauth/callback#fragment"},
		{"android", "clipp-relay://oauth/other"},
		{"android", "clipp-relay://user@oauth/callback"},
		{"android", "clipp-relay://oauth/callback#fragment"},
		{"extension", "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/other"},
		{"extension", "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay#fragment"},
		{"extension", "https://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.chromiumapp.org/clipp-relay"},
		{"unregistered", "https://example.test/callback"},
	} {
		q := url.Values{"response_type": {"code"}, "client_id": {invalid.client}, "redirect_uri": {invalid.redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(make([]byte, 32))}, "state": {"invalid-native-client-state"}}
		request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK || response.Code == http.StatusFound {
			t.Fatalf("unregistered %s redirect accepted: %d", invalid.client, response.Code)
		}
	}

	var codes, grants, generations, accesses int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&codes); err != nil || codes != 3 {
		t.Fatalf("native authorization codes=%d err=%v, want 3", codes, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_login_grants WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&grants); err != nil || grants != 3 {
		t.Fatalf("native grants=%d err=%v, want 3", grants, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_refresh_generations r JOIN public.native_login_grants g ON g.id=r.grant_id WHERE g.account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&generations); err != nil || generations != 6 {
		t.Fatalf("native refresh generations=%d err=%v, want 6", generations, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_access_tokens x JOIN public.native_login_grants g ON g.id=x.grant_id WHERE g.account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&accesses); err != nil || accesses != 6 {
		t.Fatalf("native access tokens=%d err=%v, want 6", accesses, err)
	}
	var codeLifetime, accessLifetime, absoluteLifetime, idleLifetime float64
	if err := db.Pool.QueryRow(context.Background(), `SELECT min(EXTRACT(EPOCH FROM expires_at-issued_at)) FROM public.native_authorization_codes WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&codeLifetime); err != nil || codeLifetime < 299 || codeLifetime > 301 {
		t.Fatalf("native authorization-code lifetime=%f err=%v, want about 300 seconds", codeLifetime, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT min(EXTRACT(EPOCH FROM x.expires_at-x.issued_at)),min(EXTRACT(EPOCH FROM g.absolute_expires_at-g.created_at)),min(EXTRACT(EPOCH FROM g.idle_expires_at-g.created_at)) FROM public.native_access_tokens x JOIN public.native_login_grants g ON g.id=x.grant_id WHERE g.account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, d.subject).Scan(&accessLifetime, &absoluteLifetime, &idleLifetime); err != nil {
		t.Fatal(err)
	}
	if accessLifetime < 899 || accessLifetime > 901 || absoluteLifetime < 180*86400-1 || absoluteLifetime > 180*86400+1 || idleLifetime < 30*86400-1 || idleLifetime > 30*86400+1 {
		t.Fatalf("native deadlines access=%f idle=%f absolute=%f", accessLifetime, idleLifetime, absoluteLifetime)
	}
	var androidGrant string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id::text FROM public.native_login_grants WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1) AND client_type='android'`, d.subject).Scan(&androidGrant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET absolute_expires_at=clock_timestamp()+interval '10 seconds',idle_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, androidGrant); err != nil {
		t.Fatal(err)
	}
	capped := nativeTokenPost(t, handler, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {androidRefresh}})
	if capped.Code != http.StatusOK {
		t.Fatalf("near-absolute-deadline refresh: %d %s", capped.Code, capped.Body.String())
	}
	var idleAtCap bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT idle_expires_at=absolute_expires_at FROM public.native_login_grants WHERE id=$1`, androidGrant).Scan(&idleAtCap); err != nil || !idleAtCap {
		t.Fatalf("sliding grant idle deadline was not capped at the original absolute deadline (cap=%t err=%v)", idleAtCap, err)
	}
}

func TestNativeIssuerGrantCapRefusesWithoutConsumingCode(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-grant-cap-state-0001")
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,current_refresh_generation,created_at,last_used_at,idle_expires_at,absolute_expires_at) SELECT gen_random_uuid(),a.id,a.credential_generation,'android',1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days' FROM public.accounts a CROSS JOIN generate_series(1,20) WHERE a.subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}}
	if out := nativeTokenPost(t, handler, form); out.Code != http.StatusTooManyRequests {
		t.Fatalf("native grant cap status=%d body=%s, want 429", out.Code, out.Body.String())
	}
	var audits, unconsumed int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1) AND event='grant_cap'`, d.subject).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("native cap audit rows=%d err=%v", audits, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1) AND consumed_at IS NULL`, d.subject).Scan(&unconsumed); err != nil || unconsumed != 1 {
		t.Fatalf("grant refusal consumed authorization code (unconsumed=%d err=%v)", unconsumed, err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE id=(SELECT id FROM public.native_login_grants WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1) ORDER BY created_at LIMIT 1)`, d.subject); err != nil {
		t.Fatal(err)
	}
	if out := nativeTokenPost(t, handler, form); out.Code != http.StatusOK {
		t.Fatalf("valid code after a grant slot freed: %d %s", out.Code, out.Body.String())
	}
}

func TestNativeIssuerOnlyAcceptsPublicCodeAndRefreshWithS256(t *testing.T) {
	c := config.Config{}
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	s := newTestServer(t, nil, c, config.Material{}, auth.Provider{})
	handler := s.NativeHandlerForTest(bytes.Repeat([]byte{0x73}, 32))
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	valid := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {"clipp-relay://oauth/callback"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"native-policy-state-0001"}}
	for name, change := range map[string]func(url.Values){
		"plain PKCE":     func(q url.Values) { q.Set("code_challenge_method", "plain") },
		"implicit grant": func(q url.Values) { q.Set("response_type", "token") },
		"unregistered client": func(q url.Values) {
			q.Set("client_id", "other")
			q.Set("redirect_uri", "https://example.test/callback")
		},
		"additional scope": func(q url.Values) { q.Set("scope", "openid") },
		"duplicate client": func(q url.Values) { q.Add("client_id", "extension") },
	} {
		t.Run(name, func(t *testing.T) {
			q := url.Values{}
			for key, values := range valid {
				q[key] = append([]string(nil), values...)
			}
			change(q)
			request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code == http.StatusOK || response.Code == http.StatusFound {
				t.Fatalf("invalid public authorize request accepted: %d", response.Code)
			}
		})
	}
	for name, form := range map[string]url.Values{
		"unsupported grant": {"grant_type": {"client_credentials"}, "client_id": {"android"}},
		"duplicate client":  {"grant_type": {"refresh_token"}, "client_id": {"android", "extension"}, "refresh_token": {"ory_rt_" + strings.Repeat("a", 87)}},
		"extra secret":      {"grant_type": {"refresh_token"}, "client_id": {"android"}, "client_secret": {"secret"}, "refresh_token": {"ory_rt_" + strings.Repeat("a", 87)}},
	} {
		t.Run(name, func(t *testing.T) {
			response := nativeTokenPost(t, handler, form)
			if response.Code == http.StatusOK {
				t.Fatalf("invalid public token request accepted: %s", response.Body.String())
			}
		})
	}
	oversized := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(strings.Repeat("x", (16<<10)+1)))
	oversized.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	oversizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized native token body status=%d, want 413", oversizedResponse.Code)
	}
}

func TestNativeIssuerAuthorityRecheckedAfterAccountLockWait(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	flow, csrf := beginNativeConsent(t, handler, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-stale-consent-state-01")
	accountID := nativePolicyAccountID(t, db, d.subject)

	consentTx := lockNativePolicyAccount(t, db, accountID)
	consentDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { consentDone <- confirmNativeConsent(t, handler, s.Origin, cookie, flow, csrf) }()
	waitForNativeAccountLock(t, db, "consent generation", consentDone)
	if _, err := consentTx.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := consentTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-consentDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("consent issued a code after generation changed while it waited: %d %s", out.Code, out.Body.String())
	}
	var codes int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes WHERE account_id=$1`, accountID).Scan(&codes); err != nil || codes != 0 {
		t.Fatalf("stale consent persisted %d authorization codes: %v", codes, err)
	}

	cookie = loginAs(t, s, d, d.subject, d.subject+"@example.test", "")
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-code-generation-state-01")
	codeTx := lockNativePolicyAccount(t, db, accountID)
	codeDone := make(chan *httptest.ResponseRecorder, 1)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}}
	go func() { codeDone <- nativeTokenPost(t, handler, form) }()
	waitForNativeAccountLock(t, db, "code exchange generation", codeDone)
	if _, err := codeTx.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := codeTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-codeDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("code exchanged after generation changed while it waited: %d %s", out.Code, out.Body.String())
	}
	var unconsumed int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes WHERE account_id=$1 AND consumed_at IS NULL`, accountID).Scan(&unconsumed); err != nil || unconsumed != 1 {
		t.Fatalf("stale code consumed despite generation change (unconsumed=%d err=%v)", unconsumed, err)
	}

	cookie = loginAs(t, s, d, d.subject, d.subject+"@example.test", "")
	refreshCode := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-refresh-status-state-01")
	grantResponse := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {refreshCode}, "code_verifier": {verifier}})
	_, refresh := decodeNativeTokens(t, grantResponse)
	refreshTx := lockNativePolicyAccount(t, db, accountID)
	refreshDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		refreshDone <- nativeTokenPost(t, handler, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}})
	}()
	waitForNativeAccountLock(t, db, "refresh status", refreshDone)
	if _, err := refreshTx.Exec(context.Background(), `UPDATE public.accounts SET status='Suspended' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := refreshTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-refreshDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("refresh succeeded after account was suspended while it waited: %d %s", out.Code, out.Body.String())
	}
}

func TestNativeIssuerCodeAndGrantExpiryRecheckedAfterAccountLockWait(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	accountID := nativePolicyAccountID(t, db, d.subject)
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-expired-code-state-01")
	codeTx := lockNativePolicyAccount(t, db, accountID)
	codeDone := make(chan *httptest.ResponseRecorder, 1)
	codeForm := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}}
	go func() { codeDone <- nativeTokenPost(t, handler, codeForm) }()
	waitForNativeAccountLock(t, db, "authorization-code expiry", codeDone)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.native_authorization_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1 AND consumed_at IS NULL`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := codeTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-codeDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("authorization code expired during lock wait was accepted: %d %s", out.Code, out.Body.String())
	}
	var unconsumed int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_authorization_codes WHERE account_id=$1 AND consumed_at IS NULL`, accountID).Scan(&unconsumed); err != nil || unconsumed != 1 {
		t.Fatalf("expired authorization code was consumed (unconsumed=%d err=%v)", unconsumed, err)
	}

	cookie = loginAs(t, s, d, d.subject, d.subject+"@example.test", "")
	refreshCode := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-expired-grant-state-01")
	grantResponse := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {refreshCode}, "code_verifier": {verifier}})
	_, refresh := decodeNativeTokens(t, grantResponse)
	grantTx := lockNativePolicyAccount(t, db, accountID)
	refreshDone := make(chan *httptest.ResponseRecorder, 1)
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}}
	go func() { refreshDone <- nativeTokenPost(t, handler, refreshForm) }()
	waitForNativeAccountLock(t, db, "refresh grant expiry", refreshDone)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1 AND terminated_at IS NULL`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := grantTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-refreshDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after grant idle deadline passed during lock wait was accepted: %d %s", out.Code, out.Body.String())
	}
	var terminated, audits int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_login_grants WHERE account_id=$1 AND terminated_at IS NOT NULL`, accountID).Scan(&terminated); err != nil || terminated != 0 {
		t.Fatalf("expired refresh incorrectly terminated grants=%d err=%v", terminated, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE account_id=$1 AND event='refresh_reuse'`, accountID).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("expired refresh was misclassified as replay audit=%d err=%v", audits, err)
	}
}

func TestNativeIssuerRefreshGenerationRecheckedAfterAccountLockWait(t *testing.T) {
	c, db, s, d, cookie, handler := activeNativeFixture(t)
	verifier := strings.Repeat("a", 43)
	accountID := nativePolicyAccountID(t, db, d.subject)
	code := issueNativeCode(t, handler, s.Origin, cookie, "android", c.PublicClients.AndroidRedirect, verifier, "native-refresh-generation-state-01")
	grantResponse := nativeTokenPost(t, handler, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {verifier}})
	_, refresh := decodeNativeTokens(t, grantResponse)
	accountTx := lockNativePolicyAccount(t, db, accountID)
	refreshDone := make(chan *httptest.ResponseRecorder, 1)
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}}
	go func() { refreshDone <- nativeTokenPost(t, handler, refreshForm) }()
	waitForNativeAccountLock(t, db, "refresh generation", refreshDone)
	if _, err := accountTx.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := accountTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := <-refreshDone; out.Code != http.StatusUnauthorized {
		t.Fatalf("refresh succeeded after account generation changed while it waited: %d %s", out.Code, out.Body.String())
	}
	var current int64
	var activeRefresh int
	if err := db.Pool.QueryRow(context.Background(), `SELECT current_refresh_generation FROM public.native_login_grants WHERE account_id=$1`, accountID).Scan(&current); err != nil || current != 1 {
		t.Fatalf("stale-generation refresh changed current generation to %d: %v", current, err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.native_refresh_generations r JOIN public.native_login_grants g ON g.id=r.grant_id WHERE g.account_id=$1 AND r.consumed_at IS NULL`, accountID).Scan(&activeRefresh); err != nil || activeRefresh != 1 {
		t.Fatalf("stale-generation refresh consumed its token rows=%d err=%v", activeRefresh, err)
	}
}

func nativePolicyAccountID(t *testing.T, db *database.Runtime, subject string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id::text FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func lockNativePolicyAccount(t *testing.T, db *database.Runtime, accountID string) pgx.Tx {
	t.Helper()
	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err = tx.QueryRow(context.Background(), `SELECT id::text FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&id); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func waitForNativeAccountLock(t *testing.T, db *database.Runtime, phase string, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := db.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE state='active' AND wait_event_type='Lock' AND query ILIKE '%public.accounts%' AND query ILIKE '%FOR UPDATE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case response := <-done:
			t.Fatalf("%s request returned before blocking on the account row: status=%d body=%s", phase, response.Code, response.Body.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, err := db.Pool.Query(context.Background(), `SELECT COALESCE(state,'unknown'),COALESCE(wait_event_type,'none'),COALESCE(wait_event,'none'),count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() GROUP BY state,wait_event_type,wait_event ORDER BY count(*) DESC LIMIT 8`)
	if err != nil {
		t.Fatalf("%s request did not wait on account row; pg_stat_activity summary unavailable: %v", phase, err)
	}
	defer rows.Close()
	var snapshot []string
	for rows.Next() {
		var state, eventType, event string
		var count int
		if err = rows.Scan(&state, &eventType, &event, &count); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, fmt.Sprintf("state=%s wait_type=%s wait_event=%s count=%d", state, eventType, event, count))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%s request did not wait on account row within 5s; pg_stat_activity state/wait summary: %s", phase, strings.Join(snapshot, "; "))
}

func activeNativeFixture(t *testing.T) (config.Config, *database.Runtime, *auth.Server, *providerDouble, *http.Cookie, http.Handler) {
	t.Helper()
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	d := newProvider(t)
	d.subject = fmt.Sprintf("native-policy-%d", time.Now().UnixNano())
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, d.subject, d.subject+"@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	handler := s.NativeHandlerForTest(bytes.Repeat([]byte{0x73}, 32))
	return c, db, s, d, cookie, handler
}

func issueNativeCode(t *testing.T, handler http.Handler, origin string, cookie *http.Cookie, client, redirect, verifier, state string) string {
	t.Helper()
	flow, csrf := beginNativeConsent(t, handler, cookie, client, redirect, verifier, state)
	response := confirmNativeConsent(t, handler, origin, cookie, flow, csrf)
	if response.Code != http.StatusFound {
		t.Fatalf("%s native consent: %d %s", client, response.Code, response.Body.String())
	}
	callback, err := url.Parse(response.Header().Get("Location"))
	if err != nil || callback.Query().Get("state") != state || callback.Query().Get("code") == "" {
		t.Fatalf("%s native callback: %s (%v)", client, response.Header().Get("Location"), err)
	}
	return callback.Query().Get("code")
}

func beginNativeConsent(t *testing.T, handler http.Handler, cookie *http.Cookie, client, redirect, verifier, state string) (string, string) {
	t.Helper()
	hash := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}}
	begin := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil)
	begin.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, begin)
	if page.Code != http.StatusOK {
		t.Fatalf("%s authorization page: %d %s", client, page.Code, page.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("native consent form missing flow or csrf")
	}
	return flow[1], csrf[1]
}

func confirmNativeConsent(t *testing.T, handler http.Handler, origin string, cookie *http.Cookie, flow, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	confirm := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(url.Values{"flow": {flow}, "csrf": {csrf}}.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", origin)
	confirm.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, confirm)
	return response
}

func nativeTokenPost(t *testing.T, handler http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	time.Sleep(100 * time.Millisecond)
	return response
}

func decodeNativeTokens(t *testing.T, response *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("native token response: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Access == "" || body.Refresh == "" {
		t.Fatalf("native credentials absent: %s (%v)", response.Body.String(), err)
	}
	return body.Access, body.Refresh
}
