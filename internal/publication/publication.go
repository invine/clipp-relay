// Package publication reconciles the relay's public addresses from named
// Kubernetes Services and explicit transport overrides.
package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"clipp-relay/internal/relay"
	ma "github.com/multiformats/go-multiaddr"
)

type ServiceRef struct{ Namespace, Name string }
type Transport struct {
	Enabled   bool
	Overrides []ma.Multiaddr
	Service   ServiceRef
}
type Config struct{ TCP, WSS, WebRTC Transport }

type source struct {
	ref             ServiceRef
	protocol        string
	addresses       []ma.Multiaddr
	verified        time.Time
	resourceVersion string
}

type Controller struct {
	discovery     *relay.Discovery
	client        *http.Client
	apiURL, token string
	config        Config
	ready         func(bool)
	now           func() time.Time
	mu            sync.Mutex
	sources       map[string]*source
}

func New(d *relay.Discovery, client *http.Client, apiURL, token string, cfg Config, ready func(bool)) (*Controller, error) {
	if d == nil || ready == nil {
		return nil, errors.New("publication requires discovery and readiness")
	}
	c := &Controller{discovery: d, client: client, apiURL: strings.TrimRight(apiURL, "/"), token: token, config: cfg, ready: ready, now: time.Now, sources: map[string]*source{}}
	for _, t := range []struct {
		name, protocol string
		config         Transport
	}{{"tcp", "TCP", cfg.TCP}, {"webrtc", "UDP", cfg.WebRTC}} {
		if !t.config.Enabled || len(t.config.Overrides) > 0 {
			continue
		}
		if t.config.Service.Name == "" || t.config.Service.Namespace == "" || client == nil || apiURL == "" || token == "" {
			return nil, errors.New("watched transport requires Kubernetes Service and API credentials")
		}
		c.sources[t.name] = &source{ref: t.config.Service, protocol: t.protocol}
	}
	if cfg.WSS.Enabled && len(cfg.WSS.Overrides) == 0 {
		return nil, errors.New("WSS requires configured address")
	}
	return c, nil
}

type serviceResponse struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		Ports []struct {
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
		} `json:"ports"`
	} `json:"spec"`
	Status struct {
		LoadBalancer struct {
			Ingress []struct {
				IP       string `json:"ip"`
				Hostname string `json:"hostname"`
			} `json:"ingress"`
		} `json:"loadBalancer"`
	} `json:"status"`
}

func (c *Controller) endpoint(s *source, watch bool) string {
	path := fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s", c.apiURL, url.PathEscape(s.ref.Namespace), url.PathEscape(s.ref.Name))
	if watch {
		return strings.TrimSuffix(path, "/"+url.PathEscape(s.ref.Name)) + "?watch=1&fieldSelector=" + url.QueryEscape("metadata.name="+s.ref.Name) + "&resourceVersion=" + url.QueryEscape(s.resourceVersion) + "&timeoutSeconds=60"
	}
	return path
}

func (c *Controller) get(ctx context.Context, s *source) ([]ma.Multiaddr, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(s, false), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", fmt.Errorf("Service API status %d", response.StatusCode)
	}
	var service serviceResponse
	dec := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err = dec.Decode(&service); err != nil {
		return nil, "", err
	}
	ports := make([]int, 0, 2)
	for _, p := range service.Spec.Ports {
		if p.Protocol == s.protocol && p.Port >= 1 && p.Port <= 65535 {
			ports = append(ports, p.Port)
		}
	}
	if len(ports) == 0 {
		return nil, service.Metadata.ResourceVersion, nil
	}
	set := map[string]ma.Multiaddr{}
	for _, entry := range service.Status.LoadBalancer.Ingress {
		candidates := make([]struct{ kind, name string }, 0, 2)
		if ip := net.ParseIP(entry.IP); ip != nil {
			if ip.To4() != nil {
				candidates = append(candidates, struct{ kind, name string }{"ip4", ip.To4().String()})
			} else {
				candidates = append(candidates, struct{ kind, name string }{"ip6", ip.String()})
			}
		}
		if entry.Hostname != "" && validDNS(entry.Hostname) {
			candidates = append(candidates, struct{ kind, name string }{"dns4", entry.Hostname})
		}
		proto := "tcp"
		suffix := ""
		if s.protocol == "UDP" {
			proto = "udp"
			suffix = "/webrtc-direct"
		}
		for _, candidate := range candidates {
			for _, port := range ports {
				a, err := ma.NewMultiaddr(fmt.Sprintf("/%s/%s/%s/%d%s", candidate.kind, candidate.name, proto, port, suffix))
				if err == nil {
					set[a.String()] = a
				}
			}
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 16 {
		return nil, "", errors.New("too many observed Service addresses")
	}
	addresses := make([]ma.Multiaddr, 0, len(keys))
	for _, key := range keys {
		addresses = append(addresses, set[key])
	}
	return addresses, service.Metadata.ResourceVersion, nil
}

func validDNS(name string) bool {
	if len(name) == 0 || len(name) > 253 || strings.ToLower(name) != name || strings.HasSuffix(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

// Sync verifies every watched Service. A failed API read keeps the last good
// snapshot until its original five-minute deadline; it cannot extend freshness.
func (c *Controller) Sync(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	for _, name := range []string{"tcp", "webrtc"} {
		s := c.sources[name]
		if s == nil {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		addresses, version, err := c.get(requestCtx, s)
		cancel()
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		s.addresses, s.resourceVersion, s.verified = addresses, version, c.now()
	}
	c.publishLocked()
	return first
}

func (c *Controller) publishLocked() {
	now := c.now()
	addresses := make([]ma.Multiaddr, 0, 48)
	verified := now
	for _, item := range []struct {
		name   string
		config Transport
	}{{"tcp", c.config.TCP}, {"wss", c.config.WSS}, {"webrtc", c.config.WebRTC}} {
		if !item.config.Enabled {
			continue
		}
		part := item.config.Overrides
		if s := c.sources[item.name]; s != nil {
			part = s.addresses
			if s.verified.IsZero() || now.Sub(s.verified) > 5*time.Minute {
				part = nil
			} else if s.verified.Before(verified) {
				verified = s.verified
			}
		}
		if len(part) == 0 {
			_ = c.discovery.Publish(nil)
			c.ready(false)
			return
		}
		addresses = append(addresses, part...)
	}
	if len(addresses) == 0 {
		_ = c.discovery.Publish(nil)
		c.ready(false)
		return
	}
	if err := c.discovery.PublishAt(addresses, verified); err != nil {
		c.ready(false)
		return
	}
	c.ready(c.discovery.Published())
}

func (c *Controller) watch(ctx context.Context, s *source, changed chan<- struct{}) {
	for ctx.Err() == nil {
		c.mu.Lock()
		endpoint := c.endpoint(s, true)
		c.mu.Unlock()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+c.token)
			var response *http.Response
			response, err = c.client.Do(request)
			if err == nil {
				if response.StatusCode == 200 {
					var event json.RawMessage
					err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&event)
					if err == nil {
						select {
						case changed <- struct{}{}:
						default:
						}
					}
				} else {
					err = fmt.Errorf("Service watch status %d", response.StatusCode)
				}
				_ = response.Body.Close()
			}
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// Run watches named Services and also performs a bounded full resync every
// minute so an unchanged Service renews its verification timestamp.
func (c *Controller) Run(ctx context.Context) {
	_ = c.Sync(ctx)
	changed := make(chan struct{}, 1)
	for _, s := range c.sources {
		go c.watch(ctx, s, changed)
	}
	resync := time.NewTicker(time.Minute)
	defer resync.Stop()
	expiry := time.NewTicker(time.Second)
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			_ = c.Sync(ctx)
		case <-resync.C:
			_ = c.Sync(ctx)
		case <-expiry.C:
			c.mu.Lock()
			c.publishLocked()
			c.mu.Unlock()
		}
	}
}
