package service

import (
	"context"
	"embed"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed portal/*
var portal embed.FS

type Service struct {
	live     atomic.Bool
	ready    atomic.Bool
	requests chan struct{}
	scrapes  chan struct{}
	servers  []*http.Server
	once     sync.Once
}

func New() *Service {
	s := &Service{requests: make(chan struct{}, 16), scrapes: make(chan struct{}, 2)}
	s.live.Store(true)
	return s
}

func (s *Service) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, e := portal.ReadFile("portal/index.html")
		if e != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	return mux
}

func (s *Service) PrivateHandler() http.Handler {
	mux := http.NewServeMux()
	health := func(ok bool) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if ok {
				_, _ = w.Write([]byte("ok\n"))
			} else {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("unavailable\n"))
			}
		}
	}
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { health(s.live.Load())(w, r) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) { health(s.ready.Load())(w, r) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.scrapes <- struct{}{}:
			defer func() { <-s.scrapes }()
		case <-r.Context().Done():
			http.Error(w, "unavailable", 503)
			return
		default:
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# HELP clipp_relay_ready Relay readiness.\n# TYPE clipp_relay_ready gauge\nclipp_relay_ready 0\n"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.requests <- struct{}{}:
			defer func() { <-s.requests }()
		default:
			http.Error(w, "unavailable", 503)
			return
		}
		timeout := time.Second
		if r.URL.Path == "/metrics" {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }

// Run binds both HTTP listeners. Ready remains false until a future relay slice
// starts transport listeners and publishes a complete address snapshot.
func (s *Service) Run(ctx context.Context, publicAddr, privateAddr string) error {
	pub, e := net.Listen("tcp", publicAddr)
	if e != nil {
		return errors.New("public listener unavailable")
	}
	priv, e := net.Listen("tcp", privateAddr)
	if e != nil {
		_ = pub.Close()
		return errors.New("private listener unavailable")
	}
	s.servers = []*http.Server{{Handler: s.PublicHandler(), ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second}, {Handler: s.PrivateHandler(), ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second}}
	errCh := make(chan error, 2)
	go func() { errCh <- s.servers[0].Serve(pub) }()
	go func() { errCh <- s.servers[1].Serve(&limitedListener{Listener: priv, slots: make(chan struct{}, 32)}) }()
	select {
	case <-ctx.Done():
		s.ready.Store(false)
		s.live.Store(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, srv := range s.servers {
			_ = srv.Shutdown(shutdownCtx)
		}
		return nil
	case e := <-errCh:
		s.ready.Store(false)
		s.live.Store(false)
		_ = pub.Close()
		_ = priv.Close()
		return e
	}
}
