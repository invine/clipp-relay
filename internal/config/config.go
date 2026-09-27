package config

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Version       int    `json:"version"`
	PortalOrigin  string `json:"portal_origin"`
	WSSHostname   string `json:"wss_hostname"`
	PublicClients struct {
		AndroidRedirect   string `json:"android_redirect"`
		ExtensionRedirect string `json:"extension_redirect"`
	} `json:"public_clients"`
	Listeners struct {
		Public  string `json:"public"`
		Private string `json:"private"`
	} `json:"listeners"`
	Database struct {
		Mode         string `json:"mode"`
		Host         string `json:"host"`
		Port         uint16 `json:"port"`
		Name         string `json:"name"`
		UsernameFile string `json:"username_file"`
		PasswordFile string `json:"password_file"`
		CAFile       string `json:"ca_file"`
	} `json:"database"`
	Secrets struct {
		GoogleClientIDFile     string `json:"google_client_id_file"`
		GoogleClientSecretFile string `json:"google_client_secret_file"`
		AdminAllowlistFile     string `json:"admin_allowlist_file"`
		PepperKeyringFile      string `json:"pepper_keyring_file"`
	} `json:"secrets"`
}

type Material struct {
	DBUsername, DBPassword string
	DBRootCAs              *x509.CertPool
	GoogleClientID         string
	GoogleClientSecret     string
	CurrentPepper          uint64
	Peppers                map[uint64][]byte
}

// AdminAllowlist is read from the mounted Secret on each administrative request.
// A failed read must never leave an earlier grant in effect.
type AdminAllowlist struct {
	Revision uint64   `json:"revision"`
	Emails   []string `json:"emails"`
}

func foldEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 320 || strings.Count(s, "@") != 1 {
		return "", false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return "", false
		}
	}
	return strings.ToLower(s), true
}

func LoadAdminAllowlist(path string) (AdminAllowlist, error) {
	var a AdminAllowlist
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return a, errors.New("administrator allowlist unavailable")
	}
	if strictJSON(b, &a) != nil || a.Revision == 0 || a.Emails == nil || len(a.Emails) > 1024 {
		return AdminAllowlist{}, errors.New("invalid administrator allowlist")
	}
	seen := map[string]bool{}
	for i, email := range a.Emails {
		folded, ok := foldEmail(email)
		if !ok || seen[folded] {
			return AdminAllowlist{}, errors.New("invalid administrator allowlist")
		}
		seen[folded] = true
		a.Emails[i] = folded
	}
	return a, nil
}

func (a AdminAllowlist) Allows(email string, verified bool, hostedDomain string) bool {
	if !verified {
		return false
	}
	folded, ok := foldEmail(email)
	if !ok {
		return false
	}
	domain := strings.SplitN(folded, "@", 2)[1]
	if domain != "gmail.com" && domain != "googlemail.com" && hostedDomain == "" {
		return false
	}
	for _, allowed := range a.Emails {
		if folded == allowed {
			return true
		}
	}
	return false
}

func strictJSON(data []byte, target any) error {
	if err := uniqueFields(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return errors.New("invalid or duplicate configuration field")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return errors.New("invalid or unknown configuration field")
	}
	if errors.Is(dec.Decode(new(any)), io.EOF) {
		return nil
	}
	return errors.New("multiple JSON values")
}

func uniqueFields(dec *json.Decoder) error {
	var visit func() error
	visit = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make([]string, 0)
			for dec.More() {
				keyToken, e := dec.Token()
				if e != nil {
					return e
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("duplicate field")
				}
				for _, prior := range seen {
					if strings.EqualFold(prior, key) {
						return errors.New("duplicate field")
					}
				}
				seen = append(seen, key)
				if e = visit(); e != nil {
					return e
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if e := visit(); e != nil {
					return e
				}
			}
			_, err = dec.Token()
			return err
		default:
			return errors.New("invalid JSON")
		}
	}
	if err := visit(); err != nil {
		return err
	}
	if _, err := dec.Token(); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("multiple JSON values")
}

func Load(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, errors.New("configuration file unreadable")
	}
	if err := strictJSON(data, &c); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return errors.New("unsupported config version")
	}
	u, err := url.Parse(c.PortalOrigin)
	if err != nil || u.Scheme != "https" || !hostname(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || u.Opaque != "" || u.Port() == "443" || u.String() != c.PortalOrigin {
		return errors.New("portal_origin must be a canonical HTTPS origin")
	}
	if !hostname(c.WSSHostname) || strings.EqualFold(c.WSSHostname, u.Hostname()) {
		return errors.New("wss_hostname must be a distinct exact hostname")
	}
	android, err := url.Parse(c.PublicClients.AndroidRedirect)
	if err != nil || android.Scheme == "" || android.Scheme == "http" || android.Scheme == "https" || android.Host == "" || android.User != nil || android.RawQuery != "" || android.Fragment != "" || android.RawFragment != "" || android.Opaque != "" || android.String() != c.PublicClients.AndroidRedirect {
		return errors.New("android_redirect must be a fixed private application URI")
	}
	extension, err := url.Parse(c.PublicClients.ExtensionRedirect)
	if err != nil || extension.Scheme != "https" || extension.User != nil || extension.Port() != "" || extension.Path != "/clipp-relay" || extension.RawPath != "" || extension.RawQuery != "" || extension.Fragment != "" || extension.String() != c.PublicClients.ExtensionRedirect || !strings.HasSuffix(extension.Hostname(), ".chromiumapp.org") || len(strings.TrimSuffix(extension.Hostname(), ".chromiumapp.org")) != 32 {
		return errors.New("extension_redirect must be an exact chromiumapp callback")
	}
	for _, ch := range strings.TrimSuffix(extension.Hostname(), ".chromiumapp.org") {
		if ch < 'a' || ch > 'p' {
			return errors.New("extension_redirect must contain a Chrome extension ID")
		}
	}
	pub, err := listenerPort(c.Listeners.Public)
	if err != nil {
		return errors.New("invalid public listener")
	}
	priv, err := listenerPort(c.Listeners.Private)
	if err != nil || pub == priv {
		return errors.New("invalid or conflicting private listener")
	}
	if (c.Database.Mode != "external" && c.Database.Mode != "bundled") || !hostname(c.Database.Host) || c.Database.Port == 0 || c.Database.Name == "" || strings.ContainsAny(c.Database.Name, " /\t\n") {
		return errors.New("invalid database target")
	}
	for _, path := range []string{c.Database.UsernameFile, c.Database.PasswordFile, c.Database.CAFile, c.Secrets.GoogleClientIDFile, c.Secrets.GoogleClientSecretFile, c.Secrets.AdminAllowlistFile, c.Secrets.PepperKeyringFile} {
		if path == "" || !strings.HasPrefix(path, "/") {
			return errors.New("missing or invalid Secret-file reference")
		}
	}
	return nil
}

func listenerPort(addr string) (int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	if host != "" && net.ParseIP(host) == nil {
		return 0, errors.New("listener must bind an IP address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1024 || port > 65535 {
		return 0, errors.New("listener port must be unprivileged")
	}
	return port, nil
}

func hostname(s string) bool {
	if s == "" || len(s) > 253 || strings.HasSuffix(s, ".") || s != strings.ToLower(s) || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func readSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, errors.New("required Secret material unreadable or empty")
	}
	return b, nil
}

func (c Config) ReadDatabaseMaterial() (Material, error) {
	var m Material
	username, err := readSecret(c.Database.UsernameFile)
	if err != nil {
		return m, err
	}
	password, err := readSecret(c.Database.PasswordFile)
	if err != nil {
		return m, err
	}
	ca, err := readSecret(c.Database.CAFile)
	if err != nil {
		return m, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return m, errors.New("database CA file has no valid certificate")
	}
	m.DBUsername = strings.TrimSuffix(string(username), "\n")
	m.DBPassword = strings.TrimSuffix(string(password), "\n")
	if m.DBUsername == "" || m.DBPassword == "" {
		return Material{}, errors.New("database credentials empty")
	}
	m.DBRootCAs = roots
	return m, nil
}

func (c Config) ReadMaterial() (Material, error) {
	m, err := c.ReadDatabaseMaterial()
	if err != nil {
		return m, err
	}
	for i, path := range []string{c.Secrets.GoogleClientIDFile, c.Secrets.GoogleClientSecretFile} {
		value, e := readSecret(path)
		if e != nil {
			return m, e
		}
		if i == 0 {
			m.GoogleClientID = strings.TrimSuffix(string(value), "\n")
		} else {
			m.GoogleClientSecret = strings.TrimSuffix(string(value), "\n")
		}
	}
	if m.GoogleClientID == "" || m.GoogleClientSecret == "" {
		return m, errors.New("Google client material empty")
	}
	if _, err := LoadAdminAllowlist(c.Secrets.AdminAllowlistFile); err != nil {
		return m, err
	}
	keyring, err := readSecret(c.Secrets.PepperKeyringFile)
	if err != nil {
		return m, err
	}
	var ring struct {
		Current uint64 `json:"current"`
		Keys    []struct {
			Version  uint64 `json:"version"`
			Material string `json:"material"`
		} `json:"keys"`
	}
	if strictJSON(keyring, &ring) != nil || ring.Current == 0 || len(ring.Keys) == 0 {
		return m, errors.New("invalid pepper keyring")
	}
	seen := map[uint64]bool{}
	current := false
	m.Peppers = map[uint64][]byte{}
	for _, key := range ring.Keys {
		b, e := base64.StdEncoding.DecodeString(key.Material)
		if e != nil || len(b) < 32 || key.Version == 0 || seen[key.Version] {
			return m, errors.New("invalid pepper keyring")
		}
		seen[key.Version] = true
		m.Peppers[key.Version] = b
		current = current || key.Version == ring.Current
	}
	if !current {
		return m, errors.New("invalid pepper keyring")
	}
	m.CurrentPepper = ring.Current
	return m, nil
}
