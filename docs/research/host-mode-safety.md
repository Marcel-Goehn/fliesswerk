# Host mode safety: running an AFK agent on the host without hanging and without doing harm

Research for ticket #7 (map #1). The question: on Windows, Linux and macOS, how can a harness (Claude Code first) run AFK on the host, never blocking on a permission prompt, while being unable to damage the OS, exfiltrate credentials, or attack external hosts? Where does each approach fall short?

Sources are pinned where possible: Sandcastle v0.13.0 (`mattpocock/sandcastle`, commit `f5c48e0`), `@anthropic-ai/sandbox-runtime` 0.0.81 (`anthropics/sandbox-runtime`, commit `98a4016`), and the Claude Code docs at `code.claude.com` as read on 2026-11-02. Nothing was executed. Every claim below comes from reading docs or source.

## Answer

1. **Not hanging is a harness setting, not a sandbox feature.** In `claude -p`, a call that needs approval is **denied, not waited on**, unless a permission host (`--permission-prompt-tool`, SDK `canUseTool`) is configured. `--permission-prompts none` (v2.1.259+) also removes `AskUserQuestion` and tells Claude not to retry denials [CC-headless]. The hangs that remain are process-level: a Step that never exits, background work the run waits for (up to a 10-minute cap by default), and children holding stdout open. The Factory has to bound these itself with timeouts and process-tree kills, the way Sandcastle does ([Hanging](#hanging)).
2. **Permission modes and allow/deny rules are not a safety boundary.** Anthropic's docs say so outright. `bypassPermissions` "offers no protection against prompt injection or unintended actions". Auto mode "does not guarantee safety". Bash deny rules "aren't a security boundary around the program", and Read/Edit deny rules don't cover "arbitrary subprocesses" [CC-modes, CC-perms]. These settings decide whether a call runs. They do not limit what it can reach once it runs.
3. **Claude Code's built-in sandbox covers only shell commands.** It wraps Bash, PowerShell and Monitor. Read/Edit/Write/WebFetch, hooks, MCP servers and `apiKeyHelper` "run with your full access" [CC-sandbox]. By default it **reads the whole machine, `~/.ssh` included**. **Native Windows is unsupported**. If it can't start, it **silently falls back to unsandboxed** unless `failIfUnavailable` is set. Under `bypassPermissions`, non-allowlisted hosts and unsandboxed retries **run without a prompt** unless `strictAllowlist` and `allowUnsandboxedCommands: false` are set. Anthropic's own guidance is that it "is not sufficient for fully unattended runs" [CC-envs].
4. **The only host-mode approach that meets all three goals wraps the whole harness process in an OS sandbox *with a network allowlist*.** Anthropic recommends exactly this for `--dangerously-skip-permissions`: "Always run ... inside a container, a VM, or the sandbox runtime" [CC-envs]. Two existing implementations do it:
   - **Sandcastle's `bwrap()` / `seatbelt()`** (Linux+WSL2 / macOS). They give a good filesystem, credential and process boundary: a private home, no host logins, a PID namespace on Linux. But **egress is wide open, host localhost included** (`--share-net`, `(allow network*)`) [SC-bwrap, SC-seatbelt, SC-adr24]. They don't stop exfiltration or attacks on external hosts.
   - **Anthropic's `sandbox-runtime` (`srt`)** (macOS, Linux/WSL2, Windows **alpha**). It wraps any process. Writes are denied and network is denied by default, and an HTTP/SOCKS proxy enforces a domain allowlist. **Reads are open by default** [SRT]. It is a beta research preview shipped as an npm package.
5. **Every OS-sandbox approach leaks through what it must allow.** The agent needs its model credential and network access to the model API, so a compromised agent can exfiltrate that token over an allowed host. srt and Claude Code offer `credentials` *mask* mode: the proxy substitutes the real value, which requires TLS termination, and it's experimental. Allowing broad domains like `github.com` opens exfiltration paths, including domain fronting, because the proxy does not inspect TLS [CC-sandbox].
6. **Windows is the gap.** Native Windows has no mature, dependency-free process sandbox that any of these tools ships as stable:
   - Claude Code's sandbox doesn't support it.
   - Sandcastle's answer is "run inside WSL2 and use `bwrap()`".
   - srt's Windows backend (dedicated local user + WFP egress fence + write-restricted token + ACLs) is alpha. It needs a one-time elevated install, breaks Schannel TLS (`curl.exe`, `Invoke-WebRequest`, .NET, winget), and documents unfenced egress paths (anonymous-token sockets, deny-only-token sockets, SMB, WSL) [SRT-win].
   - Codex's newest Windows backend uses Microsoft MXC process isolation and needs Windows 11 24H2 build 26100.9278+ [CODEX-win, MXC].
   - Windows Sandbox is a real VM boundary, but it is unavailable on Home, allows only one instance at a time, and has none of the host's tools [WSB].
7. **Sandcastle itself treats unsandboxed host mode as "caller owns the risk".** `noSandbox()` adds no runtime guard (ADR 0015). In v0.13.0, `run()` passes `--dangerously-skip-permissions` unconditionally, `noSandbox()` included (`Orchestrator.ts:162`). That contradicts ADR 0015's stated consequence [SC-orch, SC-adr15]. Its no-sandbox Windows path kills only the shell, not the process tree [SC-hostshell].

## Per-OS summary

"Goal met?" uses the ticket's three harms: **OS** (damage the OS or files outside the repo), **Creds** (read host credentials), **Net** (exfiltrate or attack external hosts). "Yes*" means met with the caveats in the detail sections.

| Approach | Linux | macOS | Windows (native) | No prompt hang | OS | Creds | Net |
|---|---|---|---|---|---|---|---|
| Claude Code `bypassPermissions` / `--dangerously-skip-permissions`, no sandbox | yes | yes | yes | yes | no | no | no |
| Claude Code `auto` mode (classifier) | yes | yes | yes | yes (blocked actions are skipped in `-p`) | partial, not a boundary | partial | partial |
| Claude Code `dontAsk` + narrow allow rules | yes | yes | yes | yes | partial (rule text matching is bypassable) | partial | partial |
| Claude Code built-in Bash sandbox (bubblewrap / Seatbelt) | yes (needs `bubblewrap`, `socat`) | yes (built in) | **no**: "runs commands unsandboxed"; WSL2 only | yes | Bash only | Bash only, and only if `denyRead`/`credentials` are configured | Bash only; WebFetch is separate |
| Sandcastle `bwrap()` (whole process) | yes (also WSL2) | no | via WSL2 only | yes (Sandcastle passes skip-permissions) | Yes* | Yes* (private home, env-only login) | **no** (open egress + host localhost) |
| Sandcastle `seatbelt()` (whole process) | no | yes (macOS 14+) | no | yes | Yes* (no PID namespace) | Yes* (Keychain not promised) | **no** (open egress + host localhost) |
| `srt` wrapping `claude` (whole process) | yes (needs `bubblewrap`, `socat`, `ripgrep`) | yes (needs `ripgrep`) | **alpha** (elevated one-time install) | yes | Yes* | Yes* only with `denyRead`/`credentials`; reads default-open | Yes* (allowlist proxy; domain-fronting and broad-domain caveats; Windows WFP gaps) |
| Windows Sandbox (VM) | n/a | n/a | Pro/Enterprise/Education only; one instance | depends on harness mode | yes | yes | only if networking disabled or filtered |

Per-OS notes:

- **Linux.** bubblewrap is the common primitive in Claude Code's sandbox, Sandcastle's `bwrap()` and srt. On **Ubuntu 24.04+**, `kernel.apparmor_restrict_unprivileged_userns` blocks it until an AppArmor profile is added or the sysctl is flipped, and both need `sudo` [SC-bwrap-pre, SRT]. Inside an unprivileged container, Claude Code needs `enableWeakerNestedSandbox`, which "considerably weakens security" [CC-sandbox]. Inside srt or Claude's own sandbox, **nested user namespaces are blocked**, so a nested bwrap (Sandcastle's, or Claude Code's sandbox inside srt), Chromium and rootless podman fail [SRT].
- **macOS.** Seatbelt (`/usr/bin/sandbox-exec`) ships with the OS and needs no install. **Seatbelt profiles can't nest**, so the harness's own sandbox must be off inside an outer Seatbelt [SC-seatbelt-pre, SC-guide]. There is no PID namespace: a `setsid`'d process survives Sandcastle's `close()` [SC-adr24]. `allowAppleEvents` removes code-execution isolation [CC-sandbox].
- **Windows.** The realistic options:
  1. Run the Factory inside **WSL2** and use bwrap. Hide `/mnt`, `/run/WSL` and `WSL_INTEROP` so the agent can't run Windows `.exe`s, as Sandcastle does. WSL2 also has distro-level `[interop] enabled=false` and `[automount] enabled=false` [SC-bwrap, WSL-conf].
  2. srt's Windows **alpha**.
  3. A **VM/container** (Docker Desktop, Windows Sandbox).
  4. **Unsandboxed** host mode with permission settings only.

  Bounding process trees on Windows needs **job objects**. There are no process groups, and Sandcastle's host path kills only the shell [JOB, SC-hostshell].

## Detail

### Claude Code permission layer (all OSes)

- **Mode table, as it affects AFK** [CC-modes, CC-headless]:
  - `default`/Manual: since v2.1.302, `-p` runs with no mode set start here, and calls needing approval are **denied**.
  - `acceptEdits`: edits and common filesystem commands inside the working dirs only.
  - `auto`: a classifier (default Claude Sonnet 5) reviews actions. In `-p`, "a repeatedly blocked action is skipped, and the run continues". The "3 in a row / 20 total" pause resumes *prompting*, which in `-p` again means denial. Auto mode is available on all plans, but only for certain models and providers. Bedrock, Vertex and Foundry need Sonnet 5+, Opus 4.7+, Haiku 5.5 or Fable.
  - `dontAsk`: auto-denies anything that would prompt. Docs: "the session never waits for input".
  - `bypassPermissions`: no prompts, protected-path writes allowed. **Deny rules still apply; allow rules have no effect.** It is refused as root on Linux/macOS "unless inside a recognized sandbox".
- **Actions no mode auto-approves** (explicit `ask` rules, critical-path removals): in `-p` these are denied, not waited on [CC-modes].
- **Rules are evaluated deny → ask → allow**, and an allow can't carve out of a deny [CC-perms].
- **Where rules fall short** [CC-perms]:
  - Bash rules match command *text*. `Bash(curl *)` doesn't stop `/usr/bin/curl` or `sh -c 'curl …'`.
  - Argument-constraining patterns are "fragile".
  - Read/Edit denies don't reach "a Python or Node script that opens files itself".
  - "Using WebFetch alone doesn't prevent network access. If Bash is allowed, Claude can still use curl".
- **Repo content runs before trust in `-p`.** Without `--bare`, a `-p` session runs the project's `.claude/settings.json` hooks and connects its `.mcp.json` servers, "even in a folder you've never trusted". `--bare` skips hooks, skills, plugins, MCP, CLAUDE.md and auto memory, and is "recommended for scripted and SDK calls" [CC-headless]. Bare mode reads no OAuth credentials or keychain: it uses `ANTHROPIC_API_KEY` or `apiKeyHelper`, while Bedrock, Vertex and Foundry read their own credentials. That matters for the credentials axis on map #1.
- **Managed lock-out.** `permissions.disableBypassPermissionsMode` / `disableAutoMode` = `"disable"` prevent those modes [CC-perms].

### Claude Code built-in sandbox

- **Mechanism.** Seatbelt on macOS, bubblewrap + socat on Linux/WSL2. WSL1 and native Windows are unsupported. Off by default (`sandbox.enabled`) [CC-sandbox].
- **Defaults.**
  - Writes: cwd, a per-user temp dir and added dirs. Protected paths stay denied.
  - Reads: "the entire computer, except certain denied directories". "There is no built-in credential deny list."
  - Network: proxy with `allowedDomains`, which start empty. Non-proxy-aware tools and UDP can't leave [CC-sandbox].
- **Settings an AFK host run must set** [CC-sandbox]:
  - `failIfUnavailable: true`, because the default falls back to running unsandboxed.
  - `allowUnsandboxedCommands: false`. In bypass mode, the `dangerouslyDisableSandbox` retry otherwise "runs without a prompt", and any matching allow rule also approves it.
  - `strictAllowlist: true`. In bypass mode, a host outside the allowlist is otherwise "Allowed without a prompt".
  - Keep `excludedCommands` empty.
- **Shortfalls** [CC-sandbox, CC-envs]:
  - File, web and MCP tools, hooks, LSP and `apiKeyHelper` all run outside the sandbox.
  - TLS is not inspected, so domain fronting is possible.
  - `allowUnixSockets` (e.g. the Docker socket) is a host escape.
  - Broad `allowWrite` to PATH dirs or shell rc files enables escalation.

### Whole-process sandboxes

**Sandcastle native providers (v0.13.0)** [SC-bwrap, SC-seatbelt, SC-guide, SC-adr24]

- **`bwrap()`.** Read-only binds of `/usr /etc /bin /sbin /lib* /opt` and the host `PATH` toolchain. `--tmpfs /home` with a fresh private home and `/tmp`. `--clearenv` plus only the passed env. `--unshare-all --share-net --die-with-parent --new-session`.
  - Linux and WSL2 only.
  - `/mnt` and `/run/WSL` are hidden on WSL2.
  - A watcher on an inherited fd SIGKILLs the whole PID namespace when the Node host dies, because `--die-with-parent` alone has a startup race (`bwrapProcess.ts`).
- **`seatbelt()`.** A `(deny default)` SBPL profile adapted from Codex, Gemini CLI and MXC.
  - Read-only system roots, plus writable worktree, git dirs and private dirs.
  - Docker, Colima, OrbStack and Lima sockets and dirs are denied, since they would be "a way out".
  - XPC lookups are denied.
  - **`(allow network*)`**.
  - Requires macOS 14+ and refuses to nest.
- **Behaviour shared by both providers:**
  - Logins come only from env vars (`.sandcastle/.env`).
  - Read-only overlays and anchoring protect the git control files.
  - `sudo` is refused.
  - A missing tool never falls back to `noSandbox()`.
  - Each `exec` is a fresh process.
- **Shortfalls.**
  - Open egress, including host localhost (dev servers, local DBs).
  - A shared kernel: "about runc-grade" on Linux, weaker than Docker Desktop on macOS.
  - macOS has no PID namespace and leaves path metadata visible.
  - No Windows support outside WSL2.
  - Sandcastle runs agents with prompts skipped and tells users to turn off the agent's own sandbox.

**Anthropic `sandbox-runtime` (`srt`)** [SRT, CC-envs]

- **What it is.** `srt <cmd>` (CLI) or `SandboxManager.wrapWithSandbox` (library). Install with `npm install -g @anthropic-ai/sandbox-runtime`. Apache-2.0, beta research preview.
- **Defaults.** Reads allowed everywhere (deny-then-allow), writes denied everywhere (allow-only), network denied (empty `allowedDomains`).
  - A missing settings file starts with defaults ("Don't take a clean start as proof your settings loaded").
  - An invalid settings file refuses to start.
- **Built-in write denies.** `.git/hooks`, `.git/config`, `.mcp.json`, `.claude/commands|agents` and shell rc files.
  - On Linux the deny list is built once at launch, so nested repos created later (`git clone`, `git init`) aren't covered.
- **Network.** HTTP and SOCKS5 proxies on the host.
  - Linux removes the network namespace and reaches the proxies over bind-mounted Unix sockets.
  - A resolved-address check blocks loopback, link-local and metadata IPs behind allowed names.
- **Running `claude` under srt** [CC-envs]:
  - Allow writes to the project, `~/.claude` and `~/.claude.json`, and the tmp dir (`/tmp`, or `/private/tmp` on macOS).
  - Allow `api.anthropic.com` (or the provider endpoint), plus `claude.ai` and `platform.claude.com` for OAuth.
  - Then `deny` the other config paths. A sandboxed session that can write them "can persist hooks, permission rules, or MCP servers that run unsandboxed the next time you launch Claude Code".
- **Shortfalls.**
  - Reads are default-open: `~/.ssh`, `~/.aws` and other repos stay readable unless they are listed.
  - Linux `allowWrite`/`denyWrite` take literal paths, and unsupported globs are "silently skipped".
  - Nested user namespaces are blocked.
  - Windows is alpha (next section).

### Windows specifics

- **srt Windows alpha** [SRT-win]:
  - **Setup.** One-time `npx @anthropic-ai/sandbox-runtime windows-install` (UAC). It creates an `srt-sandbox` local user and a machine-wide WFP filter set keyed on that SID.
  - **Per command.** The command runs as that user under a write-restricted token in a job object. ACEs grant `allowWrite` roots.
  - **Kept grants.** Write grants persist between sessions, and any sandboxed session (even another Windows user's) can *read* a kept root.
  - **Profile read grant.** The install makes your user profile readable to the sandbox, except credential stores. Package-feed tokens in `.npmrc`, `pip.ini` and `NuGet.Config` become readable. Opt out with `--skip-profile-grant`.
  - **Schannel clients fail HTTPS:** `curl.exe`, `Invoke-WebRequest`, .NET/NuGet, winget, and git with schannel.
  - **Documented egress holes.** Sockets created under the anonymous token or a deny-only derived token bypass the fence. SMB is not fenced, and WSL is unestablished. "Treat the network filter as stopping commands that connect directly, not code written to defeat it."
- **Codex on Windows** (prior art, not a dependency) [CODEX-win]:
  - **MXC** (recommended): native process isolation, no admin, needs Win 11 24H2 build 26100.9278+.
  - **Elevated:** dedicated sandbox users + firewall rules + ACLs, needs admin setup.
  - **Unelevated:** restricted token from the current user, weaker network isolation, no denied-read paths.
- **Microsoft MXC** [MXC]: an SDK (Rust, .NET, Node), MIT-licensed. Default Windows backend `processcontainer`. The exact OS requirements were not confirmed from the MXC repo itself; the 24H2 build number above comes from Codex's docs.
- **AppContainer** [APPC]: an OS primitive with file, registry, network-capability and credential isolation. Using it for arbitrary Win32 dev toolchains (git, node, compilers) is **unconfirmed** here. None of the surveyed tools uses it directly.
- **Windows Sandbox** [WSB]: a disposable Hyper-V VM.
  - Pro, Enterprise and Education only.
  - "doesn't allow multiple instances to run simultaneously", which blocks a parallel Run.
  - Networking is on by default.
  - Host software isn't available inside it.

### Hanging

Sources of a stuck Step, and what bounds each:

| Cause | Bound | Source |
|---|---|---|
| Permission prompt | `-p` denies instead of waiting. `--permission-prompts none` removes `AskUserQuestion` and cancels unanswered MCP elicitations. Don't configure a permission host. | CC-headless |
| Long foreground shell command | Bash/PowerShell tool timeout: default 2 min (`BASH_DEFAULT_TIMEOUT_MS`), model-settable up to 10 min (`BASH_MAX_TIMEOUT_MS`) | CC-env |
| Background work at end of `-p` | The run waits for background commands, subagents and Monitor. The 10-min idle cap (`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS`) doesn't apply while a main-conversation background command runs. `--bare` or `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` removes background commands. | CC-headless, CC-env |
| MCP startup | `-p` with `--mcp-config` waits up to `MCP_TIMEOUT` (30 s) | CC-headless |
| Interactive child tools (git credential or editor prompts, pagers) | Set `GIT_TERMINAL_PROMPT=0` and `GIT_EDITOR`/`GIT_SEQUENCE_EDITOR` to a non-interactive command. `GIT_PAGER=cat` disables the pager. Whether Claude Code's Bash tool gives commands a closed stdin was **not confirmed**. | GIT |
| Agent process done but children hold stdout | Sandcastle: after the completion signal, a 1-min "completion timeout" force-completes the Iteration (ADR 0019). Under `noSandbox()` the abandoned process leaks. | SC-adr19 |
| Agent silent mid-work | Sandcastle idle timeout (`idleTimeoutSeconds`), plus timeouts on every lifecycle step (ADR 0001) | SC-orch, SC-adr01 |
| Killing the whole tree | Linux: PID namespace (bwrap) plus a watcher fd. macOS: process group, which `setsid` escapes. Windows: job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Sandcastle's Windows host path kills only the shell. SIGTERM to `claude -p` kills its running Bash trees and exits 143. | SC-bwrap, SC-adr24, JOB, SC-hostshell, CC-headless |

## Options for the downstream decision (#16)

These are not ranked. Each line gives the trade-off.

- **A. Permission-only host mode** (`dontAsk` + narrow allows, or `auto`) on all three OSes.
  - For: zero dependencies, identical everywhere, never hangs on prompts.
  - Against: no OS boundary, and Anthropic says it isn't one. `auto` depends on model and provider. `dontAsk` with narrow rules may deny too much for real Tickets.
- **B. A + Claude Code's built-in sandbox** (strict settings above).
  - For: built in, no Factory code.
  - Against: shell-only, not native Windows, Claude-Code-specific (no help for the harness boundary), reads open by default.
- **C. Factory-owned OS sandbox around the whole Step**, modelled on Sandcastle's `bwrap()`/`seatbelt()`, adding a network allowlist.
  - For: harness-agnostic and dependency-free on macOS. Needs only `bubblewrap` on Linux.
  - Against: the Factory must build and own an egress proxy (Sandcastle didn't), and Windows needs WSL2 or a separate backend.
- **D. Delegate to `srt`** (`srt claude …`).
  - For: an Anthropic-maintained filesystem and network policy that covers all three OSes, and is harness-agnostic since it wraps any process.
  - Against: an npm dependency on the Factory host, beta config format, Windows alpha with documented holes, and default-open reads that need a deny list.
- **E. Host mode only where an OS sandbox exists**, else refuse or require WSL2/container on Windows.
  - For: honest about the gap.
  - Against: Windows users lose native host mode.

## Surfaced questions (may affect other tickets or the map)

- **Glossary.** GLOSSARY.md defines Sandbox as "a container". OS process sandboxes (bwrap, Seatbelt, srt) are neither a container nor bare host. Is "host mode" one Sandbox kind or the absence of one?
- **Credentials axis.** Any network-allowed sandbox lets the agent read and exfiltrate the model token it is given. Is token masking (proxy substitution, needs TLS termination) in scope for v1?
- **Harness boundary.** The adapter must carry harness-specific "don't prompt / don't block" flags: Claude `-p`, `--permission-mode`, `--permission-prompts none`, `--bare`. Under an outer Seatbelt it must also disable the harness's own sandbox, because Seatbelt can't nest.
- **`--bare` vs project conventions.** `--bare` skips the repo's CLAUDE.md, skills and hooks, which the map's "takes the project's own conventions" goal may rely on.
- **Sandcastle discrepancy.** ADR 0015 says `noSandbox()` doesn't get skip-permissions, but `Orchestrator.ts:162` passes it unconditionally in `run()`. Don't copy that behaviour by accident.

## Sources

- [CC-modes] Claude Code: Permission modes. https://code.claude.com/docs/en/permission-modes
- [CC-perms] Claude Code: Configure permissions. https://code.claude.com/docs/en/permissions
- [CC-sandbox] Claude Code: Sandboxing. https://code.claude.com/docs/en/sandboxing
- [CC-envs] Claude Code: Choose a sandbox environment. https://code.claude.com/docs/en/sandbox-environments
- [CC-headless] Claude Code: Run Claude Code programmatically. https://code.claude.com/docs/en/headless
- [CC-env] Claude Code: Environment variables. https://code.claude.com/docs/en/env-vars
- [SRT] `anthropics/sandbox-runtime` README (v0.0.81, commit 98a4016), sections Platform Support, Configuration. https://github.com/anthropics/sandbox-runtime
- [SRT-win] Same README, section "Windows (alpha)" incl. Security model, Known limitations
- [SC-bwrap] Sandcastle v0.13.0 `src/bwrapSandbox.ts`, `src/bwrapProcess.ts`. https://github.com/mattpocock/sandcastle/tree/v0.13.0/src
- [SC-bwrap-pre] Sandcastle `src/bwrapPreflight.ts`
- [SC-seatbelt] Sandcastle `src/seatbeltProfile.ts`, `src/seatbeltSandbox.ts`
- [SC-seatbelt-pre] Sandcastle `src/seatbeltPreflight.ts`
- [SC-guide] Sandcastle `docs/guide/sandbox-providers.md` (Native sandboxes, Containment)
- [SC-adr24] Sandcastle `docs/adr/0024-native-sandbox-providers.md`
- [SC-adr15] Sandcastle `docs/adr/0015-no-sandbox-in-run-and-create-sandbox.md`
- [SC-adr19] Sandcastle `docs/adr/0019-completion-timeout-for-hanging-process.md`
- [SC-adr01] Sandcastle `docs/adr/0001-per-step-timeouts.md`
- [SC-orch] Sandcastle `src/Orchestrator.ts` (line 162; idle timeout), `src/AgentProvider.ts` (`buildClaudePrintCommand`, `permissionMode`)
- [SC-hostshell] Sandcastle `src/hostShell.ts` ("On Windows there are no process groups, so an abort kills the shell only")
- [CODEX-win] OpenAI Codex: Windows sandbox. https://learn.chatgpt.com/docs/windows/windows-sandbox
- [MXC] Microsoft MXC repository. https://github.com/microsoft/mxc
- [APPC] Microsoft Learn: AppContainer isolation. https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation
- [WSB] Microsoft Learn: Windows Sandbox. https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/
- [WSL-conf] Microsoft Learn: Advanced settings configuration in WSL. https://learn.microsoft.com/en-us/windows/wsl/wsl-config
- [JOB] Microsoft Learn: Job Objects. https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects
- [GIT] git(1) environment variables. https://git-scm.com/docs/git
