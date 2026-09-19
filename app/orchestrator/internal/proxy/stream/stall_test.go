package stream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/acestream/acestream/internal/config"
	"github.com/acestream/acestream/internal/proxy/buffer"
)

func TestStallPolicy(t *testing.T) {
	now := time.Now()
	cfg := &config.Config{StreamStallTimeout: 25 * time.Second, ChannelInitGracePeriod: 60 * time.Second}
	for _, tc := range []struct {
		name                 string
		started, chunk, next time.Time
		clients              int
		want                 bool
	}{
		{"stalled", now.Add(-time.Minute), now.Add(-30 * time.Second), time.Time{}, 1, true},
		{"fresh", now.Add(-time.Minute), now.Add(-time.Second), time.Time{}, 1, false},
		{"no viewers", now.Add(-time.Minute), now.Add(-30 * time.Second), time.Time{}, 0, false},
		{"cooldown", now.Add(-time.Minute), now.Add(-30 * time.Second), now.Add(time.Second), 1, false},
		{"startup grace", now.Add(-30 * time.Second), time.Time{}, time.Time{}, 1, false},
		{"first chunk starvation", now.Add(-61 * time.Second), time.Time{}, time.Time{}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stallDue(now, tc.started, tc.chunk, tc.next, tc.clients, cfg); got != tc.want {
				t.Fatalf("got=%v", got)
			}
		})
	}
	cfg.StreamStallTimeout = 0
	if stallDue(now, now.Add(-time.Hour), time.Time{}, time.Time{}, 1, cfg) {
		t.Fatal("disabled recovery fired")
	}
}

func TestAPIRecoveryReplacesSessionAndCancelsCleanly(t *testing.T) {
	old := config.C.Load()
	cfg := *old
	cfg.StreamStallTimeout = 25 * time.Millisecond
	cfg.StreamStallCheckInterval = 5 * time.Millisecond
	cfg.StreamStallCooldown = 20 * time.Millisecond
	cfg.StreamStallMaxRecoveries = 1
	cfg.ChannelInitGracePeriod = time.Second
	config.C.Store(&cfg)
	defer config.C.Store(old)
	healthy := make(chan struct{})
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 188))
		w.(http.Flusher).Flush()
		if r.URL.Path == "/1" {
			<-r.Context().Done()
			return
		}
		close(healthy)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				w.Write(make([]byte, 188))
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer web.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	var starts, stops atomic.Int32
	acceptedDone := make(chan struct{})
	go func() {
		defer close(acceptedDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				scan := bufio.NewScanner(conn)
				for scan.Scan() {
					line := scan.Text()
					switch {
					case strings.HasPrefix(line, "HELLOBG"):
						fmt.Fprintln(conn, "HELLOTS key=test")
					case strings.HasPrefix(line, "READY"):
						fmt.Fprintln(conn, "AUTH 1")
					case strings.HasPrefix(line, "START"):
						attempt := starts.Add(1)
						fmt.Fprintf(conn, "START url=%s/%d\r\n", web.URL, attempt)
					case line == "STOP":
						stops.Add(1)
					}
				}
			}()
		}
	}()
	defer func() { listener.Close(); <-acceptedDone; workers.Wait() }()
	m := newManager(StreamParams{ContentID: "api-recovery", ControlMode: "api", SourceInputType: "direct_url", SourceInput: "http://example.test/stream", Engine: EngineParams{Host: "127.0.0.1", APIPort: listener.Addr().(*net.TCPAddr).Port}}, buffer.New(188, 4), &ClientManager{clients: map[string]*ClientRecord{"viewer": {}}}, nil, noopSink{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer m.sendEngineStop()
	if err := m.requestStream(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.startReadLoop(ctx, time.Now()) }()
	select {
	case <-healthy:
	case <-time.After(3 * time.Second):
		t.Fatal("API recovery did not produce new stream")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("API reader did not cancel")
	}
	if starts.Load() != 2 || stops.Load() != 1 {
		t.Fatalf("starts=%d stops=%d", starts.Load(), stops.Load())
	}
}

// A real HTTP reader stalls after one chunk. Session negotiation takes several
// watchdog intervals. The replacement emits data and must not get a stale reset.
func TestSlowHTTPRecoveryDoesNotRestartHealthyReplacement(t *testing.T) {
	old := config.C.Load()
	cfg := *old
	cfg.StreamStallTimeout = 40 * time.Millisecond
	cfg.StreamStallCheckInterval = 5 * time.Millisecond
	cfg.StreamStallCooldown = 10 * time.Millisecond
	cfg.StreamStallMaxRecoveries = 2
	cfg.ChannelInitGracePeriod = time.Second
	config.C.Store(&cfg)
	defer config.C.Store(old)
	var requests, stops atomic.Int32
	healthyStarted := make(chan struct{})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/initial":
			w.Write(make([]byte, 188))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/ace/getstream":
			requests.Add(1)
			select {
			case <-time.After(80 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"playback_url": server.URL + "/healthy", "command_url": server.URL + "/stop"}})
		case "/healthy":
			select {
			case <-healthyStarted:
			default:
				close(healthyStarted)
			}
			ticker := time.NewTicker(2 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					w.Write(make([]byte, 188))
					w.(http.Flusher).Flush()
				}
			}
		case "/stop":
			stops.Add(1)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	host, portText, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portText)
	b := buffer.New(188, 4)
	cm := &ClientManager{clients: map[string]*ClientRecord{"viewer": {ID: "viewer"}}}
	m := newManager(StreamParams{ContentID: "review", ControlMode: "http", Engine: EngineParams{Host: host, Port: port}}, b, cm, nil, noopSink{})
	m.playbackURL = server.URL + "/initial"
	m.commandURL = server.URL + "/stop"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() { out, _, _ := m.startReadLoop(ctx, time.Now()); done <- out }()
	select {
	case <-healthyStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement never started")
	}
	// Several stale watcher intervals would be consumed during this period.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not stop")
	}
	if requests.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("requests=%d stops=%d", requests.Load(), stops.Load())
	}
	if cm.LocalCount() != 1 {
		t.Fatal("recovery removed viewer")
	}
}

func TestStallRecoveryBudgetAndRequestFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			old := config.C.Load()
			cfg := *old
			cfg.StreamStallTimeout = 15 * time.Millisecond
			cfg.StreamStallCheckInterval = 2 * time.Millisecond
			cfg.StreamStallCooldown = time.Millisecond
			cfg.StreamStallMaxRecoveries = 1
			cfg.ChannelInitGracePeriod = time.Second
			config.C.Store(&cfg)
			defer config.C.Store(old)
			var count atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ace/getstream" {
					count.Add(1)
					if fail {
						w.Write([]byte(`{"error":"cannot restart"}`))
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"playback_url": server.URL + "/stream"}})
					return
				}
				w.Write(make([]byte, 188))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			host, portText, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(portText)
			m := newManager(StreamParams{ContentID: "bounded", ControlMode: "http", Engine: EngineParams{Host: host, Port: port}}, buffer.New(188, 4), &ClientManager{clients: map[string]*ClientRecord{"viewer": {}}}, nil, noopSink{})
			m.playbackURL = server.URL + "/stream"
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			outcome, reason, _ := m.startReadLoop(ctx, time.Now())
			if fail {
				if outcome != "engine_error" {
					t.Fatalf("outcome=%s reason=%s", outcome, reason)
				}
			} else if reason != "stream stall recovery budget exhausted" {
				t.Fatalf("unexpected outcome %s: %s", outcome, reason)
			}
			if count.Load() != 1 {
				t.Fatalf("restart requests=%d", count.Load())
			}
		})
	}
}
