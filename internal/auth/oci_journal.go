package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OCIJournalOptions names one operator-provided, independent Object Storage
// bucket and one native OCI API-signing identity. Production configuration
// constructs Endpoint from an explicit OCI region.
type OCIJournalOptions struct {
	Endpoint, Namespace, Bucket        string
	TenancyOCID, UserOCID, Fingerprint string
	PrivateKeyPEM                      []byte
	HTTPClient                         *http.Client
}

type OCIJournal struct {
	endpoint                 *url.URL
	namespace, bucket, keyID string
	key                      *rsa.PrivateKey
	client                   *http.Client
}

// NewOCIJournal validates the transport and signing material but neither
// creates nor initializes a repository. ReadHead must find an existing head.
func NewOCIJournal(o OCIJournalOptions) (*OCIJournal, error) {
	endpoint, err := url.Parse(o.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" || endpoint.Opaque != "" {
		return nil, errors.New("invalid OCI Object Storage endpoint")
	}
	if !safeOCIName(o.Namespace) || !safeOCIName(o.Bucket) || !strings.HasPrefix(o.TenancyOCID, "ocid1.tenancy.") || !strings.HasPrefix(o.UserOCID, "ocid1.user.") || o.Fingerprint == "" || strings.ContainsAny(o.Fingerprint, " /\r\n\t\"") {
		return nil, errors.New("invalid OCI journal identity")
	}
	block, _ := pem.Decode(o.PrivateKeyPEM)
	if block == nil {
		return nil, errors.New("invalid OCI signing key")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	default:
		return nil, errors.New("invalid OCI signing key")
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 {
		return nil, errors.New("invalid OCI signing key")
	}
	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	copyClient := *client
	if copyClient.Timeout == 0 || copyClient.Timeout > 5*time.Second {
		copyClient.Timeout = 5 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OCIJournal{endpoint: endpoint, namespace: o.Namespace, bucket: o.Bucket, keyID: o.TenancyOCID + "/" + o.UserOCID + "/" + o.Fingerprint, key: key, client: &copyClient}, nil
}

func safeOCIName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
			return false
		}
	}
	return true
}

func (j *OCIJournal) objectsPath() string {
	return "/n/" + url.PathEscape(j.namespace) + "/b/" + url.PathEscape(j.bucket) + "/o"
}
func (j *OCIJournal) objectURL(name string) string {
	return j.endpoint.String() + j.objectsPath() + "/" + url.PathEscape(name)
}

func (j *OCIJournal) request(ctx context.Context, method, urlString string, body []byte, conditionName, conditionValue string, maxBody int) ([]byte, string, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlString, reader)
	if err != nil {
		return nil, "", errors.New("OCI journal request unavailable")
	}
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	names := []string{"(request-target)", "host", "date"}
	lines := []string{"(request-target): " + strings.ToLower(method) + " " + req.URL.RequestURI(), "host: " + req.URL.Host, "date: " + req.Header.Get("Date")}
	if conditionName != "" {
		req.Header.Set(conditionName, conditionValue)
		lowered := strings.ToLower(conditionName)
		names = append(names, lowered)
		lines = append(lines, lowered+": "+conditionValue)
	}
	if method == "PUT" {
		digest := sha256.Sum256(body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Content-Sha256", base64.StdEncoding.EncodeToString(digest[:]))
		req.ContentLength = int64(len(body))
		names = append(names, "content-length", "content-type", "x-content-sha256")
		lines = append(lines, fmt.Sprintf("content-length: %d", len(body)), "content-type: application/json", "x-content-sha256: "+req.Header.Get("X-Content-Sha256"))
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, j.key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, "", errors.New("OCI journal signing failed")
	}
	req.Header.Set("Authorization", `Signature version="1",keyId="`+j.keyID+`",algorithm="rsa-sha256",headers="`+strings.Join(names, " ")+`",signature="`+base64.StdEncoding.EncodeToString(signature)+`"`)
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, "", errors.New("OCI journal transport failed")
	}
	defer resp.Body.Close()
	if method == "GET" && resp.StatusCode != http.StatusOK || method == "PUT" && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return nil, "", errors.New("OCI journal operation rejected")
	}
	if maxBody == 0 {
		return nil, resp.Header.Get("ETag"), nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBody)+1))
	if err != nil || len(data) > maxBody {
		return nil, "", errors.New("OCI journal response exceeded bound")
	}
	return data, resp.Header.Get("ETag"), nil
}

func (j *OCIJournal) ReadHead(ctx context.Context) (JournalHead, string, error) {
	data, etag, err := j.request(ctx, "GET", j.objectURL("journal/head.json"), nil, "", "", 16<<10)
	if err != nil || etag == "" || len(etag) > 256 {
		return JournalHead{}, "", errors.New("OCI journal head unavailable")
	}
	var head JournalHead
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&head) != nil || decoder.Decode(new(any)) != io.EOF {
		return JournalHead{}, "", errors.New("OCI journal head invalid")
	}
	return head, etag, nil
}

func (j *OCIJournal) CreateEvent(ctx context.Context, key string, data []byte) error {
	if !validEventKey(key) || len(data) == 0 || len(data) > 4<<10 {
		return errors.New("invalid OCI journal event")
	}
	_, _, err := j.request(ctx, "PUT", j.objectURL("journal/"+key), data, "If-None-Match", "*", 0)
	return err
}
func (j *OCIJournal) ReadEvent(ctx context.Context, key string) ([]byte, error) {
	if !validEventKey(key) {
		return nil, errors.New("invalid OCI journal event")
	}
	data, _, err := j.request(ctx, "GET", j.objectURL("journal/"+key), nil, "", "", 4<<10)
	return data, err
}
func validEventKey(key string) bool {
	if !strings.HasPrefix(key, "events/") || len(key) <= len("events/") || len(key) > 128 {
		return false
	}
	for _, ch := range strings.TrimPrefix(key, "events/") {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
			return false
		}
	}
	return true
}
func (j *OCIJournal) ReplaceHead(ctx context.Context, etag string, head JournalHead) error {
	if etag == "" || len(etag) > 256 || strings.ContainsAny(etag, "\r\n") {
		return errors.New("invalid OCI journal head condition")
	}
	data, err := json.Marshal(head)
	if err != nil || len(data) > 16<<10 {
		return errors.New("invalid OCI journal head")
	}
	_, _, err = j.request(ctx, "PUT", j.objectURL("journal/head.json"), data, "If-Match", etag, 0)
	return err
}

// ListEvents streams one bounded OCI ListObjects page. The returned cursor is
// the provider's nextStartWith, not an inferred offset or a completeness proof.
func (j *OCIJournal) ListEvents(ctx context.Context, cursor string) ([]string, string, error) {
	if cursor != "" && (!strings.HasPrefix(cursor, "journal/events/") || len(cursor) > 256) {
		return nil, "", errors.New("invalid OCI journal cursor")
	}
	query := url.Values{"prefix": {"journal/events/"}, "limit": {"250"}}
	if cursor != "" {
		query.Set("start", cursor)
	}
	target := j.endpoint.String() + j.objectsPath() + "?" + query.Encode()
	data, _, err := j.request(ctx, "GET", target, nil, "", "", 256<<10)
	if err != nil {
		return nil, "", err
	}
	var page struct {
		Objects []struct {
			Name string `json:"name"`
		} `json:"objects"`
		NextStartWith string   `json:"nextStartWith"`
		Prefixes      []string `json:"prefixes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&page) != nil || decoder.Decode(new(any)) != io.EOF || len(page.Objects) > 250 || len(page.Prefixes) > 0 {
		return nil, "", errors.New("invalid OCI journal page")
	}
	names := make([]string, 0, len(page.Objects))
	for _, object := range page.Objects {
		if !strings.HasPrefix(object.Name, "journal/events/") || len(object.Name) > 256 {
			return nil, "", errors.New("invalid OCI journal page")
		}
		names = append(names, object.Name)
	}
	if page.NextStartWith != "" && (!strings.HasPrefix(page.NextStartWith, "journal/events/") || page.NextStartWith <= cursor || len(page.NextStartWith) > 256) {
		return nil, "", errors.New("invalid OCI journal cursor")
	}
	return names, page.NextStartWith, nil
}

var _ DeletionJournal = (*OCIJournal)(nil)
