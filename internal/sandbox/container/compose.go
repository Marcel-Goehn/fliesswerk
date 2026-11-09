package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/version"
	"go.yaml.in/yaml/v3"
)

// The oldest compose tools with the `!reset` and `!override` tags the
// Factory's override file needs.
const (
	minDockerCompose = "2.24.4"
	minPodmanCompose = "1.4.0"
)

// healthPoll is the wait between looks at a podman-compose project's health.
const healthPoll = time.Second

// composeTool is the one compose program a Run uses, pinned at its start.
type composeTool struct {
	// argv starts every call, and env is added to the host's for it.
	argv, env []string
	// podmanCompose reports podman-compose, which has neither `up --wait`
	// (before 1.6) nor `--project-directory`, and prints other formats.
	podmanCompose bool
	engine        Engine
	run           func(ctx context.Context, env []string, argv ...string) (string, error)
}

// resolveCompose finds the compose tool for the engine and enforces its
// version. Docker's plugin goes through the engine's context. For Podman,
// `podman compose` would pick whichever provider it finds first, so
// podman-compose is called directly, by path, and told the connection.
func resolveCompose(ctx context.Context, e Engine, run cli, look func(string) (string, error)) (composeTool, error) {
	tool := composeTool{engine: e, run: runCLIWith}
	var have, want, name string
	if e.Binary == docker {
		name, want = "Docker Compose", minDockerCompose
		tool.argv = e.argv("compose")
		out, err := run(ctx, append(slices.Clone(tool.argv), "version", "--short")...)
		if err != nil {
			return composeTool{}, fmt.Errorf("the project's Services need %s %s or newer, and `docker compose` didn't answer: %w", name, want, err)
		}
		have = out
	} else {
		name, want = "podman-compose", minPodmanCompose
		found, err := look("podman-compose")
		if err != nil {
			return composeTool{}, fmt.Errorf("the project's Services need %s %s or newer on the PATH: %w. %s", name, want, err, composeInstall)
		}
		out, err := run(ctx, found, "version", "--short")
		if err != nil {
			return composeTool{}, fmt.Errorf("the project's Services need %s %s or newer, and it didn't answer: %w. %s", name, want, err, composeInstall)
		}
		have = out
		tool.argv, tool.podmanCompose = []string{found}, true
		if e.Connection != "" {
			tool.env = []string{"CONTAINER_CONNECTION=" + e.Connection}
		}
	}
	have = strings.TrimSpace(have)
	if !version.AtLeast(have, want) {
		return composeTool{}, fmt.Errorf("the project's Services need %s %s or newer; this is %s. %s", name, want, have, composeInstall)
	}
	return tool, nil
}

// composeInstall says how to get a compose tool the Factory can use.
const composeInstall = "For Docker, see https://docs.docker.com/compose/install/; for Podman, `pipx install podman-compose`"

// composeStack is one Sandbox's Compose project. It is written to the
// runtime state, so that a later Run can remove what a crashed one left.
type composeStack struct {
	Project, File, Override string
}

// global are the flags that select a project. The project directory is the
// compose file's own, where relative paths in it mean what they mean to the
// project, inside the Sandbox's worktree. podman-compose takes it from the
// first file.
func (c composeTool) global(s composeStack) []string {
	args := slices.Concat(c.argv, []string{"-p", s.Project})
	if !c.podmanCompose {
		args = append(args, "--project-directory", filepath.Dir(s.File))
	}
	return append(args, "-f", s.File, "-f", s.Override)
}

// config reads the compose file as the tool understands it. Docker's paths
// stay as written, or an absolute bind mount couldn't be told from a
// relative one.
func (c composeTool) config(ctx context.Context, file string) (composeProject, error) {
	var args []string
	if c.podmanCompose {
		args = slices.Concat(c.argv, []string{"-f", file, "config"})
	} else {
		args = slices.Concat(c.argv, []string{"--project-directory", filepath.Dir(file), "-f", file, "config", "--format", "json", "--no-path-resolution"})
	}
	out, err := c.run(ctx, c.env, args...)
	if err != nil {
		return composeProject{}, fmt.Errorf("reading the compose file: %w", err)
	}
	return parseProject([]byte(out))
}

// validate reads the compose file and refuses what the Factory can't run:
// what it can't give each Sandbox a copy of, and a dependency across the line
// between own and shared Services. root is the project root, in the checkout
// or a worktree, and composeFile relative to it.
func (c composeTool) validate(ctx context.Context, root, composeFile string, own, shared []string) (composeProject, error) {
	cp, err := c.config(ctx, filepath.Join(root, composeFile))
	if err != nil {
		return composeProject{}, err
	}
	problems := cp.problems(slices.Concat(own, shared), filepath.ToSlash(filepath.Dir(composeFile)))
	problems = append(problems, cp.crossDependencies(own, shared)...)
	if len(problems) > 0 {
		return composeProject{}, errors.New(strings.Join(problems, "\n"))
	}
	return cp, nil
}

// up starts the Services and returns once they are ready.
func (c composeTool) up(ctx context.Context, s composeStack, services []string) error {
	how := []string{"up", "--wait"}
	if c.podmanCompose {
		how = []string{"up", "-d"}
	}
	if _, err := c.run(ctx, c.env, slices.Concat(c.global(s), how, services)...); err != nil {
		return fmt.Errorf("starting the Services: %w", err)
	}
	if c.podmanCompose {
		return c.waitReady(ctx, s)
	}
	return nil
}

// waitReady is `up --wait` for podman-compose.
func (c composeTool) waitReady(ctx context.Context, s composeStack) error {
	entries, err := c.list(ctx, s)
	if err != nil {
		return err
	}
	format := "{{.State.Status}} {{.State.ExitCode}} {{with .State.Health}}{{.Status}}{{end}}"
	for _, entry := range entries {
		name := entry.name()
		for {
			out, err := c.run(ctx, c.env, c.engine.argv("inspect", "--format", format, name)...)
			if err != nil {
				return fmt.Errorf("waiting for the Services: %w", err)
			}
			ready, err := readiness(out)
			if err != nil {
				return fmt.Errorf("the Service container %s %w", name, err)
			}
			if ready {
				break
			}
			select {
			case <-time.After(healthPoll):
			case <-ctx.Done():
				return fmt.Errorf("waiting for the Service container %s: %w", name, ctx.Err())
			}
		}
	}
	return nil
}

// readiness reads a container's "<status> <exit code> <health>". A one-shot
// container that finished cleanly counts as ready, as it does for `--wait`.
func readiness(state string) (bool, error) {
	fields := strings.Fields(state)
	if len(fields) < 2 {
		return false, nil
	}
	status, code, health := fields[0], fields[1], ""
	if len(fields) > 2 {
		health = fields[2]
	}
	switch status {
	case "exited", "stopped":
		if code != "0" {
			return false, fmt.Errorf("exited with code %s", code)
		}
		return true, nil
	case "running":
		switch health {
		case "unhealthy":
			return false, errors.New("is unhealthy")
		case "starting":
			return false, nil
		}
		return true, nil
	}
	return false, nil
}

// list is every container of the project, running or not.
func (c composeTool) list(ctx context.Context, s composeStack) ([]psEntry, error) {
	out, err := c.run(ctx, c.env, append(c.global(s), "ps", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("finding the Service containers: %w", err)
	}
	return parsePS(out)
}

// running are the project's running containers.
func (c composeTool) running(ctx context.Context, s composeStack) ([]composeContainer, error) {
	entries, err := c.list(ctx, s)
	if err != nil {
		return nil, err
	}
	return runningOf(entries), nil
}

// down removes the project, volumes included, at once: its data is
// throwaway.
func (c composeTool) down(ctx context.Context, s composeStack) error {
	if _, err := c.run(ctx, c.env, append(c.global(s), "down", "--volumes", "--timeout", "0")...); err != nil {
		return fmt.Errorf("removing the Services: %w", err)
	}
	return nil
}

// composeContainer is one running container of a project.
type composeContainer struct {
	Name, Service, Image string
}

// psEntry is one line of `ps --format json`. Names and Labels are a string
// from Docker Compose and a list and a map from podman-compose.
type psEntry struct {
	Name    string
	Names   any
	Service string
	Image   string
	State   string
	Labels  any
}

func (e psEntry) name() string {
	if e.Name != "" {
		return e.Name
	}
	if names, ok := e.Names.([]any); ok && len(names) > 0 {
		name, _ := names[0].(string)
		return name
	}
	return ""
}

func (e psEntry) service() string {
	if e.Service != "" {
		return e.Service
	}
	labels, _ := e.Labels.(map[string]any)
	service, _ := labels["com.docker.compose.service"].(string)
	return service
}

// parsePS reads either provider's output: Docker Compose prints a JSON
// object per line, and podman-compose one array.
func parsePS(out string) ([]psEntry, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var entries []psEntry
	if strings.HasPrefix(out, "[") {
		if err := json.Unmarshal([]byte(out), &entries); err != nil {
			return nil, fmt.Errorf("reading the Service containers: %w", err)
		}
		return entries, nil
	}
	for line := range strings.Lines(out) {
		var e psEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("reading the Service containers: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func runningOf(entries []psEntry) []composeContainer {
	var out []composeContainer
	for _, e := range entries {
		if e.State == "running" {
			out = append(out, composeContainer{Name: e.name(), Service: e.service(), Image: e.Image})
		}
	}
	return out
}

// imagePorts are the TCP ports in an image's `.Config.ExposedPorts`, given
// as JSON, in order.
func imagePorts(exposed string) []int {
	var m map[string]any
	if json.Unmarshal([]byte(exposed), &m) != nil {
		return nil
	}
	var ports []int
	for _, key := range slices.Sorted(maps.Keys(m)) {
		number, proto, _ := strings.Cut(key, "/")
		if n, err := strconv.Atoi(number); err == nil && proto != "udp" {
			ports = append(ports, n)
		}
	}
	slices.Sort(ports)
	return ports
}

var unsafeProjectChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// composeProjectName is `kraftwerk-<repo dir>-<hash>-<sandbox>`, in the
// characters Compose accepts.
func composeProjectName(root, hash, sandbox string) string {
	clean := func(s string) string { return unsafeProjectChars.ReplaceAllString(strings.ToLower(s), "-") }
	return "kraftwerk-" + clean(filepath.Base(root)) + "-" + hash + "-" + clean(sandbox)
}

// composeResource is a top-level volume or network.
type composeResource struct {
	Name       string         `yaml:"name"`
	External   any            `yaml:"external"`
	DriverOpts map[string]any `yaml:"driver_opts"`
}

// device is what a volume is backed by on the host, "" for the engine's
// own storage.
func (r *composeResource) device() string {
	if r == nil {
		return ""
	}
	device, _ := r.DriverOpts["device"].(string)
	return device
}

func (r *composeResource) external() bool {
	return r != nil && r.External != nil && r.External != false
}

// composeService is the part of a Service the Factory looks at. Its lists
// have a short and a long form.
type composeService struct {
	ContainerName string `yaml:"container_name"`
	Ports         []any  `yaml:"ports"`
	Expose        []any  `yaml:"expose"`
	Volumes       []any  `yaml:"volumes"`
	DependsOn     any    `yaml:"depends_on"`
}

// aliases are the names the Service called name answers to, name first. Its
// old container_name stays a name the project's code can use.
func (s composeService) aliases(name string) []string {
	if s.ContainerName != "" && s.ContainerName != name {
		return []string{name, s.ContainerName}
	}
	return []string{name}
}

// composeProject is a compose file as the tool resolved it.
type composeProject struct {
	Services map[string]composeService   `yaml:"services"`
	Volumes  map[string]*composeResource `yaml:"volumes"`
	Networks map[string]*composeResource `yaml:"networks"`
}

// parseProject reads `compose config` output. JSON is YAML, so one parser
// takes both providers' output.
func parseProject(data []byte) (composeProject, error) {
	var cp composeProject
	if err := yaml.Unmarshal(data, &cp); err != nil {
		return composeProject{}, fmt.Errorf("reading the compose file: %w", err)
	}
	return cp, nil
}

// started are the Services that `up` of these names starts: them and what
// they depend on, sorted.
func (cp composeProject) started(names []string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		for _, dep := range dependencies(cp.Services[name].DependsOn) {
			visit(dep)
		}
	}
	for _, name := range names {
		visit(name)
	}
	return slices.Sorted(maps.Keys(seen))
}

// dependencies reads `depends_on`, a list or a map.
func dependencies(v any) []string {
	switch deps := v.(type) {
	case []any:
		var out []string
		for _, d := range deps {
			if s, ok := d.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		return slices.Collect(maps.Keys(deps))
	}
	return nil
}

// mountSpec is one entry of a Service's `volumes`.
type mountSpec struct {
	bind, readOnly bool
	source         string
}

// parseMount reads the long or short form of a mount.
func parseMount(v any) mountSpec {
	switch m := v.(type) {
	case map[string]any:
		source, _ := m["source"].(string)
		kind, _ := m["type"].(string)
		readOnly, _ := m["read_only"].(bool)
		return mountSpec{bind: kind == "bind", readOnly: readOnly, source: source}
	case string:
		parts := strings.Split(m, ":")
		if len(parts) < 2 { // only a target: an anonymous volume
			return mountSpec{}
		}
		spec := mountSpec{source: parts[0], bind: strings.HasPrefix(parts[0], ".") || strings.HasPrefix(parts[0], "/") || strings.HasPrefix(parts[0], "~")}
		if len(parts) > 2 {
			spec.readOnly = slices.Contains(strings.Split(parts[2], ","), "ro")
		}
		return spec
	}
	return mountSpec{}
}

// outsideProject reports whether a bind mount's source is real data on the
// host rather than a path in the Sandbox's worktree. A relative source
// starts at composeDir, the compose file's directory in the project.
func outsideProject(source, composeDir string) bool {
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") {
		return true
	}
	clean := path.Join(composeDir, source)
	return clean == ".." || strings.HasPrefix(clean, "../")
}

// problems says what can't be used in the compose file, for the Services
// that would start. An empty result means the file is fine.
func (cp composeProject) problems(names []string, composeDir string) []string {
	var out []string
	for _, name := range names {
		if _, ok := cp.Services[name]; !ok {
			out = append(out, fmt.Sprintf("services.%s: the compose file has no Service %q", name, name))
		}
	}
	for _, name := range cp.started(names) {
		for _, v := range cp.Services[name].Volumes {
			m := parseMount(v)
			switch {
			case m.bind && !m.readOnly && outsideProject(m.source, composeDir):
				out = append(out, fmt.Sprintf("compose file: Service %s mounts %s writable, which would be real data on this machine; add `:ro`, or use a named volume or a path inside the project", name, m.source))
			case !m.bind && cp.Volumes[m.source].external():
				out = append(out, fmt.Sprintf("compose file: Service %s uses volume %q, which is external, so every Sandbox would share real data; use a volume the project defines", name, m.source))
			case !m.bind && cp.Volumes[m.source].device() != "":
				out = append(out, fmt.Sprintf("compose file: Service %s uses volume %q, which is backed by %s, so every Sandbox would share real data; use a volume without `driver_opts.device`", name, m.source, cp.Volumes[m.source].device()))
			}
		}
	}
	return out
}

// crossDependencies are the `depends_on`s from one side of own and shared to
// the other: a Service exists once per Sandbox or once per Run, and Compose
// can only wait for what is in its own project. A helper neither names is
// fine, and simply starts on each side that needs it.
func (cp composeProject) crossDependencies(own, shared []string) []string {
	var out []string
	check := func(from, to []string, fromKind, toKind string) {
		for _, name := range cp.started(from) {
			for _, dep := range slices.Sorted(slices.Values(dependencies(cp.Services[name].DependsOn))) {
				if slices.Contains(to, dep) {
					out = append(out, fmt.Sprintf("compose file: the %s Service %s has `depends_on: %s`, a %s Service; make both shared or neither", fromKind, name, dep, toKind))
				}
			}
		}
	}
	check(own, shared, "own", "shared")
	check(shared, own, "shared", "own")
	return out
}

// port is a port of a Service.
type port struct {
	number int
	udp    bool
}

// declaredPorts are the ports a Service's `ports` and `expose` name, in
// that order, without repeats.
func (s composeService) declaredPorts() []port {
	var out []port
	for _, v := range slices.Concat(s.Ports, s.Expose) {
		for _, p := range parsePorts(v) {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// parsePorts reads an entry of `ports` or `expose`, in the container's
// numbers: 5432, "8000-8001", "127.0.0.1:5432:5432/tcp" or the long form.
func parsePorts(v any) []port {
	switch e := v.(type) {
	case int:
		return []port{{number: e}}
	case map[string]any:
		udp := e["protocol"] == "udp"
		switch target := e["target"].(type) {
		case int:
			return []port{{number: target, udp: udp}}
		case string:
			return portRange(target, udp)
		}
	case string:
		spec, proto, _ := strings.Cut(e, "/")
		return portRange(spec[strings.LastIndexByte(spec, ':')+1:], proto == "udp")
	}
	return nil
}

func portRange(spec string, udp bool) []port {
	first, last, isRange := strings.Cut(spec, "-")
	if !isRange {
		last = first
	}
	from, err1 := strconv.Atoi(first)
	to, err2 := strconv.Atoi(last)
	if err1 != nil || err2 != nil {
		return nil
	}
	var out []port
	for n := from; n <= to; n++ {
		out = append(out, port{number: n, udp: udp})
	}
	return out
}

// override is the Factory's own compose file, layered on the project's:
// nothing a Service publishes or is called can collide with the user's
// containers or another Sandbox's. Entries with nothing to change are left
// out, as are volumes with no name of their own, which Compose prefixes
// with the project anyway: podman-compose can't merge onto an empty entry.
func (cp composeProject) override(names []string, project string) string {
	var services strings.Builder
	for _, name := range cp.started(names) {
		s := cp.Services[name]
		ports := s.declaredPorts()
		if s.ContainerName == "" && len(ports) == 0 {
			continue
		}
		fmt.Fprintf(&services, "  %q:\n", name)
		if s.ContainerName != "" {
			services.WriteString("    container_name: !reset null\n")
		}
		if len(ports) > 0 {
			services.WriteString("    ports: !override\n")
			for _, p := range ports {
				suffix := ""
				if p.udp {
					suffix = "/udp"
				}
				fmt.Fprintf(&services, "      - %q\n", "127.0.0.1::"+strconv.Itoa(p.number)+suffix)
			}
		}
	}
	var b strings.Builder
	if services.Len() == 0 {
		b.WriteString("services: {}\n")
	} else {
		b.WriteString("services:\n" + services.String())
	}
	renames := func(section string, resources map[string]*composeResource) {
		var body strings.Builder
		for _, key := range slices.Sorted(maps.Keys(resources)) {
			if r := resources[key]; r != nil && r.Name != "" && !r.external() {
				fmt.Fprintf(&body, "  %q:\n    name: %q\n", key, project+"_"+key)
			}
		}
		if body.Len() > 0 {
			b.WriteString(section + ":\n" + body.String())
		}
	}
	renames("volumes", cp.Volumes)
	renames("networks", cp.Networks)
	return b.String()
}
