package auth

import (
	"context"
	"net/http"
	"time"

	"clipp-relay/internal/diagnostic"
)

type diagnosticContextKey struct{}
type requestDiagnostic struct {
	event, outcome, reason, client, id string
}

// SetDiagnostics attaches the serving process's bounded logger before Handler is used.
// It creates no identity-linked or cross-request diagnostic state.
func (s *Server) SetDiagnostics(logger *diagnostic.Logger) { s.diagnostics = logger }
func (s *Server) SetTransitions(transitions *diagnostic.Transitions) {
	s.transitions = transitions
	if s.deletionJournal != nil {
		if observed, ok := s.deletionJournal.(observedJournal); ok {
			s.deletionJournal = observed.DeletionJournal
		}
		s.deletionJournal = observedJournal{DeletionJournal: s.deletionJournal, transitions: transitions}
	}
}

func diagnosticFor(r *http.Request) *requestDiagnostic {
	trace, _ := r.Context().Value(diagnosticContextKey{}).(*requestDiagnostic)
	return trace
}
func markDiagnostic(r *http.Request, outcome, reason string) {
	if trace := diagnosticFor(r); trace != nil {
		trace.outcome, trace.reason = outcome, reason
	}
}
func markClient(r *http.Request, client string) {
	if trace := diagnosticFor(r); trace != nil {
		switch client {
		case "electron", "android", "extension":
			trace.client = client
		default:
			trace.client = "unknown"
		}
	}
}
func routeEvent(r *http.Request) string {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/auth/login" || r.URL.Path == "/auth/callback"):
		return "portal.login"
	case r.Method == http.MethodGet && r.URL.Path == "/oauth/authorize":
		return "oauth.authorize"
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/authorize":
		return "oauth.consent"
	case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
		return "oauth.code_exchange"
	}
	return ""
}

type diagnosticResponse struct {
	http.ResponseWriter
	status int
}

func (w *diagnosticResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *diagnosticResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}
func (w *diagnosticResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) observedRequest(w http.ResponseWriter, r *http.Request, next http.Handler) {
	logger := s.diagnostics
	if logger == nil {
		next.ServeHTTP(w, r)
		return
	}
	// Every HTTP request receives a fresh, process-local diagnostic ID; only
	// meaningful activity routes emit a row. No caller value is consulted.
	event := routeEvent(r)
	trace := &requestDiagnostic{event: event, client: "unknown", id: logger.NewID()}
	if event == "portal.login" {
		trace.client = ""
	}
	r = r.WithContext(context.WithValue(r.Context(), diagnosticContextKey{}, trace))
	started := time.Now()
	out := &diagnosticResponse{ResponseWriter: w}
	next.ServeHTTP(out, r)
	if event == "" {
		var reason string
		switch out.status {
		case http.StatusNotFound:
			reason = "invalid_route"
		case http.StatusMethodNotAllowed:
			reason = "invalid_method"
		}
		if reason != "" {
			logger.Emit(diagnostic.Event{Event: "http.request_refused", Outcome: "refused", Reason: reason, RequestID: trace.id})
		}
		return
	}
	if trace.outcome == "" {
		switch {
		case out.status >= 200 && out.status < 400:
			trace.outcome, trace.reason = "success", "none"
			if r.URL.Path == "/auth/login" {
				trace.outcome = "started"
			}
		case out.status == http.StatusTooManyRequests:
			trace.outcome, trace.reason = "refused", "rate_limited"
		case out.status == http.StatusServiceUnavailable:
			trace.outcome, trace.reason = "failed", "database_unavailable"
		case out.status == http.StatusUnauthorized || out.status == http.StatusForbidden:
			trace.outcome, trace.reason = "refused", "invalid_credentials"
		default:
			trace.outcome, trace.reason = "refused", "invalid_request"
		}
	}
	logger.Emit(diagnostic.Event{Event: trace.event, Outcome: trace.outcome, Reason: trace.reason, RequestID: trace.id, ClientType: trace.client, Duration: time.Since(started)})
}
