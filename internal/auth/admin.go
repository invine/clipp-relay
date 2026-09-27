package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"clipp-relay/internal/config"
	"github.com/jackc/pgx/v5"
)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type adminIdentity struct {
	id, email, csrf string
	revision        uint64
	version         uint64
}

func reasonOK(reason string) bool {
	switch reason {
	case "routine_administration", "policy_enforcement", "suspected_abuse", "security_response", "support_correction":
		return true
	}
	return false
}

func (s *Server) administrator(w http.ResponseWriter, r *http.Request, mutation bool) (adminIdentity, bool) {
	var actor adminIdentity
	id, version, _, _, csrf, err := s.session(r)
	if err != nil {
		if errors.Is(err, errInvalidSession) {
			fail(w, 401)
		} else if errors.Is(err, errRateLimit) {
			fail(w, 429)
		} else {
			fail(w, 503)
		}
		return actor, false
	}
	policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile)
	if err != nil {
		w.Header().Set("X-Admin-Policy-Revision", "0")
		fail(w, 403)
		return actor, false
	}
	w.Header().Set("X-Admin-Policy-Revision", strconv.FormatUint(policy.Revision, 10))
	var email string
	var verified bool
	var hd *string
	var authenticated time.Time
	err = s.Pool.QueryRow(r.Context(), `SELECT a.email,a.email_verified,a.hosted_domain,ps.google_authenticated_at
FROM public.accounts a JOIN public.portal_sessions ps ON ps.account_id=a.id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()`, id, s.sessionDigest(r, version), version).Scan(&email, &verified, &hd, &authenticated)
	if err != nil {
		fail(w, 503)
		return actor, false
	}
	if !policy.Allows(email, verified, deref(hd)) {
		fail(w, 403)
		return actor, false
	}
	if mutation {
		if time.Since(authenticated) > 10*time.Minute || authenticated.After(time.Now().Add(time.Minute)) {
			fail(w, 401)
			return actor, false
		}
		if r.Header.Get("Origin") != s.Origin {
			fail(w, 403)
			return actor, false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
			fail(w, 403)
			return actor, false
		}
		if !reasonOK(r.PostForm.Get("reason")) {
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
		return 401
	}
	if err != nil {
		return 503
	}
	if !recent {
		return 401
	}
	policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile)
	if err != nil {
		w.Header().Set("X-Admin-Policy-Revision", "0")
		return 403
	}
	w.Header().Set("X-Admin-Policy-Revision", strconv.FormatUint(policy.Revision, 10))
	if !policy.Allows(email, verified, deref(hd)) {
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
		return 401
	}
	if err != nil {
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
	ID, Email, Status, PlanID string
	Revision                  int64
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

var adminPage = template.Must(template.New("admin").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Clipp Relay administration</title><style>body{font:16px system-ui;background:#f8fafc;color:#172033;margin:0}.shell{max-width:1200px;margin:3vh auto;padding:28px;background:white;border:1px solid #e2e8f0;border-radius:18px}section{margin:20px 0;padding:18px;border:1px solid #e2e8f0;border-radius:12px}table{border-collapse:collapse;width:100%}th,td{padding:8px;border-bottom:1px solid #e2e8f0;text-align:left}input,select,button{font:inherit;padding:6px;margin:3px}button{background:#183f75;color:white;border:0;border-radius:8px}.muted{color:#64748b}</style><main class="shell"><a href="/">Your account</a><h1>Relay administration</h1><p class="muted">Applied allowlist revision {{.PolicyRevision}}</p><section><h2>Service totals</h2><p>Pending {{.Pending}} · Active {{.Active}} · Suspended {{.Suspended}} · Denied {{.Denied}}</p></section><section><h2>Accounts</h2><table><tr><th>Email</th><th>Status</th><th>Revision</th><th>Plan</th><th></th></tr>{{range .Accounts}}<tr><td>{{.Email}}</td><td>{{.Status}}</td><td>{{.Revision}}</td><td>{{.PlanID}}</td><td><a href="/admin?selected={{.ID}}&accounts_after={{$.AccountCursor}}&plans_after={{$.PlanCursor}}&limit={{$.Limit}}">Select</a></td></tr>{{end}}</table>{{if .NextAccount}}<a href="/admin?accounts_after={{.NextAccount}}&plans_after={{.PlanCursor}}&selected={{.Selected}}&limit={{.Limit}}">Next accounts</a>{{end}}</section><section><h2>Selected account</h2>{{with .SelectedAccount}}<p>{{.Email}} · {{.Status}} · revision {{.Revision}}</p><form method="post" action="/admin/accounts/{{.ID}}"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="revision" value="{{.Revision}}"><label>Action <select name="action"><option value="approve">Approve</option><option value="deny">Deny</option><option value="assign">Assign replacement plan</option></select></label><label>Plan <select name="plan_id">{{range $.Plans}}{{if not .Archived}}<option value="{{.ID}}">{{.Name}} ({{.Bytes}} bytes/week, {{.Sessions}} sessions)</option>{{end}}{{end}}</select></label><label>Reason <select name="reason"><option>routine_administration</option><option>policy_enforcement</option><option>suspected_abuse</option><option>security_response</option><option>support_correction</option></select></label><button>Apply</button></form>{{end}}</section><section><h2>Quota Plans</h2><table><tr><th>Name</th><th>Weekly bytes</th><th>Sessions</th><th>Revision</th><th>State</th><th></th></tr>{{range .Plans}}<tr><td>{{.Name}}</td><td>{{.Bytes}}</td><td>{{.Sessions}}</td><td>{{.Revision}}</td><td>{{if .Archived}}Archived{{else}}Available{{end}}</td><td>{{if not .Archived}}<form method="post" action="/admin/plans/{{.ID}}/archive"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="revision" value="{{.Revision}}"><input type="hidden" name="reason" value="routine_administration"><button>Archive</button></form>{{end}}</td></tr>{{end}}</table>{{if .NextPlan}}<a href="/admin?plans_after={{.NextPlan}}&accounts_after={{.AccountCursor}}&selected={{.Selected}}&limit={{.Limit}}">Next plans</a>{{end}}<h3>Create plan</h3><form method="post" action="/admin/plans"><label>Name <input name="name" maxlength="120" required></label><label>Weekly bytes <input name="weekly_bytes" type="number" min="0" required></label><label>Sessions <input name="sessions" type="number" min="0" required></label><label>Reason <select name="reason"><option>routine_administration</option><option>policy_enforcement</option><option>suspected_abuse</option><option>security_response</option><option>support_correction</option></select></label><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="operation_id" value="{{.OperationID}}"><input type="hidden" name="operation_proof" value="{{.OperationProof}}"><button>Create</button></form></section></main></html>`))

var planRetryPage = template.Must(template.New("plan-retry").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Retry plan creation</title><style>body{font:16px system-ui;background:#f8fafc;color:#172033}.shell{max-width:680px;margin:8vh auto;padding:28px;background:white;border:1px solid #e2e8f0;border-radius:18px}button{background:#183f75;color:white;border:0;border-radius:8px;padding:10px 16px;font:inherit}</style><main class="shell"><h1>Plan result could not be confirmed</h1><p>Retry this same operation to check or finish it. Do not start a new plan form for this attempt.</p><form method="post" action="/admin/plans"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="operation_id" value="{{.OperationID}}"><input type="hidden" name="operation_proof" value="{{.OperationProof}}"><input type="hidden" name="reason" value="{{.Reason}}"><input type="hidden" name="name" value="{{.Name}}"><input type="hidden" name="weekly_bytes" value="{{.Bytes}}"><input type="hidden" name="sessions" value="{{.Sessions}}"><button>Retry this plan</button></form></main></html>`))

func planRetry(w http.ResponseWriter, r *http.Request, actor adminIdentity, name string, bytes, sessions int64) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = planRetryPage.Execute(w, struct {
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
	err := s.Pool.QueryRow(ctx, `SELECT p.create_request_digest, EXISTS(SELECT 1 FROM public.audit_events e WHERE e.plan_id=p.id AND e.event='plan_created') FROM public.quota_plans p WHERE p.id=$1`, id).Scan(&stored, &audited)
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
	rows, err := s.Pool.Query(r.Context(), `SELECT status,count(*) FROM public.accounts GROUP BY status`)
	if err != nil {
		fail(w, 503)
		return
	}
	for rows.Next() {
		var state string
		var count int64
		if rows.Scan(&state, &count) != nil {
			rows.Close()
			fail(w, 503)
			return
		}
		switch state {
		case "Pending":
			data.Pending = count
		case "Active":
			data.Active = count
		case "Suspended":
			data.Suspended = count
		case "Denied":
			data.Denied = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 503)
		return
	}
	rows, err = s.Pool.Query(r.Context(), `SELECT id,email,status,COALESCE(plan_id::text,''),revision FROM public.accounts WHERE id > COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY id LIMIT $2`, accountAfter, limit+1)
	if err != nil {
		fail(w, 503)
		return
	}
	for rows.Next() {
		var x adminAccountRow
		if rows.Scan(&x.ID, &x.Email, &x.Status, &x.PlanID, &x.Revision) != nil {
			rows.Close()
			fail(w, 503)
			return
		}
		if len(data.Accounts) == limit {
			data.NextAccount = data.Accounts[len(data.Accounts)-1].ID
			break
		}
		data.Accounts = append(data.Accounts, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 503)
		return
	}
	if data.Selected != "" {
		var selected adminAccountRow
		err = s.Pool.QueryRow(r.Context(), `SELECT id,email,status,COALESCE(plan_id::text,''),revision FROM public.accounts WHERE id=$1`, data.Selected).Scan(&selected.ID, &selected.Email, &selected.Status, &selected.PlanID, &selected.Revision)
		if err == pgx.ErrNoRows {
			fail(w, 404)
			return
		}
		if err != nil {
			fail(w, 503)
			return
		}
		data.SelectedAccount = &selected
	}
	rows, err = s.Pool.Query(r.Context(), `SELECT id,name,weekly_bytes,sessions,revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id > COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) ORDER BY id LIMIT $2`, planAfter, limit+1)
	if err != nil {
		fail(w, 503)
		return
	}
	for rows.Next() {
		var x adminPlanRow
		if rows.Scan(&x.ID, &x.Name, &x.Bytes, &x.Sessions, &x.Revision, &x.Archived) != nil {
			rows.Close()
			fail(w, 503)
			return
		}
		if len(data.Plans) == limit {
			data.NextPlan = data.Plans[len(data.Plans)-1].ID
			break
		}
		data.Plans = append(data.Plans, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Values are bounded in the schema, and both lists are capped at 100 rows.
	if err := adminPage.Execute(w, data); err != nil {
		return
	}
}

func revision(raw string) (int64, bool) {
	n, e := strconv.ParseInt(raw, 10, 64)
	return n, e == nil && n > 0
}

func (s *Server) adminAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	target := r.PathValue("id")
	expected, valid := revision(r.PostForm.Get("revision"))
	action := r.PostForm.Get("action")
	if !idPattern.MatchString(target) || !valid || (action != "approve" && action != "deny" && action != "assign") {
		fail(w, 400)
		return
	}
	plan := r.PostForm.Get("plan_id")
	if action != "deny" && !idPattern.MatchString(plan) {
		fail(w, 400)
		return
	}
	if action == "deny" {
		var subject string
		if e := s.Pool.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1`, target).Scan(&subject); e != nil {
			fail(w, 503)
			return
		}
		_ = s.WithIdentityFence(subject, func() error {
			s.adminAccountUpdate(w, r, actor, target, expected, action, plan)
			return nil
		})
		return
	}
	s.adminAccountUpdate(w, r, actor, target, expected, action, plan)
}

func (s *Server) adminAccountUpdate(w http.ResponseWriter, r *http.Request, actor adminIdentity, target string, expected int64, action, plan string) {
	ctx := r.Context()
	tx, e := s.Pool.Begin(ctx)
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
	var actual int64
	e = tx.QueryRow(ctx, `SELECT status,revision FROM public.accounts WHERE id=$1`, target).Scan(&status, &actual)
	if e == pgx.ErrNoRows {
		fail(w, 404)
		return
	}
	if e != nil {
		fail(w, 503)
		return
	}
	if actual != expected {
		fail(w, 409)
		return
	}
	if action == "approve" && status != "Pending" || action == "deny" && status != "Pending" || action == "assign" && status != "Active" {
		fail(w, 409)
		return
	}
	if action != "deny" {
		var archived bool
		e = tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM public.quota_plans WHERE id=$1 FOR UPDATE`, plan).Scan(&archived)
		if e == pgx.ErrNoRows || archived {
			fail(w, 409)
			return
		}
		if e != nil {
			fail(w, 503)
			return
		}
	}
	newStatus := status
	event := "plan_assigned"
	if action == "approve" {
		newStatus = "Active"
		event = "account_approved"
	}
	if action == "deny" {
		newStatus = "Denied"
		event = "account_denied"
	}
	if action == "deny" {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET status=$2,revision=revision+1 WHERE id=$1`, target, newStatus)
	} else {
		_, e = tx.Exec(ctx, `UPDATE public.accounts SET status=$2,plan_id=$3,revision=revision+1 WHERE id=$1`, target, newStatus, plan)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE public.quota_plans SET first_assigned_at=COALESCE(first_assigned_at,clock_timestamp()) WHERE id=$1`, plan)
		}
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
	if action != "deny" {
		auditPlan = plan
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6)`, auditID, event, target, auditPlan, r.PostForm.Get("reason"), actor.email)
	if e != nil || tx.Commit(ctx) != nil {
		fail(w, 503)
		return
	}
	http.Redirect(w, r, "/admin?selected="+url.QueryEscape(target), http.StatusSeeOther)
}

func (s *Server) adminPlan(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	bytes, e1 := strconv.ParseInt(r.PostForm.Get("weekly_bytes"), 10, 64)
	sessions, e2 := strconv.ParseInt(r.PostForm.Get("sessions"), 10, 32)
	if name == "" || len(name) > 120 || e1 != nil || e2 != nil || bytes < 0 || sessions < 0 {
		fail(w, 400)
		return
	}
	id := r.PostForm.Get("operation_id")
	proof := r.PostForm.Get("operation_proof")
	if !idPattern.MatchString(id) || subtle.ConstantTimeCompare([]byte(proof), []byte(s.planOperationProof(r, actor, id))) != 1 {
		fail(w, 403)
		return
	}
	ctx := r.Context()
	tx, e := s.Pool.Begin(ctx)
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
			planRetry(w, r, actor, name, bytes, sessions)
			return
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminArchivePlan(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	expected, valid := revision(r.PostForm.Get("revision"))
	if !idPattern.MatchString(id) || !valid {
		fail(w, 400)
		return
	}
	ctx := r.Context()
	tx, e := s.Pool.Begin(ctx)
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
	if e != nil || tx.Commit(ctx) != nil {
		fail(w, 503)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
