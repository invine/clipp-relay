package relay

import (
	"bytes"
	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/record"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
	multistream "github.com/multiformats/go-multistream"
)

// Test-only client negotiation: application responses and timeouts never
// qualify as unsupported multistream, so they cannot trigger a v1 dial.
func rvDialPreferred(ctx context.Context, h host.Host, relayID peer.ID) (network.Stream, error) {
	st, err := h.NewStream(ctx, relayID, RendezvousV2Protocol)
	if err == nil {
		return st, nil
	}
	if !errors.Is(err, multistream.ErrNotSupported[protocol.ID]{}) {
		return nil, err
	}
	return h.NewStream(ctx, relayID, RendezvousV1Protocol)
}

func rvClient(t *testing.T, ctx context.Context, s *Server, token string) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	sendWireToken(t, ctx, h, s.Host.ID(), token)
	return h
}

func rvEnvelope(t *testing.T, h host.Host, relayID peer.ID) []byte {
	return rvEnvelopeFor(t, h, relayID, h.ID())
}
func rvEnvelopeFor(t *testing.T, h host.Host, relayID, subject peer.ID) []byte {
	t.Helper()
	addr, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234/p2p/" + relayID.String() + "/p2p-circuit/p2p/" + h.ID().String())
	if err != nil {
		t.Fatal(err)
	}
	env, err := record.Seal(&peer.PeerRecord{PeerID: subject, Addrs: []ma.Multiaddr{addr}, Seq: 1}, h.Peerstore().PrivKey(h.ID()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func rvExchange(t *testing.T, ctx context.Context, h host.Host, relayID peer.ID, proto string, payload []byte) map[string]json.RawMessage {
	t.Helper()
	st, err := h.NewStream(ctx, relayID, protocolID(proto))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(4 * time.Second))
	if proto == string(RendezvousV2Protocol) {
		payload = frame(string(payload))
	}
	if _, err = st.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if proto == string(RendezvousV2Protocol) {
		n, err := binary.ReadUvarint(byteReader{st})
		if err != nil {
			t.Fatal(err)
		}
		raw = make([]byte, n)
		if _, err = io.ReadFull(st, raw); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err = io.ReadAll(io.LimitReader(st, 128<<10))
		if err != nil {
			t.Fatal(err)
		}
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("response %q: %v", raw, err)
	}
	return out
}

func protocolID(s string) protocol.ID { return protocol.ID(s) }

func TestRendezvousV2RegisterV1CrossAccountLookupAndUnregister(t *testing.T) {
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "a")
	finder := rvClient(t, ctx, s, "b")
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if _, err := client.Reserve(ctx, owner, ai); err != nil {
		t.Fatal(err)
	}
	raw := rvEnvelope(t, owner, s.Host.ID())
	request, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
	registered := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), request)
	if !bytes.Equal(registered["ok"], []byte("true")) || registered["leaseExpiresAt"] == nil {
		t.Fatalf("register: %v", registered)
	}
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	found := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV1Protocol), lookup)
	var rec struct {
		Peer             string `json:"peer"`
		SignedPeerRecord []byte `json:"signedPeerRecord"`
	}
	if err := json.Unmarshal(found["record"], &rec); err != nil || rec.Peer != owner.ID().String() || !bytes.Equal(rec.SignedPeerRecord, raw) {
		t.Fatalf("lookup: %v, %v", found, err)
	}
	unregister, _ := json.Marshal(map[string]any{"action": "unregister", "topic": "clipp"})
	rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV1Protocol), unregister)
	missing := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup)
	if !bytes.Equal(missing["ok"], []byte("true")) || missing["record"] != nil {
		t.Fatalf("miss: %v", missing)
	}
}

func TestDrainRetainsLookupAndUnregisterButRejectsRegister(t *testing.T) {
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "a")
	finder := rvClient(t, ctx, s, "b")
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if _, err := client.Reserve(ctx, owner, ai); err != nil {
		t.Fatal(err)
	}
	raw := rvEnvelope(t, owner, s.Host.ID())
	register, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), register); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("register: %v", got)
	}
	s.StartDrain()
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	if got := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] == nil {
		t.Fatalf("lookup during drain: %v", got)
	}
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), register); !bytes.Equal(got["code"], []byte(`"temporarily_unavailable"`)) {
		t.Fatalf("register during drain: %v", got)
	}
	unregister, _ := json.Marshal(map[string]any{"action": "unregister", "topic": "clipp"})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), unregister); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("unregister during drain: %v", got)
	}
}

func TestRendezvousRegistrationRequiresReservationAndValidReachability(t *testing.T) {
	s, err := New(twoAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "a")
	finder := rvClient(t, ctx, s, "b")
	send := func(raw []byte) map[string]json.RawMessage {
		t.Helper()
		req, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
		return rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), req)
	}
	if got := send(rvEnvelope(t, owner, s.Host.ID())); !bytes.Equal(got["code"], []byte(`"reservation_required"`)) {
		t.Fatalf("unreserved: %v", got)
	}
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if _, err = client.Reserve(ctx, owner, ai); err != nil {
		t.Fatal(err)
	}
	if got := send(rvEnvelopeFor(t, owner, s.Host.ID(), finder.ID())); !bytes.Equal(got["code"], []byte(`"invalid_peer_record"`)) {
		t.Fatalf("subject mismatch: %v", got)
	}
	if got := send(rvEnvelope(t, owner, finder.ID())); !bytes.Equal(got["code"], []byte(`"invalid_peer_record"`)) {
		t.Fatalf("wrong relay route: %v", got)
	}
	raw := rvEnvelope(t, owner, s.Host.ID())
	raw[len(raw)-1] ^= 1
	if got := send(raw); !bytes.Equal(got["code"], []byte(`"invalid_peer_record"`)) {
		t.Fatalf("bad signature: %v", got)
	}
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	if got := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] != nil || !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("rejected record retained: %v", got)
	}
}

func TestRendezvousStrictRequestsAndV1NumericBytes(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "authorized")
	for _, tc := range []struct{ name, proto, request string }{
		{"duplicate", string(RendezvousV2Protocol), `{"action":"lookup","action":"lookup","topic":"clipp","peerId":"x"}`},
		{"unknown", string(RendezvousV2Protocol), `{"action":"unregister","topic":"clipp","extra":1}`},
		{"null", string(RendezvousV2Protocol), `{"action":"unregister","topic":null}`},
		{"list", string(RendezvousV2Protocol), `{"action":"list","topic":"clipp"}`},
		{"v1 fraction", string(RendezvousV1Protocol), `{"action":"register","topic":"clipp","signedPeerRecord":[1.5]}`},
		{"v1 overflow", string(RendezvousV1Protocol), `{"action":"register","topic":"clipp","signedPeerRecord":[256]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rvExchange(t, ctx, owner, s.Host.ID(), tc.proto, []byte(tc.request))
			if !bytes.Equal(got["code"], []byte(`"invalid_request"`)) {
				t.Fatalf("%v", got)
			}
		})
	}
}

func TestRendezvousV1AnswersLegacyOpenWriteStream(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	st, err := h.NewStream(ctx, s.Host.ID(), RendezvousV1Protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = st.Write([]byte(`{"action":"unregister","topic":"clipp"}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.LimitReader(st, 1024))
	if err != nil || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("legacy open-write response %q: %v", raw, err)
	}
}

func TestRendezvousV1RegistrationV2LookupPreservesEnvelope(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	if _, err = client.Reserve(ctx, h, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	raw := rvEnvelope(t, h, s.Host.ID())
	numbers := make([]int, len(raw))
	for i, b := range raw {
		numbers[i] = int(b)
	}
	req, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": numbers})
	if got := rvExchange(t, ctx, h, s.Host.ID(), string(RendezvousV1Protocol), req); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("v1 register: %v", got)
	}
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": h.ID().String()})
	found := rvExchange(t, ctx, h, s.Host.ID(), string(RendezvousV2Protocol), lookup)
	var rec struct {
		Peer             string `json:"peer"`
		SignedPeerRecord string `json:"signedPeerRecord"`
	}
	if err = json.Unmarshal(found["record"], &rec); err != nil || rec.Peer != h.ID().String() || rec.SignedPeerRecord != base64.RawURLEncoding.EncodeToString(raw) || found["leaseExpiresAt"] == nil {
		t.Fatalf("v2 found: %v, %v", found, err)
	}
	if s.RendezvousCountV1() != 1 || s.RendezvousCountV2() != 1 {
		t.Fatalf("version counters: v1=%d v2=%d", s.RendezvousCountV1(), s.RendezvousCountV2())
	}
}

func TestRendezvousRejectsUnauthenticatedPhysicalConnection(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{"action": "unregister", "topic": "clipp"})
	if got := rvExchange(t, ctx, h, s.Host.ID(), string(RendezvousV2Protocol), req); !bytes.Equal(got["code"], []byte(`"authentication_failed"`)) {
		t.Fatalf("unauthenticated: %v", got)
	}
}

func TestRendezvousRejectsOversizeAndExtraFrames(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	for _, tc := range []struct {
		name  string
		proto protocol.ID
		data  []byte
	}{
		{"v2 oversize", RendezvousV2Protocol, []byte{0x81, 0x80, 0x02}},
		{"v2 second frame", RendezvousV2Protocol, append(frame(`{"action":"unregister","topic":"clipp"}`), frame(`{"action":"unregister","topic":"clipp"}`)...)},
		{"v1 oversize", RendezvousV1Protocol, append([]byte(`{"action":"unregister","topic":"clipp"}`), bytes.Repeat([]byte("x"), rvV1Limit)...)},
		{"v1 whitespace over encoded cap", RendezvousV1Protocol, append([]byte(`{"action":"unregister","topic":"clipp"}`), bytes.Repeat([]byte(" "), rvV1Limit)...)},
		{"v1 second object", RendezvousV1Protocol, []byte(`{"action":"unregister","topic":"clipp"}{"action":"unregister","topic":"clipp"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := h.NewStream(ctx, s.Host.ID(), tc.proto)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			_ = st.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err = st.Write(tc.data); err != nil {
				t.Fatal(err)
			}
			_ = st.CloseWrite()
			var b [1]byte
			n, err := st.Read(b[:])
			if n != 0 || err == nil {
				t.Fatalf("invalid frame yielded data n=%d err=%v", n, err)
			}
		})
	}
}

type shortRVAuthority struct{}

func (shortRVAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	if token == "short" {
		return auth.RelayCredential{AccountID: "a", SessionLimit: 5, ExpiresAt: time.Now().Add(700 * time.Millisecond)}, nil
	}
	if token == "long" {
		return auth.RelayCredential{AccountID: "b", SessionLimit: 5, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
	}
	return auth.RelayCredential{}, auth.ErrInvalidAccess
}

func TestRendezvousLeaseEndsAtSessionDeadline(t *testing.T) {
	s, err := New(shortRVAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "short")
	finder := rvClient(t, ctx, s, "long")
	if _, err = client.Reserve(ctx, owner, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	raw := rvEnvelope(t, owner, s.Host.ID())
	req, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
	got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), req)
	var expires time.Time
	if err = json.Unmarshal(got["leaseExpiresAt"], &expires); err != nil {
		t.Fatalf("lease deadline %v: %v", got, err)
	}
	if until := time.Until(expires); until <= 0 || until > time.Second {
		t.Fatalf("lease did not follow session: %v", until)
	}
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	if found := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); found["record"] == nil {
		t.Fatalf("premature miss: %v", found)
	}
	time.Sleep(time.Until(expires) + 100*time.Millisecond)
	if miss := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); miss["record"] != nil || !bytes.Equal(miss["ok"], []byte("true")) {
		t.Fatalf("expired lease visible: %v", miss)
	}
}

func TestRendezvousNewSessionReplacesOldOwner(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	makeOwner := func() host.Host {
		h, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
		sendWireAuth(t, ctx, h, s.Host.ID())
		return h
	}
	first := makeOwner()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if _, err = client.Reserve(ctx, first, ai); err != nil {
		t.Fatal(err)
	}
	request := func(h host.Host) []byte {
		t.Helper()
		raw := rvEnvelope(t, h, s.Host.ID())
		req, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
		return req
	}
	if got := rvExchange(t, ctx, first, s.Host.ID(), string(RendezvousV2Protocol), request(first)); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatal(got)
	}
	finder := rvClient(t, ctx, s, "authorized")
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": first.ID().String()})
	if found := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); found["record"] == nil {
		t.Fatalf("initial lease absent: %v", found)
	}
	second := makeOwner()
	if miss := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); miss["record"] != nil {
		t.Fatalf("old lease survived session replacement: %v", miss)
	}
	if got := rvExchange(t, ctx, second, s.Host.ID(), string(RendezvousV2Protocol), request(second)); !bytes.Equal(got["code"], []byte(`"reservation_required"`)) {
		t.Fatalf("old reservation crossed connection: %v", got)
	}
	if _, err = client.Reserve(ctx, second, ai); err != nil {
		t.Fatal(err)
	}
	if got := rvExchange(t, ctx, second, s.Host.ID(), string(RendezvousV2Protocol), request(second)); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("new owner register: %v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if found := rvExchange(t, ctx, finder, s.Host.ID(), string(RendezvousV2Protocol), lookup); found["record"] == nil {
		t.Fatalf("late old cleanup removed replacement: %v", found)
	}
}

func TestRendezvousV2RejectsQueuedSecondFrame(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	st, err := h.NewStream(ctx, s.Host.ID(), RendezvousV2Protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(2 * time.Second))
	request := frame(`{"action":"unregister","topic":"clipp"}`)
	if _, err = st.Write(request); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Write(request); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if n, err := st.Read(b[:]); n != 0 || err == nil {
		t.Fatalf("queued second frame yielded response: n=%d err=%v", n, err)
	}
}

func TestRendezvousNegotiationFallsBackOnlyWhenV2Unsupported(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	open := func(client host.Host) protocol.ID {
		t.Helper()
		st, err := rvDialPreferred(ctx, client, s.Host.ID())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		return st.Protocol()
	}
	if got := open(h); got != RendezvousV2Protocol {
		t.Fatalf("preferred protocol %s", got)
	}
	// A semantic failure on v2 does not trigger a second v1 request.
	if got := rvExchange(t, ctx, h, s.Host.ID(), string(RendezvousV2Protocol), []byte(`{"action":"list","topic":"clipp"}`)); !bytes.Equal(got["code"], []byte(`"invalid_request"`)) {
		t.Fatalf("semantic error %v", got)
	}
	if s.RendezvousCountV1() != 0 {
		t.Fatalf("v1 fallback after v2 semantic error")
	}
	s.Host.RemoveStreamHandler(RendezvousV2Protocol)
	fresh := rvClient(t, ctx, s, "authorized")
	if got := open(fresh); got != RendezvousV1Protocol {
		t.Fatalf("unsupported v2 did not choose v1: %s", got)
	}
}

func TestRendezvousLeaseAndReservationSurviveSameConnectionRenewal(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := rvClient(t, ctx, s, "authorized")
	if _, err = client.Reserve(ctx, owner, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	raw := rvEnvelope(t, owner, s.Host.ID())
	req, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(raw)})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), req); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatal(got)
	}
	sendWireAuth(t, ctx, owner, s.Host.ID())
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": owner.ID().String()})
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] == nil {
		t.Fatalf("renewal lost lease: %v", got)
	}
	if got := rvExchange(t, ctx, owner, s.Host.ID(), string(RendezvousV2Protocol), req); !bytes.Equal(got["ok"], []byte("true")) {
		t.Fatalf("renewal lost reservation: %v", got)
	}
}

func rvNegotiatedError(t *testing.T, ctx context.Context, h host.Host, relayID peer.ID, expected string) {
	rvNegotiatedErrorRequest(t, ctx, h, relayID, expected, `{"action":"unregister","topic":"clipp"}`)
}

func rvNegotiatedErrorRequest(t *testing.T, ctx context.Context, h host.Host, relayID peer.ID, expected, request string) {
	t.Helper()
	st, err := rvDialPreferred(ctx, h, relayID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Protocol() != RendezvousV2Protocol {
		t.Fatalf("error path negotiated %s; want v2", st.Protocol())
	}
	_ = st.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = st.Write(frame(request)); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	n, err := binary.ReadUvarint(byteReader{st})
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, n)
	if _, err = io.ReadFull(st, raw); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Ok   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err = json.Unmarshal(raw, &response); err != nil || response.Ok || response.Code != expected {
		t.Fatalf("error response %q: %v", raw, err)
	}
}

func TestRendezvousNoV1FallbackForAuthQuotaTimeoutOrServerError(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
		rvNegotiatedError(t, ctx, h, s.Host.ID(), "authentication_failed")
		if s.RendezvousCountV1() != 0 || s.RendezvousCountV2() != 1 {
			t.Fatalf("auth counters: v1=%d v2=%d", s.RendezvousCountV1(), s.RendezvousCountV2())
		}
	})
	t.Run("quota", func(t *testing.T) {
		s, err := New(wireAuthority{}, failingCredit{quota.ErrExhausted}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
		if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"quota_exhausted"`) {
			t.Fatalf("quota response: %s", response)
		}
		// The quota refusal is an application response, not an unsupported v2 protocol.
		if s.RendezvousCountV1() != 0 {
			t.Fatalf("quota response caused v1 request")
		}
		other, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if err = other.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
		rvNegotiatedError(t, ctx, other, s.Host.ID(), "authentication_failed")
		if s.RendezvousCountV1() != 0 {
			t.Fatalf("quota path negotiated v1")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := rvClient(t, ctx, s, "authorized")
		st, err := rvDialPreferred(ctx, h, s.Host.ID())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Reset()
		if st.Protocol() != RendezvousV2Protocol {
			t.Fatalf("timeout path negotiated %s", st.Protocol())
		}
		_ = st.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err = st.Write([]byte{64, '{'}); err != nil {
			t.Fatal(err)
		}
		// The declared frame remains incomplete. A client read timeout must
		// not be interpreted as unsupported multistream.
		if _, err = binary.ReadUvarint(byteReader{st}); err == nil {
			t.Fatal("incomplete request received a response")
		}
		if s.RendezvousCountV1() != 0 {
			t.Fatalf("timeout path negotiated v1")
		}
	})
	t.Run("server", func(t *testing.T) {
		s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := rvClient(t, ctx, s, "authorized")
		s.StartDrain()
		rvNegotiatedErrorRequest(t, ctx, h, s.Host.ID(), "temporarily_unavailable", `{"action":"register","topic":"clipp","signedPeerRecord":"AA"}`)
		if s.RendezvousCountV1() != 0 || s.RendezvousCountV2() != 1 {
			t.Fatalf("server counters: v1=%d v2=%d", s.RendezvousCountV1(), s.RendezvousCountV2())
		}
	})
}

func TestRendezvousV2AnswersCompleteFrameWithWriteSideOpen(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := rvClient(t, ctx, s, "authorized")
	st, err := h.NewStream(ctx, s.Host.ID(), RendezvousV2Protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = st.Write(frame(`{"action":"unregister","topic":"clipp"}`)); err != nil {
		t.Fatal(err)
	}
	n, err := binary.ReadUvarint(byteReader{st})
	if err != nil {
		t.Fatalf("open-write frame header: %v", err)
	}
	raw := make([]byte, n)
	if _, err = io.ReadFull(st, raw); err != nil || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("open-write response %q: %v", raw, err)
	}
	var tail [1]byte
	if n, err := st.Read(tail[:]); n != 0 || err != io.EOF {
		t.Fatalf("stream did not close after one response: n=%d err=%v", n, err)
	}
}
