package integration

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/snakeice/gunnel/pkg/client"
	"github.com/snakeice/gunnel/pkg/connection"
	"github.com/snakeice/gunnel/pkg/manager"
	gunnelquic "github.com/snakeice/gunnel/pkg/quic"
	"github.com/snakeice/gunnel/pkg/transport"
)

// TestQUICProxyRoundTripHTTP spins up:
// - a local HTTP echo server (backend)
// - a QUIC listener that accepts client connections and wires them into the Manager
// - a Client that registers a backend "test"
// - an in-memory "user" connection piped into Manager.HandleHTTPConnection
// It then performs an HTTP request and verifies the proxied response body.
func TestQUICProxyRoundTripHTTP(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1) Start a local backend HTTP server (echo)
	backendAddr, shutdownBackend := startHTTPBackend(t, ctx, func(w http.ResponseWriter, r *http.Request) {
		// Simple echo handler with deterministic body
		_ = r.Body.Close()
		body := "hello-through-gunnel"
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	})
	defer shutdownBackend()

	// 2) Start QUIC server and Manager
	mngr := manager.New()

	qsrv, qsrvAddr := startQUICServer(t)
	defer func() { _ = qsrv.Close() }()

	// Wire QUIC accept loop -> Manager.HandleConnection
	serverCtx, serverCancel := context.WithCancel(ctx)
	defer serverCancel()
	go acceptQUICLoop(serverCtx, t, qsrv, mngr)

	// 3) Start client with backend registration
	cfg := &client.Config{
		ServerAddr: qsrvAddr,
		Backend: map[string]*client.BackendConfig{
			"test": {
				Host:      hostFromAddr(backendAddr),
				Port:      portFromAddr(backendAddr),
				Subdomain: "test",
				Protocol:  "http",
			},
		},
	}
	cl, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New error: %v", err)
	}

	go func() {
		if err := cl.Start(ctx); err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Logf("client.Start returned: %v", err)
		}
	}()

	// Wait until manager has the subdomain registered
	waitUntil(t, 3*time.Second, func() bool {
		registered := false
		mngr.ForEachClient(func(sub string, _ *connection.Connection) {
			if sub == "test" {
				registered = true
			}
		})
		return registered
	})

	// 4) Create a user-side in-memory connection to simulate an HTTP request
	serverConn, userConn := net.Pipe()
	defer func() { _ = userConn.Close() }()
	doneCh := make(chan struct{})

	// Server-side handler consumes the request from serverConn and proxies it
	go func() {
		defer close(doneCh)
		mngr.HandleHTTPConnection(serverConn) // blocks until proxy completes or error
	}()

	// 5) Send HTTP request through the "user" side of the pipe
	req, err := http.NewRequest("GET", "http://test.localhost/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	if err := req.Write(userConn); err != nil {
		t.Fatalf("write request error: %v", err)
	}

	// 6) Read proxied HTTP response
	resp, err := http.ReadResponse(bufio.NewReader(userConn), req)
	if err != nil {
		t.Fatalf("http.ReadResponse error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d", resp.StatusCode, http.StatusOK)
	}

	got := strings.TrimSpace(string(body))
	want := "hello-through-gunnel"
	if got != want {
		t.Fatalf("unexpected body: got=%q want=%q", got, want)
	}

	// Ensure server goroutine completed
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("server handler did not finish in time")
	}
}

func startHTTPBackend(t *testing.T, ctx context.Context, handler http.HandlerFunc) (addr string, shutdown func()) {
	t.Helper()

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend error: %v", err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "Server closed") {
			t.Logf("backend server exited: %v", err)
		}
	}()

	return ln.Addr().String(), func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}
}

func startQUICServer(t *testing.T) (*gunnelquic.Server, string) {
	t.Helper()

	// Use localhost ephemeral port
	qsrv, err := gunnelquic.NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("quic.NewServer error: %v", err)
	}
	return qsrv, qsrv.Addr()
}

func acceptQUICLoop(ctx context.Context, t *testing.T, qsrv *gunnelquic.Server, m *manager.Manager) {
	t.Helper()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := qsrv.Accept()
		if err != nil {
			// Accept uses timeouts; keep looping on timeout-like errors
			continue
		}

		go func() {
			transp, err := transport.NewFromServer(ctx, conn)
			if err != nil {
				_ = conn.CloseWithError(0, "wrapper error")
				t.Logf("transport.NewFromServer error: %v", err)
				return
			}
			m.HandleConnection(transp)
		}()
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func hostFromAddr(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1"
	}
	return h
}

func portFromAddr(addr string) uint32 {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	pp, _ := net.LookupPort("tcp", p)
	return uint32(pp)
}
