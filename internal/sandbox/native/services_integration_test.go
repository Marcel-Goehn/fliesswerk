//go:build integration && (linux || darwin)

package native

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// fakeServices stands in for the compose project: the Services are local TCP
// listeners, on the macOS runners too, which have no container engine.
type fakeServices struct {
	running     []sandbox.PublishedService
	shared      []sandbox.PublishedService
	validateErr error
	downLeftErr error
	stopErr     error
	downLeft    int
	stopped     int
	// stoppedShared counts removals of the shared Services, and own is what
	// the Sandbox's own Services hold.
	stoppedShared int
	own           int64
}

func (f *fakeServices) Connect(context.Context) error  { return nil }
func (f *fakeServices) DownLeft(context.Context) error { f.downLeft++; return f.downLeftErr }
func (f *fakeServices) Validate(context.Context) error { return f.validateErr }
func (f *fakeServices) Start(context.Context, string, sandbox.Request) ([]sandbox.PublishedService, func(context.Context) error, error) {
	return f.running, func(context.Context) error { f.stopped++; return f.stopErr }, nil
}

func (f *fakeServices) StartShared(context.Context, string, sandbox.Request) ([]sandbox.PublishedService, func(context.Context) error, error) {
	return f.shared, func(context.Context) error { f.stoppedShared++; return nil }, nil
}

func (f *fakeServices) MemoryUse(context.Context, string) (int64, error) { return f.own, nil }

// listener is a Service on the host that answers with its name, on a random
// port.
func listener(t *testing.T, name string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) }))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// unpublishedPort is a port on the host with a listener nothing published.
func unpublishedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func fetch(address string) string {
	return "curl -s --max-time 10 http://" + address + "/"
}

func TestARunStartStopsOnServicesItCannotReach(t *testing.T) {
	pr := newProject(t, nil, nil)
	pr.p.opts.Services = &fakeServices{validateErr: errors.New("Service db publishes no port")}

	err := pr.p.Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "publishes no port") {
		t.Fatalf("Prepare: %v", err)
	}
}

func TestDestroyingASandboxStopsItsServices(t *testing.T) {
	f := &fakeServices{running: []sandbox.PublishedService{{Name: "db", Aliases: []string{"db"}, Ports: []sandbox.PublishedPort{{Container: 5432, Host: listener(t, "db")}}}}}
	pr := newProject(t, nil, func(o *Options) { o.Services = f })
	sb := pr.open(t)

	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.stopped != 1 {
		t.Errorf("stopped %d times", f.stopped)
	}
}

func TestServicesThatCannotBeRemovedKeepTheirRecord(t *testing.T) {
	f := &fakeServices{stopErr: errors.New("engine hiccup")}
	pr := newProject(t, nil, func(o *Options) { o.Services = f })
	sb := pr.open(t)
	record := filepath.Join(pr.root, ".kraftwerk", "state", "sandboxes", "build-1", "compose-stack.json")
	writeFile(t, record, "{}")

	if err := sb.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy reported nothing")
	}
	if _, err := os.Stat(record); err != nil {
		t.Errorf("the next Run can't find them: %v", err)
	}
}

func TestACrashedRunsServicesGoBeforeTheirRecords(t *testing.T) {
	f := &fakeServices{}
	pr := newProject(t, nil, func(o *Options) { o.Services = f })
	record := filepath.Join(pr.root, ".kraftwerk", "state", "sandboxes", "build-1", "compose-stack.json")
	writeFile(t, record, "{}")

	f.downLeftErr = errors.New("engine hiccup")
	if err := pr.p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("a record of what is still to remove went: %v", err)
	}

	f.downLeftErr = nil
	if err := pr.p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("the record stayed: %v", err)
	}
}

func TestEverySandboxReachesTheSharedServicesAndTheyOutliveIt(t *testing.T) {
	f := &fakeServices{shared: []sandbox.PublishedService{{Name: "search", Aliases: []string{"search"}, Ports: []sandbox.PublishedPort{{Container: 9200, Host: listener(t, "search")}}}}}
	pr := newProject(t, nil, func(o *Options) { o.Services = f })
	stop, err := pr.p.StartShared(context.Background(), sandbox.Request{Name: "shared", Worktree: pr.worktree})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: "build-1", Worktree: pr.worktree})
	if err != nil {
		t.Fatal(err)
	}
	address := sb.Services()[0].Addresses[0]

	if got := mustSh(t, sb, fetch(address)); got != "search" {
		t.Errorf("through %s: %q", address, got)
	}
	if got := mustSh(t, sb, `printf %s "$KRAFTWERK_SANDBOX"`); got != "build-1" {
		t.Errorf("KRAFTWERK_SANDBOX = %q", got)
	}
	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.stoppedShared != 0 {
		t.Error("the shared Services went with a Sandbox")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.stoppedShared != 1 {
		t.Errorf("stopped %d times", f.stoppedShared)
	}
}

func TestASandboxsMemoryIsItsCommandsAndItsOwnServices(t *testing.T) {
	f := &fakeServices{own: 1 << 30}
	pr := newProject(t, nil, func(o *Options) { o.Services = f })
	sb := pr.open(t)
	go func() { _, _ = sh(sb, "sleep 3") }()

	var use int64
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end) && use <= 1<<30; time.Sleep(50 * time.Millisecond) {
		got, err := sb.MemoryUse(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		use = got
	}

	if use <= 1<<30 {
		t.Errorf("MemoryUse = %d, want the Services' 1 GiB and the running command's memory on top", use)
	}
}
