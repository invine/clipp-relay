package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"clipp-relay/internal/config"
)

func TestAdminAllowlistReloadFailsClosedAndMatchesExactEmail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"revision":7,"emails":["Admin+ops@gmail.com"]}`)
	a, err := config.LoadAdminAllowlist(path)
	if err != nil || a.Revision != 7 || !a.Allows(" admin+OPS@gmail.com ", true, "") || a.Allows("admin@gmail.com", true, "") || a.Allows("Admin+ops@gmail.com", false, "") {
		t.Fatalf("policy = %+v, %v", a, err)
	}
	if a.Allows("admin+ops@example.com", true, "") {
		t.Fatal("third party email became authoritative")
	}
	write(`{"revision":8,"emails":[]}`)
	a, err = config.LoadAdminAllowlist(path)
	if err != nil || a.Revision != 8 || a.Allows("Admin+ops@gmail.com", true, "") {
		t.Fatalf("empty list: %+v %v", a, err)
	}
	write(`{"revision":9,"emails":["Admin+ops@gmail.com"],"extra":true}`)
	if _, err = config.LoadAdminAllowlist(path); err == nil {
		t.Fatal("malformed projection accepted")
	}
}
