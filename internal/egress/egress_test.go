package egress_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
)

// start runs a proxy for the length of the test and returns its URL.
func start(t *testing.T, o egress.Options) *url.URL {
	t.Helper()
	p, err := egress.Start(context.Background(), o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return &url.URL{Scheme: "http", Host: p.Addr()}
}

// through returns a client that sends everything via proxy, trusting srv's
// certificate.
func through(t *testing.T, srv *httptest.Server, proxy *url.URL) *http.Client {
	t.Helper()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(proxy)
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(b)
}

func TestAllowedHTTPSHostIsReachable(t *testing.T) {
	srv := tlsTarget(t)
	proxy := start(t, egress.Options{Allow: []string{"127.0.0.1"}})

	resp, err := through(t, srv, proxy).Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := body(t, resp); got != "secret garden" {
		t.Fatalf("body = %q", got)
	}
}

// logged collects what the proxy logs.
type logged struct {
	mu    sync.Mutex
	lines []string
}

func (l *logged) log(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
}

func (l *logged) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestDeniedHTTPSHostIsUnreachableAndLogged(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	var log logged
	proxy := start(t, egress.Options{Allow: []string{"allowed.test"}, Log: log.log})

	_, err := through(t, srv, proxy).Get(srv.URL)
	if err == nil {
		t.Fatal("GET succeeded, want the proxy to refuse it")
	}
	if hits.Load() != 0 {
		t.Fatalf("the denied host got %d requests", hits.Load())
	}
	if s := log.String(); !strings.Contains(s, "127.0.0.1") || !strings.Contains(s, "allow") {
		t.Fatalf("log = %q, want the host and how to allow it", s)
	}
}

func TestPlainHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "%s %s", r.Method, b)
	}))
	defer srv.Close()

	t.Run("an allowed host is reachable", func(t *testing.T) {
		proxy := start(t, egress.Options{Allow: []string{"127.0.0.1"}})
		resp, err := through(t, srv, proxy).Post(srv.URL, "text/plain", strings.NewReader("ping"))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		if got := body(t, resp); got != "POST ping" {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("a denied host is unreachable and logged", func(t *testing.T) {
		hits.Store(0)
		var log logged
		proxy := start(t, egress.Options{Allow: []string{"allowed.test"}, Log: log.log})
		resp, err := through(t, srv, proxy).Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || hits.Load() != 0 {
			t.Fatalf("status = %d, hits = %d; want 403 and none", resp.StatusCode, hits.Load())
		}
		if !strings.Contains(log.String(), "127.0.0.1") {
			t.Fatalf("log = %q, want the host", log.String())
		}
	})
}

// dial connects to proxy for the length of the test.
func dial(t *testing.T, proxy *url.URL) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", proxy.Host, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	return c
}

// raw sends a hand-written proxy request, which a well-behaved client
// wouldn't, and returns the status line's code.
func raw(t *testing.T, proxy *url.URL, request string) int {
	t.Helper()
	c := dial(t, proxy)
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestOnlyTheRequestTargetDecidesTheDestination(t *testing.T) {
	var hits atomic.Int32
	var sawHost atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sawHost.Store(r.Host)
	}))
	defer srv.Close()
	target := srv.Listener.Addr().String()

	denied := map[string]struct {
		request string
		want    int
	}{
		"an allowed Host header doesn't excuse a denied target": {"GET http://" + target + "/ HTTP/1.1\r\nHost: allowed.test\r\n\r\n", http.StatusForbidden},
		"an allowed Host header doesn't make a CONNECT":         {"CONNECT " + target + " HTTP/1.1\r\nHost: allowed.test\r\n\r\n", http.StatusForbidden},
		"a request with no target is refused":                   {"GET / HTTP/1.1\r\nHost: allowed.test\r\n\r\n", http.StatusBadRequest},
		"a CONNECT with no target is refused":                   {"CONNECT /x HTTP/1.1\r\nHost: allowed.test\r\n\r\n", http.StatusBadRequest},
		"a scheme other than http is refused":                   {"GET ftp://allowed.test/ HTTP/1.1\r\nHost: allowed.test\r\n\r\n", http.StatusBadRequest},
	}
	for name, tt := range denied {
		t.Run(name, func(t *testing.T) {
			proxy := start(t, egress.Options{Allow: []string{"allowed.test"}})
			if got := raw(t, proxy, tt.request); got != tt.want || hits.Load() != 0 {
				t.Fatalf("status = %d, hits = %d; want %d and none", got, hits.Load(), tt.want)
			}
		})
	}

	t.Run("a forged Host header doesn't reach an allowed target", func(t *testing.T) {
		proxy := start(t, egress.Options{Allow: []string{"127.0.0.1"}})
		if got := raw(t, proxy, "GET http://"+target+"/ HTTP/1.1\r\nHost: evil.test\r\nConnection: close\r\n\r\n"); got != http.StatusOK {
			t.Fatalf("status = %d", got)
		}
		if got := sawHost.Load(); got != target {
			t.Fatalf("the host saw Host: %v, want %s", got, target)
		}
	})
}

// upstream is a fake corporate proxy: it answers plain requests itself and
// tunnels every CONNECT to tunnelTo, whatever host was asked for. A name
// that only it can route therefore proves the connection went through it.
type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newUpstream(t *testing.T, tunnelTo string) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.seen = append(u.seen, r.Method+" "+r.URL.Host)
		u.mu.Unlock()
		if r.Method != http.MethodConnect {
			_, _ = io.WriteString(w, "via upstream")
			return
		}
		remote, err := net.Dial("tcp", tunnelTo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = remote.Close() }()
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = io.WriteString(client, "HTTP/1.1 200 OK\r\n\r\n")
		go func() { _, _ = io.Copy(remote, client) }()
		_, _ = io.Copy(client, remote)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) requests() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return strings.Join(u.seen, ", ")
}

// clearProxyEnv makes a test independent of the proxy of the machine it
// runs on.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
}

func tlsTarget(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secret garden")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wantChained checks that an allowed name reaches srv through up, over
// HTTPS and over plain HTTP.
func wantChained(t *testing.T, srv *httptest.Server, proxy *url.URL, up *upstream) {
	t.Helper()
	client := through(t, srv, proxy)

	resp, err := client.Get("https://example.com/")
	if err != nil {
		t.Fatalf("HTTPS through the upstream: %v", err)
	}
	if got := body(t, resp); got != "secret garden" {
		t.Fatalf("HTTPS body = %q", got)
	}
	resp, err = client.Get("http://example.com/")
	if err != nil {
		t.Fatalf("HTTP through the upstream: %v", err)
	}
	if got := body(t, resp); got != "via upstream" {
		t.Fatalf("HTTP body = %q", got)
	}
	if got, want := up.requests(), "CONNECT example.com:443, GET example.com"; got != want {
		t.Fatalf("the upstream saw %q, want %q", got, want)
	}
}

func TestChainsThroughTheHostProxy(t *testing.T) {
	clearProxyEnv(t)
	srv := tlsTarget(t)
	up := newUpstream(t, srv.Listener.Addr().String())
	t.Setenv("HTTPS_PROXY", up.URL)
	t.Setenv("HTTP_PROXY", up.URL)

	wantChained(t, srv, start(t, egress.Options{Allow: []string{"example.com"}}), up)
}

func TestUpstreamProxyOverridesTheHostProxy(t *testing.T) {
	clearProxyEnv(t)
	srv := tlsTarget(t)
	up := newUpstream(t, srv.Listener.Addr().String())
	wrong := newUpstream(t, srv.Listener.Addr().String())
	t.Setenv("HTTPS_PROXY", wrong.URL)
	t.Setenv("HTTP_PROXY", wrong.URL)
	t.Setenv("NO_PROXY", "example.com")

	wantChained(t, srv, start(t, egress.Options{Allow: []string{"example.com"}, UpstreamProxy: up.URL}), up)
	if got := wrong.requests(); got != "" {
		t.Fatalf("the host proxy saw %q, want nothing", got)
	}
}

// A .invalid name never resolves (RFC 6761), so these requests fail with no
// network: what matters is that the upstream never saw them.
func TestNoProxyHostsSkipTheHostProxy(t *testing.T) {
	clearProxyEnv(t)
	up := newUpstream(t, "127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", up.URL)
	t.Setenv("HTTP_PROXY", up.URL)
	t.Setenv("NO_PROXY", "direct.invalid")
	proxy := start(t, egress.Options{Allow: []string{"direct.invalid"}})

	for _, request := range []string{
		"CONNECT direct.invalid:443 HTTP/1.1\r\nHost: direct.invalid\r\n\r\n",
		"GET http://direct.invalid/ HTTP/1.1\r\nHost: direct.invalid\r\n\r\n",
	} {
		if got := raw(t, proxy, request); got != http.StatusBadGateway {
			t.Fatalf("%q: status = %d, want 502", request, got)
		}
	}
	if got := up.requests(); got != "" {
		t.Fatalf("the upstream saw %q, want nothing", got)
	}
}

func TestAnUpstreamThatRefusesIsReported(t *testing.T) {
	clearProxyEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "blocked by policy", http.StatusForbidden)
	}))
	defer up.Close()
	proxy := start(t, egress.Options{Allow: []string{"example.com"}, UpstreamProxy: up.URL})

	if got := raw(t, proxy, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com\r\n\r\n"); got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestBytesSentWithTheUpstreamsAnswerAreKept(t *testing.T) {
	clearProxyEnv(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = http.ReadRequest(bufio.NewReader(c))
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\nserver speaks first")
	}()
	proxy := start(t, egress.Options{Allow: []string{"example.com"}, UpstreamProxy: "http://" + l.Addr().String()})

	_, br := openTunnel(t, proxy, "example.com:22")
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("reading the tunnel: %v", err)
	}
	if string(got) != "server speaks first" {
		t.Fatalf("tunnel = %q", got)
	}
}

func TestStartRefusesABadUpstreamProxy(t *testing.T) {
	clearProxyEnv(t)
	for _, bad := range []string{"::not a url", "socks5://proxy.corp:1080", "http://", "http://user:hunter2@proxy.corp:8080"} {
		_, err := egress.Start(context.Background(), egress.Options{UpstreamProxy: bad})
		if err == nil || !strings.Contains(err.Error(), "upstream_proxy") || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("%q: err = %v, want one naming upstream_proxy without the password", bad, err)
		}
	}
}

func TestAHostProxyWithALoginIsRefusedForEveryConnection(t *testing.T) {
	clearProxyEnv(t)
	up := newUpstream(t, "127.0.0.1:1")
	withLogin := strings.Replace(up.URL, "http://", "http://user:hunter2@", 1)
	t.Setenv("HTTPS_PROXY", withLogin)
	t.Setenv("HTTP_PROXY", withLogin)
	proxy := start(t, egress.Options{Allow: []string{"example.com"}})

	for _, request := range []string{
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
	} {
		if got := raw(t, proxy, request); got != http.StatusBadGateway {
			t.Fatalf("%q: status = %d, want 502", request, got)
		}
	}
	if got := up.requests(); got != "" {
		t.Fatalf("the upstream saw %q, want nothing", got)
	}
}

func TestADeniedHostIsLoggedWhateverTheScheme(t *testing.T) {
	var log logged
	proxy := start(t, egress.Options{Allow: []string{"allowed.test"}, Log: log.log})
	if got := raw(t, proxy, "GET ftp://evil.test/ HTTP/1.1\r\nHost: evil.test\r\n\r\n"); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	if !strings.Contains(log.String(), "evil.test") {
		t.Fatalf("log = %q, want the host", log.String())
	}
}

func TestStartWithAnEndedContextDoesNotCrash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, err := egress.Start(ctx, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
}

// viaUpstream asks for http://host/ through a proxy that chains to a fake
// upstream, so no name has to resolve, and returns the status.
func viaUpstream(t *testing.T, o egress.Options, host string) int {
	t.Helper()
	proxy := start(t, o)
	return raw(t, proxy, "GET http://"+host+"/ HTTP/1.1\r\nHost: "+host+"\r\n\r\n")
}

func TestTheAllowlist(t *testing.T) {
	clearProxyEnv(t)
	up := newUpstream(t, "127.0.0.1:1")

	tests := map[string]struct {
		allow, endpoints []string
		host             string
		want             int
	}{
		"a listed host":                   {[]string{"registry.npmjs.org"}, nil, "registry.npmjs.org", http.StatusOK},
		"a Harness endpoint":              {nil, []string{"api.anthropic.com"}, "api.anthropic.com", http.StatusOK},
		"allow and endpoints both count":  {[]string{"a.test"}, []string{"b.test"}, "a.test", http.StatusOK},
		"names ignore case":               {[]string{"Registry.NPMjs.org"}, nil, "registry.npmjs.ORG", http.StatusOK},
		"a trailing dot is the same name": {[]string{"a.test"}, nil, "a.test.", http.StatusOK},
		"a port doesn't change the host":  {[]string{"a.test"}, nil, "a.test:8080", http.StatusOK},
		"nothing is on it by default":     {nil, nil, "api.anthropic.com", http.StatusForbidden},
		"a subdomain isn't its parent":    {[]string{"a.test"}, nil, "sub.a.test", http.StatusForbidden},
		"a longer name isn't a match":     {[]string{"a.test"}, nil, "nota.test", http.StatusForbidden},
		"a star opens egress fully":       {[]string{"*"}, nil, "anything.test", http.StatusOK},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := viaUpstream(t, egress.Options{Allow: tt.allow, Endpoints: tt.endpoints, UpstreamProxy: up.URL}, tt.host)
			if got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOptionsComeFromTheConfigAndTheAuthEndpoints(t *testing.T) {
	clearProxyEnv(t)
	up := newUpstream(t, "127.0.0.1:1")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, config.Dir), 0o750); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("version = 1\n[sandbox.network]\nallow = [\"registry.npmjs.org\"]\n[network]\nupstream_proxy = %q\n", up.URL)
	if err := os.WriteFile(filepath.Join(root, config.Dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	r := config.Load(root, nil)
	if !r.OK() {
		t.Fatalf("config: %v", r.Diagnostics)
	}
	o := egress.OptionsFrom(r.Values, []string{"llm.corp.test"})

	for host, want := range map[string]int{
		"registry.npmjs.org": http.StatusOK,
		"llm.corp.test":      http.StatusOK,
		"github.com":         http.StatusForbidden,
	} {
		if got := viaUpstream(t, o, host); got != want {
			t.Fatalf("%s: status = %d, want %d", host, got, want)
		}
	}
	if got := up.requests(); got != "GET registry.npmjs.org, GET llm.corp.test" && got != "GET llm.corp.test, GET registry.npmjs.org" {
		t.Fatalf("the upstream saw %q", got)
	}
}

// openTunnel returns a live tunnel through proxy to target, and the reader
// holding whatever already came through it.
func openTunnel(t *testing.T, proxy *url.URL, target string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c := dial(t, proxy)
	_, _ = io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("resp = %v, err = %v", resp, err)
	}
	return c, br
}

func TestStoppingCutsOpenTunnels(t *testing.T) {
	clearProxyEnv(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()

	wantCut := func(t *testing.T, c net.Conn) {
		t.Helper()
		_, err := c.Read(make([]byte, 1))
		var ne net.Error
		if err == nil || errors.As(err, &ne) && ne.Timeout() {
			t.Fatalf("read = %v, want the tunnel closed", err)
		}
	}
	o := egress.Options{Allow: []string{"127.0.0.1"}}

	t.Run("by Close", func(t *testing.T) {
		p, err := egress.Start(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := openTunnel(t, &url.URL{Host: p.Addr()}, l.Addr().String())
		_ = p.Close()
		wantCut(t, c)
	})

	t.Run("by the context ending", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p, err := egress.Start(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = p.Close() }()
		c, _ := openTunnel(t, &url.URL{Host: p.Addr()}, l.Addr().String())
		cancel()
		wantCut(t, c)

		deadline := time.Now().Add(5 * time.Second)
		for {
			probe, err := net.DialTimeout("tcp", p.Addr(), time.Second)
			if err != nil {
				return
			}
			_ = probe.Close()
			if time.Now().After(deadline) {
				t.Fatal("the proxy still accepts connections after its context ended")
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}
