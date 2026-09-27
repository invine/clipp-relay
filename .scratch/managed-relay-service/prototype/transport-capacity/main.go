// PROTOTYPE: loopback transport measurements, not the account-governed service.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	rm "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	pb "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
	relayproto "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
	relay "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	util "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/util"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	webrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
	ws "github.com/libp2p/go-libp2p/p2p/transport/websocket"
	"github.com/libp2p/go-libp2p/x/rate"
	ma "github.com/multiformats/go-multiaddr"
)

const control = protocol.ID("/clipp/prototype-capacity/1.0.0")
const MiB = 1 << 20

type command struct {
	Op string
	N  int
}
type greeting struct {
	Addresses map[string]string
	Cert      string
}
type snapshot struct {
	Connections  int
	Streams      int
	Accounted    int64
	Heap         uint64
	GoManaged    uint64
	RSSKiB       int64
	PeakRSSBytes int64
	CPUSeconds   float64
	Goroutines   int
	Blocked      map[string]int
}
type reply struct {
	OK     int
	Failed int
	Error  string
	Sample snapshot
	Work   workView
}
type trace struct {
	sync.Mutex
	blocked map[string]int
}

func (t *trace) ConsumeEvent(e rm.TraceEvt) {
	if strings.Contains(strings.ToLower(string(e.Type)), "block") {
		// No peer IDs: aggregate failure types and system/transient scope only.
		scope := "other"
		if e.Name == "system" || e.Name == "transient" {
			scope = e.Name
		}
		t.Lock()
		t.blocked[string(e.Type)+":"+scope]++
		t.Unlock()
	}
}
func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func send(v any) { die(json.NewEncoder(os.Stdout).Encode(v)) }
func limits(c, ci, co, s, si, so, fd int, memory int64) rm.ResourceLimits {
	return (rm.BaseLimit{Conns: c, ConnsInbound: ci, ConnsOutbound: co, Streams: s,
		StreamsInbound: si, StreamsOutbound: so, FD: fd, Memory: memory}).ToResourceLimits()
}
func manager(t *trace) (network.ResourceManager, error) {
	// Diagnostic profile: explicit aggregate/per-peer/connection budgets. Services
	// share the system ceiling; NOT the proposed final production scope matrix.
	system := limits(8192, 8192, 512, 32768, 32768, 16384, 8192, 512*MiB)
	scoped := limits(0, 0, 0, 32768, 32768, 16384, 0, 512*MiB)
	p := rm.PartialLimitConfig{
		System: system, Transient: limits(256, 256, 256, 1024, 1024, 512, 256, 512*MiB),
		PeerDefault: limits(4, 4, 4, 64, 64, 32, 4, 32*MiB),
		Conn:        limits(1, 1, 1, 0, 0, 0, 1, 8*MiB), Stream: limits(0, 0, 0, 1, 1, 1, 0, MiB),
		ServiceDefault: scoped, ProtocolDefault: scoped,
		ServicePeerDefault:  limits(0, 0, 0, 64, 64, 32, 0, 32*MiB),
		ProtocolPeerDefault: limits(0, 0, 0, 64, 64, 32, 0, 32*MiB),
		AllowlistedSystem:   limits(0, 0, 0, 0, 0, 0, 0, 0), AllowlistedTransient: limits(0, 0, 0, 0, 0, 0, 0, 0),
	}
	base := (&rm.ScalingLimitConfig{}).Scale(0, 0)
	return rm.NewResourceManager(rm.NewFixedLimiter(p.Build(base)),
		rm.WithLimitPerSubnet([]rm.ConnLimitPerSubnet{}, []rm.ConnLimitPerSubnet{}),
		rm.WithNetworkPrefixLimit([]rm.NetworkPrefixLimit{}, []rm.NetworkPrefixLimit{}),
		rm.WithConnRateLimiters(&rate.Limiter{}), rm.WithTraceReporter(t))
}
func cert() (tls.Certificate, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	die(err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "loopback prototype"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	die(err)
	leaf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv, err := x509.MarshalECPrivateKey(key)
	die(err)
	c, err := tls.X509KeyPair(leaf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: priv}))
	die(err)
	return c, string(leaf)
}
func options() []libp2p.Option {
	return []libp2p.Option{libp2p.NoTransports, libp2p.DisableRelay(), libp2p.DisableMetrics(),
		libp2p.DisableIdentifyAddressDiscovery(), libp2p.ForceReachabilityPrivate(),
		libp2p.ConnectionManager(&connmgr.NullConnMgr{}), libp2p.Security(noise.ID, noise.New),
		libp2p.Muxer(yamux.ID, yamux.DefaultTransport)}
}
func sample(h host.Host, r network.ResourceManager, t *trace) snapshot {
	// No forced GC: these are ordinary live process samples, not best-case heaps.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var u syscall.Rusage
	die(syscall.Getrusage(syscall.RUSAGE_SELF, &u))
	peak := u.Maxrss
	if runtime.GOOS == "linux" {
		peak *= 1024
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	die(err)
	rss, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	die(err)
	s := snapshot{Heap: m.HeapAlloc, GoManaged: m.Sys - m.HeapReleased, RSSKiB: rss, PeakRSSBytes: peak,
		CPUSeconds: float64(u.Utime.Sec+u.Stime.Sec) + float64(u.Utime.Usec+u.Stime.Usec)/1e6,
		Goroutines: runtime.NumGoroutine(), Blocked: map[string]int{}}
	if h != nil {
		s.Connections = len(h.Network().Conns())
		for _, c := range h.Network().Conns() {
			s.Streams += len(c.GetStreams())
		}
	}
	if r != nil {
		die(r.ViewSystem(func(v network.ResourceScope) error { s.Accounted = v.Stat().Memory; return nil }))
	}
	if t != nil {
		t.Lock()
		for k, v := range t.blocked {
			s.Blocked[k] = v
		}
		t.Unlock()
	}
	return s
}
func echo(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(30 * time.Second))
	io.Copy(s, s)
}
func relayMode() {
	debug.SetMemoryLimit(1536 * MiB)
	tr := &trace{blocked: map[string]int{}}
	r, err := manager(tr)
	die(err)
	crt, pem := cert()
	o := options()
	o = append(o, libp2p.ResourceManager(r),
		libp2p.Transport(tcp.NewTCPTransport), libp2p.Transport(webrtc.New),
		libp2p.Transport(ws.New, ws.WithTLSConfig(&tls.Config{Certificates: []tls.Certificate{crt}, MinVersion: tls.VersionTLS12})),
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/tcp/0/tls/ws", "/ip4/127.0.0.1/udp/0/webrtc-direct"))
	h, err := libp2p.New(o...)
	die(err)
	defer h.Close()
	rc := relay.DefaultResources()
	rc.MaxReservations = 6000
	rc.MaxReservationsPerIP = 6000
	rc.MaxReservationsPerASN = 6000
	rc.ReservationTTL = 30 * time.Minute
	rc.Limit = &relay.RelayLimit{Duration: 120 * time.Second, Data: 2 * MiB}
	service, err := relay.New(h, relay.WithResources(rc), relay.WithReservationAddressFilter(func(a ma.Multiaddr) bool { return true }))
	die(err)
	defer service.Close()
	h.SetStreamHandler(control, echo)
	g := greeting{Addresses: map[string]string{}, Cert: pem}
	for _, a := range h.Addrs() {
		kind := "tcp"
		if strings.Contains(a.String(), "/ws") {
			kind = "wss"
		}
		if strings.Contains(a.String(), "/webrtc-direct") {
			kind = "webrtc"
		}
		g.Addresses[kind] = a.String() + "/p2p/" + h.ID().String()
	}
	send(g)
	dec := json.NewDecoder(os.Stdin)
	for {
		var c command
		if dec.Decode(&c) != nil {
			return
		}
		if c.Op == "exit" {
			return
		}
		send(sample(h, r, tr))
	}
}
func stop(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(30 * time.Second))
	rd := util.NewDelimitedReader(s, 4096)
	defer rd.Close()
	var msg pb.StopMessage
	if rd.ReadMsg(&msg) != nil || msg.GetType() != pb.StopMessage_CONNECT {
		return
	}
	typ, status := pb.StopMessage_STATUS, pb.Status_OK
	if util.NewDelimitedWriter(s).WriteMsg(&pb.StopMessage{Type: &typ, Status: &status}) != nil {
		return
	}
	// Prototype payload prelude, after the unchanged stock STOP handshake.
	var mode [1]byte
	if _, err := io.ReadFull(s, mode[:]); err != nil {
		return
	}
	if _, err := s.Write(mode[:]); err != nil {
		return
	}
	if mode[0] == 1 {
		time.Sleep(8 * time.Second)
		buf := make([]byte, 32<<10)
		for {
			n, err := s.Read(buf)
			receiverBytes.Add(int64(n))
			if err != nil {
				return
			}
		}
	}
	io.Copy(s, s)
}
func hop(h host.Host, id peer.ID, typ pb.HopMessage_Type, target peer.ID) (network.Stream, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := h.NewStream(network.WithNoDial(ctx, "prototype existing connection"), id, relayproto.ProtoIDv2Hop)
	if err != nil {
		return nil, err
	}
	s.SetDeadline(time.Now().Add(3 * time.Second))
	msg := &pb.HopMessage{Type: &typ}
	if target != "" {
		msg.Peer = &pb.Peer{Id: []byte(target)}
	}
	err = util.NewDelimitedWriter(s).WriteMsg(msg)
	if err != nil {
		s.Reset()
		return nil, err
	}
	rd := util.NewDelimitedReader(s, 4096)
	defer rd.Close()
	var resp pb.HopMessage
	if err = rd.ReadMsg(&resp); err != nil {
		s.Reset()
		return nil, err
	}
	if resp.GetStatus() != pb.Status_OK {
		s.Reset()
		return nil, fmt.Errorf("HOP status %s", resp.GetStatus())
	}
	s.SetDeadline(time.Now().Add(30 * time.Second))
	return s, nil
}

type client struct {
	h     host.Host
	relay peer.ID
	kind  string
}

func clientsMode(kind string) {
	var g greeting
	dec := json.NewDecoder(os.Stdin)
	die(dec.Decode(&g))
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(g.Cert)) {
		die(fmt.Errorf("bad local certificate"))
	}
	var clients []client
	var circuits []network.Stream
	var work *workload
	closeCircuits := func() {
		for _, s := range circuits {
			s.Reset()
		}
		circuits = nil
	}
	defer func() {
		closeCircuits()
		for _, c := range clients {
			c.h.Close()
		}
	}()
	send(reply{})
	for {
		var cmd command
		if dec.Decode(&cmd) != nil || cmd.Op == "exit" {
			return
		}
		res := reply{}
		if work != nil && !work.done.Load() && cmd.Op != "status" {
			die(fmt.Errorf("workload still running"))
		}
		switch cmd.Op {
		case "sustained", "slow-readers":
			closeCircuits()
			work = startWork(clients, cmd.Op, cmd.N)
		case "status":
		case "grow":
			for len(clients) < cmd.N {
				k := kind
				if k == "mixed" {
					k = []string{"tcp", "wss", "webrtc"}[len(clients)%3]
				}
				addr, err := ma.NewMultiaddr(g.Addresses[k])
				die(err)
				info, err := peer.AddrInfoFromP2pAddr(addr)
				die(err)
				// Parent-generated addresses only. Never accept a non-loopback target.
				ip, err := addr.ValueForProtocol(ma.P_IP4)
				die(err)
				if ip != "127.0.0.1" {
					die(fmt.Errorf("non-loopback target rejected"))
				}
				o := options()
				o = append(o, libp2p.NoListenAddrs, libp2p.ResourceManager(&network.NullResourceManager{}))
				switch k {
				case "tcp":
					o = append(o, libp2p.Transport(tcp.NewTCPTransport))
				case "wss":
					o = append(o, libp2p.Transport(ws.New, ws.WithTLSClientConfig(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})))
				case "webrtc":
					o = append(o, libp2p.Transport(webrtc.New))
				}
				h, err := libp2p.New(o...)
				die(err)
				h.SetStreamHandler(relayproto.ProtoIDv2Stop, stop)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err = h.Connect(ctx, *info)
				cancel()
				if err != nil {
					h.Close()
					res.Failed++
					res.Error = err.Error()
					break
				}
				clients = append(clients, client{h, info.ID, k})
				res.OK++
			}
		case "control":
			var mu sync.Mutex
			var wg sync.WaitGroup
			slots := make(chan struct{}, 4)
			for _, c := range clients {
				wg.Add(1)
				slots <- struct{}{}
				go func(c client) {
					defer wg.Done()
					defer func() { <-slots }()
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					s, err := c.h.NewStream(network.WithNoDial(ctx, "control"), c.relay, control)
					if err == nil {
						s.SetDeadline(time.Now().Add(3 * time.Second))
						_, err = s.Write([]byte("probe"))
						if err == nil {
							b := make([]byte, 5)
							_, err = io.ReadFull(s, b)
						}
						s.Close()
					}
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						res.Failed++
						res.Error = err.Error()
					} else {
						res.OK++
					}
				}(c)
			}
			wg.Wait()
		case "circuits":
			closeCircuits()
			for i := 0; i+1 < len(clients) && len(circuits) < cmd.N; i += 2 {
				a, b := clients[i], clients[i+1]
				s, err := hop(b.h, b.relay, pb.HopMessage_RESERVE, "")
				if err == nil {
					s.Close()
					s, err = hop(a.h, a.relay, pb.HopMessage_CONNECT, b.h.ID())
				}
				if err == nil {
					err = prelude(s, 0)
				}
				if err == nil {
					payload := make([]byte, 64<<10)
					s.SetDeadline(time.Now().Add(3 * time.Second))
					_, err = s.Write(payload)
					if err == nil {
						_, err = io.ReadFull(s, payload)
					}
				}
				if err != nil {
					if s != nil {
						s.Reset()
					}
					res.Failed++
					res.Error = err.Error()
				} else {
					s.SetDeadline(time.Now().Add(30 * time.Second))
					circuits = append(circuits, s)
					res.OK++
				}
			}
		case "churn":
			closeCircuits()
			n := min(cmd.N, len(clients))
			for _, c := range clients[:n] {
				c.h.Close()
			}
			clients = clients[n:]
			res.OK = n
		case "close":
			closeCircuits()
			for _, c := range clients {
				c.h.Close()
			}
			clients = nil
		}
		res.Sample = sample(nil, nil, nil)
		if work != nil {
			res.Work = work.view()
		}
		res.Sample.Connections = len(clients)
		send(res)
	}
}

type child struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *json.Decoder
}

func start(ctx context.Context, mode, kind string) *child {
	path, err := os.Executable()
	die(err)
	c := exec.CommandContext(ctx, path, "-mode", mode, "-transport", kind)
	c.Stderr = os.Stderr
	in, err := c.StdinPipe()
	die(err)
	out, err := c.StdoutPipe()
	die(err)
	die(c.Start())
	return &child{c, in, json.NewDecoder(out)}
}
func (c *child) write(v any) { die(json.NewEncoder(c.in).Encode(v)) }
func (c *child) read(v any)  { die(c.out.Decode(v)) }
func (c *child) close()      { c.write(command{Op: "exit"}); c.in.Close(); die(c.cmd.Wait()) }

type row struct {
	Transport      string
	Phase          string
	Target         int
	ElapsedSeconds float64
	Relay          snapshot
	Clients        reply
}

func run(kind string, maxN int, result string, pressure bool) {
	if maxN < 2 || maxN > 160 {
		die(fmt.Errorf("prototype max must be 2..160; no 5,000-client local run"))
	}
	if kind != "all" && kind != "tcp" && kind != "wss" && kind != "webrtc" && kind != "mixed" {
		die(fmt.Errorf("unknown transport"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var rows []row
	kinds := []string{kind}
	if kind == "all" {
		kinds = []string{"tcp", "wss", "webrtc", "mixed"}
	}
	for _, k := range kinds {
		srv := start(ctx, "relay", k)
		var g greeting
		srv.read(&g)
		gen := start(ctx, "clients", k)
		gen.write(g)
		var ready reply
		gen.read(&ready)
		observe := func(phase string, n int, op string) {
			started := time.Now()
			var cr reply
			if op != "" {
				gen.write(command{Op: op, N: n})
				gen.read(&cr)
			}
			time.Sleep(300 * time.Millisecond)
			srv.write(command{Op: "sample"})
			var ss snapshot
			srv.read(&ss)
			r := row{k, phase, n, time.Since(started).Seconds(), ss, cr}
			rows = append(rows, r)
			send(r)
			if ss.RSSKiB > 1536*1024 {
				die(fmt.Errorf("relay RSS safety stop at 1.5 GiB"))
			}
		}
		observe("baseline", 0, "")
		for _, n := range []int{2, 8, 32, maxN} {
			if n <= maxN {
				observe("grow", n, "grow")
			}
		}
		observe("control", 0, "control")
		observe("active-circuits", min(16, maxN/2), "circuits")
		if pressure {
			for _, phase := range []string{"sustained", "slow-readers"} {
				observe(phase+"-start", 12, phase)
				for i := 0; i < 16; i++ {
					time.Sleep(700 * time.Millisecond)
					observe(phase+"-sample", i+1, "status")
					last := rows[len(rows)-1]
					if last.Clients.Sample.RSSKiB > 1536*1024 {
						die(fmt.Errorf("generator RSS safety stop"))
					}
					if last.Clients.Work.Done {
						break
					}
				}
				if !rows[len(rows)-1].Clients.Work.Done {
					die(fmt.Errorf("workload did not finish within sample budget"))
				}
			}
		}
		observe("churn-close", min(8, maxN/2), "churn")
		observe("churn-replace", maxN, "grow")
		observe("cleanup", 0, "close")
		gen.close()
		srv.close()
	}
	if result != "" {
		f, err := os.OpenFile(result, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		die(err)
		defer f.Close()
		report := struct {
			Warning, Platform, Go string
			Rows                  []row
		}{"PROTOTYPE: secured Go connections, not account-authorized Relay Sessions; no OCI capacity claim", runtime.GOOS + "/" + runtime.GOARCH, runtime.Version(), rows}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		die(enc.Encode(report))
	}
}
func main() {
	mode := flag.String("mode", "run", "run, relay or clients")
	kind := flag.String("transport", "all", "tcp, wss, webrtc, mixed, all")
	n := flag.Int("max", 128, "maximum client hosts, hard maximum 160")
	result := flag.String("result", "", "new JSON result file; refuses overwrite")
	pressure := flag.Bool("pressure", false, "add bounded 12-second sustained and slow-reader workloads")
	flag.Parse()
	switch *mode {
	case "relay":
		relayMode()
	case "clients":
		clientsMode(*kind)
	case "run":
		run(*kind, *n, *result, *pressure)
	default:
		die(fmt.Errorf("unknown mode"))
	}
}
