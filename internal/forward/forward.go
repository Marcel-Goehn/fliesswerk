// Package forward relays TCP connections from a local address to a fixed
// target. It makes a Service reachable on localhost inside a Sandbox.
package forward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// dialTimeout bounds reaching a target.
const dialTimeout = 10 * time.Second

// Rule is one forwarded address.
type Rule struct {
	Listen, Target string
}

// ParseRule reads a rule written as listen=target.
func ParseRule(s string) (Rule, error) {
	listen, target, ok := strings.Cut(s, "=")
	if !ok || listen == "" || target == "" {
		return Rule{}, fmt.Errorf("forward rule %q is not listen=target", s)
	}
	return Rule{Listen: listen, Target: target}, nil
}

// Serve forwards every rule until ctx ends. An address it can't listen on
// is an error, before anything is served.
func Serve(ctx context.Context, rules []Rule) error {
	var lc net.ListenConfig
	listeners := make([]net.Listener, 0, len(rules))
	closeAll := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}
	defer closeAll()
	for _, r := range rules {
		l, err := lc.Listen(ctx, "tcp", r.Listen)
		if err != nil {
			return err
		}
		listeners = append(listeners, l)
	}
	stop := context.AfterFunc(ctx, closeAll)
	defer stop()

	var wg sync.WaitGroup
	for i, l := range listeners {
		wg.Go(func() { accept(ctx, l, rules[i].Target) })
	}
	wg.Wait()
	return nil
}

// accept relays each connection to l until l is closed.
func accept(ctx context.Context, l net.Listener, target string) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue
		}
		wg.Go(func() { relay(ctx, c, target) })
	}
}

// relay carries one connection, each direction closing on its own: a client
// may finish sending and still be waiting for the answer.
func relay(ctx context.Context, client net.Conn, target string) {
	defer func() { _ = client.Close() }()
	d := net.Dialer{Timeout: dialTimeout}
	remote, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = remote.Close() }()
	// Closing both ends is what unblocks the copies when ctx ends.
	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = remote.Close()
	})
	defer stop()

	var wg sync.WaitGroup
	wg.Go(func() { pipe(remote, client) })
	wg.Go(func() { pipe(client, remote) })
	wg.Wait()
}

func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
