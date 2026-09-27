package publication

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type tokenTransport struct {
	base http.RoundTripper
	path string
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	token, err := os.ReadFile(t.path)
	if err != nil || len(token) == 0 || len(token) > 16<<10 || strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("ServiceAccount token unavailable")
	}
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	return t.base.RoundTrip(clone)
}

// KubernetesAPI uses the mounted, namespace-scoped ServiceAccount identity.
// The API address comes only from the injected Kubernetes service environment.
func KubernetesAPI(tokenFile, caFile string) (*http.Client, string, string, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if port == "" {
		port = os.Getenv("KUBERNETES_SERVICE_PORT")
	}
	if host == "" || port == "" {
		return nil, "", "", errors.New("Kubernetes API service unavailable")
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil || len(token) == 0 || len(token) > 16<<10 {
		return nil, "", "", errors.New("ServiceAccount token unavailable")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil || len(ca) == 0 || len(ca) > 1<<20 {
		return nil, "", "", errors.New("Kubernetes API CA unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, "", "", errors.New("Kubernetes API CA invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 65 * time.Second}
	if strings.TrimSpace(string(token)) == "" {
		return nil, "", "", errors.New("ServiceAccount token unavailable")
	}
	return &http.Client{Transport: tokenTransport{base: transport, path: tokenFile}, Timeout: 65 * time.Second}, "https://" + net.JoinHostPort(host, port), "service-account", nil
}
