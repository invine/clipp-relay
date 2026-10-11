// Package diagnostic emits bounded, catalog-validated application events.
// It carries no authority and retains no identity-linked history.
package diagnostic

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

//go:embed catalog.json
var catalogJSON []byte

type definition struct {
	Event              string      `json:"event"`
	Pairs              [][2]string `json:"pairs"`
	Transports         []string    `json:"transports"`
	Clients            []string    `json:"clients"`
	Log                string      `json:"log"`
	DurationMeasurable bool        `json:"duration_measurable"`
	Signals            []string    `json:"signals"`
	States             []string    `json:"states"`
}
type catalogFile struct {
	Events        []definition `json:"events"`
	ControlEvents []definition `json:"control_events"`
}

var catalog map[string]definition

func init() {
	var file catalogFile
	if err := json.Unmarshal(catalogJSON, &file); err != nil {
		panic(err)
	}
	catalog = make(map[string]definition, len(file.Events)+len(file.ControlEvents))
	for _, d := range append(file.Events, file.ControlEvents...) {
		catalog[d.Event] = d
	}
}

// Event is a finite diagnostic observation. Invalid field combinations are
// discarded, including caller-supplied raw messages and malformed IDs.
type Event struct {
	Event, Outcome, Reason      string
	RequestID, SessionID        string
	Transport, ClientType, Gate string
	Signal, State               string
	SchemaRevision              int
	Duration                    time.Duration
}

func newID() (string, bool) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", false
	}
	return hex.EncodeToString(value[:]), true
}

type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time, rate, burst float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
		b.last = now
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rate
		if b.tokens > burst {
			b.tokens = burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type tuple struct{ event, outcome, reason, transport, client string }

// Logger owns exactly one stdout writer and a fixed 256-row queue. Emit never
// waits for the writer. Close spends at most 100 ms on best-effort flushing.
type Logger struct {
	mu                  sync.Mutex
	queue               chan []byte
	done                chan struct{}
	closed              bool
	activity, reserved  bucket
	perTuple            map[tuple]*bucket
	rateLost, queueLost uint64
	diagnosticFailures  uint64
	lastSummary         time.Time
}

// NewID creates a request or connection-local correlation value. Callers omit
// an empty result without changing the business operation.
func (l *Logger) NewID() string {
	id, ok := newID()
	if ok {
		return id
	}
	l.mu.Lock()
	l.diagnosticFailures++
	l.mu.Unlock()
	return ""
}

func New(w io.Writer) *Logger {
	l := &Logger{queue: make(chan []byte, 256), done: make(chan struct{}), perTuple: make(map[tuple]*bucket)}
	go func() {
		defer close(l.done)
		for row := range l.queue {
			n, err := w.Write(row)
			if err != nil || n != len(row) {
				l.mu.Lock()
				l.queueLost++
				l.mu.Unlock()
			}
		}
	}()
	return l
}

func (l *Logger) Close() {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.queue)
	}
	l.mu.Unlock()
	select {
	case <-l.done:
	case <-time.After(100 * time.Millisecond):
	}
}

func member(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func validID(v string) bool {
	if v == "" {
		return true
	}
	if len(v) != 32 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}
func valid(e Event) (definition, bool) {
	d, ok := catalog[e.Event]
	if !ok {
		return d, false
	}
	if e.Event == "log.suppressed" {
		return d, false
	}
	if (strings.HasPrefix(e.Event, "service.") || strings.HasPrefix(e.Event, "observation.")) && (e.RequestID != "" || e.SessionID != "" || e.Gate != "") {
		return d, false
	}
	pair := false
	for _, p := range d.Pairs {
		if p[0] == e.Outcome && p[1] == e.Reason {
			pair = true
			break
		}
	}
	if !pair || !validID(e.RequestID) || !validID(e.SessionID) || e.SchemaRevision < 0 || e.Duration < 0 {
		return d, false
	}
	if e.Transport != "" && !member(d.Transports, e.Transport) {
		return d, false
	}
	if e.Transport == "none" || e.ClientType == "none" {
		return d, false
	}
	if e.ClientType != "" && !member(d.Clients, e.ClientType) {
		return d, false
	}
	if e.Duration != 0 && !d.DurationMeasurable {
		return d, false
	}
	if e.SchemaRevision != 0 && e.Event != "service.startup" {
		return d, false
	}
	if e.Signal != "" && !member(d.Signals, e.Signal) {
		return d, false
	}
	if e.State != "" && !member(d.States, e.State) {
		return d, false
	}
	if (e.Event == "observation.state" || e.Event == "observation.threshold") != (e.Signal != "") {
		return d, false
	}
	if e.Event == "observation.state" && e.State == "" || e.Event != "observation.state" && e.State != "" {
		return d, false
	}
	if e.Gate != "" && !member([]string{"public_http", "private_http", "db_work", "provider_work", "pending_connection", "handshake", "session", "reservation", "circuit", "stream", "deletion"}, e.Gate) {
		return d, false
	}
	return d, true
}

func severity(e Event) string {
	if e.Event == "service.startup" && e.Outcome == "failed" || e.Event == "service.serving" && e.Outcome == "failed" || e.Event == "service.stopped" && e.Outcome == "failed" || e.Reason == "internal_failure" {
		return "ERROR"
	}
	if e.Outcome == "failed" || e.Outcome == "uncertain" || e.Reason == "overloaded" || e.Reason == "resource_limit" || e.Reason == "database_unavailable" || e.Reason == "journal_unavailable" || e.Event == "observation.state" && (e.State == "failed" || e.State == "stale") {
		return "WARN"
	}
	return "INFO"
}

func row(e Event, now time.Time) []byte {
	// Every string here is either a catalog constant or a validated finite enum.
	fields := map[string]any{"time": now.UTC().Format(time.RFC3339Nano), "level": severity(e), "msg": strings.ReplaceAll(e.Event, ".", " "), "event": e.Event, "outcome": e.Outcome, "reason": e.Reason}
	if e.RequestID != "" {
		fields["request_id"] = e.RequestID
	}
	if e.SessionID != "" {
		fields["session_id"] = e.SessionID
	}
	if e.Transport != "" {
		fields["transport"] = e.Transport
	}
	if e.ClientType != "" {
		fields["client_type"] = e.ClientType
	}
	if e.Gate != "" {
		fields["gate"] = e.Gate
	}
	if e.Signal != "" {
		fields["signal"] = e.Signal
	}
	if e.State != "" {
		fields["state"] = e.State
	}
	if e.SchemaRevision != 0 {
		fields["schema_revision"] = e.SchemaRevision
	}
	if e.Duration != 0 {
		fields["duration_ms"] = e.Duration.Milliseconds()
	}
	data, _ := json.Marshal(fields)
	return append(data, '\n')
}

func (l *Logger) Emit(e Event) {
	d, ok := valid(e)
	if !ok {
		l.mu.Lock()
		l.diagnosticFailures++
		l.mu.Unlock()
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.summary(now)
	reserved := d.Log == "control" || e.Event == "observation.state" || e.Event == "observation.threshold" || e.Event == "log.suppressed" || severity(e) == "ERROR"
	if reserved {
		if !l.reserved.take(now, 4, 8) {
			l.rateLost++
			return
		}
	} else {
		key := tuple{e.Event, e.Outcome, e.Reason, e.Transport, e.ClientType}
		b := l.perTuple[key]
		if b == nil {
			b = &bucket{}
			l.perTuple[key] = b
		}
		if !l.activity.take(now, 20, 40) || !b.take(now, 1, 5) {
			l.rateLost++
			return
		}
	}
	data := row(e, now)
	if len(data) > 2048 {
		l.rateLost++
		return
	}
	select {
	case l.queue <- data:
	default:
		l.queueLost++
	}
}

func (l *Logger) summary(now time.Time) {
	if l.rateLost == 0 && l.queueLost == 0 {
		return
	}
	if !l.lastSummary.IsZero() && now.Sub(l.lastSummary) < 10*time.Second {
		return
	}
	// A blocked writer must not spend the reserved lifecycle budget on a
	// summary that cannot enter the queue.
	if len(l.queue) == cap(l.queue) {
		return
	}
	if !l.reserved.take(now, 4, 8) {
		return
	}
	data, _ := json.Marshal(map[string]any{"time": now.UTC().Format(time.RFC3339Nano), "level": "WARN", "msg": "diagnostic records suppressed", "event": "log.suppressed", "outcome": "observed", "reason": "diagnostic_loss", "rate_limited_count": l.rateLost, "queue_full_count": l.queueLost, "window_seconds": 10})
	select {
	case l.queue <- append(data, '\n'):
		l.rateLost, l.queueLost = 0, 0
		l.lastSummary = now
	default:
	}
}
