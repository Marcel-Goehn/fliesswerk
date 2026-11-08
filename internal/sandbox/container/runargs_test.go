package container

import (
	"slices"
	"strings"
	"testing"
)

func TestRunArgsThatCollideWithWhatTheFactoryManagesAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"--name", "mine"},
		{"--name=mine"},
		{"--rm"},
		{"--network", "host"},
		{"--network=host"},
		{"--net", "host"},
		{"--entrypoint", "/bin/bash"},
		{"--pid=host"},
		{"--init"},
		{"--env-file", "secrets.env"},
		{"--volumes-from", "other"},
		{"-v", "/tmp/x:/workspace"},
		{"-v", "/tmp/x:/workspace/src:ro"},
		{"--volume=/tmp/x:/kraftwerk/git"},
		{"-itv", "/tmp/x:/workspace"},
		{"-itv/tmp/x:/workspace"},
		{"--mount", "type=bind,source=/tmp/x,target=/workspace"},
		{"--mount", "type=bind,src=/tmp/x,dst=/kraftwerk/git/hooks"},
		{"--mount=type=volume,destination=/workspace/a"},
		{"--tmpfs", "/workspace"},
		{"-v", "/tmp/x:/"},
		{"-e", "API_TOKEN=abc"},
		{"-eAPI_TOKEN"},
		{"--env", "API_TOKEN"},
		{"--env=api_token=abc"},
		{"--label", "kraftwerk.run=other"},
		{"-l", "kraftwerk.project=other"},
		{"--env-host"},
		{"--env", "API_*"},
		{"-e=API_TOKEN=abc"},
		{"-l=kraftwerk.run=other"},
		{"--label-file", "labels"},
		{"--mount", "TARGET=/workspace,type=tmpfs"},
		{"--mount", `type=bind,source=/tmp/x,"target=/workspace"`},
	} {
		refused, _ := CheckRunArgs(args, []string{"API_TOKEN"})
		if len(refused) != 1 {
			t.Errorf("CheckRunArgs(%q) refused %q, want one refusal", args, refused)
		}
	}
}

func TestRunArgsTheFactoryDoesNotManagePass(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--cpus", "2"},
		{"--memory=4g"},
		{"-m", "4g"},
		{"-it"},
		{"-e", "FOO=bar"},
		{"--label", "team=shop"},
		{"-v", "/tmp/cache:/cache"},
		{"-v", "/cache"},
		{"-v", `C:\cache:/cache`},
		{"--mount", "type=bind,source=/tmp/x,target=/workspaces"},
		{"--add-host", "db:10.0.0.1"},
	} {
		refused, warnings := CheckRunArgs(args, []string{"API_TOKEN"})
		if len(refused) != 0 || len(warnings) != 0 {
			t.Errorf("CheckRunArgs(%q) = refused %q, warnings %q; want neither", args, refused, warnings)
		}
	}
}

func TestPrivilegedAndEngineSocketMountsWarnButPass(t *testing.T) {
	for _, args := range [][]string{
		{"--privileged"},
		{"-v", "/var/run/docker.sock:/var/run/docker.sock"},
		{"-v", "/run/user/1000/podman/podman.sock:/engine.sock"},
		{"-v", "/var/run:/host-run"},
		{"--mount", "type=bind,source=/run/docker.sock,target=/engine.sock"},
		{"-v=/run:/host-run"},
		{"-v", "/run/user/1000:/host-run"},
		{"-v", "/run/user/1000/podman:/host-run"},
	} {
		refused, warnings := CheckRunArgs(args, nil)
		if len(refused) != 0 || len(warnings) != 1 {
			t.Errorf("CheckRunArgs(%q) = refused %q, warnings %q; want one warning", args, refused, warnings)
		}
	}
	if _, warnings := CheckRunArgs([]string{"--privileged=false"}, nil); len(warnings) != 0 {
		t.Errorf("--privileged=false warned: %q", warnings)
	}
}

func TestARefusalNamesTheFlagAndTheKey(t *testing.T) {
	refused, _ := CheckRunArgs([]string{"--network=host"}, nil)
	if len(refused) != 1 || !strings.Contains(refused[0], "--network") || !strings.Contains(refused[0], "sandbox.run_args") {
		t.Errorf("refused = %q", refused)
	}
}

func TestAVariableOfACommandIsNeverOnTheArgvAndThePlainOnesStayOffTheClient(t *testing.T) {
	b := &box{engine: Engine{Binary: docker, Connection: "default"}, name: "n", workdir: "/workspace", env: []string{"HOME=/h", "LD_PRELOAD=/x.so", "SHARED=plain"}}

	argv, hostEnv := b.Exec([]string{"true"}, []string{"DOCKER_PASSWORD=s3cret", "HOME_TOKEN=t0ken", "SHARED=from-the-step"})

	line := strings.Join(argv, " ")
	for _, leak := range []string{"s3cret", "t0ken", "from-the-step"} {
		if strings.Contains(line, leak) {
			t.Errorf("%q is on the argv: %s", leak, line)
		}
	}
	for _, want := range []string{"-e DOCKER_PASSWORD ", "-e HOME_TOKEN ", "-e SHARED ", "-e HOME=/h ", "-e LD_PRELOAD=/x.so "} {
		if !strings.Contains(line+" ", want) {
			t.Errorf("argv lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "SHARED=plain") {
		t.Errorf("a Step's own value didn't win: %s", line)
	}
	if !slices.Contains(hostEnv, "DOCKER_PASSWORD=s3cret") {
		t.Error("the client doesn't get the secret to pass on")
	}
	for _, plain := range []string{"LD_PRELOAD=/x.so", "HOME=/h"} {
		if slices.Contains(hostEnv, plain) {
			t.Errorf("%s reached the host's client", plain)
		}
	}
}
