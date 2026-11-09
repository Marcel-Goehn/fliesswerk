package container

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// dockerConfig is what `docker compose config --format json
// --no-path-resolution` prints.
const dockerConfig = `{
  "name": "shop",
  "networks": {"default": {"name": "shop_default", "ipam": {}}, "back": {"name": "fixed-net"}},
  "services": {
    "cache": {"image": "redis:7-alpine", "networks": {"default": null}},
    "db": {
      "container_name": "shop-db",
      "depends_on": {"cache": {"condition": "service_started", "required": true}},
      "expose": ["6000"],
      "image": "postgres:17-alpine",
      "ports": [{"mode": "ingress", "target": 5432, "published": "5432", "protocol": "tcp"}],
      "volumes": [
        {"type": "volume", "source": "data", "target": "/var/lib/postgresql/data", "volume": {}},
        {"type": "bind", "source": "./init", "target": "/init", "bind": {"create_host_path": true}}
      ]
    },
    "unrelated": {"image": "x", "volumes": [{"type": "bind", "source": "/srv/real", "target": "/d"}]}
  },
  "volumes": {"data": {"name": "shop-data"}}
}`

// podmanConfig is what `podman-compose config` prints: the file as written,
// merged.
const podmanConfig = `
services:
  db:
    image: postgres:17-alpine
    container_name: shop-db
    depends_on:
      cache:
        condition: service_started
    expose:
    - "6000"
    ports:
    - 127.0.0.1:5432:5432/tcp
    - 8125:8125/udp
    volumes:
    - data:/var/lib/postgresql/data
    - ./init:/init
  cache:
    image: redis:7-alpine
    depends_on: []
volumes:
  data:
  named:
    name: shop-named
`

func mustParse(t *testing.T, config string) composeProject {
	t.Helper()
	cp, err := parseProject([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

func TestDockerComposeIsPinnedThroughTheEnginesConnection(t *testing.T) {
	var called [][]string
	run := func(_ context.Context, argv ...string) (string, error) {
		called = append(called, argv)
		return "v2.40.3+ds1-0ubuntu1\n", nil
	}
	tool, err := resolveCompose(context.Background(), Engine{Binary: docker, Connection: "ctx"}, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docker", "--context", "ctx", "compose"}; !slices.Equal(tool.argv, want) {
		t.Errorf("argv = %q, want %q", tool.argv, want)
	}
	if want := []string{"docker", "--context", "ctx", "compose", "version", "--short"}; !slices.Equal(called[0], want) {
		t.Errorf("asked %q", called[0])
	}
}

func TestPodmanComposeIsPinnedByPathAndGivenTheEnginesConnection(t *testing.T) {
	run := func(context.Context, ...string) (string, error) { return "1.4.0\n", nil }
	look := func(string) (string, error) { return "/opt/bin/podman-compose", nil }
	tool, err := resolveCompose(context.Background(), Engine{Binary: podman, Connection: "machine"}, run, look)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/opt/bin/podman-compose"}; !slices.Equal(tool.argv, want) {
		t.Errorf("argv = %q, want %q", tool.argv, want)
	}
	if want := []string{"CONTAINER_CONNECTION=machine"}; !slices.Equal(tool.env, want) {
		t.Errorf("env = %q, want %q", tool.env, want)
	}
}

func TestAComposeThatIsTooOldOrMissingIsAnErrorNamingTheVersionNeeded(t *testing.T) {
	old := func(v string) cli {
		return func(context.Context, ...string) (string, error) { return v, nil }
	}
	failing := func(context.Context, ...string) (string, error) { return "", errors.New("no such command") }
	found := func(string) (string, error) { return "/bin/podman-compose", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for _, c := range []struct {
		name string
		e    Engine
		run  cli
		look func(string) (string, error)
		want string
	}{
		{"old docker compose", Engine{Binary: docker}, old("2.24.3"), nil, "2.24.4"},
		{"no docker compose", Engine{Binary: docker}, failing, nil, "2.24.4"},
		{"old podman-compose", Engine{Binary: podman}, old("1.3.9"), found, "1.4.0"},
		{"no podman-compose", Engine{Binary: podman}, failing, missing, "1.4.0"},
	} {
		_, err := resolveCompose(context.Background(), c.e, c.run, c.look)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v doesn't name %s", c.name, err, c.want)
		}
	}
	if _, err := resolveCompose(context.Background(), Engine{Binary: docker}, old("v2.24.4\n"), nil); err != nil {
		t.Errorf("the lowest accepted version was refused: %v", err)
	}
}

func TestTheOverrideResetsWhatWouldCollideOnlyForTheServicesThatStart(t *testing.T) {
	cp := mustParse(t, dockerConfig)
	want := `services:
  "db":
    container_name: !reset null
    ports: !override
      - "127.0.0.1::5432"
      - "127.0.0.1::6000"
volumes:
  "data":
    name: "kw-1_data"
networks:
  "back":
    name: "kw-1_back"
  "default":
    name: "kw-1_default"
`
	if got := cp.override([]string{"db"}, "kw-1"); got != want {
		t.Errorf("override:\n%s\nwant:\n%s", got, want)
	}
}

func TestTheOverrideLeavesABareVolumeToComposesOwnPrefix(t *testing.T) {
	// podman-compose can't merge a mapping onto a volume with no body.
	got := mustParse(t, podmanConfig).override([]string{"db"}, "kw-1")
	if strings.Contains(got, `"data"`) {
		t.Errorf("a bare volume is in the override:\n%s", got)
	}
	if !strings.Contains(got, `"named":`+"\n"+`    name: "kw-1_named"`) {
		t.Errorf("a named volume wasn't renamed:\n%s", got)
	}
	for _, port := range []string{`"127.0.0.1::5432"`, `"127.0.0.1::8125/udp"`, `"127.0.0.1::6000"`} {
		if !strings.Contains(got, port) {
			t.Errorf("%s isn't published:\n%s", port, got)
		}
	}
}

func TestAServiceWithNothingToOverrideIsLeftOutOfTheOverride(t *testing.T) {
	// podman-compose can't merge an empty entry onto a Service either.
	got := mustParse(t, "services:\n  cache:\n    image: redis\n").override([]string{"cache"}, "kw-1")
	if got != "services: {}\n" {
		t.Errorf("override:\n%s", got)
	}
}

func TestComposeRulesRejectWhatCannotBeRenamed(t *testing.T) {
	for _, c := range []struct {
		name, config string
		want         []string
	}{
		{"external volume", `{"services": {"db": {"volumes": [{"type": "volume", "source": "d", "target": "/d"}]}}, "volumes": {"d": {"name": "real", "external": true}}}`, []string{`db`, `"d"`, "external"}},
		{"legacy external volume", "services:\n  db:\n    volumes: [\"d:/d\"]\nvolumes:\n  d:\n    external:\n      name: real\n", []string{`"d"`, "external"}},
		{"absolute bind, long", `{"services": {"db": {"volumes": [{"type": "bind", "source": "/srv/x", "target": "/d"}]}}}`, []string{"db", "/srv/x", ":ro"}},
		{"absolute bind, short", "services:\n  db:\n    volumes: [\"/srv/x:/d:rw\"]\n", []string{"/srv/x"}},
		{"home bind", "services:\n  db:\n    volumes: [\"~/x:/d\"]\n", []string{"~/x"}},
		{"bind out of the project", "services:\n  db:\n    volumes: [\"../x:/d\"]\n", []string{"../x"}},
	} {
		got := strings.Join(mustParse(t, c.config).problems([]string{"db"}, "."), "\n")
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q doesn't name %s", c.name, got, want)
			}
		}
	}
}

func TestAVolumeBackedByAHostDeviceIsRejected(t *testing.T) {
	config := `{"services": {"db": {"volumes": [{"type": "volume", "source": "d", "target": "/d"}]}}, "volumes": {"d": {"name": "x", "driver_opts": {"type": "none", "o": "bind", "device": "/srv/real"}}}}`
	got := strings.Join(mustParse(t, config).problems([]string{"db"}, "."), "\n")
	if !strings.Contains(got, "/srv/real") {
		t.Errorf("problems: %q", got)
	}
}

func TestABindMountIsJudgedFromTheProjectRootNotTheComposeFile(t *testing.T) {
	config := "services:\n  db:\n    volumes: [\"../src:/src\"]\n"
	if got := mustParse(t, config).problems([]string{"db"}, "deploy"); len(got) > 0 {
		t.Errorf("a path inside the project was refused: %q", got)
	}
	config = "services:\n  db:\n    volumes: [\"../../src:/src\"]\n"
	if got := mustParse(t, config).problems([]string{"db"}, "deploy"); len(got) == 0 {
		t.Error("a path outside the project was accepted")
	}
}

func TestComposeRulesAllowWhatStaysInTheSandboxesCopy(t *testing.T) {
	for name, config := range map[string]string{
		"read-only absolute, long":  `{"services": {"db": {"volumes": [{"type": "bind", "source": "/srv/x", "target": "/d", "read_only": true}]}}}`,
		"read-only absolute, short": "services:\n  db:\n    volumes: [\"/srv/x:/d:ro,z\"]\n",
		"relative bind":             "services:\n  db:\n    volumes: [\"./x/../y:/d\"]\n",
		"named volume":              "services:\n  db:\n    volumes: [\"d:/d\"]\nvolumes:\n  d:\n",
		"anonymous volume":          "services:\n  db:\n    volumes: [\"/var/lib/data\"]\n",
		"tmpfs":                     `{"services": {"db": {"volumes": [{"type": "tmpfs", "target": "/d"}]}}}`,
	} {
		if got := mustParse(t, config).problems([]string{"db"}, "."); len(got) > 0 {
			t.Errorf("%s: refused: %q", name, got)
		}
	}
}

func TestComposeRulesCoverWhatAServiceDependsOnAndIgnoreServicesThatNeverStart(t *testing.T) {
	config := "services:\n  db:\n    depends_on: [dep]\n  dep:\n    volumes: [\"/srv/dep:/d\"]\n  other:\n    volumes: [\"/srv/other:/d\"]\n"
	got := strings.Join(mustParse(t, config).problems([]string{"db"}, "."), "\n")
	if !strings.Contains(got, "/srv/dep") {
		t.Errorf("the mount of a Service it depends on was missed: %q", got)
	}
	if strings.Contains(got, "/srv/other") {
		t.Errorf("a Service that never starts was refused: %q", got)
	}
}

func TestAConfiguredServiceTheFileDoesNotHaveIsAProblem(t *testing.T) {
	got := strings.Join(mustParse(t, "services:\n  db:\n    image: x\n").problems([]string{"postgres"}, "."), "\n")
	if !strings.Contains(got, "services.postgres") {
		t.Errorf("problems: %q", got)
	}
}

func TestRunningContainersAreReadFromEitherProvidersOutput(t *testing.T) {
	want := []composeContainer{{Name: "p-db-1", Service: "db", Image: "postgres:17"}}
	for name, out := range map[string]string{
		"docker": `{"Name":"p-db-1","Names":"p-db-1","Service":"db","Image":"postgres:17","State":"running","Labels":"a=b"}
{"Name":"p-init-1","Names":"p-init-1","Service":"init","Image":"x","State":"exited","Labels":""}
`,
		"podman": `[{"Names":["p-db-1"],"Image":"postgres:17","State":"running","Labels":{"com.docker.compose.service":"db"}},
{"Names":["p-init-1"],"Image":"x","State":"exited","Labels":{"com.docker.compose.service":"init"}}]`,
		"podman, none running first": `[{"Names":["p-db-1"],"Image":"postgres:17","State":"running","Labels":{"com.docker.compose.service":"db"}}]`,
	} {
		entries, err := parsePS(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := runningOf(entries); !slices.Equal(got, want) {
			t.Errorf("%s: got %+v", name, got)
		}
	}
	if got, err := parsePS("\n"); err != nil || len(got) != 0 {
		t.Errorf("no output: %v, %v", got, err)
	}
}

func TestProjectNamesAreValidForComposeAndSeparateSandboxes(t *testing.T) {
	got := composeProjectName("/home/me/My Shop.git", "abc123", "build-GH#54")
	if want := "kraftwerk-my-shop-git-abc123-build-gh-54"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// recordingTool is a compose tool that records its calls and answers ps.
func recordingTool(binary string, podmanCompose bool, calls *[][]string) composeTool {
	return composeTool{
		argv: []string{binary}, podmanCompose: podmanCompose,
		run: func(_ context.Context, _ []string, argv ...string) (string, error) {
			*calls = append(*calls, argv)
			return "", nil
		},
	}
}

func TestDockerComposeGetsTheWaitAndProjectDirectoryFlags(t *testing.T) {
	var calls [][]string
	tool := recordingTool("dc", false, &calls)
	s := composeStack{Project: "kw-1", File: "/wt/deploy/compose.yaml", Override: "/state/o.yaml"}
	if err := tool.up(context.Background(), s, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"dc", "-p", "kw-1", "--project-directory", "/wt/deploy", "-f", "/wt/deploy/compose.yaml", "-f", "/state/o.yaml", "up", "--wait", "db"}
	if !slices.Equal(calls[0], want) {
		t.Errorf("up = %q, want %q", calls[0], want)
	}
}

func TestPodmanComposeGetsOnlyFlagsItHas(t *testing.T) {
	var calls [][]string
	tool := recordingTool("pc", true, &calls)
	s := composeStack{Project: "kw-1", File: "/wt/compose.yaml", Override: "/state/o.yaml"}
	if err := tool.up(context.Background(), s, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"pc", "-p", "kw-1", "-f", "/wt/compose.yaml", "-f", "/state/o.yaml", "up", "-d", "db"}
	if !slices.Equal(calls[0], want) {
		t.Errorf("up = %q, want %q", calls[0], want)
	}
}

func TestHealthDecidesWhetherAServiceIsReady(t *testing.T) {
	for _, c := range []struct {
		state        string
		ready, fails bool
	}{
		{"running 0 healthy", true, false},
		{"running 0 ", true, false},
		{"running 0", true, false},
		{"running 0 starting", false, false},
		{"created 0 ", false, false},
		{"running 0 unhealthy", false, true},
		{"exited 0 ", true, false},
		{"exited 3 ", false, true},
		{"stopped 1 ", false, true},
	} {
		ready, err := readiness(c.state)
		if ready != c.ready || (err != nil) != c.fails {
			t.Errorf("%q: ready %v, err %v", c.state, ready, err)
		}
	}
}

func TestExposedPortsAreSortedAndOnlyTCP(t *testing.T) {
	got := imagePorts(`{"9200/tcp":{},"53/udp":{},"80/tcp":{}}`)
	if want := []int{80, 9200}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	if got := imagePorts("null\n"); len(got) != 0 {
		t.Errorf("null gave %v", got)
	}
}

func TestAContestedLocalhostPortIsListedOnlyForTheServiceThatHasIt(t *testing.T) {
	services := []runningService{
		{name: "b", aliases: []string{"b", "old-b"}, ip: "10.0.0.2", ports: []int{80}},
		{name: "a", aliases: []string{"a"}, ip: "10.0.0.1", ports: []int{80, 81}},
	}
	var warned []string
	claimLocalhost(services, func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) })

	want := []sandbox.Service{
		{Name: "a", Addresses: []string{"a:80", "localhost:80", "a:81", "localhost:81"}},
		{Name: "b", Addresses: []string{"b:80", "old-b:80"}},
	}
	got := addresses(services)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addresses = %+v, want %+v", got, want)
	}
	if want := []string{"127.0.0.1:80=10.0.0.1:80", "127.0.0.1:81=10.0.0.1:81"}; !slices.Equal(forwardRules(services), want) {
		t.Errorf("rules = %q, want %q", forwardRules(services), want)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "localhost:80") {
		t.Errorf("warned %q", warned)
	}
}
