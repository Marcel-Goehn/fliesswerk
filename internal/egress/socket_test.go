//go:build unix

package egress_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
)

// connectOverSocket asks the proxy at socket for a tunnel and returns the
// status it answers with.
func connectOverSocket(t *testing.T, socket, target string) int {
	t.Helper()
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dialling the socket: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestTheAllowlistHoldsOverAUnixSocket(t *testing.T) {
	clearProxyEnv(t)
	srv := tlsTarget(t)
	// Not t.TempDir(): macOS's are too long for a Unix socket's path.
	dir, err := os.MkdirTemp("/tmp", "kwk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "egress.sock")
	var log logged
	p, err := egress.Start(context.Background(), egress.Options{Socket: socket, Allow: []string{"127.0.0.1"}, Log: log.log})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if got := connectOverSocket(t, socket, srv.Listener.Addr().String()); got != http.StatusOK {
		t.Errorf("an allowed host: status %d, want 200", got)
	}
	if got := connectOverSocket(t, socket, "denied.example:443"); got != http.StatusForbidden {
		t.Errorf("a denied host: status %d, want 403", got)
	}
	if want := "denied.example"; !strings.Contains(log.String(), want) {
		t.Errorf("the denial wasn't logged: %q", log.String())
	}
}
