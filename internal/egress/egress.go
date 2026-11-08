// Package egress is the Factory's egress proxy: the one way out of a
// Sandbox, which lets it reach only allowlisted hosts.
package egress

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// dialTimeout bounds connecting to a host or to the upstream proxy.
const dialTimeout = 30 * time.Second

// Options configures a proxy.
type Options struct {
	// Allow is `[sandbox.network] allow`: hosts a Sandbox may reach, matched
	// exactly and regardless of port. "*" opens egress fully; the config
	// accepts it only from config.local.toml or a CLI flag.
	Allow []string
	// Endpoints are the hosts the Harness's auth method talks to. Nothing
	// else is ever added to the allowlist.
	Endpoints []string
	// UpstreamProxy is `[network] upstream_proxy`, an http:// URL with no
	// login. When set it replaces the host's HTTPS_PROXY, HTTP_PROXY and
	// NO_PROXY; when empty those decide. Either way, a loopback target is
	// reached directly.
	UpstreamProxy string
	// Log receives one plain-language line per denied connection.
	Log func(string)
}

// OptionsFrom reads a proxy's Options from the merged config. endpoints are
// the Harness auth method's hosts.
func OptionsFrom(values map[string]any, endpoints []string) Options {
	sandbox, _ := values["sandbox"].(map[string]any)
	sandboxNet, _ := sandbox["network"].(map[string]any)
	list, _ := sandboxNet["allow"].([]any)
	allow := make([]string, 0, len(list))
	for _, v := range list {
		if h, ok := v.(string); ok {
			allow = append(allow, h)
		}
	}
	network, _ := values["network"].(map[string]any)
	upstream, _ := network["upstream_proxy"].(string)
	return Options{Allow: allow, Endpoints: endpoints, UpstreamProxy: upstream}
}

// canonical is the form hosts are compared in: DNS names ignore case, and a
// trailing dot only spells out the root.
func canonical(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// Proxy is a running egress proxy.
type Proxy struct {
	listener  net.Listener
	server    *http.Server
	cancel    context.CancelFunc
	dialer    net.Dialer
	allow     map[string]bool
	open      bool // Allow holds "*"
	log       func(string)
	forward   *httputil.ReverseProxy
	transport *http.Transport // forward's, kept so Close can drop its idle connections
	route     route
}

// Start runs a proxy until ctx ends or Close is called.
func Start(ctx context.Context, o Options) (*Proxy, error) {
	route, err := newRoute(o.UpstreamProxy)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("the egress proxy can't listen: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &Proxy{listener: l, cancel: cancel, dialer: net.Dialer{Timeout: dialTimeout}, allow: map[string]bool{}, log: o.Log, route: route}
	if p.log == nil {
		p.log = func(string) {}
	}
	p.open = slices.Contains(o.Allow, "*")
	for _, h := range slices.Concat(o.Allow, o.Endpoints) {
		p.allow[canonical(h)] = true
	}
	p.transport = &http.Transport{
		Proxy:              func(r *http.Request) (*url.URL, error) { return p.route(r.URL) },
		DialContext:        p.dialer.DialContext,
		DisableCompression: true, // the client's own Accept-Encoding stands
	}
	p.forward = &httputil.ReverseProxy{
		// Rewrite, unlike Director, adds no X-Forwarded-For.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = pr.In.URL
			pr.Out.Host = pr.In.URL.Host
		},
		Transport:     p.transport,
		FlushInterval: -1, // server-sent events must not wait in a buffer
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.unreachable(w, r.URL.Hostname(), err)
		},
	}
	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: dialTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	context.AfterFunc(ctx, func() { _ = p.server.Close() })
	go func() { _ = p.server.Serve(l) }()
	return p, nil
}

// Addr is the address the proxy listens on.
func (p *Proxy) Addr() string { return p.listener.Addr().String() }

// Close stops the proxy and cuts every connection through it.
func (p *Proxy) Close() error {
	p.cancel()
	defer p.transport.CloseIdleConnections()
	return p.server.Close()
}

// ServeHTTP handles one proxy request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only the request target decides where a request goes. A Host header
	// is the client's to set, and CONNECT /path leaves the target empty.
	if r.URL.Hostname() == "" {
		http.Error(w, "egress proxy: this is a forward proxy; send it proxy requests", http.StatusBadRequest)
		return
	}
	if !p.open && !p.allow[canonical(r.URL.Hostname())] {
		p.deny(w, r.URL.Hostname())
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	case r.URL.Scheme == "http":
		p.forward.ServeHTTP(w, r)
	default:
		http.Error(w, "egress proxy: use CONNECT for anything but plain http", http.StatusBadRequest)
	}
}

// connect opens a tunnel for a CONNECT request.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	remote, err := p.dial(r.Context(), r.URL.Host)
	if err != nil {
		p.unreachable(w, r.URL.Hostname(), err)
		return
	}
	defer func() { _ = remote.Close() }()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "this connection can't be tunnelled", http.StatusInternalServerError)
		return
	}
	client, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}

	// Closing both ends is what unblocks the copies when the proxy stops.
	stop := context.AfterFunc(r.Context(), func() {
		_ = client.Close()
		_ = remote.Close()
	})
	defer stop()

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(remote, rw) // rw first: it may hold bytes sent early
		closeWrite(remote)
	})
	wg.Go(func() {
		_, _ = io.Copy(client, remote)
		closeWrite(client)
	})
	wg.Wait()
}

// route says which upstream proxy a connection to target goes through, nil
// for none.
type route func(target *url.URL) (*url.URL, error)

// newRoute builds the route from `upstream_proxy`, or from the host's proxy
// variables when that is empty. Errors never repeat a proxy URL, which may
// hold a password.
func newRoute(upstream string) (route, error) {
	cfg := httpproxy.FromEnvironment()
	if upstream != "" {
		u, err := url.Parse(upstream)
		if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil {
			return nil, errors.New("[network] upstream_proxy must be an http URL with no login, e.g. \"http://proxy.corp:8080\"")
		}
		cfg = &httpproxy.Config{HTTPProxy: upstream, HTTPSProxy: upstream}
	}
	proxyFor := cfg.ProxyFunc()
	return func(target *url.URL) (*url.URL, error) {
		via, err := proxyFor(target)
		if err != nil {
			return nil, errors.New("the host's proxy variable is not a valid URL")
		}
		if via != nil && via.Scheme != "http" {
			return nil, fmt.Errorf("the host's proxy variable uses %s, but only http proxies are supported", via.Scheme)
		}
		if via != nil && via.User != nil {
			return nil, errors.New("the host's proxy variable holds a login, but proxy authentication isn't supported")
		}
		return via, nil
	}, nil
}

// dial connects to hostport, through the upstream proxy if the route says so.
func (p *Proxy) dial(ctx context.Context, hostport string) (net.Conn, error) {
	via, err := p.route(&url.URL{Scheme: "https", Host: hostport})
	if err != nil {
		return nil, err
	}
	if via == nil {
		return p.dialer.DialContext(ctx, "tcp", hostport)
	}
	return p.dialVia(ctx, via, hostport)
}

// dialVia asks the upstream proxy via for a tunnel to hostport.
func (p *Proxy) dialVia(ctx context.Context, via *url.URL, hostport string) (net.Conn, error) {
	c, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(via.Hostname(), cmp.Or(via.Port(), "80")))
	if err != nil {
		return nil, fmt.Errorf("the upstream proxy: %w", err)
	}
	conn, err := handshake(c, via.Host, hostport)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return conn, nil
}

// handshake does the CONNECT exchange on c, bounded so that a silent proxy
// can't hold a connection forever.
func handshake(c net.Conn, proxyHost, hostport string) (net.Conn, error) {
	if err := c.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", hostport, hostport); err != nil {
		return nil, fmt.Errorf("the upstream proxy %s: %w", proxyHost, err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("the upstream proxy %s: %w", proxyHost, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the upstream proxy %s refused the tunnel: %s", proxyHost, resp.Status)
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if br.Buffered() > 0 {
		return &earlyBytes{Conn: c, r: br}, nil
	}
	return c, nil
}

// earlyBytes is a tunnel whose first bytes arrived with the proxy's answer
// and were read along with it.
type earlyBytes struct {
	net.Conn
	r *bufio.Reader
}

func (e *earlyBytes) Read(b []byte) (int, error) { return e.r.Read(b) }

// CloseWrite half-closes the underlying connection.
func (e *earlyBytes) CloseWrite() error {
	closeWrite(e.Conn)
	return nil
}

// deny refuses a connection to host and says how to allow it.
func (p *Proxy) deny(w http.ResponseWriter, host string) {
	msg := fmt.Sprintf("egress proxy: denied a connection to %s; add it to `allow` in [sandbox.network] if the Sandbox should reach it", host)
	p.log(msg)
	http.Error(w, msg, http.StatusForbidden)
}

// unreachable reports that an allowed host couldn't be reached.
func (p *Proxy) unreachable(w http.ResponseWriter, host string, err error) {
	http.Error(w, fmt.Sprintf("egress proxy: can't reach %s: %v", host, err), http.StatusBadGateway)
}

// closeWrite passes an end-of-stream on while the other direction, which may
// still be sending, stays open.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
