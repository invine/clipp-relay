package relay

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func frame(body string) []byte {
	var prefix [10]byte
	n := binary.PutUvarint(prefix[:], uint64(len(body)))
	return append(prefix[:n], body...)
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
