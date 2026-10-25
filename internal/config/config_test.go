package config_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/config"
)

func TestLoadRejectsUnknownFieldAndSecretDisclosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"surprise":"sensitive-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadRejectsConflictingDuplicateFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadRejectsCaseInsensitiveAliasKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"Version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadRejectsNoncanonicalOrigin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"portal_origin":"https://example.com/path","wss_hostname":"wss.example.com","listeners":{"public":"127.0.0.1:0","private":"127.0.0.1:0"},"database":{"host":"db.example.com","port":5432,"name":"relay","username_file":"/x","password_file":"/y","ca_file":"/z"},"secrets":{"google_client_id_file":"/a","google_client_secret_file":"/b","admin_allowlist_file":"/c","pepper_keyring_file":"/d","native_signing_key_file":"/e"}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "portal_origin") {
		t.Fatalf("load error = %v", err)
	}
}

func TestRelayTCPConfigurationRejectsIncompleteAddresses(t *testing.T) {
	base := `{"version":1,"portal_origin":"https://example.com","wss_hostname":"wss.example.com","public_clients":{"android_redirect":"clipp-relay://oauth/callback","extension_redirect":"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"},"listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"database":{"mode":"external","host":"localhost","port":5432,"name":"relay","username_file":"/x","password_file":"/y","ca_file":"/z"},"secrets":{"google_client_id_file":"/a","google_client_secret_file":"/b","admin_allowlist_file":"/c","pepper_keyring_file":"/d","native_signing_key_file":"/e"}}`
	for _, entry := range []string{
		`"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/18082","public_addresses":["/ip4/127.0.0.1"]}`,
		`"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/0"}`,
		`"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/18082","public_addresses":["/ip4/127.0.0.1/tcp/18082/udp/12"]}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		body := strings.Replace(base, `"database":`, entry+`,"database":`, 1)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Fatalf("accepted invalid relay TCP config: %s", entry)
		}
	}
}

func TestJournalConfigurationRequiresExplicitIdentityAndCoverage(t *testing.T) {
	base := `{"version":1,"portal_origin":"https://example.com","wss_hostname":"wss.example.com","public_clients":{"android_redirect":"clipp-relay://oauth/callback","extension_redirect":"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"},"listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"database":{"mode":"external","host":"localhost","port":5432,"name":"relay","username_file":"/x","password_file":"/y","ca_file":"/z"},"secrets":{"google_client_id_file":"/a","google_client_secret_file":"/b","admin_allowlist_file":"/c","pepper_keyring_file":"/d","native_signing_key_file":"/e"}}`
	for _, journal := range []string{
		`{"region":"us-ashburn-1"}`,
		`{"region":"us-ashburn-1","namespace":"ns","bucket":"bucket","repository_id":"repo","coverage_floor":0,"coverage_hash":"wrong","tenancy_ocid":"ocid1.tenancy.oc1..test","user_ocid":"ocid1.user.oc1..test","fingerprint":"aa:bb","private_key_file":"/secret"}`,
		`{"region":"evil.example.com","namespace":"ns","bucket":"bucket","repository_id":"repo","coverage_floor":0,"coverage_hash":"` + strings.Repeat("0", 64) + `","tenancy_ocid":"ocid1.tenancy.oc1..test","user_ocid":"ocid1.user.oc1..test","fingerprint":"aa:bb","private_key_file":"/secret"}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		body := strings.Replace(base, `"database":`, `"journal":`+journal+`,"database":`, 1)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Fatalf("accepted journal config %s", journal)
		}
	}
}

func TestMissingSecretFailsWithoutPathDisclosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"portal_origin":"https://example.com","wss_hostname":"wss.example.com","public_clients":{"android_redirect":"clipp-relay://oauth/callback","extension_redirect":"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"},"listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"database":{"mode":"external","host":"localhost","port":5432,"name":"relay","username_file":"/missing/private-identity","password_file":"/missing/private-password","ca_file":"/missing/ca"},"secrets":{"google_client_id_file":"/missing/google-id","google_client_secret_file":"/missing/google-secret","admin_allowlist_file":"/missing/allowlist","pepper_keyring_file":"/missing/keyring","native_signing_key_file":"/missing/signing"}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ReadMaterial()
	if err == nil || strings.Contains(err.Error(), "private-identity") {
		t.Fatalf("material error = %v", err)
	}
}

func TestNativeSigningKeyRequiresExactly32SecretBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oauth-signing-key")
	for _, tc := range []struct {
		name  string
		value []byte
		valid bool
	}{
		{name: "exact", value: make([]byte, 32), valid: true},
		{name: "short", value: make([]byte, 31)},
		{name: "long", value: make([]byte, 33)},
		{name: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(path)
			if tc.value != nil {
				if err := os.WriteFile(path, tc.value, 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := config.ReadNativeSigningKey(path)
			if (err == nil) != tc.valid || tc.valid && len(got) != 32 {
				t.Fatalf("key length=%d err=%v", len(got), err)
			}
		})
	}
}

func TestResetMaterialNeedsOnlyDatabaseAndSigningSecrets(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	keyPath := filepath.Join(dir, "native-key")
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := x509.Certificate{SerialNumber: big.NewInt(7), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	var c config.Config
	c.Database.UsernameFile = filepath.Join(dir, "user")
	c.Database.PasswordFile = filepath.Join(dir, "pass")
	c.Database.CAFile = caPath
	c.Secrets.NativeSigningKeyFile = keyPath
	if err := os.WriteFile(c.Database.UsernameFile, []byte("migration"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Database.PasswordFile, []byte("password"), 0600); err != nil {
		t.Fatal(err)
	}
	// The reset job does not need Google, allowlist, portal pepper or journal
	// Secret contents merely to compare key identity and write reset evidence.
	c.Secrets.GoogleClientIDFile = filepath.Join(dir, "absent-google-id")
	c.Secrets.GoogleClientSecretFile = filepath.Join(dir, "absent-google-secret")
	c.Secrets.PepperKeyringFile = filepath.Join(dir, "absent-keyring")
	c.Secrets.AdminAllowlistFile = filepath.Join(dir, "absent-allowlist")
	m, err := c.ReadResetMaterial()
	if err != nil || m.DBUsername != "migration" || m.DBPassword != "password" || len(m.NativeSigningKey) != 32 {
		t.Fatalf("reset material=%+v err=%v", m, err)
	}
}

func TestMigrationReadsOnlyDatabaseSecretFiles(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	cert, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"user": []byte("migration\n"), "pass": []byte("test-password\n"), "ca": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var c config.Config
	c.Database.UsernameFile = filepath.Join(dir, "user")
	c.Database.PasswordFile = filepath.Join(dir, "pass")
	c.Database.CAFile = filepath.Join(dir, "ca")
	c.Secrets.GoogleClientIDFile = filepath.Join(dir, "absent")
	m, err := c.ReadDatabaseMaterial()
	if err != nil || m.DBUsername != "migration" || m.DBPassword != "test-password" {
		t.Fatalf("database material: %v", err)
	}
	if _, err = c.ReadMaterial(); err == nil {
		t.Fatal("serving accepted missing application Secret")
	}
}

func TestAccountingDeadlineConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		valid bool
	}{
		{"", 3 * time.Second, true}, {`"7s"`, 7 * time.Second, true}, {`"1500ms"`, 1500 * time.Millisecond, true},
		{`""`, 0, false}, {`null`, 0, false}, {`"0s"`, 0, false}, {`"999ms"`, 0, false},
		{`"-1s"`, 0, false}, {`"NaN"`, 0, false}, {`"Inf"`, 0, false}, {`"999999999999999s"`, 0, false}, {`3`, 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			c := validTransportConfig()
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			delete(raw, "accounting_failure_deadline")
			if tc.value != "" {
				raw["accounting_failure_deadline"] = json.RawMessage(tc.value)
			}
			data, _ = json.Marshal(raw)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := config.Load(path)
			if (err == nil) != tc.valid {
				t.Fatalf("Load error=%v valid=%v", err, tc.valid)
			}
			if err == nil && got.AccountingDeadline() != tc.want {
				t.Fatalf("deadline=%v want=%v", got.AccountingDeadline(), tc.want)
			}
		})
	}
}
