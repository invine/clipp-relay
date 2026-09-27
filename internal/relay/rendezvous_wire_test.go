package relay

import (
	"bytes"
	"clipp-relay/internal/auth"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/record"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

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
	if counts := s.RendezvousCounts(); counts != [2]uint64{1, 1} {
		t.Fatalf("version counters: %v", counts)
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
