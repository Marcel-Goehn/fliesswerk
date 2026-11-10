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

func TestDockerIsPinnedToItsCurrentContext(t *testing.T) {
	e, err := resolve(context.Background(), noEnv, fakeCLI{
		"docker context show":          "colima\n",
		"docker --context colima info": "[name=seccomp]\n",
	}.run)
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
	e, err := resolve(context.Background(), noEnv, fakeCLI{
		"docker context show":           "default\n",
		"docker --context default info": "[name=seccomp name=rootless]\n",
	}.run)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Rootless {
		t.Error("not reported rootless")
	}
}

func TestPodmanIsTheFallbackAndUsesItsDefaultConnection(t *testing.T) {
	e, err := resolve(context.Background(), noEnv, fakeCLI{
		"podman system connection list":         `[{"Name":"machine","Default":false},{"Name":"machine-root","Default":true}]`,
		"podman --connection machine-root info": "false\n",
	}.run)
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
	e, err := resolve(context.Background(), noEnv, fakeCLI{
		"podman system connection list": `[]`,
		"podman info":                   "true\n",
	}.run)
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
	e, err := resolve(context.Background(), env, fakeCLI{"podman --connection work info": "true\n"}.run)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.argv("ps"), []string{"podman", "--connection", "work", "ps"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestTheEngineCanBeChosen(t *testing.T) {
	env := func(k string) string {
		if k == EngineEnv {
			return "podman"
		}
		return ""
	}
	e, err := resolve(context.Background(), env, fakeCLI{
		"docker context show":           "default\n",
		"docker --context default info": "[]\n",
		"podman system connection list": `[]`,
		"podman info":                   "true\n",
	}.run)
	if err != nil {
		t.Fatal(err)
	}
	if e.Binary != "podman" {
		t.Errorf("Binary = %q, want podman", e.Binary)
	}
}

func TestAChosenEngineThatIsDownNeverFallsBack(t *testing.T) {
	env := func(k string) string {
		if k == EngineEnv {
			return "podman"
		}
		return ""
	}
	_, err := resolve(context.Background(), env, fakeCLI{
		"docker context show":           "default\n",
		"docker --context default info": "[]\n",
	}.run)
	if err == nil || !strings.Contains(err.Error(), "podman") {
		t.Errorf("err = %v, want one naming podman", err)
	}
}

func TestAnUnknownEngineIsAnError(t *testing.T) {
	env := func(k string) string {
		if k == EngineEnv {
			return "nerdctl"
		}
		return ""
	}
	if _, err := resolve(context.Background(), env, fakeCLI{}.run); err == nil || !strings.Contains(err.Error(), "nerdctl") {
		t.Errorf("err = %v, want one naming nerdctl", err)
	}
}

func TestNoEngineNamesTheFix(t *testing.T) {
	_, err := resolve(context.Background(), noEnv, fakeCLI{}.run)
	if err == nil || !strings.Contains(err.Error(), "Docker") || !strings.Contains(err.Error(), "Podman") {
		t.Errorf("err = %v, want one naming both engines", err)
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
