package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// fakeEngine answers a compose tool's calls by what they ask for, and records
// them.
type fakeEngine struct {
	config, ps string
	// inspect is the published ports of every container.
	inspect string
	upErr   error
	calls   []string
}

func (f *fakeEngine) run(_ context.Context, _ []string, argv ...string) (string, error) {
	line := strings.Join(argv, " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.Contains(line, " config"):
		return f.config, nil
	case strings.Contains(line, " up "):
		return "", f.upErr
	case strings.Contains(line, " ps "):
		return f.ps, nil
	case strings.Contains(line, "docker inspect"):
		return f.inspect, nil
	}
	return "", nil
}

func (f *fakeEngine) called(part string) bool {
	return slices.ContainsFunc(f.calls, func(line string) bool { return strings.Contains(line, part) })
}

func publishedOn(f *fakeEngine, stateDir string, services ...string) *Published {
	p := NewPublished(PublishedOptions{Root: "/work/shop", StateDir: stateDir, ComposeFile: "compose.yaml", Services: services})
	p.tool = composeTool{argv: []string{"docker", "compose"}, engine: Engine{Binary: docker}, run: f.run}
	return p
}

func TestAConfiguredServiceWithoutAPublishedPortIsRefusedWithTheFix(t *testing.T) {
	for name, config := range map[string]string{
		"no ports at all": "services:\n  db:\n    image: x\n",
		"only expose":     "services:\n  db:\n    expose: [\"5432\"]\n",
		"only a UDP port": "services:\n  db:\n    ports: [\"8125:8125/udp\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := publishedOn(&fakeEngine{config: config}, t.TempDir(), "db").Validate(context.Background())
			if err == nil {
				t.Fatal("accepted")
			}
			for _, says := range []string{"db", `ports: ["127.0.0.1:<H>:<C>"]`} {
				if !strings.Contains(err.Error(), says) {
					t.Errorf("doesn't say %q: %v", says, err)
				}
			}
		})
	}
}

func TestOnlyConfiguredServicesNeedAPublishedPort(t *testing.T) {
	config := "services:\n  db:\n    ports: [\"5432:5432\"]\n    depends_on: [helper]\n  helper:\n    image: x\n"
	if err := publishedOn(&fakeEngine{config: config}, t.TempDir(), "db").Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTheComposeRulesStillApplyToPublishedServices(t *testing.T) {
	config := "services:\n  db:\n    ports: [\"5432:5432\"]\n    volumes: [\"/srv/real:/d\"]\n"
	err := publishedOn(&fakeEngine{config: config}, t.TempDir(), "db").Validate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/srv/real") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartReadsBackThePortsOfTheConfiguredServicesAndStopRemovesThem(t *testing.T) {
	state := t.TempDir()
	f := &fakeEngine{
		config:  "services:\n  db:\n    container_name: shop-db\n    ports: [\"5432:5432\", \"8125:8125/udp\"]\n    depends_on: [helper]\n  helper:\n    ports: [\"9000:9000\"]\n",
		ps:      `{"Name":"p-db-1","Service":"db","State":"running"}` + "\n" + `{"Name":"p-helper-1","Service":"helper","State":"running"}`,
		inspect: `{"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":"40123"}],"8125/udp":[{"HostIp":"127.0.0.1","HostPort":"40124"}],"9000/tcp":null}`,
	}
	p := publishedOn(f, filepath.Dir(state), "db")

	got, stop, err := p.Start(context.Background(), state, sandbox.Request{Name: "build-1", Worktree: "/work/shop/tree"})
	if err != nil {
		t.Fatal(err)
	}

	want := []sandbox.PublishedService{{Name: "db", Aliases: []string{"db", "shop-db"}, Ports: []sandbox.PublishedPort{{Container: 5432, Host: 40123}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	for _, file := range []string{overrideFile, stackFile} {
		if _, err := os.Stat(filepath.Join(state, file)); err != nil {
			t.Error(err)
		}
	}
	if !f.called("-f /work/shop/tree/compose.yaml") || !f.called("up --wait db") {
		t.Errorf("calls: %q", f.calls)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.called("down --volumes") {
		t.Errorf("not removed: %q", f.calls)
	}
}

func TestAStackThatFailsToStartCanStillBeRemoved(t *testing.T) {
	f := &fakeEngine{config: "services:\n  db:\n    ports: [\"5432:5432\"]\n", upErr: errors.New("no space left")}
	_, stop, err := publishedOn(f, t.TempDir(), "db").Start(context.Background(), t.TempDir(), sandbox.Request{Name: "b", Worktree: "/w"})
	if err == nil || !strings.Contains(err.Error(), "no space left") {
		t.Fatalf("err = %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.called("down --volumes") {
		t.Errorf("left behind: %q", f.calls)
	}
}

func TestAServiceWithNoPortOnTheHostAfterStartingIsAnError(t *testing.T) {
	f := &fakeEngine{
		config:  "services:\n  db:\n    network_mode: host\n    ports: [\"5432:5432\"]\n",
		ps:      `{"Name":"p-db-1","Service":"db","State":"running"}`,
		inspect: `{}`,
	}
	_, _, err := publishedOn(f, t.TempDir(), "db").Start(context.Background(), t.TempDir(), sandbox.Request{Name: "b", Worktree: "/w"})
	if err == nil || !strings.Contains(err.Error(), "db") || !strings.Contains(err.Error(), "no port published") {
		t.Fatalf("err = %v", err)
	}
}

func TestAConfiguredServiceThatIsNotRunningIsAnError(t *testing.T) {
	f := &fakeEngine{config: "services:\n  db:\n    ports: [\"5432:5432\"]\n", ps: ""}
	_, _, err := publishedOn(f, t.TempDir(), "db").Start(context.Background(), t.TempDir(), sandbox.Request{Name: "b", Worktree: "/w"})
	if err == nil || !strings.Contains(err.Error(), "db") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownLeftRemovesTheStacksACrashedRunRecorded(t *testing.T) {
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "sandboxes", "build-1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	record := `{"Project":"kraftwerk-shop-abc-build-1","File":"/w/compose.yaml","Override":"/o.yaml"}`
	if err := os.WriteFile(filepath.Join(dir, stackFile), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeEngine{}
	if err := publishedOn(f, stateDir, "db").DownLeft(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.called("-p kraftwerk-shop-abc-build-1") || !f.called("down --volumes") {
		t.Errorf("calls: %q", f.calls)
	}
}
