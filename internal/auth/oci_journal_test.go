package auth_test

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
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"clipp-relay/internal/auth"
)

func TestOCIJournalSignedConditionalObjectsAndBoundedPages(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	type stored struct {
		body []byte
		etag string
	}
	var mu sync.Mutex
	objects := map[string]stored{}
	head := auth.JournalHead{RepositoryID: "qualified", CoverageFloor: 0, CoverageHash: strings.Repeat("0", 64), Sequence: 0, Hash: strings.Repeat("0", 64), Format: 1}
	initial, _ := json.Marshal(head)
	objects["journal/head.json"] = stored{initial, "head-1"}
	var signed bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyOCIRequest(t, r, &key.PublicKey) {
			w.WriteHeader(401)
			return
		}
		signed = true
		if r.URL.Query().Get("prefix") != "" {
			if r.URL.Query().Get("limit") != "250" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("start") == "journal/events/many" {
				many := struct {
					Objects []map[string]string `json:"objects"`
				}{Objects: make([]map[string]string, 251)}
				for i := range many.Objects {
					many.Objects[i] = map[string]string{"name": "journal/events/item"}
				}
				_ = json.NewEncoder(w).Encode(many)
				return
			}
			if r.URL.Query().Get("start") == "" {
				_, _ = w.Write([]byte(`{"objects":[{"name":"journal/events/1-a","size":123,"timeCreated":"2026-09-27T00:00:00Z"}],"nextStartWith":"journal/events/2-b"}`))
			} else {
				_, _ = w.Write([]byte(`{"objects":[{"name":"journal/events/2-b"}]}`))
			}
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/n/fixture/b/events/o/")
		if name == r.URL.Path {
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		current, exists := objects[name]
		switch r.Method {
		case "GET":
			if !exists {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("ETag", current.etag)
			_, _ = w.Write(current.body)
		case "PUT":
			if r.Header.Get("If-None-Match") != "*" && r.Header.Get("If-Match") == "" {
				w.WriteHeader(400)
				return
			}
			if r.Header.Get("If-None-Match") == "*" && exists || r.Header.Get("If-Match") != "" && (!exists || current.etag != r.Header.Get("If-Match")) {
				w.WriteHeader(412)
				return
			}
			body, _ := io.ReadAll(r.Body)
			objects[name] = stored{body, "head-2"}
			w.Header().Set("ETag", "head-2")
			w.WriteHeader(200)
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	client, err := auth.NewOCIJournal(auth.OCIJournalOptions{Endpoint: server.URL, Namespace: "fixture", Bucket: "events", TenancyOCID: "ocid1.tenancy.oc1..test", UserOCID: "ocid1.user.oc1..test", Fingerprint: "aa:bb", PrivateKeyPEM: pemKey, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, etag, err := client.ReadHead(ctx)
	if err != nil || got.RepositoryID != "qualified" || etag != "head-1" {
		t.Fatalf("head %+v %s %v", got, etag, err)
	}
	event := []byte(`{"event":"test"}`)
	if err := client.CreateEvent(ctx, "events/1-a", event); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateEvent(ctx, "events/1-a", event); err == nil {
		t.Fatal("event overwrite accepted")
	}
	read, err := client.ReadEvent(ctx, "events/1-a")
	if err != nil || !bytes.Equal(read, event) {
		t.Fatalf("event read %s %v", read, err)
	}
	next := got
	next.Sequence = 1
	next.Hash = strings.Repeat("a", 64)
	if err := client.ReplaceHead(ctx, "wrong", next); err == nil {
		t.Fatal("wrong etag replaced head")
	}
	if err := client.ReplaceHead(ctx, etag, next); err != nil {
		t.Fatal(err)
	}
	got, etag, err = client.ReadHead(ctx)
	if err != nil || got.Sequence != 1 || etag != "head-2" {
		t.Fatalf("replaced head %+v %s %v", got, etag, err)
	}
	names, cursor, err := client.ListEvents(ctx, "")
	if err != nil || len(names) != 1 || cursor != "journal/events/2-b" {
		t.Fatalf("page 1 %v %q %v", names, cursor, err)
	}
	names, cursor, err = client.ListEvents(ctx, cursor)
	if err != nil || len(names) != 1 || cursor != "" {
		t.Fatalf("page 2 %v %q %v", names, cursor, err)
	}
	if !signed {
		t.Fatal("unsigned requests")
	}
	if err := client.CreateEvent(ctx, "events/too-large", bytes.Repeat([]byte("x"), 4097)); err == nil {
		t.Fatal("oversized event accepted")
	}
	mu.Lock()
	objects["journal/events/oversize"] = stored{bytes.Repeat([]byte("z"), 4097), "oversize"}
	mu.Unlock()
	if _, err := client.ReadEvent(ctx, "events/oversize"); err == nil {
		t.Fatal("oversized event read accepted")
	}
	if _, _, err := client.ListEvents(ctx, "journal/events/many"); err == nil {
		t.Fatal("oversized page accepted")
	}
}

func verifyOCIRequest(t *testing.T, r *http.Request, key *rsa.PublicKey) bool {
	t.Helper()
	authHeader := r.Header.Get("Authorization")
	if !strings.Contains(authHeader, `version="1"`) || !strings.Contains(authHeader, `algorithm="rsa-sha256"`) {
		return false
	}
	pick := func(label string) string {
		start := strings.Index(authHeader, label+`="`)
		if start < 0 {
			return ""
		}
		s := authHeader[start+len(label)+2:]
		end := strings.IndexByte(s, '"')
		if end < 0 {
			return ""
		}
		return s[:end]
	}
	headers := strings.Fields(pick("headers"))
	if len(headers) < 3 || pick("keyId") != "ocid1.tenancy.oc1..test/ocid1.user.oc1..test/aa:bb" {
		return false
	}
	if r.Header.Get("If-Match") != "" && !strings.Contains(" "+strings.Join(headers, " ")+" ", " if-match ") || r.Header.Get("If-None-Match") != "" && !strings.Contains(" "+strings.Join(headers, " ")+" ", " if-none-match ") {
		return false
	}
	lines := make([]string, 0, len(headers))
	for _, name := range headers {
		value := ""
		switch name {
		case "(request-target)":
			value = strings.ToLower(r.Method) + " " + r.URL.RequestURI()
		case "host":
			value = r.Host
		case "content-length":
			value = strconv.FormatInt(r.ContentLength, 10)
		default:
			value = r.Header.Get(name)
		}
		if value == "" {
			return false
		}
		lines = append(lines, name+": "+value)
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	sig, err := base64.StdEncoding.DecodeString(pick("signature"))
	return err == nil && rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) == nil
}
