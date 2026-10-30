package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
)

func beginAccountAccessTransaction(op persistence.AccountAccessOperation) (persistence.AccountAccessTransaction, error) {
	return op.BeginAccountAccess(op.Context())
}

func (s *Server) startAccountAccess(ctx context.Context) (persistence.AccountAccessOperation, error) {
	if s.accountAccess == nil {
		return nil, persistence.ErrUnavailable
	}
	return s.accountAccess.StartAccountAccess(ctx)
}

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
	ownerSession, err := s.ownerPortalSession(r)
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
	id, version, csrf := ownerSession.accountID, ownerSession.pepperVersion, ownerSession.csrf
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
	op, err := s.startAccountAccess(r.Context())
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return actor, false
	}
	defer op.Close()
	authTx, err := beginAccountAccessTransaction(op)
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return actor, false
	}
	adminSession, err := authTx.AdministratorSession(op.Context(), id, s.sessionDigest(r, version), version)
	_ = authTx.Rollback(op.Context())
	if err != nil {
		if errors.Is(err, persistence.ErrInvalidCredential) {
			markDiagnostic(r, "refused", "not_authorized")
			fail(w, 401)
		} else {
			markDiagnostic(r, "failed", "database_unavailable")
			fail(w, 503)
		}
		return actor, false
	}
	if !policy.Allows(adminSession.Email, adminSession.Verified, deref(adminSession.HostedDomain)) {
		markDiagnostic(r, "refused", "not_authorized")
		fail(w, 403)
		return actor, false
	}
	if mutation {
		if !adminSession.Recent {
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
	actor = adminIdentity{id: id, email: strings.ToLower(strings.TrimSpace(adminSession.Email)), csrf: csrf, revision: policy.Revision, version: version}
	return actor, true
}

// Called after ordered account locks, so wall time and metadata cannot be
// evaluated before a competing administrator or session operation completes.
func (s *Server) recheckAdministrator(w http.ResponseWriter, tx persistence.AccountAccessTransaction, r *http.Request, actor *adminIdentity) int {
	if s.beforeAdminRecheck != nil {
		s.beforeAdminRecheck()
	}
	session, err := tx.AdministratorSession(r.Context(), actor.id, s.sessionDigest(r, actor.version), actor.version)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		markDiagnostic(r, "refused", "not_authorized")
		return 401
	}
	if err != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		return 503
	}
	if !session.Recent {
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
	if !policy.Allows(session.Email, session.Verified, deref(session.HostedDomain)) {
		markDiagnostic(r, "refused", "not_authorized")
		return 403
	}
	actor.email = strings.ToLower(strings.TrimSpace(session.Email))
	actor.revision = policy.Revision
	return 0
}

func (s *Server) lockActor(w http.ResponseWriter, tx persistence.AccountAccessTransaction, r *http.Request, actor *adminIdentity) int {
	err := tx.LockAdministratorAccounts(r.Context(), actor.id, actor.id)
	if errors.Is(err, persistence.ErrNotFound) {
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
	RecoveryHeld                                                         bool
	ID, Email, Status, PlanID, PlanName, WeeklyOverride, SessionOverride string
	EffectiveBytes                                                       int64
	EffectiveSessions                                                    int
	PlanArchived                                                         bool
	Revision                                                             int64
	Usage                                                                *accountUsageQuery
	ReviewHref                                                           template.URL
}
type adminPlanRow struct {
	ID, Name string
	Bytes    int64
	Sessions int
	Revision int64
	Archived bool
}

type adminAccessActionView struct {
	ID, Action, Title, Effect, Button string
	Destructive                       bool
}

type adminAccessActionCopy struct {
	Action, ID, Title, Effect, Button string
	Destructive                       bool
}

var adminAccessActionCopyByAction = []adminAccessActionCopy{
	{
		Action: "suspend", ID: "suspend-account-access", Title: "Suspend relay access",
		Effect: "Suspends Relay Session admission for %s and invalidates its Login Grants, Relay Access Tokens, Relay Sessions, and Portal Sessions. Authorization ends immediately; physical session closure may finish asynchronously. The saved quota assignment remains unchanged.",
		Button: "Confirm suspension of %s", Destructive: true,
	},
	{
		Action: "deny", ID: "deny-account-access", Title: "Deny relay access",
		Effect: "Changes %s to Denied and invalidates its Login Grants, Relay Access Tokens, Relay Sessions, and Portal Sessions. Authorization ends immediately; physical session closure may finish asynchronously. The saved quota assignment remains unchanged.",
		Button: "Confirm denial of %s", Destructive: true,
	},
	{
		Action: "reactivate", ID: "reactivate-account-access", Title: "Restore relay access",
		Effect: "Changes %s from Suspended to Active. This does not restore any Login Grants, Relay Access Tokens, or Relay Sessions; the owner must authorize an app again.",
		Button: "Restore %s to Active",
	},
	{
		Action: "review", ID: "return-account-review", Title: "Return to pending review",
		Effect: "Changes %s from Denied to Pending. This does not grant relay access; the account must be approved again.",
		Button: "Return %s to Pending review",
	},
	{
		Action: "revoke", ID: "revoke-account-access", Title: "Revoke credentials and sessions",
		Effect: "Keeps %s's canonical status and quota assignment unchanged, while invalidating all Login Grants, Relay Access Tokens, Relay Sessions, and Portal Sessions. Authorization ends immediately; physical session closure may finish asynchronously.",
		Button: "Confirm credential revocation for %s", Destructive: true,
	},
	{
		Action: "recovery-review", ID: "review-recovery-hold", Title: "Complete Recovery Review",
		Effect: "Clears only %s's Recovery Review Hold. Its canonical status and quota assignments stay unchanged; normal status and quota rules continue to control relay access.",
		Button: "Clear Recovery Review Hold for %s",
	},
}

func adminAccessActionsFor(account adminAccountRow) []adminAccessActionView {
	var actions []adminAccessActionView
	for _, copy := range adminAccessActionCopyByAction {
		spec, exists := accountActions[copy.Action]
		if !exists || !spec.states[account.Status] {
			continue
		}
		if copy.Action == "recovery-review" && !account.RecoveryHeld {
			continue
		}
		email := account.Email
		actions = append(actions, adminAccessActionView{
			ID: copy.ID, Action: copy.Action, Title: copy.Title,
			Effect: fmt.Sprintf(copy.Effect, email), Button: fmt.Sprintf(copy.Button, email),
			Destructive: copy.Destructive,
		})
	}
	return actions
}

type adminPageData struct {
	CSRF, Email, RelayHost             string
	PolicyRevision                     uint64
	Pending, Active, Suspended, Denied int64
	Accounts                           []adminAccountRow
	FilteredCount                      int64
	StatusFilter, EmailFilter          string
	AssignablePlans                    []adminPlanRow
	AccessActions                      []adminAccessActionView
	NextAccount, PlanCursor            string
	Limit                              int
	Selected                           string
	DefaultPlanID                      string
	ListHref                           template.URL
	MutationHref                       template.URL
	MobileDetail                       bool
	SelectedAccount                    *adminAccountRow
	OperationID, OperationProof        string
}

type adminPlanLibraryData struct {
	Email, RelayHost, CSRF, After, Next, CreatedID, OperationID, OperationProof string
	PolicyRevision                                                              uint64
	Limit                                                                       int
	Plans                                                                       []adminPlanRow
	Created                                                                     *adminPlanRow
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

type adminListScope struct {
	accountAfter, planAfter, status, email string
	limit                                  int
	query                                  url.Values
}

// adminListScopeFromQuery is the single boundary for administrator list
// context. The list route asks for strict validation; return links ask for a
// safe subset so malformed caller-supplied context is simply discarded.
func adminListScopeFromQuery(r *http.Request, strict bool) (adminListScope, bool) {
	values := r.URL.Query()
	scope := adminListScope{
		accountAfter: values.Get("accounts_after"),
		planAfter:    values.Get("plans_after"),
		status:       values.Get("status"),
		email:        strings.TrimSpace(values.Get("email")),
	}
	var valid bool
	scope.limit, valid = pageLimit(values.Get("limit"))
	if strict && !valid {
		return adminListScope{}, false
	}
	statusValid := scope.status == "" || scope.status == "Pending" || scope.status == "Active" || scope.status == "Suspended" || scope.status == "Denied"
	emailValid := len(scope.email) <= 254
	accountValid := scope.accountAfter == "" || idPattern.MatchString(scope.accountAfter)
	planValid := scope.planAfter == "" || idPattern.MatchString(scope.planAfter)
	if strict && (!statusValid || !emailValid || !accountValid || !planValid) {
		return adminListScope{}, false
	}
	scope.query = url.Values{}
	if statusValid && scope.status != "" {
		scope.query.Set("status", scope.status)
	} else {
		scope.status = ""
	}
	if emailValid && scope.email != "" {
		scope.query.Set("email", scope.email)
	} else if !emailValid {
		scope.email = ""
	}
	if accountValid && scope.accountAfter != "" {
		scope.query.Set("accounts_after", scope.accountAfter)
	} else if !accountValid {
		scope.accountAfter = ""
	}
	if planValid && scope.planAfter != "" {
		scope.query.Set("plans_after", scope.planAfter)
	} else if !planValid {
		scope.planAfter = ""
	}
	if valid && values.Get("limit") != "" {
		scope.query.Set("limit", strconv.Itoa(scope.limit))
	}
	return scope, true
}

func relayHost(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return parsed.Host
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
	op, err := s.startAccountAccess(ctx)
	if err != nil {
		return false
	}
	defer op.Close()
	tx, err := beginAccountAccessTransaction(op)
	if err != nil {
		return false
	}
	defer tx.Rollback(op.Context())
	committed, err := tx.PlanCreateStatus(op.Context(), id, digest[:])
	return err == nil && committed
}

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, false)
	if !ok {
		return
	}
	scope, valid := adminListScopeFromQuery(r, true)
	if !valid {
		fail(w, 400)
		return
	}
	limit, accountAfter, planAfter := scope.limit, scope.accountAfter, scope.planAfter
	statusFilter, emailFilter := scope.status, scope.email
	data := adminPageData{CSRF: actor.csrf, Email: actor.email, RelayHost: relayHost(s.Origin), PolicyRevision: actor.revision, Selected: r.URL.Query().Get("selected"), PlanCursor: planAfter, Limit: limit, StatusFilter: statusFilter, EmailFilter: emailFilter, MobileDetail: r.URL.Query().Get("view") == "detail"}
	operationID, err := uuid()
	if err != nil {
		fail(w, 503)
		return
	}
	data.OperationID = operationID
	data.OperationProof = s.planOperationProof(r, actor, data.OperationID)
	contextQuery := scope.query
	if limit == 50 {
		contextQuery.Del("limit")
	}
	data.ListHref = template.URL("/admin")
	if query := contextQuery.Encode(); query != "" {
		data.ListHref = template.URL("/admin?" + query)
	}
	if data.Selected != "" && !idPattern.MatchString(data.Selected) {
		fail(w, 400)
		return
	}
	if data.Selected != "" {
		mutationQuery := url.Values{}
		for key, values := range contextQuery {
			for _, value := range values {
				mutationQuery.Add(key, value)
			}
		}
		mutationQuery.Set("selected", data.Selected)
		mutationQuery.Set("view", "detail")
		data.MutationHref = template.URL("/admin/accounts/" + url.PathEscape(data.Selected) + "?" + mutationQuery.Encode())
	}
	// `plans_after` is retained only as an opaque legacy context query so old
	// /admin URLs remain harmless. Assignment choices always come from the
	// first available plan page; plan browsing now has its own view.
	views, err := s.readViews.AdminHome(r.Context(), accountAfter, "", statusFilter, emailFilter, limit)
	if err != nil {
		fail(w, 503)
		return
	}
	data.Pending, data.Active, data.Suspended, data.Denied = views.Counts.Pending, views.Counts.Active, views.Counts.Suspended, views.Counts.Denied
	data.FilteredCount = views.FilteredCount
	for _, x := range views.Accounts {
		if len(data.Accounts) == limit {
			data.NextAccount = data.Accounts[len(data.Accounts)-1].ID
			break
		}
		account := adminAccountRow{ID: x.ID, Email: x.Email, Status: x.Status, PlanID: x.PlanID, PlanName: x.PlanName, WeeklyOverride: x.WeeklyOverride, SessionOverride: x.SessionOverride, Revision: x.Revision, RecoveryHeld: x.RecoveryHeld, PlanArchived: x.PlanArchived, EffectiveBytes: x.EffectiveBytes, EffectiveSessions: x.EffectiveSessions}
		selection := contextQuery
		selection.Set("selected", x.ID)
		selection.Set("view", "detail")
		account.ReviewHref = template.URL("/admin?" + selection.Encode())
		data.Accounts = append(data.Accounts, account)
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
		data.AccessActions = adminAccessActionsFor(selected)
	}
	for _, x := range views.Plans {
		plan := adminPlanRow{ID: x.ID, Name: x.Name, Bytes: x.Bytes, Sessions: x.Sessions, Revision: x.Revision, Archived: x.Archived}
		if !plan.Archived {
			data.AssignablePlans = append(data.AssignablePlans, plan)
		}
	}
	if len(data.AssignablePlans) > 0 {
		data.DefaultPlanID = data.AssignablePlans[0].ID
		if data.SelectedAccount != nil {
			for _, plan := range data.AssignablePlans {
				if plan.ID == data.SelectedAccount.PlanID {
					data.DefaultPlanID = plan.ID
					break
				}
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Values are bounded in the schema, and both lists are capped at 100 rows.
	portal.Render(w, http.StatusOK, "admin", data)
}

func (s *Server) adminPlanLibrary(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.administrator(w, r, false)
	if !ok {
		return
	}
	after := r.URL.Query().Get("after")
	createdID := r.URL.Query().Get("created")
	limit, valid := pageLimit(r.URL.Query().Get("limit"))
	if !valid || after != "" && !idPattern.MatchString(after) || createdID != "" && !idPattern.MatchString(createdID) {
		portal.PlanRequestError(w, http.StatusBadRequest)
		return
	}
	operationID, err := uuid()
	if err != nil {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	queryLimit := limit + 1
	if createdID != "" {
		queryLimit++
	}
	plans, err := s.readViews.AdminPlans(r.Context(), after, queryLimit)
	if err != nil {
		portal.Unavailable(w, "/admin/plans", "Check quota plans")
		return
	}
	data := adminPlanLibraryData{Email: actor.email, RelayHost: relayHost(s.Origin), CSRF: actor.csrf, After: after, CreatedID: createdID, Limit: limit, OperationID: operationID, OperationProof: s.planOperationProof(r, actor, operationID), PolicyRevision: actor.revision}
	if createdID != "" {
		created, readErr := s.readViews.AdminPlan(r.Context(), createdID)
		if errors.Is(readErr, persistence.ErrNotFound) {
			portal.PlanRequestError(w, http.StatusNotFound)
			return
		}
		if readErr != nil {
			portal.Unavailable(w, "/admin/plans", "Check quota plans")
			return
		}
		data.Created = &adminPlanRow{ID: created.ID, Name: created.Name, Bytes: created.Bytes, Sessions: created.Sessions, Revision: created.Revision, Archived: created.Archived}
	}
	for _, plan := range plans {
		if plan.ID == createdID {
			continue
		}
		if len(data.Plans) == limit {
			data.Next = data.Plans[len(data.Plans)-1].ID
			break
		}
		data.Plans = append(data.Plans, adminPlanRow{ID: plan.ID, Name: plan.Name, Bytes: plan.Bytes, Sessions: plan.Sessions, Revision: plan.Revision, Archived: plan.Archived})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	portal.Render(w, http.StatusOK, "plan-library", data)
}

func planLibraryHref(after, created string) string {
	query := url.Values{}
	if after != "" {
		query.Set("after", after)
	}
	if created != "" {
		query.Set("created", created)
	}
	if len(query) == 0 {
		return "/admin/plans"
	}
	return "/admin/plans?" + query.Encode()
}

func revision(raw string) (int64, bool) {
	n, e := strconv.ParseInt(raw, 10, 64)
	return n, e == nil && n > 0
}

// adminReturnHref preserves only the bounded list scope needed to return to
// the reviewed account. Caller-supplied return destinations are never used.
func adminReturnHref(r *http.Request, target string) string {
	scope, _ := adminListScopeFromQuery(r, false)
	query := scope.query
	query.Set("selected", target)
	query.Set("view", "detail")
	return "/admin?" + query.Encode()
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
		markAction(r, "http.request_refused")
	}
	if !idPattern.MatchString(target) || !valid || !known {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, http.StatusBadRequest)
		return
	}
	plan := r.PostForm.Get("plan_id")
	if spec.planRequired && !idPattern.MatchString(plan) {
		markDiagnostic(r, "refused", "invalid_request")
		fail(w, http.StatusBadRequest)
		return
	}
	outcome := s.changeAdminAccount(r, actor, target, expected, action, spec, plan)
	s.renderAdminAccountOutcome(w, r, target, outcome)
}

func (s *Server) adminPlan(w http.ResponseWriter, r *http.Request) {
	unavailable := func() { portal.Unavailable(w, "/admin/plans", "Check quota plans") }
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	bytes, e1 := parseQuotaBytes(r.PostForm.Get("weekly_bytes"), r.PostForm.Get("weekly_bytes_unit"))
	sessions, e2 := strconv.ParseInt(r.PostForm.Get("sessions"), 10, 32)
	if name == "" || len(name) > 120 || e1 != nil || e2 != nil || bytes < 0 || sessions < 0 || !reasonOK(r.PostForm.Get("reason")) {
		markDiagnostic(r, "refused", "invalid_request")
		portal.PlanRequestError(w, http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("operation_id")
	proof := r.PostForm.Get("operation_proof")
	if !idPattern.MatchString(id) || subtle.ConstantTimeCompare([]byte(proof), []byte(s.planOperationProof(r, actor, id))) != 1 {
		markDiagnostic(r, "refused", "invalid_request")
		portal.PlanRequestError(w, http.StatusForbidden)
		return
	}
	ctx := r.Context()
	op, e := s.startAccountAccess(ctx)
	if e != nil {
		unavailable()
		return
	}
	defer op.Close()
	tx, e := beginAccountAccessTransaction(op)
	if e != nil {
		unavailable()
		return
	}
	defer tx.Rollback(op.Context())
	if status := s.lockActor(w, tx, r, &actor); status != 0 {
		if status == http.StatusServiceUnavailable {
			unavailable()
		} else {
			fail(w, status)
		}
		return
	}
	digest := planRequestDigest(actor.id, name, r.PostForm.Get("reason"), bytes, sessions)
	auditID, e := uuid()
	if e != nil {
		unavailable()
		return
	}
	created, e := tx.CreateQuotaPlan(op.Context(), persistence.PlanCreate{ID: id, Name: name, WeeklyBytes: bytes, Sessions: int(sessions), Digest: digest[:], AuditID: auditID, ActorEmail: actor.email, Reason: r.PostForm.Get("reason")})
	if e != nil {
		unavailable()
		return
	}
	if !created.Created {
		if !created.Matching {
			markDiagnostic(r, "refused", "revision_conflict")
			portal.Conflict(w, "/admin/plans", "Reload quota plans")
			return
		}
		if !created.Audited {
			unavailable()
			return
		}
		http.Redirect(w, r, planLibraryHref("", id), http.StatusSeeOther)
		return
	}
	if s.commitPlanAccess != nil {
		e = s.commitPlanAccess(op.Context(), tx)
	} else {
		e = tx.Commit(op.Context())
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
	http.Redirect(w, r, planLibraryHref("", id), http.StatusSeeOther)
}

func (s *Server) adminArchivePlan(w http.ResponseWriter, r *http.Request) {
	unavailable := func() { portal.Unavailable(w, "/admin/plans", "Check quota plans") }
	markAction(r, "admin.plan_archive")
	actor, ok := s.administrator(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	after, created := r.PostForm.Get("after"), r.PostForm.Get("created")
	expected, valid := revision(r.PostForm.Get("revision"))
	if !idPattern.MatchString(id) || !valid || after != "" && !idPattern.MatchString(after) || created != "" && !idPattern.MatchString(created) || r.PostForm.Get("confirm") != "yes" || !reasonOK(r.PostForm.Get("reason")) {
		markDiagnostic(r, "refused", "invalid_request")
		portal.PlanRequestError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	op, e := s.startAccountAccess(ctx)
	if e != nil {
		unavailable()
		return
	}
	defer op.Close()
	tx, e := beginAccountAccessTransaction(op)
	if e != nil {
		unavailable()
		return
	}
	defer tx.Rollback(op.Context())
	if status := s.lockActor(w, tx, r, &actor); status != 0 {
		if status == http.StatusServiceUnavailable {
			unavailable()
		} else {
			fail(w, status)
		}
		return
	}
	auditID, e := uuid()
	if e != nil {
		unavailable()
		return
	}
	e = tx.ArchiveQuotaPlan(op.Context(), persistence.PlanArchive{ID: id, ExpectedRevision: expected, AuditID: auditID, ActorEmail: actor.email, Reason: r.PostForm.Get("reason")})
	if errors.Is(e, persistence.ErrConflict) {
		markDiagnostic(r, "refused", "revision_conflict")
		portal.Conflict(w, planLibraryHref(after, created), "Reload quota plans")
		return
	}
	if errors.Is(e, persistence.ErrNotFound) {
		portal.PlanRequestError(w, http.StatusNotFound)
		return
	}
	if e != nil {
		fail(w, 503)
		return
	}
	if tx.Commit(op.Context()) != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		unavailable()
		return
	}
	http.Redirect(w, r, planLibraryHref(after, created), http.StatusSeeOther)
}
