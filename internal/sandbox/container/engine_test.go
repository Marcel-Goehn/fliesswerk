package container

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeCLI answers engine commands by the words they start with; anything
// else fails, as an engine that isn't there does.
type fakeCLI map[string]string

func (f fakeCLI) run(_ context.Context, argv ...string) (string, error) {
	line := strings.Join(argv, " ")
	for prefix, out := range f {
		if strings.HasPrefix(line, prefix) {
			return out, nil
		}
	}
	return "", errors.New("not there: " + line)
}

func noEnv(string) string { return "" }

// onPath finds every engine, as on a machine that has both installed.
func onPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func TestDockerIsPinnedToItsCurrentContext(t *testing.T) {
	e, err := resolve(context.Background(), "docker", noEnv, fakeCLI{
		"docker buildx version":        "github.com/docker/buildx 0.30.1\n",
		"docker context show":          "colima\n",
		"docker --context colima info": "[name=seccomp]\n",
	}.run, onPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.argv("ps"), []string{"docker", "--context", "colima", "ps"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if e.Rootless {
		t.Error("reported rootless")
	}
}

func TestRootlessDockerIsNoticed(t *testing.T) {
	e, err := resolve(context.Background(), "docker", noEnv, fakeCLI{
		"docker buildx version":         "github.com/docker/buildx 0.30.1\n",
		"docker context show":           "default\n",
		"docker --context default info": "[name=seccomp name=rootless]\n",
	}.run, onPath)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Rootless {
		t.Error("not reported rootless")
	}
}

func TestPodmanUsesItsDefaultConnection(t *testing.T) {
	e, err := resolve(context.Background(), "podman", noEnv, fakeCLI{
		"podman system connection list":         `[{"Name":"machine","Default":false},{"Name":"machine-root","Default":true}]`,
		"podman --connection machine-root info": "false\n",
	}.run, onPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.argv("ps"), []string{"podman", "--connection", "machine-root", "ps"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if e.Rootless {
		t.Error("a rootful connection reported rootless")
	}
}

func TestLocalRootlessPodmanNeedsNoConnection(t *testing.T) {
	e, err := resolve(context.Background(), "podman", noEnv, fakeCLI{
		"podman system connection list": `[]`,
		"podman info":                   "true\n",
	}.run, onPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.argv("ps"), []string{"podman", "ps"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if !e.Rootless {
		t.Error("not reported rootless")
	}
}

func TestPodmansEnvConnectionWins(t *testing.T) {
	env := func(k string) string {
		if k == "CONTAINER_CONNECTION" {
			return "work"
		}
		return ""
	}
	e, err := resolve(context.Background(), "podman", env, fakeCLI{"podman --connection work info": "true\n"}.run, onPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.argv("ps"), []string{"podman", "--connection", "work", "ps"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// bothRespond is a machine where Docker and Podman both answer.
var bothRespond = fakeCLI{
	"docker buildx version":         "github.com/docker/buildx 0.30.1\n",
	"docker context show":           "default\n",
	"docker --context default info": "[]\n",
	"podman system connection list": `[]`,
	"podman info":                   "true\n",
}

func TestTheConfiguredEngineIsUsedWhereBothRespond(t *testing.T) {
	for _, want := range []string{"docker", "podman"} {
		e, err := resolve(context.Background(), want, noEnv, bothRespond.run, onPath)
		if err != nil {
			t.Fatal(err)
		}
		if e.Binary != want {
			t.Errorf("configured %s, got %q", want, e.Binary)
		}
	}
}

func TestTheConfiguredEngineThatIsDownNeverFallsBack(t *testing.T) {
	_, err := resolve(context.Background(), "podman", noEnv, fakeCLI{
		"docker context show":           "default\n",
		"docker --context default info": "[]\n",
	}.run, onPath)
	if err == nil || !strings.Contains(err.Error(), "podman") || !strings.Contains(err.Error(), "sandbox.engine") {
		t.Errorf("err = %v, want one naming podman and sandbox.engine", err)
	}
}

func TestNoConfiguredEngineNamesTheKey(t *testing.T) {
	_, err := resolve(context.Background(), "", noEnv, bothRespond.run, onPath)
	if err == nil || !strings.Contains(err.Error(), "sandbox.engine") || !strings.Contains(err.Error(), "config.local.toml") {
		t.Errorf("err = %v, want one naming sandbox.engine in config.local.toml", err)
	}
}

func TestAnEngineThatIsNotInstalledNamesTheInstallCommand(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for engine, want := range map[string]string{"docker": "apt install docker.io", "podman": "apt install podman"} {
		_, err := resolve(context.Background(), engine, noEnv, bothRespond.run, missing)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want one with %q", engine, err, want)
		}
	}
}

func TestADockerWithoutBuildxNamesTheFix(t *testing.T) {
	_, err := resolve(context.Background(), "docker", noEnv, fakeCLI{
		"docker context show":           "default\n",
		"docker --context default info": "[]\n",
	}.run, onPath)

	if err == nil {
		t.Fatal("resolved a Docker without buildx")
	}
	for _, want := range []string{"buildx", "sudo apt install docker-buildx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "didn't respond") {
		t.Errorf("%q blames a Docker that responded", err)
	}
}

func TestABuildWithoutBuildKitNamesTheFix(t *testing.T) {
	err := explainBuildFailure(errors.New("docker build: exit status 1: the --mount option requires BuildKit. Refer to https://docs.docker.com/go/buildkit/"))

	for _, want := range []string{"buildx", "docker-buildx", "requires BuildKit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
}

func TestAnyOtherBuildFailureIsLeftAsItIs(t *testing.T) {
	cause := errors.New("docker build: exit status 1: no space left on device")

	if err := explainBuildFailure(cause); !errors.Is(err, cause) || err.Error() != cause.Error() {
		t.Errorf("got %v", err)
	}
}
