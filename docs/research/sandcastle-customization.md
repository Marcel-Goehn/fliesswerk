# What can you customize in Sandcastle's loop, and what do real setups need?

Research for [#2](https://github.com/Marcel-Goehn/ralph/issues/2). Reference: Sandcastle v0.13.0, commit `7193477` ([S1]). Real setup inspected: `~/dev/flashcards/.sandcastle/` ([F1]–[F6]; `.env` not read).

Sandcastle has no config file and no outer loop of its own. Everything in this doc is a TypeScript call made from a user-owned `main.mts`. Sandcastle's own Iteration loop sits inside `run()` (`orchestrate()` in `Orchestrator.ts`, which isn't exported) ([S2] `Orchestrator.ts:350`, `index.ts`). The Run/Step/Ticket loop (plan, implement, review, merge) is code the user writes ([F1]).

## Answer

1. **Configuration happens at five layers.** These are the facts the declarative config has to replace:
   - **Run/Step**: `run()` / `sandbox.run()` options. These cover the prompt, the Iterations, the completion signal, the timeouts, logging, structured output, the branch strategy and resume.
   - **Sandbox lifetime**: `createSandbox()`, which shares one warm Sandbox between Steps (implement, then review).
   - **Hooks**: three fixed points that run shell commands only (`host.onWorktreeReady`, `host.onSandboxReady`, `sandbox.onSandboxReady`).
   - **Sandbox provider**: options passed to `docker()`, `podman()` and the others, such as image, mounts, env, network, groups, devices, cap-drop, cpus, SELinux label and userns.
   - **Agent provider**: model, effort, permission mode, env and session capture.

   Everything else lives in files the user owns: the Dockerfile/Containerfile, the prompt files and `.sandcastle/.env`.
2. **The flashcards setup only customizes a few things.** Its `main.mts` changes the provider (`podman()`), one sandbox hook (create a venv and pip install), the Iteration counts (planner 1, implementer 5, reviewer 1, merger 1, at most 5 outer rounds), the model (Opus for every Step) and the prompts ([F1]). The project-specific content sits in the **prompts**, which contain the test commands, things not to run (paid evals), the commit format and a closing rule. It also sits in the **Containerfile**, which pins Python from apt because a test hardcodes `/usr/bin/python3` ([F2]–[F6]). Everything else is template boilerplate: the plan→execute→merge loop, `Promise.allSettled`, collecting commits and passing branches to the merger. The Factory would own all of that.
3. **Ticket selection is left to the model in the reference setup.** The planner is an Opus agent that reads `gh issue list` and builds a dependency graph ([F4]). The merger agent closes the issues ([F6]). The map rules that code decides these things, so the Factory will diverge from the template here by design. GitHub's native `blocked-by` links could replace the planner, but that is still an open question.
4. **Postgres and S3 from host Compose can be reached from the Sandbox today, using only `network`.** Neither `docker()` nor `podman()` accepts `--add-host`, `-p` or raw extra args ([S2] `podman.ts:243-262`, `DockerLifecycle.ts:140-170`). Adding a flag per feature is explicitly out of scope upstream, and the escape hatches upstream names are a compose provider or a custom provider ([S3]). The viable options:
   - **A. Join the Compose network.** For example, `podman({ network: "<project>_default" })`. Services are then reachable by service name (`postgres:5432`, `minio:9000`) ([D3]). This is the most portable option, but the container engine and its rootless/rootful mode must match the one that ran Compose.
   - **B. Use the host gateway.** Point tests at the ports Compose published on the host. Podman adds `host.containers.internal` / `host.docker.internal` to `/etc/hosts` by default, and under `podman machine` gvproxy resolves these names instead ([D1]). Docker Desktop resolves `host.docker.internal` automatically. Docker Engine on Linux needs `--add-host=host.docker.internal:host-gateway` ([D2]), which `docker()` can't pass, so Docker on Linux needs option A or C.
   - **C. `network: "host"`.** Docker Engine on Linux, Docker Desktop 4.34 or later (opt-in) and Podman support it ([D1], [D4]). It gives up network isolation, including access to everything bound to the host's localhost.
   - **D. Native sandboxes (`bwrap`, `seatbelt`).** The network is open and the host's localhost is reachable directly ([S4]). This is zero-config, but there is no image and no root.

   In every option, the app still needs the connection settings (`DATABASE_URL`, the S3 endpoint). They come in as env through `.sandcastle/.env` or the provider's `env` ([S5]). Starting the services and waiting for them is left to the user. A `host.onWorktreeReady` hook running `docker compose up -d --wait` would work ([S6], [D5]).
5. **Parallel Runs break the shared-database case.** This is an inference: N parallel Sandboxes would share one Postgres and one bucket, so integration tests can collide. The config needs a per-Sandbox discriminator, such as a DB name or schema prefix derived from the Ticket or branch. Sandcastle has no such concept, and the flashcards project has no services. **Unconfirmed** against a real multi-service setup.
6. **Hook semantics have traps the config should make explicit.**
   - Hooks inside one hook point run **in parallel** (only `host.onWorktreeReady` runs sequentially) ([S6]).
   - Each hook has a 60-second default timeout ([S6]).
   - Under top-level `run()`, the Sandbox, and so its hooks, is re-created **for every Iteration**. Under `createSandbox()`, hooks run **once** at creation ([S2] `Orchestrator.ts:404-421`, `SandboxFactory.ts:166-188`; [S5] `run.md:336`).

   The flashcards comment "run inside the sandbox before the agent starts each iteration" is therefore only true for its `run()` calls ([F1]).

## Catalogue of knobs

Sources: [S5] (run.md), [S6] (hooks.md), [S7] (api-report).

### `run()` options (one Step, N Iterations)

| Knob | Default | What it does / why it exists |
|---|---|---|
| `agent` | required | Agent provider plus model (see below). This is the harness boundary. |
| `sandbox` | required | Sandbox provider. `noSandbox()` is host mode. |
| `cwd` | `process.cwd()` | Host repo to anchor `.sandcastle/` and git to, so one script can drive another repo. |
| `prompt` / `promptFile` | one required | The prompt. Inline prompts are passed literally, with no `{{}}` or `` !`cmd` `` expansion ([S8]). |
| `promptArgs` | — | `{{KEY}}` substitution, done on the host before shell expansion. A missing key is an error. Values are inert, so it is safe to pass issue text ([S8]). |
| `maxIterations` | 1 | Upper bound on Iterations for the Step. |
| `completionSignal` | `<promise>COMPLETE</promise>` | String or strings that end the loop early. The matched one is returned, so different signals can mean different outcomes, such as "done" or "can't be done". |
| `idleTimeoutSeconds` | 600 | Fails a stuck agent. Resets on output. |
| `completionTimeoutSeconds` | 60 | Grace period after the signal for processes that keep hanging (e.g. a `gh` child holding stdout). The Step succeeds with a warning ([S9]). |
| `branchStrategy` | `head` for bind-mount, `merge-to-head` for isolated | `head` writes to the host working tree. `merge-to-head` uses a temp branch merged back. `branch` uses a named branch in a reused worktree ([S10]). |
| `copyToWorktree` | — | Host files copied into the worktree (e.g. `.env`). Not available with `head`. |
| `hooks` | — | See below. |
| `timeouts` | copy 60 s, git setup 10 s, commit collection 30 s, merge-to-host 30 s | Built-in lifecycle step timeouts, which large repos need to raise. |
| `logging` | file under `.sandcastle/logs/` | `file` (with `onAgentStreamEvent` callback, `verbose`) or `stdout`. |
| `output` | — | `Output.object({tag, schema})` / `Output.string`. Typed result parsed from `<tag>`. Requires `maxIterations: 1`. Throws `StructuredOutputError`. |
| `resumeSession` | — | Resume an agent session by id (one Iteration only). |
| `signal` | — | AbortSignal that kills the agent and hooks. The worktree is preserved. |
| `name` | — | Log prefix. Also goes into auto branch and worktree names. |

The result carries `commits`, `branch`, `iterations[]` (session id, `usage` tokens, `contextWindow`), `completionSignal`, `stdout`, `logFilePath`, `preservedWorktreePath`, `output`, and `resume()`/`fork()`.

### `createSandbox()` (one Sandbox, several Steps)

Options: `branch` (required), `sandbox`, `cwd`, `hooks` (once), `copyToWorktree`, `timeouts`. The handle provides `run()`, which takes `run()`'s per-Step options minus the sandbox-level ones; `exec(cmd, {cwd, sudo, stdin, onLine})` for a code gate such as running the tests between implement and review, where non-zero exit is returned rather than thrown; `interactive()`; `close()` (keeps the worktree if dirty); `sandboxId`; and `worktreePath`. It exists so dependencies installed by hooks persist across implement → review, and so that Steps accumulate commits on one branch. `createWorktree()` is a similar mid-level primitive that takes a worktree first and a Sandbox later ([S7]).

### Hooks

| Hook | Where | When | Shape |
|---|---|---|---|
| `host.onWorktreeReady` | host, in the worktree | after `copyToWorktree`, before the Sandbox starts, **sequentially** | `{command, timeoutMs?}` |
| `host.onSandboxReady` | host | after the Sandbox is up, in parallel with the hook below | `{command, timeoutMs?}` |
| `sandbox.onSandboxReady` | Sandbox repo dir | after the Sandbox is up, **in parallel** | `{command, sudo?, timeoutMs?}` |

Shell commands only, with no `cwd`. Any hook that exits non-zero fails setup. `sudo` runs as container root on docker/podman and is rejected on native providers ([S6]). Prompt shell expressions (`` !`cmd` ``) run after the sandbox hooks, inside the Sandbox, in parallel, and fail fast ([S8]). There are no before/after-Iteration, on-close or on-failure hooks. Upstream's early idea notes list those as candidates ([S11]).

### Sandbox providers

| Provider | Options | Notes |
|---|---|---|
| `docker()` | `imageName`, `mounts` (`hostPath`, `sandboxPath`, `readonly`), `env`, `network` (string or array), `groups`, `devices`, `capDrop`, `cpus`, `containerUid`/`containerGid`, `selinuxLabel` (`z`/`Z`/false), `maxOutputTailChars` | Default bridge network ([S2] `docker.ts:73`). |
| `podman()` | as docker, plus `userns` (`keep-id` default / false for rootful) | Checks that `podman machine` is running on macOS/Windows ([S2] `podman.ts`). |
| `bwrap()` / `seatbelt()` | `env`, `mounts` (host path = sandbox path), `maxOutputTailChars` | Native, image-less. Private home with no host logins, open network, no root, no `interactive()` ([S4]). |
| `noSandbox()` | `env`, `maxOutputTailChars` | Host mode. |
| `e2b`, `modal`, `daytona`, `sprites`, `vercel` | Vendor SDK pass-through (template, timeout, resources, `ports`, `networkPolicy`, tokens) | Isolated: the repo is copied in and commits are synced out. |
| custom | `createBindMountSandboxProvider` / `createIsolatedSandboxProvider` with a handle (`exec`, `copyFileIn`/`copyIn`, `copyFileOut`, `close`, ...) | The upstream escape hatch for any `docker run` flag ([S3], [S4]). |

The image is built with `sandcastle docker|podman build-image [--image-name] [--dockerfile|--containerfile]`. The Docker build passes `AGENT_UID`/`AGENT_GID` build args ([S12]). The Dockerfile belongs to the user and has no abstraction layer ([S13]).

### Agent providers

`claudeCode(model, {effort, permissionMode, env, captureSessions, sessionStorage})`. `permissionMode` replaces the default `--dangerously-skip-permissions`. `codex`, `pi`, `cursor`, `opencode` and `copilot` take similar options ([S14]).

### Env and credentials

`.sandcastle/.env` declares which keys get forwarded. An empty value falls back to `process.env`. The repo-root `.env` is ignored ([S2] `EnvResolver.ts`). Agent and sandbox provider `env` are layered on top, and the two must not share a key ([S2] `mergeProviderEnv.ts`). Under `createSandbox()`, the agent's `env` reaches only the agent process, not hooks or `exec` ([S14]). The flashcards keys are `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`, plus `GH_TOKEN` ([F3]).

## What a declarative config would have to cover

Each row comes from flashcards (F), from the Postgres/S3 case (P) or from Sandcastle's surface (S).

| Need | Source | Sandcastle today |
|---|---|---|
| Sandbox image: base image, toolchain, pinned runtime paths | F ([F2]) | User-owned Dockerfile |
| Dependency setup per Sandbox (venv/pip, npm ci) with a long timeout | F ([F1]) | `sandbox.onSandboxReady` with `timeoutMs` |
| Dependency caches mounted from the host (pip, npm) | S ([S5] mounts example) | `mounts` |
| Feedback-loop commands: fast tests, full tests, lint/typecheck, plus a "never run" list | F ([F4]–[F6]) | Hardcoded in each prompt. A config could hold them once and inject them into every Step's prompt |
| Commit message convention | F ([F4]) | In the prompt |
| Model, effort and Iteration budget per Step | F ([F1]) | Code |
| Steps and their order (implement → review), with an optional review gate | F ([F1]) | Code |
| Prompt overrides per Step, plus shared standards files (`@.sandcastle/CODING_STANDARDS.md`) | F ([F5]) | Prompt files |
| Credentials (OAuth/API key, tracker token), never committed | F ([F3]) | `.sandcastle/.env` plus `.gitignore` |
| Bring up host services and wait until healthy | P | `host.onWorktreeReady` command |
| Sandbox network attachment (Compose network / host gateway / host) | P | `network` (A/C). B needs `--add-host` on Docker Linux, which isn't available |
| Service connection env (`DATABASE_URL`, `S3_ENDPOINT`, keys), with a hostname that differs per network option | P | env |
| Per-Sandbox data isolation for parallel Runs (DB/schema/bucket prefix from the Ticket) | P (inference) | Not present |
| Escape hatch for raw engine flags (`--add-host`, ports, ulimits) | P, [S3] | Custom provider only |
| Files to copy into the worktree, and which to deliberately skip (host `.venv` won't work) | F ([F1]) | `copyToWorktree` |
| Timeouts: idle, completion, lifecycle steps | S | options |

## Options for the Factory (for the downstream grilling)

- **Network config shape:** a single `network:` passthrough (Sandcastle's choice, minimal), or a higher-level `services:` block that names Compose services and resolves the hostname per engine and OS (more magic, more to test across the three OSes). A third possibility is a raw `extraRunArgs:` escape hatch, which upstream rejected because it grows without bound ([S3]).
- **Who starts the services:** the Factory, through a host hook (`compose up --wait`) before the first Sandbox, once per Run, or the user beforehand. Once per Run fits parallel Runs better than per Sandbox.
- **Hooks:** keep Sandcastle's three points with commands only, or add sequential ordering and per-Iteration/teardown points. Parallel-by-default is a footgun when the steps depend on each other.
- **Feedback loops as data:** move test and lint commands out of the prompts into config so every Step's prompt gets them. This is what flashcards repeats three times ([F4]–[F6]).

## Sources

- [S1] Sandcastle repo, `package.json` version 0.13.0, commit 7193477: https://github.com/mattpocock/sandcastle/tree/719347729bd03c290ddec58ae464e69185f8c31d
- [S2] Sandcastle source at S1: `src/Orchestrator.ts`, `src/SandboxFactory.ts`, `src/SandboxLifecycle.ts`, `src/createSandbox.ts`, `src/sandboxes/docker.ts`, `src/sandboxes/podman.ts`, `src/DockerLifecycle.ts`, `src/EnvResolver.ts`, `src/mergeProviderEnv.ts`, `src/index.ts`
- [S3] `.out-of-scope/docker-provider-bespoke-options.md` (at S1). It names `dockerCompose()` "where available", but v0.13.0 exports no such provider (`package.json` exports, `src/` has no match)
- [S4] `docs/guide/sandbox-providers.md` (at S1)
- [S5] `docs/guide/run.md` (at S1)
- [S6] `docs/guide/hooks.md` (at S1)
- [S7] `api-report/index.api.md`, `api-report/sandboxes-*.api.md` (at S1)
- [S8] `docs/guide/prompts.md` (at S1)
- [S9] `docs/guide/iterations.md` (at S1)
- [S10] `docs/guide/branch-strategies.md` (at S1)
- [S11] `ideas/config-and-hooks.md` (at S1), early design notes, not shipped
- [S12] `docs/guide/cli.md` (at S1)
- [S13] `.out-of-scope/custom-base-image-abstraction.md` (at S1)
- [S14] `docs/guide/agent-providers.md` (at S1)
- [F1] `~/dev/flashcards/.sandcastle/main.mts`
- [F2] `~/dev/flashcards/.sandcastle/Containerfile`
- [F3] `~/dev/flashcards/.sandcastle/.env.example`
- [F4] `~/dev/flashcards/.sandcastle/implement-prompt.md`, `plan-prompt.md`
- [F5] `~/dev/flashcards/.sandcastle/review-prompt.md`
- [F6] `~/dev/flashcards/.sandcastle/merge-prompt.md`
- [D1] podman-run(1), `--add-host`, `--network`: https://docs.podman.io/en/latest/markdown/podman-run.1.html
- [D2] docker container run, `--add-host` / `host-gateway`: https://docs.docker.com/reference/cli/docker/container/run/
- [D3] Docker Compose networking (`<project>_default`, service-name DNS, external networks): https://docs.docker.com/compose/how-tos/networking/
- [D4] Docker host network driver: https://docs.docker.com/engine/network/drivers/host/
- [D5] `docker compose up --wait`: https://docs.docker.com/reference/cli/docker/compose/up/

Unconfirmed:
- Whether a Podman rootless container on `pasta` can reach Compose ports published only on `127.0.0.1` through `host.containers.internal`.
- Whether `podman-compose` and `podman compose` name networks `<project>_default` exactly like Docker Compose.
