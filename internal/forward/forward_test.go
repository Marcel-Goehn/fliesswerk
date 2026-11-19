package forward_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/forward"
)

// echoUntilEOF answers each connection with what it sent, once the client
// has finished sending, which only works if a half-close gets through.
func echoUntilEOF(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				got, _ := io.ReadAll(c)
				_, _ = c.Write(append([]byte("echo: "), got...))
			}()
		}
	}()
	return l.Addr().String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func dial(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			return c.(*net.TCPConn)
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAConnectionReachesItsTargetAndAHalfCloseGetsThrough(t *testing.T) {
	listen, target := freeAddr(t), echoUntilEOF(t)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- forward.Serve(ctx, []forward.Rule{{Listen: listen, Target: target}}) }()

	c := dial(t, listen)
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := bufio.NewReader(c).ReadString('\x00')
	if err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if got != "echo: hello" {
		t.Errorf("got %q", got)
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("Serve after its context ended: %v", err)
	}
}

func TestAPortThatCannotBeBoundIsAnError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	if err := forward.Serve(context.Background(), []forward.Rule{{Listen: taken.Addr().String(), Target: "127.0.0.1:1"}}); err == nil {
		t.Fatal("Serve bound a port already in use")
	}
}

func TestParseRuleReadsListenAndTarget(t *testing.T) {
	got, err := forward.ParseRule("127.0.0.1:5432=10.89.0.3:5432")
	if err != nil {
		t.Fatal(err)
	}
	if want := (forward.Rule{Listen: "127.0.0.1:5432", Target: "10.89.0.3:5432"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := forward.ParseRule("127.0.0.1:5432"); err == nil {
		t.Error("a rule with no target was accepted")
	}
}

func TestAUnixSocketCarriesAConnectionBothWays(t *testing.T) {
	dir, err := os.MkdirTemp("", "fwk-") // t.TempDir() is too long for a socket's path on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listen, target := freeAddr(t), echoUntilEOF(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// As in a native Sandbox: TCP to a socket, and from the socket on to TCP.
	server, err := forward.Start(ctx, []forward.Rule{
		{Listen: "unix:" + socket, Target: target},
		{Listen: listen, Target: "unix:" + socket},
	})
	if err != nil {
		t.Fatal(err)
	}

	c, err := net.Dial("tcp", listen) // no retry: Start returns listening
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "echo: hello" {
		t.Errorf("got %q, %v", got, err)
	}

	stop()
	server.Wait()
	if _, err := net.Dial("tcp", listen); err == nil {
		t.Error("still listening after its context ended")
	}
}
