package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"net"
	"sync"
	"time"

	"clipp-relay/internal/auth"
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
	"github.com/libp2p/go-libp2p/x/rate"
	ma "github.com/multiformats/go-multiaddr"
	multistream "github.com/multiformats/go-multistream"
)

const AuthProtocol protocol.ID = "/clipp/relay-auth/1.0.0"
const hopProtocol protocol.ID = "/libp2p/circuit/relay/0.2.0/hop"

type Authority interface {
	AuthenticateRelay(context.Context, string) (auth.RelayCredential, error)
}
type Credit interface {
	Ensure(context.Context, string, int64) (quota.Result, error)
	Take(context.Context, string, int64, int64) (quota.Result, error)
}

type Options struct {
	ListenAddress string // complete TCP multiaddr; port zero is accepted for isolated tests
	MaxSessions   int
}

type session struct {
	conn       network.Conn
	account    string
	generation int64
	deadline   time.Time
	timer      *time.Timer
}

type Server struct {
	Host        host.Host
	stock       *relay.Relay
	manager     network.ResourceManager
	tracer      *circuitTracer
	authority   Authority
	credit      Credit
	maxSessions int
	mu          sync.Mutex
	byConn      map[network.Conn]*session
	byPeer      map[peer.ID]*session
	byAccount   map[string]map[*session]struct{}
	preauth     map[network.Conn]*time.Timer
	peerGuards  [256]sync.Mutex
	globalRate  tokenBucket
	connRate    map[network.Conn]tokenBucket
	closing     bool
	hopHandlers int
	closeOnce   sync.Once
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
	check    func(network.Stream) bool
	doneHop  func()
	stopConn func(peer.ID) network.Conn
}

func (h gatedHost) SetStreamHandler(id protocol.ID, handler network.StreamHandler) {
	if id == hopProtocol {
		h.Host.SetStreamHandler(id, func(s network.Stream) {
			if !h.check(s) {
				hopStatus(s, pbv2.Status_PERMISSION_DENIED)
				return
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

// New starts an inbound-only, Noise-authenticated TCP relay with a new in-RAM
// identity. It returns only after the listener and fixed resource profile exist.
func New(a Authority, credit Credit, opts Options) (*Server, error) {
	if opts.ListenAddress == "" {
		return nil, errors.New("TCP listener required")
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
	s := &Server{manager: manager, tracer: &circuitTracer{}, authority: a, credit: credit, maxSessions: opts.MaxSessions, byConn: map[network.Conn]*session{}, byPeer: map[peer.ID]*session{}, byAccount: map[string]map[*session]struct{}{}, preauth: map[network.Conn]*time.Timer{}, connRate: map[network.Conn]tokenBucket{}}
	reporter := &endpointReporter{server: s}
	h, err := libp2p.New(libp2p.Identity(key), libp2p.NoTransports,
		libp2p.Transport(tcp.NewTCPTransport), libp2p.ListenAddrStrings(opts.ListenAddress),
		libp2p.Security(noise.ID, noise.New), libp2p.DisableRelay(), libp2p.DisableIdentifyAddressDiscovery(),
		libp2p.ConnectionManager(&connmgr.NullConnMgr{}), libp2p.ResourceManager(manager), libp2p.BandwidthReporter(reporter))
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	s.Host = h
	h.Network().Notify(&network.NotifyBundle{ConnectedF: s.connected, DisconnectedF: s.disconnected})
	h.SetStreamHandler(AuthProtocol, s.handleAuth)
	resources := relay.Resources{Limit: &relay.RelayLimit{Duration: 120 * time.Second, Data: 2 << 20}, ReservationTTL: 30 * time.Minute, MaxReservations: 6000, MaxCircuits: 16, BufferSize: 2048, MaxReservationsPerPeer: 1, MaxReservationsPerIP: 6000, MaxReservationsPerASN: 6000}
	s.stock, err = relay.New(gatedHost{Host: h, check: s.permitted, doneHop: s.doneHop, stopConn: s.authoritativeConn}, relay.WithResources(resources), relay.WithMetricsTracer(s.tracer), relay.WithReservationAddressFilter(func(ma.Multiaddr) bool { return true }))
	if err != nil {
		_ = h.Close()
		_ = manager.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) Close() error {
	var result error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		conns := make([]network.Conn, 0, len(s.byConn))
		for c := range s.byConn {
			conns = append(conns, c)
		}
		s.mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
		_ = s.stock.Close()
		_ = s.Host.Close()
		result = s.manager.Close()
	})
	return result
}

func (s *Server) connected(_ network.Network, c network.Conn) {
	s.mu.Lock()
	if s.closing || len(s.preauth) >= 256 {
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	timer := time.AfterFunc(10*time.Second, func() {
		s.mu.Lock()
		_, pending := s.preauth[c]
		s.mu.Unlock()
		if pending {
			_ = c.Close()
		}
	})
	s.preauth[c] = timer
	s.mu.Unlock()
}

func (s *Server) disconnected(_ network.Network, c network.Conn) {
	s.mu.Lock()
	if t := s.preauth[c]; t != nil {
		t.Stop()
		delete(s.preauth, c)
	}
	delete(s.connRate, c)
	if old := s.byConn[c]; old != nil {
		s.removeLocked(old)
	}
	s.mu.Unlock()
}

func (s *Server) removeLocked(v *session) {
	if s.byConn[v.conn] != v {
		return
	}
	delete(s.byConn, v.conn)
	if s.byPeer[v.conn.RemotePeer()] == v {
		delete(s.byPeer, v.conn.RemotePeer())
	}
	delete(s.byAccount[v.account], v)
	if len(s.byAccount[v.account]) == 0 {
		delete(s.byAccount, v.account)
	}
	if v.timer != nil {
		v.timer.Stop()
	}
}

func (s *Server) permitted(st network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byConn[st.Conn()]
	if s.closing || v == nil || !time.Now().Before(v.deadline) {
		return false
	}
	s.hopHandlers++
	return true
}

func (s *Server) doneHop() {
	s.mu.Lock()
	s.hopHandlers--
	s.mu.Unlock()
}

// Drain stops admission and waits for stock circuits to finish. Existing
// physical connections remain usable until the last circuit is gone.
func (s *Server) StartDrain() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
}

func (s *Server) Drain(ctx context.Context) error {
	s.StartDrain()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		pending := s.hopHandlers
		s.mu.Unlock()
		if pending == 0 && s.tracer.active.Load() == 0 {
			return s.Close()
		}
		select {
		case <-ctx.Done():
			_ = s.Close()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) authoritativeConn(id peer.ID) network.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byPeer[id]
	if s.closing || v == nil || !time.Now().Before(v.deadline) || v.conn.IsClosed() {
		return nil
	}
	return v.conn
}

func (s *Server) expire(v *session) {
	s.mu.Lock()
	owned := s.byConn[v.conn] == v && !time.Now().Before(v.deadline)
	if owned {
		s.removeLocked(v)
	}
	s.mu.Unlock()
	if owned {
		_ = v.conn.Close()
	}
}

func (s *Server) authenticate(ctx context.Context, c network.Conn, raw string) (time.Time, time.Duration, string) {
	s.mu.Lock()
	allowed := s.globalRate.allow(time.Now(), 200, 400)
	b := s.connRate[c]
	allowed = b.allow(time.Now(), 1, 2) && allowed
	s.connRate[c] = b
	s.mu.Unlock()
	if !allowed {
		return time.Time{}, 0, "rate_limited"
	}
	guard := &s.peerGuards[peerGuardIndex(c.RemotePeer())]
	guard.Lock()
	defer guard.Unlock()
	credential, err := s.authority.AuthenticateRelay(ctx, raw)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidAccess) {
			return time.Time{}, 0, "authentication_failed"
		}
		return time.Time{}, 0, "temporarily_unavailable"
	}
	s.mu.Lock()
	current := s.byConn[c]
	replaced := s.byPeer[c.RemotePeer()]
	s.mu.Unlock()
	if current != nil && current.account != credential.AccountID {
		return time.Time{}, 0, "account_change_requires_new_connection"
	}
	if _, err := s.credit.Ensure(ctx, credential.AccountID, credential.Generation); err != nil {
		if errors.Is(err, quota.ErrExhausted) {
			return time.Time{}, 0, "quota_exhausted"
		}
		return time.Time{}, 0, "temporarily_unavailable"
	}
	deadline := time.Now().Add(15 * time.Minute)
	if credential.ExpiresAt.Before(deadline) {
		deadline = credential.ExpiresAt
	}
	if !time.Now().Before(deadline) {
		return time.Time{}, 0, "authentication_failed"
	}
	var jitter [8]byte
	if _, err := rand.Read(jitter[:]); err != nil {
		return time.Time{}, 0, "temporarily_unavailable"
	}
	// Uniform 75–85% of admitted lifetime, independent of a stable Peer ID.
	renewFraction := 0.75 + float64(binary.LittleEndian.Uint64(jitter[:])%10001)/100000
	s.mu.Lock()
	if s.closing || c.IsClosed() {
		s.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable"
	}
	if current != nil && s.byConn[c] != current || replaced != nil && s.byPeer[c.RemotePeer()] != replaced {
		s.mu.Unlock()
		return time.Time{}, 0, "temporarily_unavailable"
	}
	if current == nil {
		count := len(s.byAccount[credential.AccountID])
		if replaced != nil && replaced.account == credential.AccountID {
			count--
		}
		if count >= credential.SessionLimit {
			s.mu.Unlock()
			return time.Time{}, 0, "session_limit_exceeded"
		}
		if len(s.byConn) >= s.maxSessions && replaced == nil {
			s.mu.Unlock()
			return time.Time{}, 0, "session_limit_exceeded"
		}
	}
	if current != nil {
		s.removeLocked(current)
	}
	if replaced != nil && replaced != current {
		s.removeLocked(replaced)
	}
	v := &session{conn: c, account: credential.AccountID, generation: credential.Generation, deadline: deadline}
	s.byConn[c] = v
	s.byPeer[c.RemotePeer()] = v
	if s.byAccount[v.account] == nil {
		s.byAccount[v.account] = map[*session]struct{}{}
	}
	s.byAccount[v.account][v] = struct{}{}
	if t := s.preauth[c]; t != nil {
		t.Stop()
		delete(s.preauth, c)
	}
	v.timer = time.AfterFunc(time.Until(deadline), func() { s.expire(v) })
	s.mu.Unlock()
	if replaced != nil && replaced != current {
		_ = replaced.conn.Close()
	}
	lifetime := time.Until(deadline)
	return deadline, time.Duration(float64(lifetime) * renewFraction), ""
}

func peerGuardIndex(id peer.ID) uint8 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return uint8(h.Sum32())
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

func (s *Server) handleAuth(st network.Stream) {
	if st.Scope().SetService("clipp.relay-auth") != nil {
		_ = st.Reset()
		return
	}
	if st.Scope().ReserveMemory(4096, network.ReservationPriorityAlways) != nil {
		_ = st.Reset()
		return
	}
	defer st.Scope().ReleaseMemory(4096)
	_ = st.SetDeadline(time.Now().Add(10 * time.Second))
	raw, err := readAuth(st)
	if err == nil || errors.Is(err, errInvalidRequest) {
		var extra [1]byte
		if n, tailErr := st.Read(extra[:]); n != 0 || tailErr != io.EOF {
			_ = st.Reset()
			_ = st.Conn().Close()
			return
		}
	}
	if err != nil {
		if errors.Is(err, errInvalidRequest) {
			_ = writeAuth(st, map[string]any{"ok": false, "code": "invalid_request"})
			_ = st.CloseWrite()
			_ = st.Close()
			time.Sleep(100 * time.Millisecond)
			_ = st.Conn().Close()
		} else {
			_ = st.Reset()
			_ = st.Conn().Close()
		}
		return
	}
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	deadline, renew, code := s.authenticate(ctx, st.Conn(), raw)
	if code != "" {
		if writeAuth(st, map[string]any{"ok": false, "code": code}) != nil {
			_ = st.Reset()
		} else {
			_ = st.CloseWrite()
			if !s.isAuthenticated(st.Conn()) {
				// Give the peer time to consume the framed error before closing
				// its whole connection; half-closed requests reach EOF immediately.
				time.Sleep(100 * time.Millisecond)
			}
			_ = st.Close()
		}
		if !s.isAuthenticated(st.Conn()) {
			_ = st.Conn().Close()
		}
		return
	}
	if writeAuth(st, map[string]any{"ok": true, "sessionExpiresAt": deadline.UTC().Format(time.RFC3339Nano), "renewAfterMillis": renew.Milliseconds()}) != nil {
		_ = st.Reset()
	} else {
		_ = st.Close()
	}
}

func (s *Server) isAuthenticated(c network.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byConn[c] != nil
}

// ActiveSessions is an aggregate observation; it never exposes Peer IDs.
func (s *Server) ActiveSessions() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.byConn) }
func (s *Server) Serving() bool       { s.mu.Lock(); defer s.mu.Unlock(); return !s.closing }
func (s *Server) AccountSessions(account string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byAccount[account])
}

// ListenAddress gives the bound TCP multiaddr; discovery adds the current Peer ID.
func (s *Server) ListenAddress() (ma.Multiaddr, error) {
	for _, a := range s.Host.Addrs() {
		if _, err := a.ValueForProtocol(ma.P_TCP); err == nil {
			return a, nil
		}
	}
	return nil, net.ErrClosed
}
