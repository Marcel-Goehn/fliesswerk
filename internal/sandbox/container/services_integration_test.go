//go:build integration

package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// A database that says when it is ready, with everything a Sandbox's copy
// must not collide on: a fixed name, a fixed port and a fixed volume name.
const databaseCompose = `services:
  postgres:
    image: docker.io/library/postgres:17-alpine
    container_name: shop-db
    environment:
      POSTGRES_HOST_AUTH_METHOD: trust
    ports:
      - "55432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 1s
      timeout: 3s
      retries: 60
    volumes:
      - data:/var/lib/postgresql/data
volumes:
  data:
    name: shop-data
`

const psqlImage = baseImage + "RUN apk add --no-cache postgresql-client\n"

// psql is a query from the Sandbox at host.
func psql(host, query string) string {
	return "PGCONNECT_TIMEOUT=5 psql -h " + host + " -U postgres -Atc '" + query + "'"
}

// servicesProject is a project with the database as its Service, and two
// worktrees to hold Sandboxes.
func servicesProject(t *testing.T, compose string) (pr *project, worktrees []string) {
	t.Helper()
	pr = newProject(t)
	writeFile(t, filepath.Join(pr.root, ".fliesswerk", "Containerfile"), psqlImage)
	second := filepath.Join(pr.root, ".fliesswerk", "state", "worktrees", "ticket-2")
	gitIn(t, pr.root, "worktree", "add", "-q", "-b", "ticket-2", second)
	worktrees = []string{pr.worktree, second}
	for _, dir := range []string{pr.root, pr.worktree, second} {
		writeFile(t, filepath.Join(dir, "compose.yaml"), compose)
	}
	pr.p.opts.ComposeFile = "compose.yaml"
	pr.p.opts.Services = []string{"postgres"}
	return pr, worktrees
}

func (pr *project) openOn(t *testing.T, name, worktree string) sandbox.Sandbox {
	t.Helper()
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: name, Worktree: worktree})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func (pr *project) names(t *testing.T, kind, project string) string {
	t.Helper()
	label := "label=com.docker.compose.project=" + project
	if kind == "ps" {
		return strings.TrimSpace(pr.engine(t, "ps", "-a", "--filter", label, "--format", "{{.Names}}"))
	}
	return strings.TrimSpace(pr.engine(t, kind, "ls", "--filter", label, "--format", "{{.Name}}"))
}

func TestTwoSandboxesEachSeeTheirOwnEmptyDatabase(t *testing.T) {
	pr, worktrees := servicesProject(t, databaseCompose)
	pr.prepare(t)
	first := pr.openOn(t, "build-1", worktrees[0])
	second := pr.openOn(t, "build-2", worktrees[1])

	mustSh(t, first, psql("postgres", "create table only_in_first (id int)"))
	if got := mustSh(t, first, psql("postgres", "select count(*) from pg_tables where tablename = $$only_in_first$$")); got != "1" {
		t.Fatalf("the first Sandbox's table: %q", got)
	}
	if got := mustSh(t, second, psql("postgres", "select count(*) from pg_tables where tablename = $$only_in_first$$")); got != "0" {
		t.Errorf("the second Sandbox sees the first's table: %q", got)
	}
}

func TestAServiceIsReachableEveryWayTheSandboxIsTold(t *testing.T) {
	pr, worktrees := servicesProject(t, databaseCompose)
	pr.prepare(t)
	sb := pr.openOn(t, "build-1", worktrees[0])

	for _, host := range []string{"postgres", "shop-db", "localhost", "127.0.0.1"} {
		if got := mustSh(t, sb, psql(host, "select 1")); got != "1" {
			t.Errorf("through %s: %q", host, got)
		}
	}
	if got := mustSh(t, sb, `printf '%s:%s' "$FLIESSWERK_SERVICE_POSTGRES_HOST" "$FLIESSWERK_SERVICE_POSTGRES_PORT"`); got != "postgres:5432" {
		t.Errorf("the variables say %q", got)
	}
	if got := mustSh(t, sb, `printf %s "$NO_PROXY"`); !strings.Contains(got, "postgres") {
		t.Errorf("NO_PROXY = %q; the proxy would refuse the Service", got)
	}
	listed := sb.Services()
	if len(listed) != 1 || !strings.Contains(strings.Join(listed[0].Addresses, " "), "postgres:5432") || !strings.Contains(strings.Join(listed[0].Addresses, " "), "localhost:5432") {
		t.Errorf("Services() = %+v", listed)
	}

	// What a Step leaves behind is killed; the way to the Service stays.
	if err := sb.KillAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustSh(t, sb, psql("localhost", "select 1")); got != "1" {
		t.Errorf("localhost after a KillAll: %q", got)
	}
}

func TestServicesLeaveTheUsersNamesAloneAndGoWithTheSandbox(t *testing.T) {
	pr, worktrees := servicesProject(t, databaseCompose)
	pr.prepare(t)
	sb := pr.openOn(t, "build-1", worktrees[0])
	project := composeProjectName(pr.root, pr.p.project, "build-1")

	if got := pr.names(t, "ps", project); got == "" || strings.Contains(got, "shop-db") {
		t.Errorf("the project's containers: %q", got)
	}
	if got := pr.names(t, "volume", project); got != project+"_data" {
		t.Errorf("the project's volumes: %q", got)
	}
	if out, _ := pr.p.engine.run(context.Background(), "volume", "inspect", "shop-data"); strings.Contains(out, "shop-data") {
		t.Error("the user's volume name was used")
	}
	before, err := os.ReadFile(filepath.Join(pr.worktree, "compose.yaml"))
	if err != nil || string(before) != databaseCompose {
		t.Errorf("the compose file changed: %v", err)
	}

	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ps", "volume"} {
		if got := pr.names(t, kind, project); got != "" {
			t.Errorf("%s left: %q", kind, got)
		}
	}
}

func TestACrashedRunsServicesAreRemovedByTheNextRun(t *testing.T) {
	pr, worktrees := servicesProject(t, databaseCompose)
	pr.prepare(t)
	pr.openOn(t, "build-1", worktrees[0]) // never destroyed: the Run "crashed"
	project := composeProjectName(pr.root, pr.p.project, "build-1")

	next := pr.provider(t)
	next.opts.ComposeFile, next.opts.Services = "compose.yaml", []string{"postgres"}
	if err := next.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ps", "volume"} {
		if got := pr.names(t, kind, project); got != "" {
			t.Errorf("%s left: %q", kind, got)
		}
	}
}

func TestARunStartRejectsAWritableAbsoluteBindMount(t *testing.T) {
	compose := "services:\n  postgres:\n    image: docker.io/library/postgres:17-alpine\n    volumes:\n      - /srv/real:/data\n"
	pr, _ := servicesProject(t, compose)
	ctx := context.Background()
	if err := pr.p.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	err := pr.p.Prepare(ctx)
	if err == nil || !strings.Contains(err.Error(), "/srv/real") {
		t.Fatalf("Prepare: %v", err)
	}
}

// sharedProject is a project whose database is shared, the Run's copy started
// from a worktree of its own.
func sharedProject(t *testing.T) (pr *project, worktrees []string, stop func(context.Context) error) {
	t.Helper()
	pr, worktrees = servicesProject(t, databaseCompose)
	pr.p.opts.Services, pr.p.opts.Shared = nil, []string{"postgres"}
	pr.prepare(t)
	base := filepath.Join(pr.root, ".fliesswerk", "state", "worktrees", "shared-services")
	gitIn(t, pr.root, "worktree", "add", "-q", "--detach", base)
	writeFile(t, filepath.Join(base, "compose.yaml"), databaseCompose)
	stop, err := pr.p.StartShared(context.Background(), sandbox.Request{Name: "shared", Worktree: base})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop(context.Background()) })
	return pr, worktrees, stop
}

func TestTwoSandboxesShareOneCopyOfASharedService(t *testing.T) {
	pr, worktrees, _ := sharedProject(t)
	first := pr.openOn(t, "build-1", worktrees[0])
	second := pr.openOn(t, "build-2", worktrees[1])

	mustSh(t, first, psql("postgres", "create table seen_by_both (id int)"))
	if got := mustSh(t, second, psql("postgres", "select count(*) from pg_tables where tablename = $$seen_by_both$$")); got != "1" {
		t.Errorf("the second Sandbox doesn't see the first's table: %q", got)
	}
	if got := mustSh(t, second, psql("localhost", "select 1")); got != "1" {
		t.Errorf("through localhost: %q", got)
	}
	project := composeProjectName(pr.root, pr.p.project, "shared")
	if got := strings.Fields(pr.names(t, "ps", project)); len(got) != 1 {
		t.Errorf("the shared project's containers: %q", got)
	}
	for _, name := range []string{"build-1", "build-2"} {
		if got := pr.names(t, "ps", composeProjectName(pr.root, pr.p.project, name)); got != "" {
			t.Errorf("%s has its own copy: %q", name, got)
		}
	}
}

func TestEverySandboxIsToldItsNameToSeparateItsData(t *testing.T) {
	pr, worktrees, _ := sharedProject(t)
	sb := pr.openOn(t, "build-1", worktrees[0])

	if got := mustSh(t, sb, `printf %s "$FLIESSWERK_SANDBOX"`); got != "build-1" {
		t.Errorf("FLIESSWERK_SANDBOX = %q", got)
	}
}

func TestASharedServiceOutlivesASandboxAndGoesAtTheEndOfTheRun(t *testing.T) {
	pr, worktrees, stop := sharedProject(t)
	project := composeProjectName(pr.root, pr.p.project, "shared")
	first := pr.openOn(t, "build-1", worktrees[0])
	if err := first.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := pr.names(t, "ps", project); got == "" {
		t.Fatal("the shared Service went with the Sandbox")
	}
	second := pr.openOn(t, "build-2", worktrees[1])
	if got := mustSh(t, second, psql("postgres", "select 1")); got != "1" {
		t.Errorf("after the first Sandbox went: %q", got)
	}

	if err := second.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ps", "volume"} {
		if got := pr.names(t, kind, project); got != "" {
			t.Errorf("%s left: %q", kind, got)
		}
	}
}

func TestACrashedRunsSharedServiceIsRemovedByTheNextRun(t *testing.T) {
	pr, _, _ := sharedProject(t)
	project := composeProjectName(pr.root, pr.p.project, "shared")

	next := pr.provider(t)
	next.opts.ComposeFile, next.opts.Shared = "compose.yaml", []string{"postgres"}
	if err := next.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := pr.names(t, "ps", project); got != "" {
		t.Errorf("left: %q", got)
	}
}

func TestASandboxsMemoryCountsItsOwnServicesButNotSharedOnes(t *testing.T) {
	pr, worktrees := servicesProject(t, databaseCompose)
	pr.prepare(t)
	withOwn := pr.openOn(t, "build-1", worktrees[0])
	own, err := withOwn.MemoryUse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if own == 0 {
		t.Error("a Sandbox with a running database holds nothing")
	}
	free, err := pr.p.FreeMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if free <= 0 {
		t.Errorf("free memory = %d", free)
	}
}

func TestASharedServicesMemoryIsNotTheSandboxs(t *testing.T) {
	pr, worktrees, _ := sharedProject(t)
	sb := pr.openOn(t, "build-1", worktrees[0])

	use, err := sb.MemoryUse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limit := int64(20) << 20; use >= limit {
		t.Errorf("the Sandbox holds %d bytes, want well under Postgres's", use)
	}
}

func TestADependencyAcrossOwnAndSharedStopsTheRunStart(t *testing.T) {
	compose := "services:\n  app:\n    image: docker.io/library/postgres:17-alpine\n    depends_on: [postgres]\n  postgres:\n    image: docker.io/library/postgres:17-alpine\n"
	pr, _ := servicesProject(t, compose)
	pr.p.opts.Services, pr.p.opts.Shared = []string{"app"}, []string{"postgres"}
	ctx := context.Background()
	if err := pr.p.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pr.p.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "depends_on") {
		t.Fatalf("Prepare: %v", err)
	}
}

func TestAHelperOfBothOwnAndSharedServicesIsOnTheSandboxsNetworkOnce(t *testing.T) {
	const pg = "    image: docker.io/library/postgres:17-alpine\n    environment:\n      POSTGRES_HOST_AUTH_METHOD: trust\n"
	compose := "services:\n  app:\n" + pg + "    depends_on: [helper]\n  search:\n" + pg + "    depends_on: [helper]\n  helper:\n" + pg
	pr, worktrees := servicesProject(t, compose)
	pr.p.opts.Services, pr.p.opts.Shared = []string{"app"}, []string{"search"}
	pr.prepare(t)
	base := filepath.Join(pr.root, ".fliesswerk", "state", "worktrees", "shared-services")
	gitIn(t, pr.root, "worktree", "add", "-q", "--detach", base)
	writeFile(t, filepath.Join(base, "compose.yaml"), compose)
	stop, err := pr.p.StartShared(context.Background(), sandbox.Request{Name: "shared", Worktree: base})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop(context.Background()) })

	sb := pr.openOn(t, "build-1", worktrees[0])

	helpers := 0
	for _, s := range sb.Services() {
		if s.Name == "helper" {
			helpers++
		}
	}
	if helpers != 1 {
		t.Errorf("the Sandbox lists the helper %d times, want its own copy once: %+v", helpers, sb.Services())
	}
}
