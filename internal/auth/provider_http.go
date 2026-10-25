package auth

import (
	"errors"
	"io"
	"net/http"
	"time"
)

const providerBodyLimit = 256 << 10

// providerTransport bounds the complete lifetime of each token and JWKS HTTP
// operation. The slot remains held until the response body is exhausted or
// closed, including when a RemoteKeySet waiter cancels while its shared fetch
// is still running.
type providerTransport struct {
	base  http.RoundTripper
	slots chan struct{}
}

func newProviderHTTPClient(source *http.Client, slots chan struct{}) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if source != nil && source.Transport != nil {
		base = source.Transport
	}
	if transport, ok := base.(*http.Transport); ok {
		bounded := transport.Clone()
		bounded.MaxResponseHeaderBytes = 16 << 10
		base = bounded
	}
	return &http.Client{
		Transport: providerTransport{base: base, slots: slots},
		Timeout:   8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (t providerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case t.slots <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		<-t.slots
		return nil, err
	}
	if headerBytes(resp.Header) > 16<<10 {
		_ = resp.Body.Close()
		<-t.slots
		return nil, errors.New("provider response headers exceed limit")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		<-t.slots
		return nil, errors.New("provider returned unexpected status")
	}
	resp.Body = &boundedProviderBody{body: resp.Body, release: func() { <-t.slots }}
	return resp, nil
}

func headerBytes(headers http.Header) int {
	total := 0
	for key, values := range headers {
		for _, value := range values {
			total += len(key) + len(value) + 4
		}
	}
	return total
}

type boundedProviderBody struct {
	body    io.ReadCloser
	read    int64
	closed  bool
	release func()
}

func (b *boundedProviderBody) Read(p []byte) (int, error) {
	remaining := int64(providerBodyLimit+1) - b.read
	if remaining <= 0 {
		return 0, errors.New("provider response body exceeds limit")
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.body.Read(p)
	b.read += int64(n)
	if b.read > providerBodyLimit {
		return n, errors.New("provider response body exceeds limit")
	}
	if err == io.EOF {
		b.finish()
	}
	return n, err
}

func (b *boundedProviderBody) Close() error {
	err := b.body.Close()
	b.finish()
	return err
}

func (b *boundedProviderBody) finish() {
	if b.closed {
		return
	}
	b.closed = true
	b.release()
}
