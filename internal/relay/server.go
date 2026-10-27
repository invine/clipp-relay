package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/util"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	libp2pwebrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
	"github.com/libp2p/go-libp2p/p2p/transport/websocket"
	"github.com/libp2p/go-libp2p/x/rate"
	ma "github.com/multiformats/go-multiaddr"
	multistream "github.com/multiformats/go-multistream"
)

const AuthProtocol protocol.ID = "/clipp/relay-auth/1.0.0"
const hopProtocol protocol.ID = "/libp2p/circuit/relay/0.2.0/hop"

type Authority interface {
	AuthenticateRelay(context.Context, string) (auth.RelayCredential, error)
}
type Accounting interface {
	Eligibility(context.Context, string) (quota.AccountingEligibility, error)
	Observe(string, int64) error
	AccountingFailed(string)
	AccountingReady(string) bool
	AccountingCurrent(string, uint64) bool
	AccountingSessions(string, int)
	ClosureCurrent(quota.ClosureOutcome) bool
	FlushAllAccounting(context.Context) error
}

// DiagnosticSink is the shared, catalog-validated application event boundary.
// It is optional so protocol fixtures can run without capturing stdout.
type DiagnosticSink interface {
	Emit(diagnostic.Event)
	NewID() string
}

type Options struct {
	ListenAddress          string // complete TCP multiaddr; port zero is accepted for isolated tests
	WebSocketListenAddress string // internal plaintext WS behind exact-hostname public TLS
	WebRTCListenAddress    string // WebRTC Direct UDP; the listener supplies its process certhash
	WebSocketHostname      string // exact public TLS name; validated with every WSS snapshot
	MaxSessions            int
	Diagnostics            DiagnosticSink
}

type connectionDiagnostic struct {
	id, transport string
	ended         string
}

type session struct {
	identity  uint64
	endReason string
	conn      network.Conn
	account   string
	grantID   string
	deadline  time.Time
	timer     sessionTimer
}

type sessionTimer interface{ Stop() bool }

// sessionManager owns the physical connection lifecycle and the indexes that
// represent authenticated Relay Sessions. The protocol server supplies the
// authority, accounting and diagnostic collaborators used by lifecycle work.
type authAttempt struct {
	identity byte
	cancel   context.CancelFunc
}

type connectionLifecycle uint8

const (
	connectionUnauthenticated connectionLifecycle = iota + 1
	connectionAuthenticated
	connectionClosing
	connectionClosed
)

type sessionManager struct {
	server                *Server
	authority             Authority
	accounting            Accounting
	diagnostics           DiagnosticSink
	maxSessions           int
	scheduleExpiry        func(time.Duration, func()) sessionTimer
	globalRate            tokenBucket
	rvGlobalRate          tokenBucket
	nextSessionIdentity   uint64
	relayID               peer.ID
	verifyPeerRecord      func([]byte, peer.ID, peer.ID) bool
	physicalClose         func(network.Conn)
	closePending          map[network.Conn]struct{}
	closeInFlight         map[network.Conn]struct{}
	closeWake             chan struct{}
	closeStop             chan struct{}
	closeDone             chan struct{}
	deferredGrantCloses   map[string]map[network.Conn]struct{}
	lifecycle             map[network.Conn]connectionLifecycle
	deferredAccountCloses map[string]map[network.Conn]struct{}

	mu             sync.Mutex
	connectionInfo map[network.Conn]connectionDiagnostic
	byConn         map[network.Conn]*session
	byPeer         map[peer.ID]*session
	byAccount      map[string]map[*session]struct{}
	authAttempts   map[network.Conn]*authAttempt
	preauth        map[network.Conn]*time.Timer
	connRate       map[network.Conn]tokenBucket
	rvConnRate     map[network.Conn]tokenBucket
	reservations   map[peer.ID]reservationOwner
	leases         map[peer.ID]*rendezvousLease
	closing        bool
	hopHandlers    int
}

type Server struct {
	Host        host.Host
	stock       *relay.Relay
	manager     network.ResourceManager
	tracer      *circuitTracer
	authority   Authority
	diagnostics DiagnosticSink
	*sessionManager
	accounting        Accounting
	maxSessions       int
	tcpEnabled        bool
	wsEnabled         bool
	webrtcEnabled     bool
	webSocketHostname string
	rvCounts          [2]atomic.Uint64
	closeOnce         sync.Once
}

type tokenBucket struct {
	tokens float64
	at     time.Time
}

func (b *tokenBucket) allow(now time.Time, rate, burst float64) bool {
	if b.at.IsZero() {
		b.tokens = burst
		b.at = now
	}
	b.tokens += now.Sub(b.at).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type gatedHost struct {
	host.Host
	check              func(network.Stream) (bool, string)
	doneHop            func()
	observeReservation func(network.Stream) network.Stream
	observeDeniedHop   func(network.Stream, string)
	stopConn           func(peer.ID) network.Conn
}

func (h gatedHost) SetStreamHandler(id protocol.ID, handler network.StreamHandler) {
	if id == hopProtocol {
		h.Host.SetStreamHandler(id, func(s network.Stream) {
			if allowed, reason := h.check(s); !allowed {
				if h.observeDeniedHop != nil {
					h.observeDeniedHop(s, reason)
				}
				hopStatus(s, pbv2.Status_PERMISSION_DENIED)
				return
			}
			if h.observeReservation != nil {
				s = h.observeReservation(s)
			}
			handler(s)
			h.doneHop()
		})
		return
	}
	h.Host.SetStreamHandler(id, handler)
}
func (h gatedHost) RemoveStreamHandler(id protocol.ID) { h.Host.RemoveStreamHandler(id) }

// Stock relay requests only STOP. Pin its stream to the authoritative physical
// connection instead of allowing the swarm to select another connection.
func (h gatedHost) NewStream(ctx context.Context, id peer.ID, protocols ...protocol.ID) (network.Stream, error) {
	if len(protocols) != 1 || protocols[0] != "/libp2p/circuit/relay/0.2.0/stop" {
		return nil, errors.New("relay-initiated protocol denied")
	}
	conn := h.stopConn(id)
	if conn == nil {
		return nil, network.ErrNoConn
	}
	st, err := conn.NewStream(ctx)
	if err != nil {
		return nil, err
	}
	result := make(chan error, 1)
	go func() { result <- multistream.SelectProtoOrFail(protocols[0], st) }()
	select {
	case err = <-result:
	case <-ctx.Done():
		_ = st.Reset()
		<-result
		return nil, ctx.Err()
	}
	if err != nil {
		_ = st.Reset()
		return nil, err
	}
	if err = st.SetProtocol(protocols[0]); err != nil {
		_ = st.Reset()
		return nil, err
	}
	return st, nil
}

func hopStatus(s network.Stream, status pbv2.Status) {
	_ = s.SetWriteDeadline(time.Now().Add(time.Second))
	msg := &pbv2.HopMessage{Type: pbv2.HopMessage_STATUS.Enum(), Status: status.Enum()}
	if util.NewDelimitedWriter(s).WriteMsg(msg) != nil {
		_ = s.Reset()
	} else {
		_ = s.Close()
	}
}

// NewProtected installs the sole production accounting and policy composition.
func NewProtected(a *auth.Server, q *quota.Quota, opts Options) (*Server, error) {
	if a == nil || q == nil {
		return nil, errors.New("relay authority and quota required")
	}
	return newServer(a, q, opts, func(s *Server) error {
		if err := a.ConfigureRelayProtection(q, s); err != nil {
			return err
		}
		s.accounting = q
		if err := q.StartAccounting(s.accountingClosure); err != nil {
			return err
		}
		return nil
	})
}
func (s *Server) accountingClosure(out quota.ClosureOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.accounting.ClosureCurrent(out) {
		return
	}
	for v := range s.byAccount[out.AccountID] {
		s.endSessionLocked(v, out.Reason)
		s.sessionManager.queueCloseLocked(v.conn)
	}
}

// New starts an inbound-only, Noise-authenticated relay with a new in-RAM
// identity. Controlled authority adapters must provide all admission operations.
func New(a Authority, accounting Accounting, opts Options) (*Server, error) {
	return newServer(a, accounting, opts, nil)
}

func newServer(a Authority, accounting Accounting, opts Options, installProtection func(*Server) error) (*Server, error) {
	if opts.ListenAddress == "" && opts.WebSocketListenAddress == "" && opts.WebRTCListenAddress == "" {
		return nil, errors.New("relay listener required")
	}
	if opts.MaxSessions == 0 {
		opts.MaxSessions = 6000
	}
	if opts.MaxSessions < 0 || opts.MaxSessions > 6000 {
		return nil, errors.New("invalid session ceiling")
	}
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	manager, err := rcmgr.NewResourceManager(TCPProfile(),
		rcmgr.WithMetricsDisabled(),
		rcmgr.WithAllowlistedMultiaddrs([]ma.Multiaddr{}),
		rcmgr.WithLimitPerSubnet([]rcmgr.ConnLimitPerSubnet{}, []rcmgr.ConnLimitPerSubnet{}),
		rcmgr.WithNetworkPrefixLimit([]rcmgr.NetworkPrefixLimit{}, []rcmgr.NetworkPrefixLimit{}),
		rcmgr.WithConnRateLimiters(&rate.Limiter{SubnetRateLimiter: rate.SubnetLimiter{IPv4SubnetLimits: []rate.SubnetLimit{}, IPv6SubnetLimits: []rate.SubnetLimit{}}}),
	)
	if err != nil {
		return nil, err
	}
	s := &Server{manager: manager, tracer: &circuitTracer{diagnostics: opts.Diagnostics}, authority: a, accounting: accounting, diagnostics: opts.Diagnostics, maxSessions: opts.MaxSessions, tcpEnabled: opts.ListenAddress != "", wsEnabled: opts.WebSocketListenAddress != "", webrtcEnabled: opts.WebRTCListenAddress != "", webSocketHostname: opts.WebSocketHostname}
	s.sessionManager = &sessionManager{server: s, authority: a, accounting: accounting, diagnostics: opts.Diagnostics, maxSessions: opts.MaxSessions, scheduleExpiry: func(d time.Duration, callback func()) sessionTimer { return time.AfterFunc(d, callback) }, physicalClose: func(c network.Conn) { _ = c.Close() }, closePending: map[network.Conn]struct{}{}, closeInFlight: map[network.Conn]struct{}{}, closeWake: make(chan struct{}, 1), closeStop: make(chan struct{}), closeDone: make(chan struct{}), deferredGrantCloses: map[string]map[network.Conn]struct{}{}, deferredAccountCloses: map[string]map[network.Conn]struct{}{}, lifecycle: map[network.Conn]connectionLifecycle{}, connectionInfo: map[network.Conn]connectionDiagnostic{}, byConn: map[network.Conn]*session{}, byPeer: map[peer.ID]*session{}, byAccount: map[string]map[*session]struct{}{}, authAttempts: map[network.Conn]*authAttempt{}, preauth: map[network.Conn]*time.Timer{}, connRate: map[network.Conn]tokenBucket{}, rvConnRate: map[network.Conn]tokenBucket{}, reservations: map[peer.ID]reservationOwner{}, leases: map[peer.ID]*rendezvousLease{}}
	reporter := &endpointReporter{sessions: s.sessionManager}
	listen := make([]string, 0, 3)
	transportOptions := []libp2p.Option{libp2p.Identity(key), libp2p.NoTransports,
		libp2p.Security(noise.ID, noise.New), libp2p.DisableRelay(), libp2p.DisableIdentifyAddressDiscovery(),
		libp2p.ConnectionManager(&connmgr.NullConnMgr{}), libp2p.ResourceManager(manager), libp2p.BandwidthReporter(reporter)}
	if opts.ListenAddress != "" {
		listen = append(listen, opts.ListenAddress)
		transportOptions = append(transportOptions, libp2p.Transport(tcp.NewTCPTransport))
	}
	if opts.WebSocketListenAddress != "" {
		listen = append(listen, opts.WebSocketListenAddress)
		transportOptions = append(transportOptions, libp2p.Transport(websocket.New, websocket.WithHandshakeTimeout(10*time.Second)))
	}
	if opts.WebRTCListenAddress != "" {
		listen = append(listen, opts.WebRTCListenAddress)
		transportOptions = append(transportOptions, libp2p.Transport(libp2pwebrtc.New))
	}
	transportOptions = append(transportOptions, libp2p.ListenAddrStrings(listen...))
	h, err := libp2p.New(transportOptions...)
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	s.Host = h
	s.sessionManager.relayID = h.ID()
	if installProtection != nil {
		if err := installProtection(s); err != nil {
			_ = h.Close()
			_ = manager.Close()
			return nil, err
		}
	}
	h.Network().Notify(&network.NotifyBundle{ConnectedF: s.sessionManager.connected, DisconnectedF: s.sessionManager.disconnected})
	h.SetStreamHandler(AuthProtocol, s.handleAuth)
	h.SetStreamHandler(RendezvousV1Protocol, s.handleRendezvous)
	h.SetStreamHandler(RendezvousV2Protocol, s.handleRendezvous)
	resources := relay.Resources{Limit: &relay.RelayLimit{Duration: 120 * time.Second, Data: 2 << 20}, ReservationTTL: 30 * time.Minute, MaxReservations: 6000, MaxCircuits: 16, BufferSize: 2048, MaxReservationsPerPeer: 1, MaxReservationsPerIP: 6000, MaxReservationsPerASN: 6000}
	s.stock, err = relay.New(gatedHost{Host: h, check: s.sessionManager.permitted, doneHop: s.sessionManager.doneHop, stopConn: s.sessionManager.authoritativeConn, observeDeniedHop: s.observeDeniedHop, observeReservation: func(st network.Stream) network.Stream {
		return &reservationStream{Stream: st, manager: s.sessionManager}
	}}, relay.WithResources(resources), relay.WithMetricsTracer(s.tracer), relay.WithReservationAddressFilter(func(ma.Multiaddr) bool { return true }))
	if err != nil {
		_ = h.Close()
		_ = manager.Close()
		return nil, err
	}
	go s.sessionManager.closeWorker()
	return s, nil
}

func (s *Server) Close() error {
	var result error
	s.closeOnce.Do(func() {
		pending := s.sessionManager.prepareClose()
		// Host.Close is the force-close backstop for real transports. The worker
		// retains ownership of queued and already-detached connections until it
		// observes completion.
		_ = s.stock.Close()
		_ = s.Host.Close()
		for _, c := range pending {
			_ = c.Close()
		}
		close(s.sessionManager.closeStop)
		<-s.sessionManager.closeDone
		result = s.manager.Close()
	})
	return result
}

func (m *sessionManager) prepareClose() []network.Conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	draining := m.closing
	m.closing = true
	for c, v := range m.byConn {
		if draining {
			v.endReason = "draining"
		}
		m.queueCloseLocked(c)
	}
	for c := range m.preauth {
		m.queueCloseLocked(c)
	}
	pending := make([]network.Conn, 0, len(m.closePending)+len(m.closeInFlight))
	for c := range m.closePending {
		pending = append(pending, c)
	}
	for c := range m.closeInFlight {
		pending = append(pending, c)
	}
	for _, deferred := range m.deferredGrantCloses {
		for c := range deferred {
			m.queueCloseLocked(c)
		}
	}
	clear(m.deferredGrantCloses)
	for _, deferred := range m.deferredAccountCloses {
		for c := range deferred {
			m.queueCloseLocked(c)
		}
	}
	clear(m.deferredAccountCloses)
	return pending
}

func connectionTransport(c network.Conn) string {
	a := c.RemoteMultiaddr()
	local := c.LocalMultiaddr()
	if local != nil && hasProtocol(local, ma.P_WEBRTC_DIRECT) {
		return "webrtc_direct"
	}
	if a == nil {
		return "unknown"
	}
	if hasProtocol(a, ma.P_WEBRTC_DIRECT) {
		return "webrtc_direct"
	}
	if hasProtocol(a, ma.P_WS) || hasProtocol(a, ma.P_WSS) {
		return "wss"
	}
	if hasProtocol(a, ma.P_TCP) {
		return "tcp"
	}
	return "unknown"
}

func (m *sessionManager) emitConnection(c network.Conn, event, outcome, reason, requestID string, duration time.Duration) {
	if m.diagnostics == nil {
		return
	}
	info := m.connectionInfoFor(c)
	e := diagnostic.Event{Event: event, Outcome: outcome, Reason: reason, RequestID: requestID, Duration: duration}
	if info.id != "" {
		e.SessionID = info.id
		e.Transport = info.transport
	}
	m.diagnostics.Emit(e)
}

func (m *sessionManager) connectionInfoFor(c network.Conn) connectionDiagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectionInfo[c]
}

func (m *sessionManager) connected(_ network.Network, c network.Conn) {
	m.mu.Lock()
	reason := ""
	if m.closing {
		reason = "draining"
	} else if len(m.preauth) >= 256 {
		reason = "resource_limit"
	}
	if reason != "" {
		m.mu.Unlock()
		if m.diagnostics != nil {
			m.diagnostics.Emit(diagnostic.Event{Event: "relay.connection", Outcome: "refused", Reason: reason, Transport: connectionTransport(c)})
		}
		_ = c.Close()
		return
	}
	m.lifecycle[c] = connectionUnauthenticated
	if m.diagnostics != nil {
		m.connectionInfo[c] = connectionDiagnostic{id: m.diagnostics.NewID(), transport: connectionTransport(c)}
	}
	timer := time.AfterFunc(10*time.Second, func() {
		m.mu.Lock()
		_, pending := m.preauth[c]
		if pending {
			if m.diagnostics != nil {
				info := m.connectionInfo[c]
				info.ended = "preauth_expired"
				m.connectionInfo[c] = info
			}
			m.queueCloseLocked(c)
		}
		m.mu.Unlock()
	})
	m.preauth[c] = timer
	m.mu.Unlock()
	m.emitConnection(c, "relay.connection", "success", "none", "", 0)
}

func (m *sessionManager) disconnected(_ network.Network, c network.Conn) {
	m.mu.Lock()
	if t := m.preauth[c]; t != nil {
		t.Stop()
		delete(m.preauth, c)
	}
	delete(m.connRate, c)
	delete(m.rvConnRate, c)
	delete(m.authAttempts, c)
	m.lifecycle[c] = connectionClosed
	if old := m.byConn[c]; old != nil {
		reason := old.endReason
		if reason == "" {
			reason = "connection_closed"
		}
		m.endSessionLocked(old, reason)
	}
	info, observed := m.connectionInfo[c]
	delete(m.connectionInfo, c)
	m.mu.Unlock()
	if observed {
		reason := info.ended
		if reason == "" {
			reason = "connection_closed"
		}
		m.diagnostics.Emit(diagnostic.Event{Event: "relay.connection", Outcome: "ended", Reason: reason, SessionID: info.id, Transport: info.transport})
	}
	m.mu.Lock()
	delete(m.lifecycle, c)
	m.mu.Unlock()
}

func (s *Server) removeLocked(v *session) { s.endSessionLocked(v, "connection_closed") }
func (s *Server) endSessionLocked(v *session, reason string) {
	s.sessionManager.endSessionLocked(v, reason)
}

func (m *sessionManager) endSessionLocked(v *session, reason string) {
	if m.byConn[v.conn] != v {
		return
	}
	m.markClosingLocked(v.conn)
	if m.diagnostics != nil {
		info := m.connectionInfo[v.conn]
		m.diagnostics.Emit(diagnostic.Event{Event: "relay.session", Outcome: "ended", Reason: reason, SessionID: info.id, Transport: info.transport})
	}
	m.removeSessionLocked(v, false)
}

func (s *Server) removeSessionLocked(v *session, preserveRendezvous bool) {
	s.sessionManager.removeSessionLocked(v, preserveRendezvous)
}

func (m *sessionManager) removeSessionLocked(v *session, preserveRendezvous bool) {
	if m.byConn[v.conn] != v {
		return
	}
	if !preserveRendezvous {
		m.removeRendezvousLocked(v)
	}
	delete(m.byConn, v.conn)
	if m.byPeer[v.conn.RemotePeer()] == v {
		delete(m.byPeer, v.conn.RemotePeer())
	}
	delete(m.byAccount[v.account], v)
	if m.accounting != nil {
		m.accounting.AccountingSessions(v.account, len(m.byAccount[v.account]))
	}
	if len(m.byAccount[v.account]) == 0 {
		delete(m.byAccount, v.account)
	}
	if v.timer != nil {
		v.timer.Stop()
	}
}

func (m *sessionManager) permitted(st network.Stream) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.byConn[st.Conn()]
	if m.closing {
		return false, "draining"
	}
	if v == nil || !time.Now().Before(v.deadline) {
		return false, "authentication_required"
	}
	if m.accounting != nil && !m.accounting.AccountingReady(v.account) {
		return false, "accounting_unavailable"
	}
	m.hopHandlers++
	return true, ""
}

// On a denied HOP stream, inspect only the bounded first stock request so the
// refusal can be labeled RESERVE or CONNECT. This never grants admission and
// the existing permission-denied wire response remains authoritative.
func (s *Server) observeDeniedHop(st network.Stream, reason string) {
	if s.diagnostics == nil {
		return
	}
	_ = st.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	reader := util.NewDelimitedReader(st, 4096)
	defer reader.Close()
	var msg pbv2.HopMessage
	if reader.ReadMsg(&msg) != nil {
		return
	}
	event := ""
	switch msg.GetType() {
	case pbv2.HopMessage_RESERVE:
		event = "relay.reservation"
	case pbv2.HopMessage_CONNECT:
		event = "relay.circuit"
	default:
		return
	}
	info := s.sessionManager.connectionInfoFor(st.Conn())
	s.diagnostics.Emit(diagnostic.Event{Event: event, Outcome: "refused", Reason: reason, RequestID: s.diagnostics.NewID(), SessionID: info.id, Transport: "unknown"})
}

func (m *sessionManager) doneHop() { m.mu.Lock(); m.hopHandlers--; m.mu.Unlock() }

// Drain stops admission and waits for stock circuits to finish. Existing
// physical connections remain usable until the last circuit is gone.
func (s *Server) StartDrain() {
	s.sessionManager.startDrain()
}

func (m *sessionManager) startDrain() { m.mu.Lock(); m.closing = true; m.mu.Unlock() }

func (s *Server) Drain(ctx context.Context) error {
	s.StartDrain()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		pending := s.sessionManager.activeHopHandlers()
		if pending == 0 && s.tracer.active.Load() == 0 {
			err := s.Close()
			if s.accounting != nil && ctx.Err() == nil {
				if flushErr := s.accounting.FlushAllAccounting(ctx); err == nil {
					err = flushErr
				}
			}
			return err
		}
		select {
		case <-ctx.Done():
			_ = s.Close()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *sessionManager) activeHopHandlers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hopHandlers
}

func (m *sessionManager) authoritativeConn(id peer.ID) network.Conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.byPeer[id]
	if m.closing || v == nil || !time.Now().Before(v.deadline) || v.conn.IsClosed() {
		return nil
	}
	if m.accounting != nil && !m.accounting.AccountingReady(v.account) {
		return nil
	}
	return v.conn
}

func (s *Server) expire(v *session) {
	s.sessionManager.expire(v)
}

func (m *sessionManager) expire(v *session) {
	m.mu.Lock()
	owned := m.byConn[v.conn] == v && !time.Now().Before(v.deadline)
	if owned {
		m.endSessionLocked(v, "session_expired")
		m.queueCloseLocked(v.conn)
	}
	m.mu.Unlock()
}

func (s *Server) authenticate(ctx context.Context, c network.Conn, raw string) (time.Time, time.Duration, string) {
	deadline, renew, code, _ := s.sessionManager.authenticateObserved(ctx, c, raw)
	return deadline, renew, code
}

func (s *Server) authenticateObserved(ctx context.Context, c network.Conn, raw string) (time.Time, time.Duration, string, string) {
	return s.sessionManager.authenticateObserved(ctx, c, raw)
}

func (m *sessionManager) authenticateObserved(ctx context.Context, c network.Conn, raw string) (time.Time, time.Duration, string, string) {
	reason := ""
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	attempt := &authAttempt{cancel: cancel}

	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable", "draining"
	}
	if (m.lifecycle[c] != connectionUnauthenticated && m.lifecycle[c] != connectionAuthenticated) || c.IsClosed() {
		m.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable", "connection_closed"
	}
	previous := m.authAttempts[c]
	m.authAttempts[c] = attempt
	allowed := m.globalRate.allow(time.Now(), 200, 400)
	b := m.connRate[c]
	allowed = b.allow(time.Now(), 1, 2) && allowed
	m.connRate[c] = b
	m.mu.Unlock()
	if previous != nil && previous.cancel != nil {
		previous.cancel()
	}
	if !allowed {
		m.mu.Lock()
		if m.authAttempts[c] == attempt {
			delete(m.authAttempts, c)
		}
		m.mu.Unlock()
		return time.Time{}, 0, "rate_limited", "rate_limited"
	}
	var deadline time.Time
	var renew time.Duration
	var code string
	var oldToClose network.Conn
	var installed *session
	deadline, renew, code, oldToClose, installed = m.authenticateSerial(attemptCtx, c, raw, &reason, attempt)
	m.mu.Lock()
	if m.authAttempts[c] == attempt {
		delete(m.authAttempts, c)
	}
	m.mu.Unlock()
	if code != "" && installed != nil {
		m.mu.Lock()
		owned := m.byConn[c] == installed
		if owned {
			m.endSessionLocked(installed, "connection_closed")
		}
		m.mu.Unlock()
		if owned {
			m.closeConnection(c)
		}
	}
	if oldToClose != nil {
		m.closeConnection(oldToClose)
	}
	return deadline, renew, code, reason
}

func (m *sessionManager) markClosingLocked(conn network.Conn) {
	if conn != nil {
		m.lifecycle[conn] = connectionClosing
	}
}

func (m *sessionManager) queueCloseLocked(conn network.Conn) {
	if conn == nil {
		return
	}
	if _, ok := m.closePending[conn]; ok {
		return
	}
	if _, ok := m.closeInFlight[conn]; ok {
		return
	}
	m.markClosingLocked(conn)
	m.closePending[conn] = struct{}{}
	select {
	case m.closeWake <- struct{}{}:
	default:
	}
}

// closeConnection is the protocol adapter's complete handoff for failed,
// malformed, or otherwise terminal physical connections. Callers never take
// the session lock or enqueue closure work themselves.
func (m *sessionManager) closeConnection(conn network.Conn) {
	m.mu.Lock()
	m.queueCloseLocked(conn)
	m.mu.Unlock()
}

// closeUnauthenticated completes a failed initial authentication after its
// response has been written. An unrelated authenticated renewal is retained.
func (m *sessionManager) closeUnauthenticated(conn network.Conn) {
	m.mu.Lock()
	if m.byConn[conn] == nil {
		m.queueCloseLocked(conn)
	}
	m.mu.Unlock()
}

func (m *sessionManager) closeWorker() {
	defer close(m.closeDone)
	for {
		select {
		case <-m.closeWake:
		case <-m.closeStop:
		}
		for {
			m.mu.Lock()
			var conn network.Conn
			for candidate := range m.closePending {
				conn = candidate
				break
			}
			if conn == nil {
				stopping := false
				select {
				case <-m.closeStop:
					stopping = true
				default:
				}
				m.mu.Unlock()
				if stopping {
					return
				}
				break
			}
			delete(m.closePending, conn)
			m.closeInFlight[conn] = struct{}{}
			closeConn := m.physicalClose
			m.mu.Unlock()

			closeConn(conn)

			m.mu.Lock()
			delete(m.closeInFlight, conn)
			if m.lifecycle[conn] == connectionClosing && conn.IsClosed() {
				m.lifecycle[conn] = connectionClosed
				delete(m.lifecycle, conn)
			}
			m.mu.Unlock()
		}
	}
}

// InvalidateGrant processes a committed grant revocation. Pending authentication
// attempts are conservatively retired because their grant association may not
// have arrived yet; established sessions are removed only when they use grantID.
// The manager lock serializes this transition with applying authentication.
func (s *Server) RetireGrant(grantID string) error {
	if grantID == "" {
		return errors.New("grant ID is required")
	}
	return s.sessionManager.invalidateSessions("grant_revoked", func(session *session) bool { return session.grantID == grantID }, grantID, "", true)
}

// CompleteGrantInvalidation releases deferred physical cleanup after Auth has
// dropped identity protection. No connection handle or close callback crosses
// the module boundary.
func (s *Server) CompleteGrantInvalidation(grantID string) error {
	if grantID == "" {
		return errors.New("grant ID is required")
	}
	m := s.sessionManager
	m.mu.Lock()
	for conn := range m.deferredGrantCloses[grantID] {
		m.queueCloseLocked(conn)
	}
	delete(m.deferredGrantCloses, grantID)
	m.mu.Unlock()
	return nil
}

// InvalidateGrant retains the synchronous combined operation for callers that
// do not hold Auth protection across the mutation.
func (s *Server) InvalidateGrant(grantID string) error {
	if err := s.RetireGrant(grantID); err != nil {
		return err
	}
	return s.CompleteGrantInvalidation(grantID)
}

// InvalidateAccount processes account-wide revocation or restriction without
// enumerating persisted grants.
func (s *Server) InvalidateAccount(accountID string) error {
	if accountID == "" {
		return errors.New("account ID is required")
	}
	if err := s.RetireAccount(accountID, "account_access_changed"); err != nil {
		return err
	}
	return s.CompleteAccountInvalidation(accountID)
}

// RetireAccount removes account sessions from authorization and defers their
// physical close until the owner releases its logical protection.
func (s *Server) RetireAccount(accountID, reason string) error {
	if accountID == "" {
		return errors.New("account ID is required")
	}
	if reason == "" {
		reason = "account_access_changed"
	}
	return s.sessionManager.invalidateSessions(reason, func(session *session) bool { return session.account == accountID }, "", accountID, reason == "account_access_changed")
}

// CompleteAccountInvalidation releases manager-owned closes after Auth has
// released identity and account protection. No connection handle crosses the
// Auth boundary.
func (s *Server) CompleteAccountInvalidation(accountID string) error {
	if accountID == "" {
		return errors.New("account ID is required")
	}
	m := s.sessionManager
	m.mu.Lock()
	for conn := range m.deferredAccountCloses[accountID] {
		m.queueCloseLocked(conn)
	}
	delete(m.deferredAccountCloses, accountID)
	m.mu.Unlock()
	return nil
}

// invalidateSessions retires pending attempts whose authorization scope may
// not yet be known, then removes only matching established sessions. The same
// lock serializes invalidation with successful authentication application.
func (m *sessionManager) invalidateSessions(reason string, matches func(*session) bool, deferredGrant, deferredAccount string, cancelPending bool) error {
	s := m.server
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.authAttempts))
	if cancelPending {
		for conn, attempt := range s.authAttempts {
			delete(s.authAttempts, conn)
			if attempt.cancel != nil {
				cancels = append(cancels, attempt.cancel)
			}
			// A pending unauthenticated connection has no known grant association.
			// Preserve conservative cancellation by making it terminal too, while
			// leaving an established unrelated session usable during renewal.
			if s.byConn[conn] == nil {
				m.markClosingLocked(conn)
			}
		}
	}
	for _, session := range s.byConn {
		if matches(session) {
			s.endSessionLocked(session, reason)
			if deferredGrant != "" {
				if m.deferredGrantCloses[deferredGrant] == nil {
					m.deferredGrantCloses[deferredGrant] = map[network.Conn]struct{}{}
				}
				m.deferredGrantCloses[deferredGrant][session.conn] = struct{}{}
			} else if deferredAccount != "" {
				if m.deferredAccountCloses[deferredAccount] == nil {
					m.deferredAccountCloses[deferredAccount] = map[network.Conn]struct{}{}
				}
				m.deferredAccountCloses[deferredAccount][session.conn] = struct{}{}
			} else {
				m.queueCloseLocked(session.conn)
			}
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (m *sessionManager) authenticateSerial(ctx context.Context, c network.Conn, raw string, reason *string, attempt *authAttempt) (time.Time, time.Duration, string, network.Conn, *session) {
	m.mu.Lock()
	currentAttempt := m.authAttempts[c] == attempt && !m.closing && !c.IsClosed()
	m.mu.Unlock()
	if !currentAttempt || ctx.Err() != nil {
		*reason = "deadline"
		return time.Time{}, 0, "temporarily_unavailable", nil, nil
	}
	var oldToClose network.Conn
	credential, err := m.authority.AuthenticateRelay(ctx, raw)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidAccess) {
			*reason = "invalid_credentials"
			return time.Time{}, 0, "authentication_failed", nil, nil
		}
		*reason = "database_unavailable"
		return time.Time{}, 0, "temporarily_unavailable", nil, nil
	}
	deadline, renew, code := m.authenticateAdmitted(ctx, c, credential, &oldToClose, reason, attempt)
	var installed *session
	if code == "" {
		m.mu.Lock()
		installed = m.byConn[c]
		m.mu.Unlock()
	}
	return deadline, renew, code, oldToClose, installed
}

// Session Manager's current-attempt and accounting-revision checks fence
// committed account and policy changes before a session can be installed.
func (s *Server) authenticateAdmitted(ctx context.Context, c network.Conn, credential auth.RelayCredential, oldToClose *network.Conn, reason *string, attempt *authAttempt) (time.Time, time.Duration, string) {
	return s.sessionManager.authenticateAdmitted(ctx, c, credential, oldToClose, reason, attempt)
}

func (m *sessionManager) authenticateAdmitted(ctx context.Context, c network.Conn, credential auth.RelayCredential, oldToClose *network.Conn, reason *string, attempt *authAttempt) (time.Time, time.Duration, string) {
	if ctx.Err() != nil {
		*reason = "deadline"
		return time.Time{}, 0, "temporarily_unavailable"
	}
	m.mu.Lock()
	current := m.byConn[c]
	replaced := m.byPeer[c.RemotePeer()]
	m.mu.Unlock()
	if current != nil && current.account != credential.AccountID {
		*reason = "account_mismatch"
		return time.Time{}, 0, "account_change_requires_new_connection"
	}
	var eligibility quota.AccountingEligibility
	var quotaErr error
	eligibility, quotaErr = m.accounting.Eligibility(ctx, credential.AccountID)
	if err := quotaErr; err != nil {
		if errors.Is(err, quota.ErrExhausted) {
			*reason = "" // The fixed authentication catalog has no quota-exhausted pair.
			return time.Time{}, 0, "quota_exhausted"
		}
		*reason = "quota_unavailable"
		return time.Time{}, 0, "temporarily_unavailable"
	}
	deadline := time.Now().Add(15 * time.Minute)
	if credential.ExpiresAt.Before(deadline) {
		deadline = credential.ExpiresAt
	}
	if !time.Now().Before(deadline) {
		*reason = "expired_credentials"
		return time.Time{}, 0, "authentication_failed"
	}
	var jitter [8]byte
	if _, err := rand.Read(jitter[:]); err != nil {
		return time.Time{}, 0, "temporarily_unavailable"
	}
	// Uniform 75–85% of admitted lifetime, independent of a stable Peer ID.
	renewFraction := 0.75 + float64(binary.LittleEndian.Uint64(jitter[:])%10001)/100000
	m.mu.Lock()
	if ctx.Err() != nil || m.closing || (m.lifecycle[c] != connectionUnauthenticated && m.lifecycle[c] != connectionAuthenticated) || c.IsClosed() || m.authAttempts[c] != attempt || m.accounting != nil && !m.accounting.AccountingCurrent(credential.AccountID, eligibility.Revision) {
		m.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable"
	}
	if m.byConn[c] != current || m.byPeer[c.RemotePeer()] != replaced {
		m.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable"
	}
	if current == nil {
		count := len(m.byAccount[credential.AccountID])
		if replaced != nil && replaced.account == credential.AccountID {
			count--
		}
		if count >= eligibility.SessionLimit {
			m.mu.Unlock()
			*reason = "session_limit"
			return time.Time{}, 0, "session_limit_exceeded"
		}
		if len(m.byConn) >= m.maxSessions && replaced == nil {
			m.mu.Unlock()
			*reason = "session_limit"
			return time.Time{}, 0, "session_limit_exceeded"
		}
	}
	if current != nil {
		m.removeSessionLocked(current, true)
	}
	if replaced != nil && replaced != current {
		m.endSessionLocked(replaced, "replaced")
		m.queueCloseLocked(replaced.conn)
	}
	m.nextSessionIdentity++
	v := &session{identity: m.nextSessionIdentity, conn: c, account: credential.AccountID, grantID: credential.GrantID, deadline: deadline}
	m.byConn[c] = v
	m.lifecycle[c] = connectionAuthenticated
	m.byPeer[c.RemotePeer()] = v
	if current != nil {
		if lease := m.leases[c.RemotePeer()]; lease != nil && lease.owner == current {
			lease.owner = v
		}
	}
	if m.byAccount[v.account] == nil {
		m.byAccount[v.account] = map[*session]struct{}{}
	}
	m.byAccount[v.account][v] = struct{}{}
	if m.accounting != nil {
		m.accounting.AccountingSessions(v.account, len(m.byAccount[v.account]))
	}
	if t := m.preauth[c]; t != nil {
		t.Stop()
		delete(m.preauth, c)
	}
	v.timer = m.scheduleExpiry(time.Until(deadline), func() { m.expire(v) })
	delete(m.authAttempts, c)
	m.mu.Unlock()
	if replaced != nil && replaced != current {
		*oldToClose = replaced.conn
	}
	lifetime := time.Until(deadline)
	return deadline, time.Duration(float64(lifetime) * renewFraction), ""
}

type byteReader struct{ io.Reader }

var errBadFrame = errors.New("bad relay auth frame")
var errInvalidRequest = errors.New("invalid relay auth request")

func (r byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r.Reader, b[:])
	return b[0], err
}

func readAuth(st io.Reader) (string, error) {
	n, err := binary.ReadUvarint(byteReader{st})
	if err != nil || n == 0 || n > 4096 {
		return "", errBadFrame
	}
	data := make([]byte, int(n))
	if _, err = io.ReadFull(st, data); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return "", errInvalidRequest
	}
	if !dec.More() {
		return "", errInvalidRequest
	}
	key, err := dec.Token()
	if err != nil || key != "accessToken" {
		return "", errInvalidRequest
	}
	var token string
	if dec.Decode(&token) != nil || token == "" || dec.More() {
		return "", errInvalidRequest
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return "", errInvalidRequest
	}
	if _, err = dec.Token(); err != io.EOF {
		return "", errInvalidRequest
	}
	return token, nil
}

func writeAuth(st network.Stream, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 4096 {
		return errors.New("response oversized")
	}
	var header [10]byte
	n := binary.PutUvarint(header[:], uint64(len(data)))
	if _, err = st.Write(header[:n]); err != nil {
		return err
	}
	_, err = st.Write(data)
	return err
}

func authDiagnostic(code string) (string, string) {
	switch code {
	case "":
		return "success", "none"
	case "invalid_request":
		return "refused", "invalid_request"
	case "authentication_failed":
		return "refused", "invalid_credentials"
	case "account_change_requires_new_connection":
		return "refused", "account_mismatch"
	case "rate_limited":
		return "refused", "rate_limited"
	case "session_limit_exceeded":
		return "refused", "session_limit"
	case "quota_exhausted":
		return "", ""
	default:
		return "failed", "database_unavailable"
	}
}

func (s *Server) handleAuth(st network.Stream) {
	requestID := ""
	if s.diagnostics != nil {
		requestID = s.diagnostics.NewID()
	}
	started := time.Now()
	emit := func(outcome, reason string) {
		s.sessionManager.emitConnection(st.Conn(), "relay.authentication", outcome, reason, requestID, time.Since(started))
	}

	if st.Scope().SetService("clipp.relay-auth") != nil {
		if s.diagnostics != nil {
			s.diagnostics.Emit(diagnostic.Event{Event: "service.admission", Outcome: "refused", Reason: "resource_limit", Gate: "stream"})
		}
		_ = st.Reset()
		return
	}
	if st.Scope().ReserveMemory(4096, network.ReservationPriorityAlways) != nil {
		if s.diagnostics != nil {
			s.diagnostics.Emit(diagnostic.Event{Event: "service.admission", Outcome: "refused", Reason: "resource_limit", Gate: "stream"})
		}
		_ = st.Reset()
		return
	}
	defer st.Scope().ReleaseMemory(4096)
	_ = st.SetDeadline(time.Now().Add(10 * time.Second))
	raw, err := readAuth(st)
	if err == nil || errors.Is(err, errInvalidRequest) {
		var extra [1]byte
		if n, tailErr := st.Read(extra[:]); n != 0 || tailErr != io.EOF {
			emit("refused", "invalid_request")
			_ = st.Reset()
			s.sessionManager.closeConnection(st.Conn())
			return
		}
	}
	if err != nil {
		emit("refused", "invalid_request")
		if errors.Is(err, errInvalidRequest) {
			_ = writeAuth(st, map[string]any{"ok": false, "code": "invalid_request"})
			_ = st.CloseWrite()
			_ = st.Close()
			time.Sleep(100 * time.Millisecond)
			s.sessionManager.closeConnection(st.Conn())
		} else {
			_ = st.Reset()
			s.sessionManager.closeConnection(st.Conn())
		}
		return
	}
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wasAuthenticated := s.sessionManager.isAuthenticated(st.Conn())
	deadline, renew, code, observedReason := s.sessionManager.authenticateObserved(ctx, st.Conn(), raw)
	outcome, reason := authDiagnostic(code)
	if observedReason != "" {
		reason = observedReason
	}
	if code == "quota_exhausted" {
		outcome = ""
	}
	if code == "temporarily_unavailable" {
		switch reason {
		case "deadline", "overloaded":
			outcome = ""
			if s.diagnostics != nil {
				s.diagnostics.Emit(diagnostic.Event{Event: "service.admission", Outcome: "refused", Reason: reason, Gate: "session"})
			}
		case "draining":
			outcome = "refused"
		case "":
			outcome = ""
		}
	}
	if outcome != "" {
		emit(outcome, reason)
	}
	if code == "" {
		if wasAuthenticated {
			s.sessionManager.emitConnection(st.Conn(), "relay.session", "success", "renewed", "", 0)
		} else {
			s.sessionManager.emitConnection(st.Conn(), "relay.session", "started", "none", "", 0)
		}
	}
	if code != "" {
		if writeAuth(st, map[string]any{"ok": false, "code": code}) != nil {
			_ = st.Reset()
		} else {
			_ = st.CloseWrite()
			if !s.sessionManager.isAuthenticated(st.Conn()) {
				// Give the peer time to consume the framed error before closing
				// its whole connection; half-closed requests reach EOF immediately.
				time.Sleep(100 * time.Millisecond)
			}
			_ = st.Close()
		}
		s.sessionManager.closeUnauthenticated(st.Conn())
		return
	}
	if writeAuth(st, map[string]any{"ok": true, "sessionExpiresAt": deadline.UTC().Format(time.RFC3339Nano), "renewAfterMillis": renew.Milliseconds()}) != nil {
		_ = st.Reset()
	} else {
		_ = st.Close()
	}
}

func (m *sessionManager) isAuthenticated(c network.Conn) bool {
	s := m.server
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byConn[c] != nil
}

// ActiveSessions is an aggregate observation; it never exposes Peer IDs.
func (s *Server) ActiveSessions() int { return s.sessionManager.activeSessions() }
func (s *Server) Serving() bool       { return s.sessionManager.serving() }
func (s *Server) AccountSessions(account string) int {
	return s.sessionManager.accountSessions(account)
}

func (m *sessionManager) activeSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byConn)
}

func (s *Server) SessionCapacityReached() bool { return s.sessionManager.capacityReached() }

func (m *sessionManager) capacityReached() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byConn) >= m.maxSessions
}

func (m *sessionManager) serving() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closing
}

func (m *sessionManager) ObserveTraffic(id peer.ID, n int64) bool {
	m.mu.Lock()
	active := m.byPeer[id]
	accounting := m.accounting
	current := active != nil && time.Now().Before(active.deadline)
	account := ""
	if current {
		account = active.account
	}
	m.mu.Unlock()
	if !current {
		return false
	}
	if accounting != nil {
		if err := accounting.Observe(account, n); err != nil {
			accounting.AccountingFailed(account)
		}
	}
	return true
}

func (m *sessionManager) accountSessions(account string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byAccount[account])
}

// ListenAddress gives the bound TCP multiaddr; discovery adds the current Peer ID.
func (s *Server) ListenAddress() (ma.Multiaddr, error) {
	for _, a := range s.Host.Addrs() {
		if _, err := a.ValueForProtocol(ma.P_TCP); err == nil && !hasProtocol(a, ma.P_WS) {
			return a, nil
		}
	}
	return nil, net.ErrClosed
}

// ListenAddressFor reports the bound address of an enabled listener. WebRTC's
// returned address includes the ephemeral certificate hash generated at startup.
func (s *Server) ListenAddressFor(transport string) (ma.Multiaddr, error) {
	for _, a := range s.Host.Addrs() {
		switch transport {
		case "websocket":
			if hasProtocol(a, ma.P_WS) {
				return a, nil
			}
		case "webrtc-direct":
			if hasProtocol(a, ma.P_WEBRTC_DIRECT) && hasProtocol(a, ma.P_CERTHASH) {
				return a, nil
			}
		case "tcp":
			if _, err := a.ValueForProtocol(ma.P_TCP); err == nil && !hasProtocol(a, ma.P_WS) {
				return a, nil
			}
		}
	}
	return nil, net.ErrClosed
}

func hasProtocol(a ma.Multiaddr, code int) bool {
	for _, p := range a.Protocols() {
		if p.Code == code {
			return true
		}
	}
	return false
}
