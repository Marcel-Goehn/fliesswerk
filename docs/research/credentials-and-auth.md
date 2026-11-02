# Credentials and auth: how harnesses authenticate, and how credentials reach a Run without leaking

Research for ticket #10 (map #1). Feeds the grilling ticket #17. Sources were read on 2026-11-02: Claude Code docs at code.claude.com, AWS docs, and Sandcastle `main` at commit `719347729bd0`, whose `package.json` and CHANGELOG are at 0.13.0. Every claim is cited inline as [n]; the list is at the end. **Unconfirmed** marks what I couldn't check against a primary source.

## Answer

1. **Claude Code takes every non-interactive credential from env vars or settings, so a Factory never needs a browser login inside a Run.** When several are present, it uses the first in this order: cloud provider (`CLAUDE_CODE_USE_BEDROCK` / `_VERTEX` / `_FOUNDRY`), `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, `apiKeyHelper`, `CLAUDE_CODE_OAUTH_TOKEN`, Anthropic profile/WIF, then the stored `/login` [1]. `claude setup-token` prints a **one-year** subscription token that can only make model requests. It is saved nowhere, and `--bare` doesn't read it [1].
2. **The agent can reach any credential the harness process holds, unless the harness hides it.** The agent runs as the same user in the same Sandbox, so it can read the harness's env and files. Claude Code has two built-in mitigations that the Factory can switch on:
   - `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` strips credentials from Bash, hooks and MCP subprocesses. On Linux it also puts Bash in its own PID namespace, so `/proc/<pid>/environ` of the parent can't be read. It deliberately **keeps `GH_TOKEN`/`GITHUB_TOKEN`** [3].
   - `awsCredentialExport` scopes the AWS credentials it returns to Claude Code's own Bedrock client [4].

   A credential can't be made invisible to the agent, so the goal is **short-lived, least-privilege and scrubbed**.
3. **Sandcastle keeps secrets in a gitignored `.sandcastle/.env` and hands them over as plain env vars, and it leaks in four places** [S1–S6]:
   - The values go on command lines: `docker run -e K=V`, bwrap `--setenv K V`, and `export K=…;` prefixed to the `docker exec sh -c` command under `createSandbox()`.
   - Docker stores them in the container's inspectable `Config.Env` [12].
   - `noSandbox()` hands the agent the whole host env and home.
   - Run logs and captured sessions get no redaction.

   It has no Bedrock or AWS support and no credential refresh. Its native sandboxes (bwrap, seatbelt) do hide `~/.claude`, `~/.codex` and `~/.config/gh` [S7].
4. **Bedrock works for AFK Runs if the credentials refresh without a human:**
   - Claude Code re-resolves the AWS chain 5 minutes before expiry, or hourly if there is no expiry [2].
   - An SSO profile using the `sso-session` format refreshes on its own until the IAM Identity Center session ends: **8 h by default, configurable from 15 min to 90 days** [6][7]. Role credentials last 1–12 h [8].
   - `awsAuthRefresh` (`aws sso login`) **can't help an AFK Run**: it needs a human at the browser and gets no interactive input [2].
   - A short-term `AWS_BEARER_TOKEN_BEDROCK` lasts **at most 12 h** and isn't refreshed through the chain [2][9].
   - STS `AssumeRole` from SSO credentials is role chaining, which **caps each session at 1 h** [10].
5. **Inside a container, mounting `~/.aws` is the easy path and the worst one.** With the SSO cache mounted, the agent holds the user's refreshable SSO token for every account and role they can reach. The safer options are a host-side broker that hands the Sandbox short-lived, Bedrock-scoped STS credentials:
   - as env vars per Iteration,
   - as a read-only file that `awsCredentialExport` reads, or
   - as an AWS container-credentials HTTP endpoint.

   The trade-offs are in the [options section](#options-for-getting-credentials-into-a-run).
6. **The env-var path works the same on Windows, Linux and macOS.** Claude Code's stored login is in the macOS Keychain on macOS. On Linux and Windows it is a plain `.credentials.json` file, protected by file mode `0600` on Linux and by profile ACLs on Windows [1]. **Unconfirmed:** whether a Factory can use the OS keychain for its own secrets on all three OSes without adding a dependency (see [Open questions](#open-questions-for-the-grilling-ticket)).

## Auth methods per harness

### Claude Code

| Method | How it is supplied | Lifetime / refresh | Notes for AFK Runs |
| --- | --- | --- | --- |
| Subscription OAuth token | `CLAUDE_CODE_OAUTH_TOKEN`, from `claude setup-token` [1] | 1 year. The token you set is used for the whole session; replacing it means restarting [3] | Pro/Max/Ultra/Team/Enterprise. Model requests only, no Remote Control or claude.ai connectors. Not read in `--bare` [1] |
| Stored `/login` | `~/.claude/.credentials.json` (Linux, Windows) or macOS Keychain [1] | Refreshed automatically. Warns 3 days before it expires. An unattended session that outlives it stalls [1] | Lives in the user's home, so the agent would need that home: avoid in a Sandbox |
| Anthropic API key | `ANTHROPIC_API_KEY`, always used in `-p` [1] | Static | Pay-per-token billing |
| Bearer token for a gateway | `ANTHROPIC_AUTH_TOKEN` (+ `ANTHROPIC_BASE_URL`) [1][3] | Static | For LLM gateways |
| `apiKeyHelper` | Settings key: a shell command whose output is sent as `X-Api-Key` and `Authorization: Bearer` [4] | Rerun after 5 min (`CLAUDE_CODE_API_KEY_HELPER_TTL_MS`), on 401/403, or when a JWT it returned has expired [4] | Good for vault-issued short-lived keys. Works in `--bare` via `--settings` [5] |
| Amazon Bedrock | `CLAUDE_CODE_USE_BEDROCK=1` + AWS default chain, or `AWS_BEARER_TOKEN_BEDROCK` [2] | See [Bedrock in depth](#amazon-bedrock-in-depth) | Still read in `--bare` [5] |
| Google Vertex | `CLAUDE_CODE_USE_VERTEX=1`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID`, ADC / `GOOGLE_APPLICATION_CREDENTIALS` [11] | `gcpAuthRefresh` runs a command when credentials expire. It shows output but takes no interactive input, and times out after 3 min [11] | Same AFK problem as `awsAuthRefresh` |
| Microsoft Foundry | `CLAUDE_CODE_USE_FOUNDRY`, `ANTHROPIC_FOUNDRY_API_KEY` or `ANTHROPIC_FOUNDRY_AUTH_TOKEN`, or the Azure default chain [3] | Depends on the method | Not researched further |

Three more facts the design depends on:

- **Settings sources.** Settings are layered: managed, then command line (`--settings`), then project local, shared project, and user [13]. The `--settings` flag lets the Factory inject `apiKeyHelper`, `awsCredentialExport` or `env` **from outside the repo**. Credential-masking entries are honoured only from user settings, managed settings and `--settings`, never from the repo's `.claude/settings*.json` [14].
- **`-p` doesn't ask for trust.** Without `--bare`, a `-p` session runs the hooks in the project's `.claude/settings.json` even in a folder you never trusted, and shows no trust dialog [5]. An agent that writes such a file in one Iteration gets code run by the next Iteration's harness, still inside the Sandbox. `--bare` skips that discovery but **doesn't read `CLAUDE_CODE_OAUTH_TOKEN`** [5]. That is a real tension for subscription users.
- **Claude Code's own sandbox can protect credentials.** `sandbox.credentials` can deny or *mask* files and env vars for sandboxed Bash. With mask, commands see a placeholder and a TLS-terminating proxy substitutes the real value, or re-signs SigV4 for AWS keys, on allowed hosts. There is no built-in deny list [14].

### Other harnesses (for the harness boundary)

Sandcastle's `init` lists the env var each harness needs [S3]:

| Harness | Env var(s) |
| --- | --- |
| Codex | `CODEX_API_KEY` |
| Cursor | `CURSOR_API_KEY` |
| OpenCode | `OPENCODE_API_KEY` |
| Pi | `ANTHROPIC_API_KEY` |
| Copilot CLI | `COPILOT_GITHUB_TOKEN`, then `GH_TOKEN`, then `GITHUB_TOKEN` |

Codex's own docs say it caches its login in plaintext `~/.codex/auth.json` or the OS keyring (`cli_auth_credentials_store` = `file|keyring|auto|ephemeral`). They recommend API keys for automation, and a ChatGPT login refreshes itself [15]. So every harness accepts *some* env-var credential. The harness boundary can declare "these env var names are my credentials" and leave delivery to the Factory.

## Amazon Bedrock in depth

**Minimum config** [2]:
- `CLAUDE_CODE_USE_BEDROCK=1`.
- A region. It resolves from `AWS_REGION`, then `AWS_DEFAULT_REGION`, then the profile's `region`, then `us-east-1`.
- AWS credentials.

**Strongly advised.** Pin the model IDs (`ANTHROPIC_DEFAULT_OPUS_MODEL`, `_SONNET_`, `_HAIKU_` or `ANTHROPIC_MODEL`, using inference-profile IDs such as `us.anthropic.…`). Otherwise aliases fall back at startup, and unpinned sessions bill at the Opus rate [2].

**IAM.** The identity needs these actions on inference-profile and foundation-model ARNs [2]:
- `bedrock:InvokeModel`
- `bedrock:InvokeModelWithResponseStream`
- `bedrock:ListInferenceProfiles`
- `bedrock:GetInferenceProfile`

It also needs the Marketplace subscribe actions, under the condition `aws:CalledViaLast = bedrock.amazonaws.com` [2]. This is the policy to put on a Factory-scoped role or session policy.

**Credential methods** Claude Code accepts. It uses the AWS default chain unless a bearer token is set [2]:

| Method | AFK behaviour |
| --- | --- |
| `aws configure` (IAM user keys in `~/.aws/credentials`) | Never expire. AWS advises against them [2] |
| `AWS_ACCESS_KEY_ID`/`SECRET`/`SESSION_TOKEN` env | A temporary set expires and can't be refreshed from env alone |
| SSO profile (`AWS_PROFILE`, after `aws sso login`) | Role credentials refresh automatically. With the `sso-session` format, the SSO access token also refreshes until the IAM Identity Center session ends (default 8 h, 15 min–90 days, set by an admin) [6][7]. The legacy format is fixed at 8 h with no refresh [6]. Role credentials last 1 h by default, up to 12 h, per permission set [8]. The token is cached in `~/.aws/sso/cache` [6] |
| `aws login` (console credentials, CLI ≥ 2.32) | Session lasts up to 12 h, then a human runs `aws login` again [2] |
| `AWS_BEARER_TOKEN_BEDROCK` (Bedrock API key) | Bypasses the chain completely [2]. A short-term key lasts ≤ 12 h or the issuing session, whichever is shorter, and inherits the issuer's permissions. A long-term key is backed by an IAM user and is "for exploration only" [9] |
| EC2 instance profile / ECS task role / container endpoint | Refreshes through the chain [2][16] |

**Refresh machinery in Claude Code** [2][4]:

- **Cache.** Credentials resolved from the chain are cached until 5 min before expiry, or for 1 h when they have no expiry. A credential error clears the cache and retries. `CLAUDE_CODE_SKIP_AWS_CRED_CACHE=1` resolves on every request instead.
- **Timeout.** Resolving the chain times out after 60 s (`CLAUDE_CODE_AWS_CHAIN_RESOLVE_TIMEOUT_MS`). A `credential_process` that waits for input therefore fails instead of hanging.
- **`awsAuthRefresh`.** Runs only when STS `GetCallerIdentity` confirms the credentials are expired, then re-reads `~/.aws`. Output is shown and there is no interactive input. A lock lets one of several processes run it. For AFK, it can only *start* a browser SSO flow that nobody completes. **It can't recover an expired Identity Center session unattended.**
- **`awsCredentialExport`.** Runs at session start and on every credential reload, so roughly 5 min before each expiry. It prints `{"Credentials":{AccessKeyId,SecretAccessKey,SessionToken,Expiration}}` or the flat `aws configure export-credentials --format process` shape. With a valid `Expiration`, the result is cached until 5 min before it. Claude Code keeps these credentials **scoped to its own Bedrock client**, so the Bash commands the agent runs don't see them.
- **What that means for long Runs.** The longest unattended Run is bounded by the **IAM Identity Center session**, not by the role-credential lifetime. An admin can raise it, up to 90 days. A Run of more than 8 h on default settings will fail partway unless the user signs in just before it starts. **Unconfirmed:** exactly what Claude Code does after a mid-Run expiry: whether one `-p` Step fails and the next succeeds once someone has signed in again.

**Getting Bedrock into a container or other Sandbox.** These are the options, cheapest first:

1. **Mount `~/.aws` (config + `sso/cache`, read-write).** Refresh works exactly as on the host. But the agent can read the SSO access/refresh token and get credentials for *every* account and role the user has, and the token outlives the Run. Claude Code's devcontainer guide says: "Avoid mounting host secrets such as `~/.ssh` or cloud credential files into the container", and suggests passing cloud credentials as env vars or through workload identity [17].
2. **Static temporary env per Iteration.** On the host, the Factory runs `aws configure export-credentials --profile X --format process` [18] (or the SDK). Optionally it narrows the credentials with `sts:AssumeRole` plus an inline session policy that allows only `bedrock:Invoke*` [10]. It passes them as env vars when each Step starts. A leak can only call Bedrock, and only until the credentials expire. Limit: a Step that runs longer than the credentials fails, and chained `AssumeRole` caps them at 1 h [10]. Without `AssumeRole`, the lifetime is the permission-set duration, 1–12 h [8].
3. **Read-only credential file + `awsCredentialExport`.** The Factory writes the short-lived JSON to a host file, mounts it read-only into the Sandbox, and rewrites it before expiry. It injects `{"awsCredentialExport":"cat /run/factory/aws.json"}` through `--settings`. Claude Code re-runs the export at each reload [4], so long Steps keep working, and the credentials stay out of the agent's subprocess env. The agent can still `cat` the file, so pair this with option 2's scoping. **Unconfirmed:** not tested end-to-end. Also not confirmed: whether a Windows host path mounted into a Linux container behaves the same.
4. **AWS container-credentials endpoint.** The Factory runs a tiny HTTP server on the host that returns fresh scoped credentials. The Sandbox sets `AWS_CONTAINER_CREDENTIALS_FULL_URI` and an `AWS_CONTAINER_AUTHORIZATION_TOKEN` [16]. All AWS SDKs support this, JS v3 included [16]. Every client in the Sandbox then refreshes automatically, the `aws` CLI included. The catch is the JS SDK's allowlist for plain `http`: loopback (`127.0.0.0/8`, `localhost`, `::1`) or the ECS/EKS link-local hosts only; anything else must be `https` [19].
   - **bwrap/seatbelt (shared host network):** `http://127.0.0.1:port` works.
   - **Docker on macOS/Windows:** the host is not loopback from inside, so this needs an in-container relay, `--network host` (Linux only), or TLS.
   - **Unconfirmed:** that Claude Code's Bedrock client uses the JS SDK's standard chain including this provider. The docs say "default AWS SDK credential chain" and list ECS task credentials as supported [2].
5. **`AWS_BEARER_TOKEN_BEDROCK` (short-term).** One env var, which works the same on all OSes. It lasts ≤ 12 h, has no refresh path through the chain [2][9], and is Bedrock-only by nature. That makes it simple for Runs shorter than 12 h, and the Factory could mint a new one per Step (`@aws/bedrock-token-generator`) [9].

## What Sandcastle does, and where it leaks

How it works:

- `.sandcastle/.env` holds the keys. `init` writes a `.gitignore` that contains `.env`, `logs/` and `worktrees/` [S1].
- `resolveEnv` takes each key **declared** in `.sandcastle/.env`. An empty value falls back to `process.env[key]` [S2].
- The result is merged with the `env` options of the agent and sandbox providers, which throw if they share a key [S4].
- `init` scaffolds `CLAUDE_CODE_OAUTH_TOKEN` (or `ANTHROPIC_API_KEY`) and `GH_TOKEN`, with a fine-grained PAT advice: Issues RW + Metadata R [S3].

| # | Where it leaks | Evidence |
| --- | --- | --- |
| L1 | **Command lines.** Docker and Podman get `-e K=V` on the `docker run` argv [S5]. bwrap gets `--setenv K V` on the argv of a process that lives as long as each command [S6]. Under `createSandbox()`, the agent env becomes `export K='…'; claude …` inside the `docker exec … sh -c` argv for the whole agent lifetime [S8]. On Linux, any process's `/proc/<pid>/cmdline` is readable by other users unless `/proc` is mounted with `hidepid` [20]. | `DockerLifecycle.ts` startContainer, `podman.ts`, `bwrapSandbox.ts` envArgs, `createSandbox.ts` withAgentEnv |
| L2 | **Container metadata.** Values passed with `-e` are stored in the container config and returned by `docker inspect` (`Config.Env`) to anyone who can reach the Docker socket, for as long as the container exists [12]. | Engine API `ContainerInspectResponse.Config` → `ContainerConfig.Env` |
| L3 | **Agent-readable env.** Everything in `.sandcastle/.env` reaches the agent, including `GH_TOKEN` for the tracker. Sandcastle only sets env; the harness passes it through. Sandcastle's own docs warn not to put an `E2B_API_KEY` in `.sandcastle/.env` "because the keys declared there are forwarded into the sandbox" [S7]. | `EnvResolver.ts`, `sandbox-providers.md` |
| L4 | **`noSandbox()`.** The process env is `{...process.env, ...env}`, the whole host env, and the host home with `~/.claude` and `~/.aws` stays reachable. ADR 0024 names this as the motivation for native sandboxes [S9]. | `no-sandbox.ts` |
| L5 | **Logs and session copies.** Run logs (`.sandcastle/logs/`, gitignored) record agent output, tool calls and, in verbose mode, raw stdout. Session capture copies Claude Code transcripts to the host `~/.claude/projects`. I found no redaction code anywhere in `src/` [S10]. An agent that runs `env` or `cat`s a token writes it to both places. | `run-logs.md`, `SessionStore.ts` |
| L6 | **`copyToWorktree: [".env"]`.** The documented example copies the repo's `.env` into the worktree, where the agent could `git add -f` it [S11]. | `run.md` |
| L7 | **No refresh.** The env is fixed when the Sandbox starts, so credentials that expire mid-Run aren't renewed. There is no Bedrock, AWS or Vertex template, doc or code path. | grep for `AWS`/`Bedrock` in `src`/`docs`: none |

What Sandcastle does well:

- bwrap and seatbelt start with `--clearenv` or an explicit env, and give the agent a fresh private home that is deleted on close, so host logins stay hidden. The caveat: seatbelt doesn't promise to keep the agent away from the macOS Keychain [S6][S7].
- Host-side git neutralises credential helpers that the repo itself configured [S12].
- `DockerError` messages carry only stderr, not argv [S5].

## Options for getting credentials into a Run

| Option | Leaks avoided | Costs / limits | OS notes |
| --- | --- | --- | --- |
| **A. Env vars set at process spawn** (`spawn(..., {env})`, `docker run -e NAME` *without a value*, or `--env-file`) | L1 when values never reach argv. `docker run -e NAME` takes the value from the CLI's own env [21] | L2 remains for Docker, because inspect still shows the value. Agent-readable unless the harness scrubs | Works the same on all three |
| **B. A read-only secret file mounted at a fixed path** (Compose-style `/run/secrets/x` [22]) + harness reads it (`apiKeyHelper: cat …`, `awsCredentialExport: cat …`) | L1, L2. Keeps secrets out of subprocess env | The agent can `cat` it. Needs a harness hook that reads files (Claude Code: yes; others: unknown) | Bind mounts on Windows/macOS Docker go through the VM (unconfirmed for this use) |
| **C. Short-lived, scoped credentials minted per Run or Step** (STS + session policy, Bedrock short-term key, `apiKeyHelper` against a vault) | Limits how bad L3 and L5 can get | Needs the Factory to talk to AWS or a vault on the host. Chained STS ≤ 1 h [10] | Same everywhere if done in the Factory's own language runtime |
| **D. Host-side credential broker** (container-credentials endpoint [16]) | Same as C, plus refresh for every SDK client | HTTP allowlist forces loopback or https [19]. Adds a long-lived host process | Easy for native sandboxes. Docker Desktop needs a relay |
| **E. Harness-side scrubbing / masking** (`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` [3], `sandbox.credentials` deny/mask [14]) | L3 for subprocesses, so a prompt-injected `env` shows nothing | Claude Code only. The scrub keeps GitHub tokens on purpose [3]. Masking needs TLS termination and an allowlist [14] | PID-namespace part is Linux only [3] |
| **F. Redact known secret values in Factory logs** | L5 | Only catches exact values the Factory knows. Transcripts written by the harness are outside its control | — |
| **G. OS keychain as the Factory's at-rest store** | Keeps secrets out of repo files like `.sandcastle/.env` | **Unconfirmed:** a dependency-free CLI exists on macOS (`security`). On Linux, `secret-tool` needs libsecret installed. On Windows, there's no built-in CLI that *reads* generic credentials. Needs its own research | Uneven |

Two cross-cutting points:

- **The tracker token is a separate credential.** `GH_TOKEN` needs a different scope from the model credential, and the "code decides everything" rule on map #1 means the Factory, not the agent, does the closing and commenting. If the agent never calls `gh`, the tracker token never has to enter the Sandbox at all. That removes the one credential Claude Code's scrub deliberately leaves in place [3].
- **The harness and the model provider are separate axes on the map.** The same Bedrock credential machinery would serve Pi or OpenCode on Bedrock, but their env-var names and refresh hooks differ. This research doesn't confirm them.

## Open questions for the grilling ticket

- Should the agent ever hold a tracker token, or does the Factory do every tracker write on the host?
- Where do secrets live at rest: the user's shell env only, a gitignored file like Sandcastle's, the OS keychain, or delegated to provider tooling (`aws sso`, `claude setup-token`)?
- For Bedrock AFK Runs: which delivery option, B, C or D? And does the Factory pre-flight check that the Identity Center session outlives the expected Run (for example, by reading the expiry in `~/.aws/sso/cache` on the host)?
- `--bare` vs subscription token: `--bare` blocks repo-planted hooks but ignores `CLAUDE_CODE_OAUTH_TOKEN` [5]. Does the Factory require API key, `apiKeyHelper` or Bedrock for hardened Runs?
- Is `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` on by default in Factory Runs?
- Does the Factory need a secret-value redactor for its Step logs?

## Sources

Claude Code docs (code.claude.com, fetched 2026-11-02):

1. Authentication: methods, precedence, credential storage, `setup-token`. https://code.claude.com/docs/en/authentication
2. Claude Code on Amazon Bedrock: credentials, caching, `awsAuthRefresh`/`awsCredentialExport`, IAM, model pinning. https://code.claude.com/docs/en/amazon-bedrock
3. Environment variables, incl. `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` and "What the subprocess environment scrub removes", `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_SKIP_AWS_CRED_CACHE`. https://code.claude.com/docs/en/env-vars
4. Settings reference: `apiKeyHelper`, `awsAuthRefresh`, `awsCredentialExport`. https://code.claude.com/docs/en/settings-reference
5. Headless / bare mode. https://code.claude.com/docs/en/headless
6. AWS SDK reference: IAM Identity Center credential provider. https://docs.aws.amazon.com/sdkref/latest/guide/feature-sso-credentials.html
7. IAM Identity Center: user interactive sessions (default 8 h, 15 min–90 days). https://docs.aws.amazon.com/singlesignon/latest/userguide/user-interactive-sessions.html and the CLI/SDK considerations page https://docs.aws.amazon.com/singlesignon/latest/userguide/user-session-duration-prereqs-considerations.html
8. IAM Identity Center: permission set session duration (1 h default, 12 h max). https://docs.aws.amazon.com/singlesignon/latest/userguide/howtosessionduration.html
9. Amazon Bedrock API keys. https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html
10. STS `AssumeRole`: `DurationSeconds`, role chaining 1 h, session policies. https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html
11. Claude Code on Google Cloud's Agent Platform (Vertex), incl. `gcpAuthRefresh`. https://code.claude.com/docs/en/google-vertex-ai
12. Docker Engine API `swagger.yaml`: `ContainerInspectResponse.Config` is `ContainerConfig`, whose `Env` is `["VAR=value", …]`. https://raw.githubusercontent.com/moby/moby/master/api/swagger.yaml
13. Claude Code settings files and precedence. https://code.claude.com/docs/en/settings
14. Claude Code sandboxing: Protect / Mask credentials. https://code.claude.com/docs/en/sandboxing
15. Codex authentication. https://learn.chatgpt.com/docs/auth (redirect target of developers.openai.com/codex/auth)
16. AWS SDK reference: container credential provider. https://docs.aws.amazon.com/sdkref/latest/guide/feature-container-credentials.html
17. Claude Code dev containers. https://code.claude.com/docs/en/devcontainer
18. AWS CLI `configure export-credentials`. https://docs.aws.amazon.com/cli/latest/reference/configure/export-credentials.html
19. aws-sdk-js-v3 `packages-internal/credential-provider-http/src/fromHttp/checkUrl.ts` (main). https://github.com/aws/aws-sdk-js-v3/blob/main/packages-internal/credential-provider-http/src/fromHttp/checkUrl.ts
20. proc(5): `/proc/pid/cmdline` and the `hidepid` mount option. https://man7.org/linux/man-pages/man5/proc.5.html (standard Linux behaviour; not re-fetched for this note)
21. `docker container run`: `-e VAR` without a value takes it from the client env; `--env-file`. https://docs.docker.com/reference/cli/docker/container/run/
22. Docker Compose secrets. https://docs.docker.com/compose/how-tos/use-secrets/

Sandcastle source (github.com/mattpocock/sandcastle @ `719347729bd0`, v0.13.0):

- S1. `src/InitService.ts`: `GITIGNORE` const (`.env`, `logs/`, `worktrees/`), `.env.example` writing.
- S2. `src/EnvResolver.ts`: `resolveEnv`, declared keys only, `process.env` fallback.
- S3. `src/InitService.ts`: `AGENT_REGISTRY` `envExample` blocks, GitHub tracker `envExample`, `envNotes`.
- S4. `src/mergeProviderEnv.ts`.
- S5. `src/DockerLifecycle.ts`: `startContainer` env flags, `dockerExec` error messages; `src/sandboxes/podman.ts` env args.
- S6. `src/bwrapSandbox.ts`: `envArgs` (`--setenv`), `--clearenv`; `src/seatbeltSandbox.ts` env via spawn.
- S7. `docs/guide/sandbox-providers.md`: native-sandbox "Logins" and "Env" bullets, E2B key warning, Containment section.
- S8. `src/createSandbox.ts`: `withAgentEnv` (`export K=…;` prefix); `src/sandboxes/docker.ts` `exec` (`docker exec … sh -c <command>`).
- S9. `src/sandboxes/no-sandbox.ts` (`{ ...process.env, ...createOptions.env }`); `docs/adr/0024-native-sandbox-providers.md`.
- S10. `docs/guide/run-logs.md`; `src/SessionStore.ts`; no `redact`/`mask` code in `src/`.
- S11. `docs/guide/run.md` (`copyToWorktree: [".env"]`).
- S12. `src/hostGitConfig.ts`: `credentialOverrides`.
