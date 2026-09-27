package config_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
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
	body := `{"version":1,"portal_origin":"https://example.com/path","wss_hostname":"wss.example.com","listeners":{"public":"127.0.0.1:0","private":"127.0.0.1:0"},"database":{"host":"db.example.com","port":5432,"name":"relay","username_file":"/x","password_file":"/y","ca_file":"/z"},"secrets":{"google_client_id_file":"/a","google_client_secret_file":"/b","admin_allowlist_file":"/c","pepper_keyring_file":"/d"}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "portal_origin") {
		t.Fatalf("load error = %v", err)
	}
}

func TestMissingSecretFailsWithoutPathDisclosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"portal_origin":"https://example.com","wss_hostname":"wss.example.com","listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"database":{"mode":"external","host":"localhost","port":5432,"name":"relay","username_file":"/missing/private-identity","password_file":"/missing/private-password","ca_file":"/missing/ca"},"secrets":{"google_client_id_file":"/missing/google-id","google_client_secret_file":"/missing/google-secret","admin_allowlist_file":"/missing/allowlist","pepper_keyring_file":"/missing/keyring"}}`
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
