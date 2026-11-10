// Package setup is what `fliesswerk init` knows: what a repo tells about
// itself, what to ask about the rest, and which files come of the answers.
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
)

// The language ids, as `init` shows and takes them.
const (
	node   = "node"
	golang = "go"
	python = "python"
	rust   = "rust"
)

// recipe is what the Factory knows about installing a language.
type recipe struct {
	id, label      string
	defaultVersion string
	// allow are the registries a Sandbox needs for its packages.
	allow []string
}

// recipes are the languages with a Containerfile block, in the order
// `init` offers them.
var recipes = []recipe{
	{node, "Node.js", "24", []string{"registry.npmjs.org"}},
	{golang, "Go", "1.25", []string{"proxy.golang.org", "sum.golang.org"}},
	{python, "Python (uv)", "3.13", []string{"pypi.org", "files.pythonhosted.org"}},
	{rust, "Rust", "stable", []string{"index.crates.io", "static.crates.io"}},
}

// wellKnownCommands are the Feedback commands `init` asks about. Every one
// that is set goes into `verify`.
var wellKnownCommands = []string{"test", "typecheck", "lint", "format"}

// Package is one language's project in one directory of the repo.
type Package struct {
	Language string
	// Dir is relative to the project root, "" for the root itself.
	Dir string
	// Version is pinned by the repo, "" if it pins none.
	Version string
	// VersionFrom names the file that pins it.
	VersionFrom string
	// Manager is the Node package manager.
	Manager string
	// Install installs from the lockfile, run in Dir.
	Install string
	// Commands are Feedback commands by name, run in Dir.
	Commands map[string]string
	// Allow are registries this Package needs beyond its language's own.
	Allow []string
}

// Facts are what the repo says about itself.
type Facts struct {
	// Repo is owner/name of a GitHub remote, "" for any other.
	Repo       string
	BaseBranch string
	Packages   []Package
	// Unknown are files that show a language with no recipe.
	Unknown []string
	// Compose and Standards are the files found, "" for none.
	Compose, Standards string
	HasClaudeDir       bool
}

// Languages are the ids of the languages found, in recipe order.
func (f Facts) Languages() []string {
	var ids []string
	for _, r := range recipes {
		if slices.ContainsFunc(f.Packages, func(p Package) bool { return p.Language == r.id }) {
			ids = append(ids, r.id)
		}
	}
	return ids
}

// packagesOf are the Packages of the chosen languages.
func (f Facts) packagesOf(languages []string) []Package {
	return slices.DeleteFunc(slices.Clone(f.Packages), func(p Package) bool {
		return !slices.Contains(languages, p.Language)
	})
}

// commandSuggestions are the Feedback commands of the chosen languages'
// Packages. Where several have the same one, it runs them all.
func (f Facts) commandSuggestions(languages []string) map[string]string {
	out := map[string]string{}
	for _, name := range wellKnownCommands {
		var parts []inDir
		for _, p := range f.packagesOf(languages) {
			if line := p.Commands[name]; line != "" {
				parts = append(parts, inDir{p.Dir, line})
			}
		}
		if len(parts) > 0 {
			out[name] = chain(parts)
		}
	}
	return out
}

// inDir is a command line and the directory it runs in.
type inDir struct{ dir, line string }

// chain joins commands into one line. The directories are at most one
// level deep, which is what makes `cd ..` the way back; it also means the
// line works in cmd.exe, where a group in parentheses keeps its `cd`.
func chain(parts []inDir) string {
	var words []string
	current := ""
	for _, p := range parts {
		if p.dir != current && current != "" {
			words = append(words, "cd ..")
		}
		if p.dir != current && p.dir != "" {
			words = append(words, "cd "+p.dir)
		}
		current = p.dir
		words = append(words, p.line)
	}
	return strings.Join(words, " && ")
}

var (
	skippedDirs = []string{"node_modules", "vendor", "target", "dist", "build", "testdata"}
	// noRecipe are files that show a language the Factory has no recipe for.
	noRecipe     = []string{"Gemfile", "pom.xml", "build.gradle", "build.gradle.kts", "composer.json", "mix.exs", "Package.swift", "pubspec.yaml"}
	noRecipeExts = []string{".csproj", ".sln"}

	composeFiles  = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
	standardFiles = []string{"docs/coding-standards.md", "CODING_STANDARDS.md", "docs/STANDARDS.md", "STANDARDS.md"}
)

// repo reads a checkout. It can't be led outside it by a symlink.
type repo struct{ root *os.Root }

// read is the content of a file, and whether it could be read.
func (r repo) read(name string) (string, bool) {
	data, err := r.root.ReadFile(name)
	return string(data), err == nil
}

// has reports whether anything is at name.
func (r repo) has(name string) bool {
	_, err := r.root.Stat(name)
	return err == nil
}

// entries lists dir ("" for the root), sorted by name.
func (r repo) entries(dir string) []fs.DirEntry {
	f, err := r.root.Open(orElse(dir, "."))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	entries, _ := f.ReadDir(-1)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries
}

// orElse is s, or def if s is empty.
func orElse(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Detect reads what the repo at root says about itself, and nothing else
// of the machine.
func Detect(ctx context.Context, root string) (Facts, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return Facts{}, fmt.Errorf("can't read the project: %w", err)
	}
	defer func() { _ = opened.Close() }()
	r := repo{opened}

	f := Facts{HasClaudeDir: r.has(".claude")}
	f.Repo, f.BaseBranch = remoteFacts(ctx, root)
	f.Compose = firstOf(r, composeFiles)
	f.Standards = firstOf(r, standardFiles)

	dirs := []string{""}
	for _, e := range r.entries("") {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !slices.Contains(skippedDirs, e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}
	for _, dir := range dirs {
		for _, scan := range []func(string) (Package, bool){r.node, r.golang, r.python, r.rust} {
			p, ok := scan(dir)
			// What the root holds of a language owns its subdirectories:
			// a workspace's members have no lockfile of their own.
			atRoot := slices.ContainsFunc(f.Packages, func(q Package) bool { return q.Language == p.Language && q.Dir == "" })
			if ok && !atRoot {
				f.Packages = append(f.Packages, p)
			}
		}
		f.Unknown = append(f.Unknown, r.noRecipeFiles(dir)...)
	}
	return f, nil
}

// firstOf is the first of names that exists, "" if none does.
func firstOf(r repo, names []string) string {
	for _, n := range names {
		if r.has(n) {
			return n
		}
	}
	return ""
}

// noRecipeFiles are the files in dir that show a language with no recipe.
func (r repo) noRecipeFiles(dir string) []string {
	var found []string
	for _, e := range r.entries(dir) {
		if slices.Contains(noRecipe, e.Name()) || slices.Contains(noRecipeExts, path.Ext(e.Name())) {
			found = append(found, path.Join(dir, e.Name()))
		}
	}
	return found
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)

// remoteFacts are what `origin` tells: the GitHub repository and the
// default branch.
func remoteFacts(ctx context.Context, root string) (name, branch string) {
	r := git.Repo{Dir: root}
	if url, err := r.Run(ctx, "config", "--get", "remote.origin.url"); err == nil {
		if m := githubRemote.FindStringSubmatch(url); m != nil {
			name = m[1]
		}
	}
	if head, err := r.Run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return name, strings.TrimPrefix(head, "origin/")
	}
	branch, _ = r.Run(ctx, "branch", "--show-current")
	return name, branch
}

var (
	numericVersion = regexp.MustCompile(`^\d+(\.\d+){0,2}$`)
	rustChannel    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	versionFloor   = regexp.MustCompile(`^[\^~>=v\s]*(\d+(?:\.\d+){0,2})`)
	goDirective    = regexp.MustCompile(`(?m)^go\s+(\d+(?:\.\d+){0,2})\s*$`)
	requiresPython = regexp.MustCompile(`(?m)^requires-python\s*=\s*"[^0-9"]*(\d+(?:\.\d+){0,2})`)
	rustChannelKey = regexp.MustCompile(`(?m)^channel\s*=\s*"([^"]+)"`)
)

// fileVersion is the version a file holds as its whole content, if it is
// one (`lts/iron` isn't an image tag).
func (r repo) fileVersion(name string) (string, bool) {
	text, ok := r.read(name)
	v := strings.TrimPrefix(strings.TrimSpace(text), "v")
	return v, ok && numericVersion.MatchString(v)
}

// toolVersion is what .tool-versions in dir pins tool to.
func (r repo) toolVersion(dir, tool string) (string, bool) {
	text, _ := r.read(path.Join(dir, ".tool-versions"))
	for line := range strings.Lines(text) {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == tool && numericVersion.MatchString(f[1]) {
			return f[1], true
		}
	}
	return "", false
}

// packageJSON is what `init` reads of a package.json.
type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
	Engines struct {
		Node string `json:"node"`
	} `json:"engines"`
	PackageManager string `json:"packageManager"`
}

// nodeScripts are the scripts each Feedback command may be, best first.
var nodeScripts = map[string][]string{
	"test":      {"test"},
	"typecheck": {"typecheck", "type-check", "tsc"},
	"lint":      {"lint"},
	"format":    {"format:check", "format"},
}

// node reads the Node project in dir, if there is one.
func (r repo) node(dir string) (Package, bool) {
	p := Package{Language: node, Dir: dir, Commands: map[string]string{}}
	data, ok := r.read(path.Join(dir, "package.json"))
	if !ok {
		return p, false
	}
	var pkg packageJSON
	_ = json.Unmarshal([]byte(data), &pkg) // a broken file is still a Node project, with less to suggest

	p.Version, p.VersionFrom = r.nodeVersion(dir, pkg)
	p.Manager = r.nodeManager(dir, pkg)
	p.Install = r.nodeInstall(dir, p.Manager)
	if p.Manager == "yarn" {
		p.Allow = []string{"registry.yarnpkg.com"} // where Yarn's own default registry is
		if p.Install == yarnBerry {
			p.Allow = append(p.Allow, "repo.yarnpkg.com") // where corepack gets Yarn 2+ from
		}
	}
	for name, scripts := range nodeScripts {
		for _, s := range scripts {
			if line, ok := pkg.Scripts[s]; ok && !strings.Contains(line, "no test specified") {
				p.Commands[name] = runScript(p.Manager, s)
				break
			}
		}
	}
	return p, true
}

// nodeVersion is the Node version dir pins, and the file that pins it.
func (r repo) nodeVersion(dir string, pkg packageJSON) (version, from string) {
	for _, name := range []string{".nvmrc", ".node-version"} {
		if v, ok := r.fileVersion(path.Join(dir, name)); ok {
			return v, name
		}
	}
	if v, ok := r.toolVersion(dir, "nodejs"); ok {
		return v, ".tool-versions"
	}
	if m := versionFloor.FindStringSubmatch(pkg.Engines.Node); m != nil {
		return m[1], "package.json engines"
	}
	return "", ""
}

// nodeManager is the package manager of dir: what package.json names, or
// else what its lockfile shows.
func (r repo) nodeManager(dir string, pkg packageJSON) string {
	name, _, _ := strings.Cut(pkg.PackageManager, "@")
	if slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, name) {
		return name
	}
	for _, lock := range []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"yarn.lock", "yarn"},
	} {
		if r.has(path.Join(dir, lock.file)) {
			return lock.manager
		}
	}
	return "npm"
}

// yarnBerry is the lockfile install of Yarn 2 and later.
const yarnBerry = "yarn install --immutable"

// nodeInstall is the command that installs dir from its lockfile.
func (r repo) nodeInstall(dir, manager string) string {
	switch manager {
	case "pnpm":
		return "pnpm install --frozen-lockfile"
	case "bun":
		return "bun install --frozen-lockfile"
	case "yarn":
		if lock, _ := r.read(path.Join(dir, "yarn.lock")); strings.Contains(lock, "yarn lockfile v1") {
			return "yarn install --frozen-lockfile"
		}
		return yarnBerry
	}
	if r.has(path.Join(dir, "package-lock.json")) || r.has(path.Join(dir, "npm-shrinkwrap.json")) {
		return "npm ci"
	}
	return "npm install" // nothing to be strict about
}

// runScript is the command that runs a package.json script.
func runScript(manager, script string) string {
	switch {
	case manager == "npm" && script == "test":
		return "npm test"
	case manager == "npm" || manager == "bun": // `bun test` is Bun's own runner, not the script
		return manager + " run " + script
	}
	return manager + " " + script
}

// golang reads the Go module in dir, if there is one.
func (r repo) golang(dir string) (Package, bool) {
	text, ok := r.read(path.Join(dir, "go.mod"))
	p := Package{Language: golang, Dir: dir, Install: "go mod download",
		Commands: map[string]string{"test": "go test ./...", "lint": "go vet ./..."}}
	if m := goDirective.FindStringSubmatch(text); m != nil {
		p.Version, p.VersionFrom = m[1], "go.mod"
	}
	return p, ok
}

// python reads the Python project in dir, if there is one.
func (r repo) python(dir string) (Package, bool) {
	project, hasProject := r.read(path.Join(dir, "pyproject.toml"))
	requirements, hasRequirements := r.read(path.Join(dir, "requirements.txt"))
	p := Package{Language: python, Dir: dir, Commands: map[string]string{}}
	switch {
	case r.has(path.Join(dir, "uv.lock")):
		p.Install = "uv sync --locked"
	case hasRequirements && !hasProject:
		p.Install = "uv venv && uv pip install -r requirements.txt"
	default:
		p.Install = "uv sync"
	}
	if v, ok := r.fileVersion(path.Join(dir, ".python-version")); ok {
		p.Version, p.VersionFrom = v, ".python-version"
	} else if v, ok := r.toolVersion(dir, "python"); ok {
		p.Version, p.VersionFrom = v, ".tool-versions"
	} else if m := requiresPython.FindStringSubmatch(project); m != nil {
		p.Version, p.VersionFrom = m[1], "pyproject.toml"
	}
	deps := project + requirements
	for _, tool := range []struct{ dep, name, line string }{
		{"pytest", "test", "uv run pytest"},
		{"ruff", "lint", "uv run ruff check ."},
		{"mypy", "typecheck", "uv run mypy ."},
	} {
		if strings.Contains(deps, tool.dep) {
			p.Commands[tool.name] = tool.line
		}
	}
	return p, hasProject || hasRequirements
}

// rust reads the Rust crate in dir, if there is one.
func (r repo) rust(dir string) (Package, bool) {
	p := Package{Language: rust, Dir: dir, Install: "cargo fetch",
		Commands: map[string]string{"test": "cargo test", "lint": "cargo clippy -- -D warnings"}}
	if r.has(path.Join(dir, "Cargo.lock")) {
		p.Install = "cargo fetch --locked"
	}
	if text, ok := r.read(path.Join(dir, "rust-toolchain.toml")); ok {
		if m := rustChannelKey.FindStringSubmatch(text); m != nil && rustChannel.MatchString(m[1]) {
			p.Version, p.VersionFrom = m[1], "rust-toolchain.toml"
		}
	} else if text, ok := r.read(path.Join(dir, "rust-toolchain")); ok && rustChannel.MatchString(strings.TrimSpace(text)) {
		p.Version, p.VersionFrom = strings.TrimSpace(text), "rust-toolchain"
	}
	return p, r.has(path.Join(dir, "Cargo.toml"))
}

// Service is one service of the compose file.
type Service struct {
	Name string
	// Offers are env templates suggested for it.
	Offers []EnvOffer
}

// EnvOffer is a variable that reaches a Service, as a `{host}`/`{port}`
// template.
type EnvOffer struct{ Name, Template string }

// composeFile is what `init` reads of a compose file.
type composeFile struct {
	Services map[string]struct {
		Ports []yaml.Node `yaml:"ports"`
	} `yaml:"services"`
}

// envExamples are the files that may show how a project reaches its
// Services. A real .env is never read: it holds secrets.
var envExamples = []string{".env.example", ".env.sample"}

var (
	envLine = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=["']?([a-z][a-z0-9+.-]*://(?:[^@/\s]*@)?)([^:/?#\s]+):(\d+)([^"'\s]*)["']?\s*$`)
	portRE  = regexp.MustCompile(`^(?:[\d.]+:)?(\d+):\d+(?:/\w+)?$`)
)

// readServices lists the Services of the compose file, with the variables
// of the project's env examples that point at them.
func readServices(r repo, file string) ([]Service, error) {
	text, ok := r.read(file)
	if !ok {
		return nil, errors.New("can't be read")
	}
	var c composeFile
	if err := yaml.Unmarshal([]byte(text), &c); err != nil {
		return nil, err
	}
	var services []Service
	for name, s := range c.Services {
		service := Service{Name: name}
		published := publishedPorts(s.Ports)
		for _, file := range envExamples {
			examples, _ := r.read(file)
			for line := range strings.Lines(examples) {
				if m := envLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil && pointsAt(m[3], m[4], name, published) {
					service.Offers = append(service.Offers, EnvOffer{m[1], m[2] + "{host}:{port}" + m[5]})
				}
			}
		}
		services = append(services, service)
	}
	slices.SortFunc(services, func(a, b Service) int { return strings.Compare(a.Name, b.Name) })
	return services, nil
}

// pointsAt reports whether host:port reaches the Service: by its name, or
// on localhost through a port it publishes.
func pointsAt(host, port, service string, published []string) bool {
	if host == service {
		return true
	}
	return (host == "localhost" || host == "127.0.0.1") && slices.Contains(published, port)
}

// publishedPorts are the host ports of compose `ports:` entries.
func publishedPorts(ports []yaml.Node) []string {
	var out []string
	for _, n := range ports {
		var long struct {
			Published yaml.Node `yaml:"published"`
		}
		if n.Kind == yaml.MappingNode && n.Decode(&long) == nil && long.Published.Value != "" {
			out = append(out, long.Published.Value)
		} else if m := portRE.FindStringSubmatch(n.Value); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
