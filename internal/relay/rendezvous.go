package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/record"
	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const RendezvousV1Protocol protocol.ID = "/clipp/rendezvous/1.0.0"
const RendezvousV2Protocol protocol.ID = "/clipp/rendezvous/2.0.0"
const rvRawLimit = 16 << 10
const rvV1Limit = 128 << 10
const rvV2Limit = 32 << 10

type rendezvousLease struct {
	owner        *session
	record       []byte
	registeredAt time.Time
	deadline     time.Time
	timer        *time.Timer
}
type reservationOwner struct {
	conn     network.Conn
	deadline time.Time
}

// reservationStream observes a successful stock RESERVE response. The stock
// relay owns forwarding and reservation admission; this records the exact
// connection and expiry for Rendezvous's stricter registration gate.
type reservationStream struct {
	network.Stream
	server   *Server
	response []byte
}

func (st *reservationStream) Write(p []byte) (int, error) {
	n, err := st.Stream.Write(p)
	if err != nil || n <= 0 || len(st.response)+n > 4096 {
		return n, err
	}
	st.response = append(st.response, p[:n]...)
	size, header := binary.Uvarint(st.response)
	if header <= 0 || size != uint64(len(st.response)-header) {
		return n, err
	}
	var msg pbv2.HopMessage
	if proto.Unmarshal(st.response[header:], &msg) == nil && msg.GetStatus() == pbv2.Status_OK && msg.GetReservation() != nil {
		deadline := time.Unix(int64(msg.GetReservation().GetExpire()), 0)
		st.server.mu.Lock()
		if owner := st.server.byConn[st.Conn()]; owner != nil {
			st.server.reservations[st.Conn().RemotePeer()] = reservationOwner{conn: st.Conn(), deadline: deadline}
		}
		st.server.mu.Unlock()
	}
	return n, err
}

func (s *Server) removeRendezvousLocked(v *session) {
	id := v.conn.RemotePeer()
	if lease := s.leases[id]; lease != nil && lease.owner == v {
		s.deleteLeaseLocked(id, lease)
	}
	if r := s.reservations[id]; r.conn == v.conn {
		delete(s.reservations, id)
	}
}
func (s *Server) deleteLeaseLocked(id peer.ID, lease *rendezvousLease) {
	if s.leases[id] != lease {
		return
	}
	delete(s.leases, id)
	if lease.timer != nil {
		lease.timer.Stop()
	}
}
func (s *Server) currentLeaseLocked(id peer.ID, now time.Time) *rendezvousLease {
	lease := s.leases[id]
	if lease == nil {
		return nil
	}
	r := s.reservations[id]
	if !now.Before(lease.deadline) || s.byConn[lease.owner.conn] != lease.owner || r.conn != lease.owner.conn || !now.Before(r.deadline) {
		s.deleteLeaseLocked(id, lease)
		return nil
	}
	return lease
}

func (s *Server) handleRendezvous(st network.Stream) {
	version := st.Protocol()
	if version == RendezvousV1Protocol {
		s.rvCounts[0].Add(1)
	} else {
		s.rvCounts[1].Add(1)
	}
	limit := rvV1Limit
	if version == RendezvousV2Protocol {
		limit = rvV2Limit
	}
	if st.Scope().SetService("clipp.rendezvous") != nil || st.Scope().ReserveMemory(limit*2, network.ReservationPriorityAlways) != nil {
		_ = st.Reset()
		return
	}
	defer st.Scope().ReleaseMemory(limit * 2)
	_ = st.SetDeadline(time.Now().Add(12 * time.Second))
	defer st.SetDeadline(time.Time{})
	now := time.Now()
	s.mu.Lock()
	owner := s.byConn[st.Conn()]
	authenticated := owner != nil && now.Before(owner.deadline)
	allowed := s.rvGlobalRate.allow(now, 500, 1000)
	if authenticated {
		b := s.rvConnRate[st.Conn()]
		allowed = b.allow(now, 4, 8) && allowed
		s.rvConnRate[st.Conn()] = b
	}
	s.mu.Unlock()
	if !authenticated {
		if s.Serving() {
			s.rvError(st, version, "authentication_failed", 0)
		} else {
			s.rvError(st, version, "temporarily_unavailable", 5000)
		}
		return
	}
	if !allowed {
		s.rvError(st, version, "rate_limited", 5000)
		return
	}
	data, err := readRV(st, version, limit)
	if err != nil {
		_ = st.Reset()
		return
	}
	request, err := parseRV(data, version)
	if err != nil {
		s.rvError(st, version, "invalid_request", 0)
		return
	}
	response := s.applyRV(st, owner, request)
	if err := writeRV(st, version, response, limit); err != nil {
		_ = st.Reset()
		return
	}
	_ = st.CloseWrite()
	_ = st.Close()
}

func (s *Server) rvError(st network.Stream, version protocol.ID, code string, retry int) {
	response := map[string]any{"ok": false, "code": code}
	if retry > 0 {
		response["retryAfterMillis"] = retry
	}
	limit := rvV1Limit
	if version == RendezvousV2Protocol {
		limit = rvV2Limit
	}
	if writeRV(st, version, response, limit) != nil {
		_ = st.Reset()
		return
	}
	_ = st.CloseWrite()
	_ = st.Close()
}
func readRV(st network.Stream, version protocol.ID, limit int) ([]byte, error) {
	if version == RendezvousV2Protocol {
		n, err := binary.ReadUvarint(byteReader{st})
		if err != nil || n == 0 || n > uint64(limit) {
			return nil, errBadFrame
		}
		data := make([]byte, int(n))
		if _, err = io.ReadFull(st, data); err != nil {
			return nil, errBadFrame
		}
		// A complete frame is a complete request, including when a client
		// keeps its write side open while awaiting the response. Reject bytes
		// already queued after the frame before producing the one response.
		if !rvV2TailClean(st) {
			return nil, errBadFrame
		}
		return data, nil
	}
	// Legacy v1 clients write one unframed JSON object and await the response
	// without half-closing their write side. Decode one complete value instead
	// of waiting for EOF, then reject already-arrived additional data.
	bounded := &io.LimitedReader{R: st, N: int64(limit + 1)}
	dec := json.NewDecoder(bounded)
	var data json.RawMessage
	if dec.Decode(&data) != nil || len(data) == 0 || len(data) > limit {
		return nil, errBadFrame
	}
	// Decoder may read ahead. Count all encoded bytes, including JSON
	// whitespace outside the object, against the full v1 wire limit.
	consumed := int64(limit+1) - bounded.N
	if consumed > int64(limit) {
		return nil, errBadFrame
	}
	if extra, _ := io.ReadAll(dec.Buffered()); !rvWhitespace(extra) {
		return nil, errBadFrame
	}
	if !rvTailClean(st, int64(limit)-consumed) {
		return nil, errBadFrame
	}
	return data, nil
}

// A short bounded probe rejects an already queued second request while allowing
// clients to keep the write side open until they receive the response. Data
// arriving after that response cannot invalidate work already completed.
func rvTailClean(st network.Stream, remaining int64) bool {
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	var buf [4096]byte
	for {
		n, err := st.Read(buf[:])
		remaining -= int64(n)
		if remaining < 0 || !rvWhitespace(buf[:n]) {
			return false
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return true
			}
			var netErr net.Error
			return errors.As(err, &netErr) && netErr.Timeout()
		}
	}
}

func rvV2TailClean(st network.Stream) bool {
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	var b [1]byte
	n, err := st.Read(b[:])
	if n != 0 {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func rvWhitespace(data []byte) bool {
	for _, b := range data {
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
	}
	return true
}

func writeRV(st network.Stream, version protocol.ID, response any, limit int) error {
	data, err := json.Marshal(response)
	if err != nil || len(data) > limit {
		return errBadFrame
	}
	if version == RendezvousV2Protocol {
		var header [10]byte
		n := binary.PutUvarint(header[:], uint64(len(data)))
		if err = writeRVBytes(st, header[:n]); err != nil {
			return err
		}
	}
	return writeRVBytes(st, data)
}
func writeRVBytes(st network.Stream, data []byte) error {
	for len(data) > 0 {
		n, err := st.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type rvRequest struct {
	action   string
	topic    string
	peer     peer.ID
	envelope []byte
}

func parseRV(data []byte, version protocol.ID) (rvRequest, error) {
	invalid := errors.New("invalid rendezvous request")
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return rvRequest{}, invalid
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return rvRequest{}, invalid
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return rvRequest{}, invalid
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return rvRequest{}, invalid
		}
		fields[key] = raw
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return rvRequest{}, invalid
	}
	if _, err = dec.Token(); err != io.EOF {
		return rvRequest{}, invalid
	}
	var req rvRequest
	if json.Unmarshal(fields["action"], &req.action) != nil || json.Unmarshal(fields["topic"], &req.topic) != nil || req.topic != "clipp" {
		return rvRequest{}, invalid
	}
	if req.action != "register" && req.action != "lookup" && req.action != "unregister" {
		return rvRequest{}, invalid
	}
	wanted := map[string]bool{"action": true, "topic": true}
	switch req.action {
	case "lookup":
		wanted["peerId"] = true
		var id string
		if json.Unmarshal(fields["peerId"], &id) != nil {
			return rvRequest{}, invalid
		}
		req.peer, err = peer.Decode(id)
		if err != nil {
			return rvRequest{}, invalid
		}
	case "register":
		wanted["signedPeerRecord"] = true
		if version == RendezvousV2Protocol {
			var encoded string
			if json.Unmarshal(fields["signedPeerRecord"], &encoded) != nil || encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(rvRawLimit) {
				return rvRequest{}, invalid
			}
			req.envelope, err = base64.RawURLEncoding.DecodeString(encoded)
			if err != nil || len(req.envelope) == 0 || len(req.envelope) > rvRawLimit || base64.RawURLEncoding.EncodeToString(req.envelope) != encoded {
				return rvRequest{}, invalid
			}
		} else {
			var items []json.RawMessage
			if json.Unmarshal(fields["signedPeerRecord"], &items) != nil || len(items) == 0 || len(items) > rvRawLimit {
				return rvRequest{}, invalid
			}
			req.envelope = make([]byte, len(items))
			for i, item := range items {
				rawNumber := string(item)
				if rawNumber == "" {
					return rvRequest{}, invalid
				}
				for _, digit := range rawNumber {
					if digit < '0' || digit > '9' {
						return rvRequest{}, invalid
					}
				}
				n, err := strconv.Atoi(rawNumber)
				if err != nil || n < 0 || n > 255 {
					return rvRequest{}, invalid
				}
				req.envelope[i] = byte(n)
			}
		}
	}
	if len(fields) != len(wanted) {
		return rvRequest{}, invalid
	}
	for key := range fields {
		if !wanted[key] {
			return rvRequest{}, invalid
		}
	}
	return req, nil
}

func (s *Server) applyRV(st network.Stream, owner *session, req rvRequest) any {
	id := st.Conn().RemotePeer()
	s.mu.Lock()
	now := time.Now()
	if s.closing && req.action == "register" {
		s.mu.Unlock()
		return map[string]any{"ok": false, "code": "temporarily_unavailable", "retryAfterMillis": 5000}
	}
	if s.byConn[st.Conn()] != owner || !now.Before(owner.deadline) {
		s.mu.Unlock()
		return map[string]any{"ok": false, "code": "authentication_failed"}
	}
	switch req.action {
	case "lookup":
		lease := s.currentLeaseLocked(req.peer, now)
		s.mu.Unlock()
		if lease == nil {
			return map[string]any{"ok": true}
		}
		if st.Protocol() == RendezvousV2Protocol {
			return map[string]any{"ok": true, "record": map[string]any{"peer": req.peer.String(), "signedPeerRecord": base64.RawURLEncoding.EncodeToString(lease.record)}, "leaseExpiresAt": lease.deadline.UTC().Format(time.RFC3339Nano)}
		}
		numbers := make([]int, len(lease.record))
		for i, b := range lease.record {
			numbers[i] = int(b)
		}
		return map[string]any{"ok": true, "record": map[string]any{"peer": req.peer.String(), "signedPeerRecord": numbers, "lastSeen": lease.registeredAt.UnixMilli()}}
	case "unregister":
		if lease := s.leases[id]; lease != nil && lease.owner == owner {
			s.deleteLeaseLocked(id, lease)
		}
		s.mu.Unlock()
		return map[string]any{"ok": true}
	case "register":
		reservation := s.reservations[id]
		if reservation.conn != owner.conn || !now.Before(reservation.deadline) {
			s.mu.Unlock()
			return map[string]any{"ok": false, "code": "reservation_required"}
		}
		s.mu.Unlock()
		// Signature verification and address parsing operate on the already bounded
		// request without holding the shared session/lease lock.
		if !validPeerRecord(req.envelope, id, s.Host.ID()) {
			return map[string]any{"ok": false, "code": "invalid_peer_record"}
		}
		s.mu.Lock()
		now = time.Now()
		if s.closing {
			s.mu.Unlock()
			return map[string]any{"ok": false, "code": "temporarily_unavailable", "retryAfterMillis": 5000}
		}
		if s.byConn[st.Conn()] != owner || !now.Before(owner.deadline) {
			s.mu.Unlock()
			return map[string]any{"ok": false, "code": "authentication_failed"}
		}
		reservation = s.reservations[id]
		if reservation.conn != owner.conn || !now.Before(reservation.deadline) {
			s.mu.Unlock()
			return map[string]any{"ok": false, "code": "reservation_required"}
		}
		deadline := now.Add(5 * time.Minute)
		if owner.deadline.Before(deadline) {
			deadline = owner.deadline
		}
		if reservation.deadline.Before(deadline) {
			deadline = reservation.deadline
		}
		if !now.Before(deadline) {
			s.mu.Unlock()
			return map[string]any{"ok": false, "code": "reservation_required"}
		}
		if old := s.leases[id]; old != nil {
			s.deleteLeaseLocked(id, old)
		}
		lease := &rendezvousLease{owner: owner, record: bytes.Clone(req.envelope), registeredAt: now, deadline: deadline}
		s.leases[id] = lease
		lease.timer = time.AfterFunc(time.Until(deadline), func() { s.mu.Lock(); s.deleteLeaseLocked(id, lease); s.mu.Unlock() })
		s.mu.Unlock()
		if st.Protocol() == RendezvousV2Protocol {
			return map[string]any{"ok": true, "peer": id.String(), "leaseExpiresAt": deadline.UTC().Format(time.RFC3339Nano)}
		}
		return map[string]any{"ok": true, "peer": id.String()}
	}
	s.mu.Unlock()
	return map[string]any{"ok": false, "code": "invalid_request"}
}

func validPeerRecord(raw []byte, subject, relayID peer.ID) bool {
	if len(raw) == 0 || len(raw) > rvRawLimit {
		return false
	}
	unchecked, err := record.UnmarshalEnvelope(raw)
	if err != nil || !bytes.Equal(unchecked.PayloadType, peer.PeerRecordEnvelopePayloadType) || !boundedPeerAddresses(unchecked.RawPayload) {
		return false
	}
	rec := new(peer.PeerRecord)
	env, err := record.ConsumeTypedEnvelope(raw, rec)
	if err != nil || env == nil || rec.PeerID != subject || len(rec.Addrs) == 0 || len(rec.Addrs) > 64 {
		return false
	}
	signer, err := peer.IDFromPublicKey(env.PublicKey)
	if err != nil || signer != subject {
		return false
	}
	relayPart := "/p2p/" + relayID.String() + "/p2p-circuit"
	subjectPart := "/p2p/" + subject.String()
	for _, addr := range rec.Addrs {
		if len(addr.Bytes()) > 1024 {
			return false
		}
		route := addr.String()
		if strings.Contains(route, relayPart) && strings.HasSuffix(route, subjectPart) {
			return true
		}
	}
	return false
}

// Validate the protobuf address count and each encoded multiaddr before the
// library allocates address objects for the signed Peer Record.
func boundedPeerAddresses(payload []byte) bool {
	count := 0
	for len(payload) > 0 {
		field, wire, n := protowire.ConsumeTag(payload)
		if n < 0 {
			return false
		}
		payload = payload[n:]
		if field == 3 {
			if wire != protowire.BytesType {
				return false
			}
			address, n := protowire.ConsumeBytes(payload)
			if n < 0 {
				return false
			}
			count++
			if count > 64 || len(address) > 1030 {
				return false
			}
			var multiaddr []byte
			for len(address) > 0 {
				inner, kind, m := protowire.ConsumeTag(address)
				if m < 0 {
					return false
				}
				address = address[m:]
				if inner == 1 {
					if kind != protowire.BytesType || multiaddr != nil {
						return false
					}
					multiaddr, m = protowire.ConsumeBytes(address)
					if m < 0 || len(multiaddr) == 0 || len(multiaddr) > 1024 {
						return false
					}
				} else {
					m = protowire.ConsumeFieldValue(inner, kind, address)
					if m < 0 {
						return false
					}
				}
				address = address[m:]
			}
			if _, err := ma.NewMultiaddrBytes(multiaddr); err != nil {
				return false
			}
			payload = payload[n:]
		} else {
			n = protowire.ConsumeFieldValue(field, wire, payload)
			if n < 0 {
				return false
			}
			payload = payload[n:]
		}
	}
	return count > 0
}

// RendezvousCountV1 and RendezvousCountV2 expose only fixed-version totals.
func (s *Server) RendezvousCountV1() uint64 { return s.rvCounts[0].Load() }
func (s *Server) RendezvousCountV2() uint64 { return s.rvCounts[1].Load() }
