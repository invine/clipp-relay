package relay

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestReporterChargesCurrentPeerAssociationAndRecordsUnattributedTail(t *testing.T) {
	credit := &recordingCredit{counts: map[string]int64{}}
	s, err := New(twoAuthority{}, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reporter := &endpointReporter{server: s}
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connect := func() host.Host {
		t.Helper()
		h, err := libp2p.New(libp2p.Identity(key), libp2p.NoListenAddrs, libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			_ = h.Close()
			t.Fatal(err)
		}
		return h
	}
	first := connect()
	reporter.LogRecvMessageStream(7, hopProtocol, first.ID())
	if got := reporter.unattributed.Load(); got != 7 {
		t.Fatalf("pre-auth bytes were attributed: %d", got)
	}
	sendWireToken(t, ctx, first, s.Host.ID(), "a")
	reporter.LogRecvMessageStream(11, hopProtocol, first.ID())
	reporter.LogSentMessageStream(13, "/libp2p/circuit/relay/0.2.0/stop", first.ID())
	reporter.LogSentMessageStream(17, AuthProtocol, first.ID())
	if got := credit.count("a"); got != 24 {
		t.Fatalf("first association charged %d, want 24 HOP/STOP bytes", got)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.AccountSessions("a") != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("old association remained after disconnect: %d", got)
	}
	reporter.LogSentMessageStream(19, hopProtocol, first.ID())
	if got := reporter.unattributed.Load(); got != 26 {
		t.Fatalf("unassociated tail count = %d, want 26", got)
	}
	second := connect()
	defer second.Close()
	sendWireToken(t, ctx, second, s.Host.ID(), "b")
	// The callback identifies a peer, not the original connection. A late
	// callback for the old connection therefore uses the current association.
	reporter.LogSentMessageStream(23, hopProtocol, first.ID())
	if got := credit.count("a"); got != 24 {
		t.Fatalf("late bytes charged old account: %d", got)
	}
	if got := credit.count("b"); got != 23 {
		t.Fatalf("late bytes charged current account %d, want 23", got)
	}
}
