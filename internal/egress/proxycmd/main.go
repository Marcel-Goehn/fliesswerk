// Command proxycmd is the egress proxy as a program of its own: what runs in
// a Run's proxy sidecar, built static for Linux and embedded in every
// release (see internal/proxyimage).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "egress proxy:", err)
		os.Exit(1)
	}
}

func run() error {
	var o egress.Options
	flag.StringVar(&o.Listen, "listen", ":3128", "the address to listen on")
	flag.StringVar(&o.UpstreamProxy, "upstream", "", "the upstream proxy, instead of the proxy variables")
	flag.Func("allow", "a host Sandboxes may reach; repeat for more", func(host string) error {
		o.Allow = append(o.Allow, host)
		return nil
	})
	flag.Parse()
	o.Log = func(line string) { _, _ = fmt.Fprintln(os.Stdout, line) }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, err := egress.Start(ctx, o)
	if err != nil {
		return err
	}
	defer func() { _ = p.Close() }()
	<-ctx.Done()
	return nil
}
