package auth_test

import (
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
)

func TestOfflineRecoveryPreservesReceiptsInvalidatesCredentialsAndIsIdempotent(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["recovery-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	mc, err := config.Load(os.Getenv("CLIPP_TEST_MIGRATION_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	mm, err := mc.ReadDatabaseMaterial()
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := database.NewPool(context.Background(), mc, mm, true)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	s := newTestServer(t, maintenance, c, m, d.endpoints())
	subject := fmt.Sprintf("recovery-%d", time.Now().UnixNano())
	cookie := loginAs(t, s, d, subject, "recovery@example.test", "")
	var id string
	ctx := context.Background()
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	access, refresh := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect))
	oldCode := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	q := newQuota(db.Pool)
	defer q.Close()
	op, _ := quota.NewOperationID()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO public.weekly_relay_usage VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,12345,1,$2,12345)`, id, op); err != nil {
		t.Fatal(err)
	}
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "recovery-fixture", CoverageHash: zero, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	s.SetDeletionJournal(j, "recovery-fixture", 0, zero)
	run, _ := quota.NewOperationID()
	request := auth.RecoveryRequest{RunID: run, Source: "isolated-backup", Target: c.Database.Name}
	s.SetRecoveryCommitHookForTest(func(ctx context.Context, tx persistence.RecoveryTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost recovery commit acknowledgement")
	})
	if err := s.RecoverOffline(ctx, request); err != nil {
		t.Fatal(err)
	}
	if got := portalRequest(s, "GET", "/", cookie, nil, ""); got.Code != http.StatusOK || !containsSignedOut(got.Body.String()) {
		t.Fatal("restored portal credential survived")
	}
	if _, err := q.Eligibility(ctx, id); err == nil {
		t.Fatal("recovery hold allowed admission")
	}
	if _, err := s.AuthenticateRelay(ctx, access); err == nil {
		t.Fatal("restored relay access survived")
	}
	if _, _, err := s.AuthenticateAccess(ctx, access); err == nil {
		t.Fatal("restored discovery access survived")
	}
	if out := tokenPost(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}}); out.Code == 200 {
		t.Fatal("restored refresh survived")
	}
	if out := tokenPost(s, url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "code": {oldCode}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_verifier": {strings.Repeat("a", 43)}}); out.Code == 200 {
		t.Fatal("restored code survived")
	}
	var generation int64
	var held bool
	if err := db.Pool.QueryRow(ctx, `SELECT credential_generation,recovery_review_hold FROM public.accounts WHERE id=$1`, id).Scan(&generation, &held); err != nil || !held || generation != 1 {
		t.Fatalf("generation=%d hold=%v %v", generation, held, err)
	}
	usage, err := q.RecordedUsage(ctx, id, time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -((int(time.Now().UTC().Weekday())+6)%7)))
	if err != nil || usage.RecordedBytes != 12345 || usage.Sequence != 1 || usage.OperationID != op {
		t.Fatalf("receipt=%+v %v", usage, err)
	}
	fresh := loginAs(t, s, d, subject, "recovery@example.test", "")
	if got := portalRequest(s, "GET", "/", fresh, nil, ""); !strings.Contains(got.Body.String(), "Recovery Review Hold") {
		t.Fatal("portal concealed recovery hold")
	}
	// Begin a fresh PKCE authorization with the new, valid held portal session.
	// Reusing oldCode here would only prove the independent credential reset.
	digest := sha256.Sum256([]byte(strings.Repeat("a", 43)))
	authorize := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "state": {"fresh-held-authorization"}}
	if out := portalRequest(s, "GET", "/oauth/authorize?"+authorize.Encode(), fresh, nil, ""); out.Code != http.StatusServiceUnavailable || !strings.Contains(out.Body.String(), "invalid_grant") || out.Header().Get("Location") != "" {
		t.Fatalf("hold permitted fresh authorization: %d %s", out.Code, out.Body.String())
	}
	admin := loginAs(t, s, d, "recovery-admin-"+subject, "recovery-admin@gmail.com", "")
	adminCSRF := csrfFrom(t, s, admin)
	heldReview := portalRequest(s, "GET", "/admin?selected="+id+"&view=detail", admin, nil, "")
	if heldReview.Code != http.StatusOK || !strings.Contains(heldReview.Body.String(), `class="badge status-Active">Active`) || !strings.Contains(heldReview.Body.String(), "Recovery Review Hold") || !strings.Contains(heldReview.Body.String(), `id="review-recovery-hold"`) || strings.Contains(heldReview.Body.String(), `select name="action"`) {
		t.Fatalf("recovery hold must remain distinct from canonical status with an explicit review action: %d %s", heldReview.Code, heldReview.Body.String())
	}
	review := url.Values{"csrf": {adminCSRF}, "reason": {"security_response"}, "action": {"recovery-review"}, "revision": {"2"}}
	// A current admin must review this separate hold explicitly. Generic status
	// and quota commands never clear it.
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, review, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("review=%d %s", out.Code, out.Body.String())
	}
	if _, err := q.Eligibility(ctx, id); err != nil {
		t.Fatalf("review did not permit fresh eligibility: %v", err)
	}
	freshAccess, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, fresh, "android", c.PublicClients.AndroidRedirect))
	if _, _, err := s.AuthenticateAccess(ctx, freshAccess); err != nil {
		t.Fatalf("review did not permit fresh credential issuance: %v", err)
	}
	if err := s.RecoverOffline(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT credential_generation,recovery_review_hold FROM public.accounts WHERE id=$1`, id).Scan(&generation, &held); err != nil || held || generation != 1 {
		t.Fatalf("retry changed reviewed account: %d %v %v", generation, held, err)
	}
	request.Source = "different-backup"
	if err := s.RecoverOffline(ctx, request); err == nil {
		t.Fatal("conflicting run accepted")
	}
}

func TestOfflineRecoveryRestoredOldUUIDCannotResurrectOrDebitFreshRegistration(t *testing.T) {
	p := protectedPortalFixture(t)
	ctx := context.Background()
	mc, err := config.Load(os.Getenv("CLIPP_TEST_MIGRATION_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	mm, err := mc.ReadDatabaseMaterial()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewPool(ctx, mc, mm, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	c, m, _ := fixture(t)
	offline := newTestServer(t, pool, c, m, p.provider.endpoints())
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "restore-fixture", CoverageHash: zero, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
	p.auth.SetDeletionJournal(j, "restore-fixture", 0, zero)
	offline.SetDeletionJournal(j, "restore-fixture", 0, zero)
	var subject string
	if err = pool.QueryRow(ctx, `SELECT subject FROM public.accounts WHERE id=$1`, p.account).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	week := time.Now().UTC()
	week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -((int(week.Weekday()) + 6) % 7))
	op, _ := quota.NewOperationID()
	if _, err = p.quota.Settle(ctx, quota.Settlement{AccountID: p.account, WeekStart: week, OperationID: op, Sequence: 1, DeltaBytes: 12345}); err != nil {
		t.Fatal(err)
	}
	// Snapshot rows to memory as a logical local restore fixture. The independent
	// journal deliberately remains outside this restored snapshot.
	backup := map[string][]byte{}
	tables := []string{"native_authorization_codes", "native_login_grants", "native_refresh_generations", "native_access_tokens", "portal_sessions"}
	for _, table := range tables {
		where := "t.account_id=$1"
		if table == "native_refresh_generations" || table == "native_access_tokens" {
			where = "t.grant_id IN(SELECT id FROM public.native_login_grants WHERE account_id=$1)"
		}
		var data []byte
		if err = pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t)),'[]'::jsonb) FROM public.`+table+` t WHERE `+where, p.account).Scan(&data); err != nil {
			t.Fatal(err)
		}
		backup[table] = data
	}
	out := portalRequest(p.auth, "POST", "/auth/delete", p.owner, url.Values{"csrf": {csrfFrom(t, p.auth, p.owner)}}, p.config.PortalOrigin)
	if out.Code != http.StatusAccepted {
		t.Fatalf("delete=%d %s", out.Code, out.Body.String())
	}
	if err = p.auth.ReconcileOneDeletion(ctx); err != nil {
		t.Fatal(err)
	}
	// Restore the predeletion account and credentials into the isolated target.
	// No deletion operation need have existed in the restored backup.
	if _, err = pool.Exec(ctx, `DELETE FROM public.deletion_operations WHERE account_id=$1`, p.account); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"native_access_tokens", "native_refresh_generations", "native_login_grants", "native_authorization_codes", "portal_sessions"} {
		where := "account_id=$1"
		if table == "native_access_tokens" || table == "native_refresh_generations" {
			where = "grant_id IN(SELECT id FROM public.native_login_grants WHERE account_id=$1)"
		}
		if _, err = pool.Exec(ctx, `DELETE FROM public.`+table+` WHERE `+where, p.account); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE public.accounts SET issuer='https://accounts.google.com',subject=$2,email='restored@example.test',email_verified=true,plan_id='6dd09395-51a0-451c-96b3-716e6038e870',status='Active',credential_generation=0,revision=2,deleted_at=NULL,deletion_started_at=NULL WHERE id=$1`, p.account, subject); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if _, err = pool.Exec(ctx, `INSERT INTO public.`+table+` SELECT * FROM jsonb_populate_recordset(NULL::public.`+table+`,$1)`, backup[table]); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := quota.NewOperationID()
	request := auth.RecoveryRequest{RunID: run, Source: "predelete-backup", Target: c.Database.Name}
	// A valid orphan at the committed sequence is independently uploaded but
	// absent from the head-linked path. It must never erase a surviving UUID.
	survivorSubject := "orphan-survivor-" + subject
	_ = loginAs(t, p.auth, p.provider, survivorSubject, "survivor@example.test", "")
	var survivor string
	if err = pool.QueryRow(ctx, `SELECT id FROM public.accounts WHERE subject=$1`, survivorSubject).Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	var committedData []byte
	for _, v := range j.events {
		committedData = v
		break
	}
	j.mu.Unlock()
	var event struct {
		Format       int       `json:"format"`
		AccountID    string    `json:"account_id"`
		EventID      string    `json:"event_id"`
		Sequence     int64     `json:"sequence"`
		PreviousHash string    `json:"previous_hash"`
		AdmittedAt   time.Time `json:"admitted_at"`
		Hash         string    `json:"hash"`
	}
	if err = json.Unmarshal(committedData, &event); err != nil {
		t.Fatal(err)
	}
	event.AccountID = survivor
	event.EventID, _ = quota.NewOperationID()
	event.Hash = ""
	bare, _ := json.Marshal(event)
	sum := sha256.Sum256(bare)
	event.Hash = hex.EncodeToString(sum[:])
	orphan, _ := json.Marshal(event)
	j.mu.Lock()
	j.events[fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID)] = orphan
	j.mu.Unlock()
	moving := &movingRecoveryJournal{DeletionJournal: j}
	offline.SetDeletionJournal(moving, "restore-fixture", 0, zero)
	if err = offline.RecoverOffline(ctx, request); err == nil {
		t.Fatal("moving journal snapshot accepted")
	}
	offline.SetDeletionJournal(j, "restore-fixture", 0, zero)
	// Missing committed chain material must fail before credential/reset/hold or
	// completion changes. An orphan upload never substitutes for the linked head.
	j.mu.Lock()
	var key string
	var data []byte
	for k, v := range j.events {
		if string(v) == string(committedData) {
			key, data = k, v
			break
		}
	}
	delete(j.events, key)
	j.mu.Unlock()
	if err = offline.RecoverOffline(ctx, request); err == nil {
		t.Fatal("missing chain accepted")
	}
	var generation int64
	if err = pool.QueryRow(ctx, `SELECT credential_generation FROM public.accounts WHERE id=$1`, p.account).Scan(&generation); err != nil || generation != 0 {
		t.Fatalf("failed proof mutated restore: %d %v", generation, err)
	}
	j.mu.Lock()
	j.events[key] = data
	j.mu.Unlock()
	offline.SetDeletionJournal(j, "wrong-repository", 0, zero)
	if err = offline.RecoverOffline(ctx, request); err == nil {
		t.Fatal("wrong journal repository accepted")
	}
	offline.SetDeletionJournal(j, "restore-fixture", 0, zero)
	if err = offline.RecoverOffline(ctx, request); err != nil {
		t.Fatal(err)
	}
	var survivorHeld bool
	if err = pool.QueryRow(ctx, `SELECT recovery_review_hold FROM public.accounts WHERE id=$1 AND deleted_at IS NULL AND subject=$2`, survivor, survivorSubject).Scan(&survivorHeld); err != nil || !survivorHeld {
		t.Fatalf("orphan erased survivor: %v", err)
	}
	var sealed bool
	if err = pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND subject IS NULL AND email IS NULL FROM public.accounts WHERE id=$1`, p.account).Scan(&sealed); err != nil || !sealed {
		t.Fatalf("old UUID resurrected: %v %v", sealed, err)
	}
	if _, err = p.auth.AuthenticateRelay(ctx, p.access); err == nil {
		t.Fatal("restored relay credential remained valid")
	}
	if got := portalRequest(p.auth, "GET", "/", p.owner, nil, ""); !containsSignedOut(got.Body.String()) {
		t.Fatal("restored portal credential remained valid")
	}
	fresh := loginAs(t, p.auth, p.provider, subject, "fresh@example.test", "")
	var freshID, status string
	if err = pool.QueryRow(ctx, `SELECT id,status FROM public.accounts WHERE subject=$1`, subject).Scan(&freshID, &status); err != nil || freshID == p.account || status != "Pending" {
		t.Fatalf("fresh registration=%s %s %v", freshID, status, err)
	}
	usage, err := p.quota.RecordedUsage(ctx, freshID, week)
	if err != nil || usage.RecordedBytes != 12345 || usage.Sequence != 0 || usage.OperationID != "" {
		t.Fatalf("retained import=%+v %v", usage, err)
	}
	// A later recovery sees the same old immutable UUID, and cannot erase the new
	// registration even though its external identity is identical.
	request.RunID, _ = quota.NewOperationID()
	if err = offline.RecoverOffline(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM public.accounts WHERE id=$1 AND deleted_at IS NULL`, freshID).Scan(&status); err != nil || status != "Pending" {
		t.Fatalf("fresh UUID deleted by old journal: %v", err)
	}
	_ = fresh
	if _, err = p.quota.Settle(ctx, quota.Settlement{AccountID: p.account, WeekStart: week, OperationID: op, Sequence: 1, DeltaBytes: 12345}); err != quota.ErrUsageRetired {
		t.Fatalf("old receipt revived: %v", err)
	}
}

// Changes only the boundary ETag between proof and mutation, modeling a current
// independent journal writer that has not actually been fenced by the operator.
type movingRecoveryJournal struct {
	auth.DeletionJournal
	mu    sync.Mutex
	reads int
}

func (j *movingRecoveryJournal) ReadHead(ctx context.Context) (auth.JournalHead, string, error) {
	h, tag, err := j.DeletionJournal.ReadHead(ctx)
	j.mu.Lock()
	j.reads++
	if j.reads > 1 {
		tag += "-changed"
	}
	j.mu.Unlock()
	return h, tag, err
}
