package container

import (
	"encoding/csv"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Where a container Sandbox has what the Factory mounts into it. A
// `run_args` mount can't go on, under or above them.
const (
	workspaceDir = "/workspace"
	factoryDir   = "/kraftwerk"
)

// Labels on everything a Run creates.
const (
	labelRun     = "kraftwerk.run"
	labelProject = "kraftwerk.project"
)

// managed are the flags the Factory sets itself, with what breaks if
// `run_args` sets them as well.
var managed = map[string]string{
	"name":         "the Factory names the Sandbox",
	"rm":           "the Factory removes the Sandbox itself",
	"network":      "the Sandbox must stay on its own internal network",
	"net":          "the Sandbox must stay on its own internal network",
	"entrypoint":   "the Factory starts the Sandbox's first process",
	"pid":          "the Factory kills what a Step leaves behind by signalling every process the Sandbox can see",
	"init":         "the Factory's first process must stay the Sandbox's first process",
	"env-file":     "it can set credential variables, and its values would show in `inspect`",
	"env-host":     "it would copy the host's whole env, credentials included, into the Sandbox",
	"label-file":   "it can set the kraftwerk.* labels the Factory finds what it created by",
	"volumes-from": "it can mount over the workspace",
}

// shortFlags are the long names of the short flags that matter here; every
// other short flag that takes a value only needs skipping.
var shortFlags = map[byte]string{'e': "env", 'v': "volume", 'l': "label"}

// shortSwitches are the short flags of `create` that take no value.
const shortSwitches = "itdPq"

// CheckRunArgs checks `sandbox.run_args`. refused are the flags that
// collide with what the Factory manages, and warnings those that weaken
// the Sandbox, which stays the user's call. credentialEnv are the variables
// that hold credentials.
func CheckRunArgs(args, credentialEnv []string) (refused, warnings []string) {
	for _, f := range parseFlags(args) {
		if why, ok := managed[f.name]; ok {
			refused = append(refused, fmt.Sprintf("sandbox.run_args: `--%s` is refused: %s", f.name, why))
			continue
		}
		switch f.name {
		case "privileged":
			if f.value != "false" {
				warnings = append(warnings, "sandbox.run_args: `--privileged` lets the agent take over the machine the engine runs on")
			}
		case "env":
			name, _, _ := strings.Cut(f.value, "=")
			if strings.Contains(name, "*") {
				refused = append(refused, fmt.Sprintf("sandbox.run_args: `--env %s` is refused: a pattern can match a credential variable", name))
			} else if slices.ContainsFunc(credentialEnv, func(c string) bool { return strings.EqualFold(c, name) }) {
				refused = append(refused, fmt.Sprintf("sandbox.run_args: `--env %s` is refused: the Factory delivers that credential to each Step", name))
			}
		case "label":
			if strings.HasPrefix(f.value, "kraftwerk.") {
				refused = append(refused, fmt.Sprintf("sandbox.run_args: `--label %s` is refused: the Factory finds what it created by its kraftwerk.* labels", f.value))
			}
		case "volume", "mount", "tmpfs":
			source, target := mountOf(f)
			if collides(target) {
				refused = append(refused, fmt.Sprintf("sandbox.run_args: `--%s %s` is refused: it collides with the Factory's mounts at %s and %s", f.name, f.value, workspaceDir, factoryDir))
			} else if engineSocket(source) {
				warnings = append(warnings, fmt.Sprintf("sandbox.run_args: `--%s %s` gives the agent the container engine, and with it the machine", f.name, f.value))
			}
		}
	}
	return refused, warnings
}

// flag is one flag of `run_args`, by its long name where that matters.
type flag struct{ name, value string }

// parseFlags reads run_args the way the engines do. A long flag takes the
// next word as its value unless it is another flag, which errs on the side
// of seeing every flag.
func parseFlags(args []string) []flag {
	var flags []flag
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			name, value, hasValue := strings.Cut(a[2:], "=")
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value = args[i]
			}
			flags = append(flags, flag{name, value})
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				if strings.IndexByte(shortSwitches, a[j]) >= 0 {
					continue
				}
				value := strings.TrimPrefix(a[j+1:], "=") // the engines accept -e=X
				if value == "" && i+1 < len(args) {
					i++
					value = args[i]
				}
				if name, ok := shortFlags[a[j]]; ok {
					flags = append(flags, flag{name, value})
				}
				break
			}
		}
	}
	return flags
}

// mountOf is where a mount flag's mount comes from, "" for none, and where
// it goes.
func mountOf(f flag) (source, target string) {
	switch f.name {
	case "tmpfs":
		target, _, _ = strings.Cut(f.value, ":")
		return "", target
	case "mount":
		for _, field := range mountFields(f.value) {
			key, value, _ := strings.Cut(field, "=")
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "source", "src":
				source = value
			case "target", "dst", "destination":
				target = value
			}
		}
		return source, target
	}
	parts := strings.Split(f.value, ":")
	// A Windows source has a colon of its own.
	if len(parts) > 2 && len(parts[0]) == 1 {
		parts = append([]string{parts[0] + ":" + parts[1]}, parts[2:]...)
	}
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// mountFields splits a `--mount` value the way the engines do: as a CSV
// record, whose fields may be quoted.
func mountFields(value string) []string {
	fields, err := csv.NewReader(strings.NewReader(value)).Read()
	if err != nil {
		return strings.Split(value, ",")
	}
	return fields
}

// collides reports whether a mount at target would replace one of the
// Factory's: on, under or above them.
func collides(target string) bool {
	target = path.Clean(target)
	for _, dir := range []string{workspaceDir, factoryDir} {
		if target == dir || strings.HasPrefix(target, dir+"/") || strings.HasPrefix(dir, strings.TrimSuffix(target, "/")+"/") {
			return true
		}
	}
	return false
}

// engineSocket reports whether a mount of source exposes a container
// engine's socket, directly or through a directory that holds it.
func engineSocket(source string) bool {
	if source == "" {
		return false
	}
	clean := path.Clean(source)
	switch path.Base(clean) {
	case "docker.sock", "podman.sock":
		return true
	}
	return engineSocketDir.MatchString(clean)
}

// engineSocketDir matches the directories that hold an engine's socket,
// and those above them.
var engineSocketDir = regexp.MustCompile(`^/$|^(/var)?/run(/user(/\d+)?)?(/(docker|podman))?$`)
