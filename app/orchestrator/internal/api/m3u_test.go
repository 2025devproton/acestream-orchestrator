package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func m3uRequest(source, key string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/v1/modify_m3u?host=player&port=8000&"+key+"="+url.QueryEscape(source), nil)
}

func TestM3UFetchConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, timeout, proxy string
		want                 time.Duration
		invalid              bool
	}{
		{"defaults", "", "", 30 * time.Second, false},
		{"minimum", " 1 ", "", time.Second, false},
		{"maximum", "300", " https://proxy.example:8443 ", 300 * time.Second, false},
		{"zero", "0", "", 0, true},
		{"negative", "-1", "", 0, true},
		{"too large", "301", "", 0, true},
		{"duration", "30s", "", 0, true},
		{"fraction", "1.5", "", 0, true},
		{"socks", "", "socks5://localhost:1080", 0, true},
		{"missing host", "", "http://", 0, true},
		{"malformed", "", "://", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("M3U_FETCH_TIMEOUT_S", tc.timeout)
			t.Setenv("M3U_FETCH_PROXY_URL", tc.proxy)
			client, err := m3uFetchClient()
			if tc.invalid {
				if err == nil {
					client.CloseIdleConnections()
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			if client.Timeout != tc.want {
				t.Fatalf("timeout = %v", client.Timeout)
			}
			transport := client.Transport.(*http.Transport)
			if transport == http.DefaultTransport {
				t.Fatal("shared transport was reused")
			}
			if tc.proxy != "" {
				proxy, err := transport.Proxy(m3uRequest("http://example.com/list", "url"))
				if err != nil || proxy.String() != strings.TrimSpace(tc.proxy) {
					t.Fatalf("proxy = %v, %v", proxy, err)
				}
			} else if transport.Proxy == nil {
				t.Fatal("environment proxy behavior lost")
			}
		})
	}
}

func TestModifyM3UProxyAndRewrite(t *testing.T) {
	t.Setenv("M3U_FETCH_TIMEOUT_S", "")
	// A synthetic proxy serves a destination that cannot be reached directly.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "http://playlist.invalid/list" {
			t.Errorf("proxy request = %s", r.URL)
		}
		io.WriteString(w, "#EXTM3U\r\nacestream://first\nace://second\nhttp://127.0.0.1:6878/ace/getstream?id=third\nhttps://cdn.example/video\n")
	}))
	defer proxy.Close()
	t.Setenv("M3U_FETCH_PROXY_URL", proxy.URL)
	for _, key := range []string{"m3u_url", "url"} {
		w := httptest.NewRecorder()
		(&ProxyServer{}).mgHandleModifyM3U(w, m3uRequest("http://playlist.invalid/list", key))
		want := "#EXTM3U\nhttp://player:8000/ace/getstream?id=first\nhttp://player:8000/ace/getstream?id=second\nhttp://player:8000/ace/getstream?id=third\nhttps://cdn.example/video\n"
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Content-Type") != "application/x-mpegurl" {
			t.Fatalf("response = %d %s", w.Code, w.Body)
		}
	}
}

func TestModifyM3UFetchFailures(t *testing.T) {
	for _, mode := range []string{"status", "truncated", "timeout headers", "timeout body", "canceled", "invalid config", "success"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("M3U_FETCH_PROXY_URL", "")
			t.Setenv("M3U_FETCH_TIMEOUT_S", "1")
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "status":
					w.WriteHeader(http.StatusForbidden)
				case "truncated":
					w.Header().Set("Content-Length", "1000")
					io.WriteString(w, "#EXTM3U\nace://partial\n")
				case "timeout headers":
					<-r.Context().Done()
				case "timeout body":
					io.WriteString(w, "#EXTM3U\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					io.WriteString(w, "#EXTM3U\nace://complete")
				}
			}))
			defer source.Close()
			req := m3uRequest(source.URL, "m3u_url")
			want := http.StatusBadGateway
			if mode == "invalid config" {
				t.Setenv("M3U_FETCH_TIMEOUT_S", "oops")
				want = http.StatusInternalServerError
			}
			if mode == "success" {
				want = http.StatusOK
			}
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			(&ProxyServer{}).mgHandleModifyM3U(w, req)
			if w.Code != want {
				t.Fatalf("response = %d %s; want %d", w.Code, w.Body, want)
			}
			if want != 200 && strings.Contains(w.Body.String(), "#EXTM3U") {
				t.Fatal("returned partial playlist")
			}
			if mode == "success" && w.Body.String() != "#EXTM3U\nhttp://player:8000/ace/getstream?id=complete\n" {
				t.Fatalf("response = %s", w.Body)
			}
		})
	}
}

type finalM3UReader struct{}

func (finalM3UReader) Read(p []byte) (int, error) { return copy(p, "one\ntwo\nthree"), io.EOF }

func TestM3UScannerFinalRead(t *testing.T) {
	scanner := newLineScanner(finalM3UReader{})
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if strings.Join(lines, ",") != "one,two,three" || scanner.Err() != nil {
		t.Fatalf("lines = %v, error = %v", lines, scanner.Err())
	}
}
