package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
	"github.com/jackc/pgx/v5"
)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type adminIdentity struct {
	id, email, csrf string
	revision        uint64
	version         uint64
}

type adminFormErrorKey struct{}

func adminAccountDiagnosticEvent(action string) string {
	switch action {
	case "approve", "suspend", "deny", "reactivate":
		return "admin." + action
	case "assign":
		return "admin.assign_plan"
	case "override":
		return "admin.override_quota"
	case "revoke":
		return "admin.revoke_credentials"
	}
	return ""
}

type accountActionSpec struct {
	states       map[string]bool
	event        string
	nextStatus   string
	security     bool
	planRequired bool
}

var accountActions = map[string]accountActionSpec{
	"recovery-review": {map[string]bool{"Pending": true, "Active": true, "Suspended": true, "Denied": true}, "account_reviewed", "", false, false},
	"approve":         {map[string]bool{"Pending": true}, "account_approved", "Active", false, true},
	"deny":            {map[string]bool{"Pending": true, "Active": true, "Suspended": true}, "account_denied", "Denied", true, false},
	"assign":          {map[string]bool{"Active": true}, "plan_assigned", "", false, true},
	"suspend":         {map[string]bool{"Active": true}, "account_suspended", "Suspended", true, false},
	"reactivate":      {map[string]bool{"Suspended": true}, "account_reactivated", "Active", false, false},
	"review":          {map[string]bool{"Denied": true}, "account_reviewed", "Pending", false, false},
	"revoke":          {map[string]bool{"Pending": true, "Active": true, "Suspended": true, "Denied": true}, "credentials_revoked", "", true, false},
	"override":        {map[string]bool{"Active": true}, "quota_overridden", "", false, false},
}

func reasonOK(reason string) bool {
	switch reason {
	case "routine_administration", "policy_enforcement", "suspected_abuse", "security_response", "support_correction":
		return true
	}
	return false
}

func uncertainResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "1")
	portal.Render(w, http.StatusServiceUnavailable, "uncertain", nil)
}

func (s *Server) administrator(w http.ResponseWriter, r *http.Request, mutation bool) (adminIdentity, bool) {
	var actor adminIdentity
	id, version, _, _, csrf, err := s.session(r)
	if err != nil {
		if errors.Is(err, errInvalidSession) {
			markDiagnostic(r, "refused", "not_authorized")
			fail(w, 401)
		} else if errors.Is(err, errRateLimit) {
			fail(w, 429)
		} else {
			markDiagnostic(r, "failed", "database_unavailable")
			fail(w, 503)
		}
		return actor, false
	}
	policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile)
	if err != nil {
		markDiagnostic(r, "refused", "not_authorized")
		w.Header().Set("X-Admin-Policy-Revision", "0")
		fail(w, 403)
		return actor, false
	}
	w.Header().Set("X-Admin-Policy-Revision", strconv.FormatUint(policy.Revision, 10))
	if s.beforeAdminDetailsRead != nil {
		s.beforeAdminDetailsRead()
	}
	var email string
	var verified bool
	var hd *string
	var authenticated time.Time
	err = s.db.QueryRow(r.Context(), `SELECT a.email,a.email_verified,a.hosted_domain,ps.google_authenticated_at
FROM public.accounts a JOIN public.portal_sessions ps ON ps.account_id=a.id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()`, id, s.sessionDigest(r, version), version).Scan(&email, &verified, &hd, &authenticated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			markDiagnostic(r, "refused", "not_authorized")
			fail(w, 401)
		} else {
			markDiagnostic(r, "failed", "database_unavailable")
			fail(w, 503)
		}
		return actor, false
	}
	if !policy.Allows(email, verified, deref(hd)) {
		markDiagnostic(r, "refused", "not_authorized")
		fail(w, 403)
		return actor, false
	}
	if mutation {
		if time.Since(authenticated) > 10*time.Minute || authenticated.After(time.Now().Add(time.Minute)) {
			markDiagnostic(r, "refused", "recent_auth_required")
			fail(w, 401)
			return actor, false
		}
		if r.Header.Get("Origin") != s.Origin {
			markDiagnostic(r, "refused", "origin_rejected")
			fail(w, 403)
			return actor, false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		parseErr := r.ParseForm()
		if prior, ok := r.Context().Value(adminFormErrorKey{}).(error); ok {
			parseErr = prior
		}
		if parseErr != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
			markDiagnostic(r, "refused", "csrf_rejected")
			fail(w, 403)
			return actor, false
		}
		if !reasonOK(r.PostForm.Get("reason")) {
			markDiagnostic(r, "refused", "invalid_request")
			fail(w, 400)
			return actor, false
		}
	}
	actor = adminIdentity{id: id, email: strings.ToLower(strings.TrimSpace(email)), csrf: csrf, revision: policy.Revision, version: version}
	return actor, true
}

// Called after ordered account locks, so wall time and metadata cannot be
// evaluated before a competing administrator or session operation completes.
func (s *Server) recheckAdministrator(w http.ResponseWriter, tx pgx.Tx, r *http.Request, actor *adminIdentity) int {
	if s.beforeAdminRecheck != nil {
		s.beforeAdminRecheck()
	}
	var email string
	var verified, recent bool
	var hd *string
	err := tx.QueryRow(r.Context(), `SELECT a.email,a.email_verified,a.hosted_domain,
ps.google_authenticated_at >= clock_timestamp()-interval '10 minutes'
AND ps.google_authenticated_at <= clock_timestamp()+interval '1 minute'
FROM public.accounts a JOIN public.portal_sessions ps ON ps.account_id=a.id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3
AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()`, actor.id, s.sessionDigest(r, actor.version), actor.version).Scan(&email, &verified, &hd, &recent)
	if err == pgx.ErrNoRows {
		markDiagnostic(r, "refused", "not_authorized")
		return 401
	}
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		return 503
	}
	if !recent {
		markDiagnostic(r, "refused", "recent_auth_required")
		return 401
	}
	policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile)
	if err != nil {
		markDiagnostic(r, "refused", "not_authorized")
		w.Header().Set("X-Admin-Policy-Revision", "0")
		return 403
	}
	w.Header().Set("X-Admin-Policy-Revision", strconv.FormatUint(policy.Revision, 10))
	if !policy.Allows(email, verified, deref(hd)) {
		markDiagnostic(r, "refused", "not_authorized")
		return 403
	}
	actor.email = strings.ToLower(strings.TrimSpace(email))
	actor.revision = policy.Revision
	return 0
}

func (s *Server) lockActor(w http.ResponseWriter, tx pgx.Tx, r *http.Request, actor *adminIdentity) int {
	var id string
	err := tx.QueryRow(r.Context(), `SELECT id FROM public.accounts WHERE id=$1 FOR UPDATE`, actor.id).Scan(&id)
	if err == pgx.ErrNoRows {
		markDiagnostic(r, "refused", "not_authorized")
		return 401
	}
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		return 503
	}
	return s.recheckAdministrator(w, tx, r, actor)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func (s *Server) sessionDigest(r *http.Request, version uint64) []byte {
	c, _ := r.Cookie(sessionCookie)
	return s.digest(version, "portal-session", c.Value)
}

type adminAccountRow struct {
	RecoveryHeld                                               bool
	ID, Email, Status, PlanID, WeeklyOverride, SessionOverride string
	Revision                                                   int64
	Usage                                                      *accountUsageQuery
}
type adminPlanRow struct {
	ID, Name string
	Bytes    int64
	Sessions int
	Revision int64
	Archived bool
}
type adminPageData struct {
	CSRF                               string
	PolicyRevision                     uint64
	Pending, Active, Suspended, Denied int64
	Accounts                           []adminAccountRow
	Plans                              []adminPlanRow
	NextAccount, NextPlan              string
	AccountCursor, PlanCursor          string
	Limit                              int
	Selected                           string
	SelectedAccount                    *adminAccountRow
	OperationID, OperationProof        string
}

func planRetry(w http.ResponseWriter, r *http.Request, actor adminIdentity, name string, bytes, sessions int64) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "1")
	portal.Render(w, http.StatusServiceUnavailable, "plan-retry", struct {
		CSRF, OperationID, OperationProof, Reason, Name string
		Bytes, Sessions                                 int64
	}{actor.csrf, r.PostForm.Get("operation_id"), r.PostForm.Get("operation_proof"), r.PostForm.Get("reason"), name, bytes, sessions})
}

func pageLimit(raw string) (int, bool) {
	if raw == "" {
		return 50, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= 1 && n <= 100
}

func (s *Server) planOperationProof(r *http.Request, actor adminIdentity, id string) string {
	c, _ := r.Cookie(sessionCookie)
	return base64.RawURLEncoding.EncodeToString(s.digest(actor.version, "plan-create-operation", id+"\x00"+c.Value))
}

func planRequestDigest(actorID, name, reason string, bytes, sessions int64) [32]byte {
	b, _ := json.Marshal(struct {
		ActorID, Name, Reason string
		Bytes, Sessions       int64
	}{actorID, name, reason, bytes, sessions})
	return sha256.Sum256(b)
}

func (s *Server) planCreateCommitted(ctx context.Context, id string, digest [32]byte) bool {
	var stored []byte
	var audited bool
	err := s.db.QueryRow(ctx, `SELECT p.create_request_digest, EXISTS(SELECT 1 FROM public.audit_events e WHERE e.plan_id=p.id AND e.event='plan_created') FROM public.quota_plans p WHERE p.id=$1`, id).Scan(&stored, &audited)
	return err == nil && audited && subtle.ConstantTimeCompare(stored, digest[:]) == 1
}

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, false)
	if !ok {
		return
	}
	limit, valid := pageLimit(r.URL.Query().Get("limit"))
	if !valid {
		fail(w, 400)
		return
	}
	accountAfter, planAfter := r.URL.Query().Get("accounts_after"), r.URL.Query().Get("plans_after")
	if accountAfter != "" && !idPattern.MatchString(accountAfter) || planAfter != "" && !idPattern.MatchString(planAfter) {
		fail(w, 400)
		return
	}
	data := adminPageData{CSRF: actor.csrf, PolicyRevision: actor.revision, Selected: r.URL.Query().Get("selected"), AccountCursor: accountAfter, PlanCursor: planAfter, Limit: limit}
	operationID, err := uuid()
	if err != nil {
		fail(w, 503)
		return
	}
	data.OperationID = operationID
	data.OperationProof = s.planOperationProof(r, actor, data.OperationID)
	if data.Selected != "" && !idPattern.MatchString(data.Selected) {
		fail(w, 400)
		return
	}
	views, err := s.readViews.AdminHome(r.Context(), accountAfter, planAfter, limit)
	if err != nil {
		fail(w, 503)
		return
	}
	data.Pending, data.Active, data.Suspended, data.Denied = views.Counts.Pending, views.Counts.Active, views.Counts.Suspended, views.Counts.Denied
	for _, x := range views.Accounts {
		if len(data.Accounts) == limit {
			data.NextAccount = data.Accounts[len(data.Accounts)-1].ID
			break
		}
		data.Accounts = append(data.Accounts, adminAccountRow{ID: x.ID, Email: x.Email, Status: x.Status, PlanID: x.PlanID, WeeklyOverride: x.WeeklyOverride, SessionOverride: x.SessionOverride, Revision: x.Revision, RecoveryHeld: x.RecoveryHeld})
	}
	if data.Selected != "" {
		selected, readErr := s.adminAccountUsage(r.Context(), data.Selected)
		err = readErr
		if errors.Is(err, persistence.ErrNotFound) {
			fail(w, 404)
			return
		}
		if err != nil {
			fail(w, 503)
			return
		}
		data.SelectedAccount = &selected
	}
	for _, x := range views.Plans {
		if len(data.Plans) == limit {
			data.NextPlan = data.Plans[len(data.Plans)-1].ID
			break
		}
		data.Plans = append(data.Plans, adminPlanRow{ID: x.ID, Name: x.Name, Bytes: x.Bytes, Sessions: x.Sessions, Revision: x.Revision, Archived: x.Archived})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Values are bounded in the schema, and both lists are capped at 100 rows.
	portal.Render(w, http.StatusOK, "admin", data)
}

func revision(raw string) (int64, bool) {
	n, e := strconv.ParseInt(raw, 10, 64)
	return n, e == nil && n > 0
}

func (s *Server) adminAccount(w http.ResponseWriter, r *http.Request) {
	// Parse once under the same body limit before authorization so a known
	// fixed action can retain its refusal outcome. Preserve a parsing error
	// for administrator(), which still owns the actual CSRF decision.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		r = r.WithContext(context.WithValue(r.Context(), adminFormErrorKey{}, err))
	}
	action := r.PostForm.Get("action")
	if event := adminAccountDiagnosticEvent(action); event != "" {
		markAction(r, event)
	} else if action == "review" {
		// Review has no fixed catalog pair; the route is still handled.
		markAction(r, "")
	}
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	target := r.PathValue("id")
	expected, valid := revision(r.PostForm.Get("revision"))
	spec, known := accountActions[action]
	if !known {
		// Unknown action text never becomes an event name.
		markAction(r, "http.request_refused")
	}
	if !idPattern.MatchString(target) || !valid || !known {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, 400)
		return
	}
	plan := r.PostForm.Get("plan_id")
	if spec.planRequired && !idPattern.MatchString(plan) {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, 400)
		return
	}
	var applied *AccountChange
	var uncertain bool
	var invalidationFailed bool
	var securitySubject string
	var closeConnections func()
	if s.beforeAccountActionGuard != nil {
		s.beforeAccountActionGuard()
	}
	update := func(unitCtx context.Context) {
		s.adminAccountUpdate(w, r.WithContext(unitCtx), actor, target, expected, action, spec, plan, securitySubject, &applied, &uncertain)
		if applied != nil {
			closeConnections = s.accountChanged(*applied)
			if applied.CloseAll {
				invalidationFailed = s.deliverAccountInvalidation(applied.AccountID) != nil
			}
		}
	}
	if spec.security {
		var subject string
		if e := s.db.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1 AND deletion_started_at IS NULL`, target).Scan(&subject); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				markDiagnostic(r, "refused", "invalid_request")
			} else {
				markDiagnostic(r, "failed", "database_unavailable")
			}
			fail(w, 503)
			return
		}
		securitySubject = subject
		guardEntered := false
		guardErr := s.withIdentityGuard(r.Context(), subject, func(unitCtx context.Context) error {
			guardEntered = true
			if accountGuardErr := s.withAccountGuards(unitCtx, []string{actor.id, target}, update); accountGuardErr != nil {
				markGuardDiagnostic(r, accountGuardErr)
				fail(w, 503)
			}
			return nil
		})
		if guardErr != nil && !guardEntered {
			markGuardDiagnostic(r, guardErr)
			fail(w, 503)
		}
	} else if accountGuardErr := s.withAccountGuards(r.Context(), []string{actor.id, target}, update); accountGuardErr != nil {
		markGuardDiagnostic(r, accountGuardErr)
		fail(w, 503)
	}
	if applied != nil {
		if closeConnections != nil {
			closeConnections()
		}
		if uncertain {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			uncertainResponse(w)
		} else if invalidationFailed {
			markDiagnostic(r, "failed", "invalidation_incomplete")
			http.Error(w, "Account change committed, but relay session invalidation could not be confirmed.", http.StatusServiceUnavailable)
		} else {
			http.Redirect(w, r, "/admin?selected="+url.QueryEscape(target), http.StatusSeeOther)
		}
	}
}

func (s *Server) adminAccountUpdate(w http.ResponseWriter, r *http.Request, actor adminIdentity, target string, expected int64, action string, spec accountActionSpec, plan, subject string, applied **AccountChange, uncertain *bool) {
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		fail(w, 503)
		return
	}
	defer tx.Rollback(ctx)
	// Lock actor and target in one stable order to avoid cross-admin deadlocks.
	rows, e := tx.Query(ctx, `SELECT id FROM public.accounts WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, actor.id, target)
	if e != nil {
		fail(w, 503)
		return
	}
	for rows.Next() {
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		fail(w, 503)
		return
	}
	if status := s.recheckAdministrator(w, tx, r, &actor); status != 0 {
		fail(w, status)
		return
	}
	var status string
	var deleting bool
	var actual int64
	var oldGeneration int64
	e = tx.QueryRow(ctx, `SELECT status,revision,deletion_started_at IS NOT NULL,credential_generation FROM public.accounts WHERE id=$1`, target).Scan(&status, &actual, &deleting, &oldGeneration)
	if e == pgx.ErrNoRows {
		fail(w, 404)
		return
	}
	if e != nil {
		fail(w, 503)
		return
	}
	if deleting || actual != expected {
		markDiagnostic(r, "refused", "revision_conflict")
		fail(w, 409)
		return
	}
	if !spec.states[status] {
		markDiagnostic(r, "refused", "revision_conflict")
		fail(w, 409)
		return
	}
	var oldPlan *string
	var oldBytes *int64
	var oldSessions *int
	if e = tx.QueryRow(ctx, `SELECT plan_id::text,weekly_bytes_override,sessions_override FROM public.accounts WHERE id=$1`, target).Scan(&oldPlan, &oldBytes, &oldSessions); e != nil {
		fail(w, 503)
		return
	}
	var weeklyBytes int64
	var sessionLimit int
	if action == "approve" || action == "assign" {
		var archived bool
		e = tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL,weekly_bytes,sessions FROM public.quota_plans WHERE id=$1 FOR UPDATE`, plan).Scan(&archived, &weeklyBytes, &sessionLimit)
		if e == pgx.ErrNoRows || archived {
			markDiagnostic(r, "refused", "revision_conflict")
			fail(w, 409)
			return
		}
		if e != nil {
			fail(w, 503)
			return
		}
	} else if oldPlan != nil {
		e = tx.QueryRow(ctx, `SELECT weekly_bytes,sessions FROM public.quota_plans WHERE id=$1`, *oldPlan).Scan(&weeklyBytes, &sessionLimit)
		if e != nil {
			fail(w, 503)
			return
		}
	}
	if oldBytes != nil {
		weeklyBytes = *oldBytes
	}
	if oldSessions != nil {
		sessionLimit = *oldSessions
	}
	newStatus := status
	if spec.nextStatus != "" {
		newStatus = spec.nextStatus
	}
	event, security := spec.event, spec.security
	if action == "override" {
		if values, present := r.PostForm["weekly_bytes_override"]; present && len(values) == 1 && values[0] != "" {
			var n int64
			n, e = parseQuotaBytes(values[0], r.PostForm.Get("weekly_bytes_override_unit"))
			if e != nil || n < 0 {
				fail(w, 400)
				return
			}
			oldBytes, weeklyBytes = &n, n
		} else if present {
			oldBytes = nil
		}
		if values, present := r.PostForm["sessions_override"]; present && len(values) == 1 && values[0] != "" {
			var n int64
			n, e = strconv.ParseInt(values[0], 10, 32)
			if e != nil || n < 0 {
				fail(w, 400)
				return
			}
			v := int(n)
			oldSessions, sessionLimit = &v, v
		} else if present {
			oldSessions = nil
		}
	}
	if action == "recovery-review" {
		tag, err := tx.Exec(ctx, `UPDATE public.accounts SET recovery_review_hold=false,revision=revision+1 WHERE id=$1 AND recovery_review_hold`, target)
		e = err
		if e == nil && tag.RowsAffected() != 1 {
			fail(w, 409)
			return
		}
	} else if action == "approve" || action == "assign" {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET status=$2,plan_id=$3,revision=revision+1 WHERE id=$1`, target, newStatus, plan)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE public.quota_plans SET first_assigned_at=COALESCE(first_assigned_at,clock_timestamp()) WHERE id=$1`, plan)
		}
	} else if action == "override" {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=$2,sessions_override=$3,revision=revision+1 WHERE id=$1`, target, oldBytes, oldSessions)
		if e == nil && (oldBytes == nil || oldSessions == nil) {
			e = tx.QueryRow(ctx, `SELECT COALESCE(a.weekly_bytes_override,p.weekly_bytes),COALESCE(a.sessions_override,p.sessions) FROM public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1`, target).Scan(&weeklyBytes, &sessionLimit)
		}
	} else if security {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET status=$2,revision=revision+1 WHERE id=$1`, target, newStatus)
		if e == nil {
			e = revokeAccountCredentials(ctx, tx, target)
		}
	} else {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET status=$2,revision=revision+1 WHERE id=$1`, target, newStatus)
	}
	if e != nil {
		fail(w, 503)
		return
	}
	auditID, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	var auditPlan any
	if action == "approve" || action == "assign" {
		auditPlan = plan
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6)`, auditID, event, target, auditPlan, r.PostForm.Get("reason"), actor.email)
	if e != nil {
		fail(w, 503)
		return
	}
	var commitErr error
	if s.commitAccount != nil {
		commitErr = s.commitAccount(ctx, tx)
	} else {
		commitErr = tx.Commit(ctx)
	}
	if security {
		s.installIdentityFence(subject)
	}
	if commitErr != nil {
		newGeneration := oldGeneration
		if security {
			newGeneration++
		}
		s.uncertainAccounts.Store(target, uncertainMutation{revision: actual + 1, generation: newGeneration, status: newStatus})
	}
	// Auth invalidates under account protection after SQL completion, then runs
	// the returned connection-close I/O after releasing the logical guards.
	closeAll := security || commitErr != nil
	*applied = &AccountChange{AccountID: target, WeeklyBytes: weeklyBytes, SessionLimit: sessionLimit, CloseAll: closeAll}
	*uncertain = commitErr != nil
}

func (s *Server) adminPlan(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	bytes, e1 := parseQuotaBytes(r.PostForm.Get("weekly_bytes"), r.PostForm.Get("weekly_bytes_unit"))
	sessions, e2 := strconv.ParseInt(r.PostForm.Get("sessions"), 10, 32)
	if name == "" || len(name) > 120 || e1 != nil || e2 != nil || bytes < 0 || sessions < 0 {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, 400)
		return
	}
	id := r.PostForm.Get("operation_id")
	proof := r.PostForm.Get("operation_proof")
	if !idPattern.MatchString(id) || subtle.ConstantTimeCompare([]byte(proof), []byte(s.planOperationProof(r, actor, id))) != 1 {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, 403)
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		fail(w, 503)
		return
	}
	defer tx.Rollback(ctx)
	if status := s.lockActor(w, tx, r, &actor); status != 0 {
		fail(w, status)
		return
	}
	digest := planRequestDigest(actor.id, name, r.PostForm.Get("reason"), bytes, sessions)
	tag, e := tx.Exec(ctx, `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions,create_request_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`, id, name, bytes, sessions, digest[:])
	if e != nil {
		fail(w, 503)
		return
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		var audited bool
		if e = tx.QueryRow(ctx, `SELECT p.create_request_digest, EXISTS(SELECT 1 FROM public.audit_events e WHERE e.plan_id=p.id AND e.event='plan_created') FROM public.quota_plans p WHERE p.id=$1`, id).Scan(&stored, &audited); e != nil {
			fail(w, 503)
			return
		}
		if subtle.ConstantTimeCompare(stored, digest[:]) != 1 {
			markDiagnostic(r, "refused", "revision_conflict")
			fail(w, 409)
			return
		}
		if !audited {
			fail(w, 503)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	auditID, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),'plan_created',$2,$3,$4)`, auditID, id, r.PostForm.Get("reason"), actor.email)
	if e != nil {
		fail(w, 503)
		return
	}
	if s.commitPlan != nil {
		e = s.commitPlan(ctx, tx)
	} else {
		e = tx.Commit(ctx)
	}
	if e != nil {
		// The request may have timed out after PostgreSQL committed. A separate,
		// tightly bounded read must survive that cancellation; replay keeps the
		// same signed operation ID when the outcome still cannot be confirmed.
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		committed := s.planCreateCommitted(recoveryCtx, id, digest)
		cancel()
		if !committed {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			planRetry(w, r, actor, name, bytes, sessions)
			return
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminArchivePlan(w http.ResponseWriter, r *http.Request) {
	markAction(r, "admin.plan_archive")
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	expected, valid := revision(r.PostForm.Get("revision"))
	if !idPattern.MatchString(id) || !valid {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, 400)
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		fail(w, 503)
		return
	}
	defer tx.Rollback(ctx)
	if status := s.lockActor(w, tx, r, &actor); status != 0 {
		fail(w, status)
		return
	}
	var actual int64
	var archived bool
	e = tx.QueryRow(ctx, `SELECT revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id=$1 FOR UPDATE`, id).Scan(&actual, &archived)
	if e == pgx.ErrNoRows {
		fail(w, 404)
		return
	}
	if e != nil {
		fail(w, 503)
		return
	}
	if actual != expected || archived {
		markDiagnostic(r, "refused", "revision_conflict")
		fail(w, 409)
		return
	}
	_, e = tx.Exec(ctx, `UPDATE public.quota_plans SET archived_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, id)
	if e != nil {
		fail(w, 503)
		return
	}
	auditID, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),'plan_archived',$2,$3,$4)`, auditID, id, r.PostForm.Get("reason"), actor.email)
	if e != nil {
		fail(w, 503)
		return
	}
	if tx.Commit(ctx) != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		fail(w, 503)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
