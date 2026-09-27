package relay

import (
	"bytes"
	"encoding/binary"
	"github.com/libp2p/go-libp2p/core/peer"
	"testing"
)

func frame(body string) []byte {
	var prefix [10]byte
	n := binary.PutUvarint(prefix[:], uint64(len(body)))
	return append(prefix[:n], body...)
}

func TestPeerGuardUsesWholeIdentity(t *testing.T) {
	first := peer.ID("\x12\x20aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	second := peer.ID("\x12\x20bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if peerGuardIndex(first) == peerGuardIndex(second) {
		t.Fatal("distinct Peer IDs with the same multihash prefix share a guard")
	}
}

func TestAuthRequestRequiresOneUniqueTypedKey(t *testing.T) {
	for _, body := range []string{
		`{"accessToken":"first","accessToken":"second"}`,
		`{"accessToken":null}`,
		`{"accessToken":42}`,
		`{"accessToken":"token","extra":true}`,
	} {
		if got, err := readAuth(bytes.NewReader(frame(body))); err == nil {
			t.Fatalf("accepted %s as %q", body, got)
		}
	}
}
