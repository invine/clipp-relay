package service

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func unusedLoopback(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}

func runningHTTP(t *testing.T, s *Service) (string, func()) {
	t.Helper()
	public := unusedLoopback(t)
	private := unusedLoopback(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, public, private) }()
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + private + "/livez")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return public, func() {
					cancel()
					select {
					case err := <-done:
						if err != nil {
							t.Errorf("HTTP stop: %v", err)
						}
					case <-time.After(3 * time.Second):
						t.Error("HTTP shutdown stalled")
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatalf("private listener did not start: %v", <-done)
	return "", nil
}

func openHTTP(t *testing.T, address, request string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	return conn
}

func rejectOverCapacity(t *testing.T, address string) {
	t.Helper()
	conn := openHTTP(t, address, "GET / HTTP/1.1\r\nHost: local\r\nConnection: close\r\n\r\n")
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err == nil || os.IsTimeout(err) {
		t.Fatalf("over-capacity socket got response %q, err=%v", line, err)
	}
}

func resourceCounts() (goroutines, descriptors int, heap uint64, rssKiB int) {
	goroutines = runtime.NumGoroutine()
	entries, err := os.ReadDir("/dev/fd")
	if err == nil {
		descriptors = len(entries)
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	rssKiB = -1
	if output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output(); err == nil {
		if value, err := strconv.Atoi(strings.TrimSpace(string(output))); err == nil {
			rssKiB = value
		}
	}
	return goroutines, descriptors, memory.HeapAlloc, rssKiB
}

func TestDefaultPublicSocketCapAndRecovery(t *testing.T) {
	s := New()
	s.SetPublicHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	beforeG, beforeFD, beforeHeap, beforeRSS := resourceCounts()
	address, stop := runningHTTP(t, s)
	var held []net.Conn
	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
		stop()
	}()
	for range 512 {
		held = append(held, openHTTP(t, address, "GET / HTTP/1.1\r\nHost: local\r\nX-Hold: "))
	}
	peakG, peakFD, peakHeap, peakRSS := resourceCounts()
	rejectOverCapacity(t, address)
	_ = held[0].Close()
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				recovered = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("public socket gate did not recover after release")
	}
	for _, conn := range held {
		_ = conn.Close()
	}
	stop()
	stop = func() {}
	deadline = time.Now().Add(2 * time.Second)
	var afterG, afterFD int
	var afterHeap uint64
	var afterRSS int
	for time.Now().Before(deadline) {
		afterG, afterFD, afterHeap, afterRSS = resourceCounts()
		if afterG <= beforeG+8 && (beforeFD == 0 || afterFD <= beforeFD+8) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("profile public connections=512; samples before/at-limit/after: goroutines=%d/%d/%d; FD=%d/%d/%d; heap bytes=%d/%d/%d; RSS KiB=%d/%d/%d", beforeG, peakG, afterG, beforeFD, peakFD, afterFD, beforeHeap, peakHeap, afterHeap, beforeRSS, peakRSS, afterRSS)
	if afterG > beforeG+8 || beforeFD != 0 && afterFD > beforeFD+8 {
		t.Fatal("socket overload left active process resources")
	}
}

func TestSlowReadersRespectSmallSocketProfile(t *testing.T) {
	const sockets = 4
	s := New()
	entered := make(chan struct{}, sockets)
	s.SetPublicHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		block := make([]byte, 32<<10)
		for {
			if _, err := w.Write(block); err != nil || r.Context().Err() != nil {
				return
			}
		}
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	server := &http.Server{Handler: s.PublicHandler(), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(&limitedListener{Listener: listener, slots: make(chan struct{}, sockets)})
	}()
	var held []net.Conn
	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
		_ = server.Close()
		<-done
	}()
	for range sockets {
		held = append(held, openHTTP(t, address, "GET / HTTP/1.1\r\nHost: local\r\nX-Reader: slow\r\n\r\n"))
	}
	for range sockets {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("slow reader did not reach handler")
		}
	}
	rejectOverCapacity(t, address)
}
