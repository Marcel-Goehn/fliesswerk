// Package forward relays connections from a local address to a fixed target,
// over TCP or a Unix socket. It makes a Service reachable on localhost inside
// a Sandbox.
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

// unixPrefix marks an address as a Unix socket's path.
const unixPrefix = "unix:"

// Rule is one forwarded address: host:port, or unix:<path>.
type Rule struct {
	Listen, Target string
}

// Unix is the address of the Unix socket at path.
func Unix(path string) string { return unixPrefix + path }

// endpoint is the network and address of a rule's side.
func endpoint(addr string) (network, address string) {
	if path, ok := strings.CutPrefix(addr, unixPrefix); ok {
		return "unix", path
	}
	return "tcp", addr
}

// ParseRule reads a rule written as listen=target.
func ParseRule(s string) (Rule, error) {
	listen, target, ok := strings.Cut(s, "=")
	if !ok || listen == "" || target == "" {
		return Rule{}, fmt.Errorf("forward rule %q is not listen=target", s)
	}
	return Rule{Listen: listen, Target: target}, nil
}

// Server is a set of rules being forwarded.
type Server struct {
	wg sync.WaitGroup
}

// Wait returns when the context given to Start has ended and every
// connection is closed.
func (s *Server) Wait() { s.wg.Wait() }

// Start listens on every rule, and forwards them in the background until ctx
// ends. An address it can't listen on is an error, before anything is served.
func Start(ctx context.Context, rules []Rule) (*Server, error) {
	var lc net.ListenConfig
	listeners := make([]net.Listener, 0, len(rules))
	closeAll := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}
	for _, r := range rules {
		network, address := endpoint(r.Listen)
		l, err := lc.Listen(ctx, network, address)
		if err != nil {
			closeAll()
			return nil, err
		}
		listeners = append(listeners, l)
	}
	context.AfterFunc(ctx, closeAll)

	s := &Server{}
	for i, l := range listeners {
		s.wg.Go(func() { accept(ctx, l, rules[i].Target) })
	}
	return s, nil
}

// Serve forwards every rule until ctx ends.
func Serve(ctx context.Context, rules []Rule) error {
	s, err := Start(ctx, rules)
	if err != nil {
		return err
	}
	s.Wait()
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
	network, address := endpoint(target)
	remote, err := d.DialContext(ctx, network, address)
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
