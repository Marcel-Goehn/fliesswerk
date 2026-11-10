# Config reference

Every key the Factory reads, what it does and its default. The same shape is published as a JSON Schema, [`schema/config.schema.json`](../schema/config.schema.json), so your editor can autocomplete and validate the files. A test keeps this page and the schema in step.

## How the config is read

The config is TOML in `.kraftwerk/`, read in three layers, each merged over the one before key by key:

1. `config.toml`: the team's settings, committed
2. `config.local.toml`: your own settings, git-ignored
3. CLI flags: `--sandbox`, `--i-accept-unsandboxed-risk`, `--allow`, `--model` and `--max-parallel`

A list replaces the list of the layer below; it is not appended to. No layer may hold a secret value: a secret is always a reference to where it comes from (see [Secret sources](#secret-sources)).

Every command checks the config before it does anything else, so a typo never costs a token. An unknown key or a wrong type is an error, with a did-you-mean suggestion. `kraftwerk check` exits with 3 on any error.

**Local-only settings** can disarm the Sandbox or hold a credential, so they are refused in `config.toml` and accepted only from `config.local.toml` or a flag: credentials, `sandbox.kind = "none"` and its acknowledgement, `allow = ["*"]`, and the acknowledgement for passing AWS keys through. A committed change can never turn them on for the whole team.

**Upgrades.** Within a major version, an unchanged config never changes behaviour, except for security fixes, which are announced loudly. A renamed key keeps working with a deprecation warning until the next major version, and `kraftwerk migrate` rewrites it. See the [install guide](install.md#renamed-and-removed-config-keys).

Durations are written like `"30s"`, `"5m"` or `"1h30m"`.

### Secret sources

Every key that takes a secret takes a table with exactly one of these:

| Source | Example | Reads the secret from |
| --- | --- | --- |
| `stored` | `{ stored = "claude" }` | Your user-level credentials file, written by `kraftwerk auth set <key>` |
| `env` | `{ env = "ANTHROPIC_API_KEY" }` | An environment variable of the Factory |
| `command` | `{ command = "op read op://dev/claude/token" }` | The stdout of a shell command |
| `dotenv` | `{ dotenv = "CLAUDE_TOKEN" }` | `.kraftwerk/.env`, which must be git-ignored |

## Top-level keys

### `version`

The config format version. Must be `1`, and is the only required key. A version newer than this Kraftwerk understands is refused with the command that updates it.

### `factory`

A semver range the Factory's version must match, such as `"1"`, `"~1.2"` or `">=1.2 <2"`. A teammate on a version outside it gets an error that names the command to run. Unset, any version is accepted.

### `update_notice`

Whether `check`, `run` and `release` say when a newer release is out. They ask GitHub at most once a day and never in CI. Default `true`.

### `harness`

The Harness every Step uses unless the Step sets its own. Default and only value in v1: `"claude_code"`.

### `model`

The model every Step uses unless the Step sets its own, such as `"claude-opus-5-5"`. Unset, the Harness's own default is used. `--model` overrides it for one Run.

### `effort`

The reasoning effort every Step uses unless the Step sets its own. Claude Code takes `"low"`, `"medium"`, `"high"`, `"xhigh"` and `"max"`. Unset, the Harness's own default is used.

### `max_parallel`

How many Tickets an Iteration works on at once. Default `1`, which works Tickets one after another. Memory can lower it for an Iteration: see the [Services guide](services.md#memory). `--max-parallel` overrides it for one Run.

### `max_iterations`

How many Iterations a Run may have. Default `10`.

### `verify`

The names of Feedback commands from `[commands]` that the Factory runs itself after the last Step that changed code, and again on the merged result in a parallel Run. A Ticket only lands when they all pass. `init` sets it to the well-known commands you gave (`test`, `typecheck`, `lint`, `format`). Each name must be in `[commands]`.

### `standards`

The Coding standards file, relative to the project root. Only the review Step reads it, through the `{{standards}}` placeholder of its prompt. It is a Protected path. Unset, review checks against its built-in baseline only.

### `protected_paths`

Extra Protected paths: files, directories (ending in `/`) or globs, relative to the project root, that the agent may not change. `.kraftwerk/`, the compose file and the standards file are always protected. A Step that changes one gets its Ticket handed back with no retry. In a native Sandbox they are also read-only.

```toml
protected_paths = [".claude/", ".github/workflows/", "secrets/*.pem"]
```

## `tracker`

Where Tickets live.

### `tracker.kind`

The Tracker. Only `"github"` in v1.

### `tracker.repo`

The repository, as `owner/name`.

### `tracker.ready_label`

The label that marks a Ticket Ready for agent. Default `"ready-for-agent"`.

### `tracker.in_progress_label`

The label that marks a claimed Spec or standalone Ticket. Default `"in-progress"`. The label must exist in the repository.

### `tracker.cannot_do_label`

The label that replaces the ready label on a Ticket the Factory hands back to a person. Default `"needs-investigation"`. The label must exist in the repository.

### `tracker.token`

Where the GitHub token comes from, as a [secret source](#secret-sources). Local only. Unset, `gh` uses its own login. The token is used on your machine only and never reaches the agent. See the [setup guide](setup.md#creating-a-fine-grained-token) for a token that reaches one repository.

## `branches`

Branch names.

### `branches.base_branch`

The remote branch Spec branches are cut from, on `origin`. Default `"main"`. The Factory never pushes to it.

### `branches.prefix`

The prefix of every branch the Factory creates: `<prefix>/spec-<N>-<slug>`, `<prefix>/ticket-<N>` and `<prefix>/ticket-<N>-<slug>`. Default `"kraftwerk"`.

## `sandbox`

The Sandbox each Step runs in. See [Choosing a Sandbox kind](setup.md#choosing-a-sandbox-kind).

### `sandbox.kind`

`"container"` (default), `"native"` or `"none"`. `"none"` is local only and also needs `i_accept_unsandboxed_risk = true`. `--sandbox` overrides it for one Run.

### `sandbox.i_accept_unsandboxed_risk`

Set to `true` with `kind = "none"`: you accept that the agent runs with no Sandbox and can read and change anything your user can. Local only, or `--i-accept-unsandboxed-risk`. Every Run warns while it is on.

### `sandbox.run_args`

Extra flags for the container engine when it creates a container Sandbox. Flags that collide with what the Factory manages are refused, such as `--name`, `--rm`, `--network`, `--entrypoint`, `--pid`, `--env-file`, `--env-host`, a mount over the workspace, or an `--env` that could set a credential variable. `--privileged` and socket mounts are allowed with a warning.

### `sandbox.read_only_paths`

Extra paths a native Sandbox may read, absolute or starting with `~/`. Engine sockets, D-Bus, keychains and, under WSL2, the Windows side are refused.

### `sandbox.writable_paths`

Extra paths a native Sandbox may write, with the same rules. `check` warns about paths that reach past the Sandbox, such as your home directory, shell startup files or a directory on your `PATH`.

### `sandbox.scrub_credentials`

Whether the agent's own commands run without credentials in their environment. Default `true`.

The Harness needs its credential, but the commands the agent runs, such as your tests, don't. With scrubbing on, those commands, the Factory's own `verify` run included, get neither the Harness's credential nor the [project secrets](#sandboxsecrets). Only Hooks get project secrets.

That is the trade-off. Say your tests need a `DATABASE_URL` with a password:

- **Keep scrubbing on** (recommended), and give the tests the address in a way that isn't a secret. A test database in a [Service](services.md) has a throwaway password, so put the whole URL in `[services.postgres] env` or `[sandbox.env]`. A Hook that needs a real secret, for seeding say, gets it from `[sandbox.secrets]`.
- **Turn scrubbing off** with `scrub_credentials = false`, and the agent's commands and `verify` get every project secret. The agent's own commands also get the Harness's credential. The agent can then read them and send them to any allowed host. `check` warns about it when there are project secrets.

### `sandbox.network`

Network access from the Sandbox. Every connection goes through the Factory's egress proxy, which lets through only the hosts on this list plus the Harness's own endpoints, and logs every host it denies.

### `sandbox.network.allow`

Hosts the Sandbox may reach, besides the Harness's endpoints, which are added for you: `api.anthropic.com` for `oauth_token` and `api_key`, the `base_url` host for `gateway`, and `bedrock-runtime.<region>.amazonaws.com` for `bedrock`. `init` adds the registries your languages install from. `["*"]` allows every host, and is local only and warned about. `--allow` overrides the list for one Run.

```toml
[sandbox.network]
allow = ["registry.npmjs.org", "proxy.golang.org", "sum.golang.org"]
```

No GitHub endpoint is ever added: the agent has no Tracker access.

### `sandbox.env`

Environment variables set in every Sandbox. They are plain text in the config, so they are not for secrets: a value that looks like one gets a warning naming the `[sandbox.secrets]` line to use instead.

### `sandbox.env.<name>`

One variable and its value.

```toml
[sandbox.env]
NODE_ENV = "test"
```

### `sandbox.secrets`

Project secrets, by the environment variable that holds them, each from a [secret source](#secret-sources). Every Hook in a Sandbox gets them all, and a host Hook gets only the ones its `secrets` names. With `scrub_credentials = false` the agent gets them too.

### `sandbox.secrets.<name>`

One project secret.

```toml
[sandbox.secrets]
STRIPE_TEST_KEY = { env = "STRIPE_TEST_KEY" }
```

## `network`

Your machine's network, for the egress proxy and for image builds. See [Behind a corporate proxy](corporate-proxy.md).

### `network.ca_certs`

PEM files trusted in every Sandbox on top of its system roots, and passed into image builds as the build secret `kraftwerk-ca`. A path can be absolute, start with `~/`, or be relative to the project root.

### `network.upstream_proxy`

The proxy the egress proxy chains to, such as `"http://proxy.corp.example:8080"`. It replaces the host's `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. Unset, the egress proxy follows those variables.

## `services`

Services from the compose file, by name. Only the named Services start, with what they `depends_on`. See the [Services guide](services.md).

### `services.compose_file`

The compose file Services come from, relative to the project root. Default `"compose.yaml"`. It is a Protected path when any Service is named.

### `services.<name>`

A Service from the compose file. By default every Sandbox gets its own copy with fresh data.

### `services.<name>.shared`

Set to `true` for one copy that serves the whole Run, for Services too heavy to run once per Sandbox. Default `false`.

### `services.<name>.env`

Environment variables set in every Sandbox, with `{host}` and `{port}` replaced by where that Sandbox reaches the Service.

### `services.<name>.env.<name>`

One variable and its template.

```toml
[services.postgres]
env = { DATABASE_URL = "postgres://app:app@{host}:{port}/app" }
```

## `commands`

Feedback commands, by name: how the agent and the Factory check that a change works. They are declared once and listed to every Step through the `{{commands}}` placeholder of the prompts.

### `commands.<name>`

One Feedback command, run by the shell in the project's directory inside the worktree: the directory that holds `.kraftwerk/`.

```toml
[commands]
test      = "pnpm test"
typecheck = "pnpm typecheck"
```

## `hooks`

Shell commands the Factory runs at named moments of a Run. Each Hook point is a list of entries, which run one after another in the order written. The first failure ends the Hook point.

```toml
[[hooks.sandbox_created]]
parallel = ["cd web && pnpm install --frozen-lockfile", "cd api && uv sync --locked"]
timeout  = "10m"

[[hooks.spec_finished]]
run = 'gh pr create --fill --head "$KRAFTWERK_BRANCH"'
```

### `hooks.run_start`

Runs on your machine when a Run starts, after the credentials are resolved. A failure stops the Run with exit code 2.

### `hooks.sandbox_created`

Runs when a Sandbox has been created and its Services are healthy, before the agent starts. It runs in the Sandbox by default, with `KRAFTWERK_SANDBOX` set. A failure is a Step failure: the Hook gets one fresh retry, then the Ticket is handed back.

### `hooks.spec_finished`

Runs on your machine in the Spec branch's worktree when a Spec's last Ticket closes, or a standalone Ticket closes, with `KRAFTWERK_SPEC` (such as `#31`) and `KRAFTWERK_BRANCH` set. A failure only warns.

### `hooks.run_end`

Runs on your machine when a Run ends, however it ended. A failure only warns.

### `hooks.<point>[].run`

One shell command. Each entry sets exactly one of `run` and `parallel`.

### `hooks.<point>[].parallel`

Shell commands run at once. On the first failure the others are stopped, and every command's output is reported.

### `hooks.<point>[].where`

Where the entry runs: `"sandbox"` or `"host"`. Only `sandbox_created` may run in the Sandbox, and does by default. Every other point runs on the host.

### `hooks.<point>[].timeout`

Stop the entry after this long. Default `"5m"`.

### `hooks.<point>[].secrets`

The names of the [project secrets](#sandboxsecrets) a host entry gets. An entry in the Sandbox gets them all. Each name must be in `[sandbox.secrets]`. A host Hook never gets the Harness's credential or the Tracker token.

## `steps`

Per-Step settings. There are three Steps: implement, review and merge. Each Step's table takes the keys below.

```toml
[steps.review]
model  = "claude-sonnet-5-5"
effort = "high"
```

### `steps.implement`

Settings of the implement Step, which writes the code.

### `steps.review`

Settings of the review Step, which reviews the change in a fresh session and fixes what it finds.

### `steps.merge`

Settings of the merge Step, which resolves a real merge conflict. Clean merges are done by code.

### `steps.<step>.enabled`

Whether the Step runs. Only review can be turned off in v1: `[steps.review] enabled = false` skips it. Implement and merge always run. Default `true`.

### `steps.<step>.harness`

The Harness for this Step. A Step that sets it must also set `model`.

### `steps.<step>.model`

The model for this Step, overriding the top-level `model`.

### `steps.<step>.effort`

The reasoning effort for this Step, overriding the top-level `effort`. Setting it for a Harness that has no effort setting is a `check` error.

### `steps.<step>.idle_timeout`

Stop the Step after this long without output. Default `"10m"`. A warning comes at half of it.

### `steps.<step>.timeout`

Stop the Step after this long in all. Default `"60m"`. A timed-out Step is killed with everything it started, and gets one fresh retry.

## `harnesses`

Per-Harness settings, by Harness name. They say how you sign in, so they belong in `config.local.toml`. See [Signing in to Claude](setup.md#signing-in-to-claude).

### `harnesses.claude_code`

How Claude Code signs in.

```toml
[harnesses.claude_code]
method = "bedrock"
region = "eu-central-1"
profile = "dev"
```

### `harnesses.claude_code.method`

How to sign in: `"oauth_token"` (a Claude subscription, with a token from `claude setup-token`), `"api_key"`, `"bedrock"` or `"gateway"`. Unset, the Factory hands Claude Code no credential and tests none, which works only where Claude Code finds a login of its own, as with `kind = "none"`.

### `harnesses.claude_code.secret`

Where the subscription token, API key or gateway token comes from, as a [secret source](#secret-sources). Local only.

### `harnesses.claude_code.base_url`

The gateway's URL, with `method = "gateway"`. Its host is allowed for the Sandbox.

### `harnesses.claude_code.region`

The AWS region, with `method = "bedrock"`.

### `harnesses.claude_code.profile`

The AWS profile, with `method = "bedrock"`: SSO, `~/.aws/credentials` or `credential_process`. Give either this or the access keys.

### `harnesses.claude_code.access_key_id`

Where the AWS access key ID comes from, with `method = "bedrock"`, as a [secret source](#secret-sources). Local only.

### `harnesses.claude_code.secret_access_key`

Where the AWS secret access key comes from, with `method = "bedrock"`, as a [secret source](#secret-sources). Local only.

### `harnesses.claude_code.role_arn`

A role to assume for Bedrock, with `method = "bedrock"`.

### `harnesses.claude_code.i_accept_long_lived_keys_in_sandbox`

Set to `true` to hand your own AWS credentials to every Sandbox as they are, instead of the short-lived, Bedrock-only ones the Factory mints. The agent can read them, and every Run warns. Local only.

## `logs`

Run logs, in `.kraftwerk/logs/`. See the [usage guide](usage.md#the-log-directory).

### `logs.keep_days`

How many days a Run's logs are kept: a positive whole number, or `"forever"`. Default `30`. Older Run directories are removed when a Run starts.

## Residual risks

The Sandbox narrows what an agent can do; it doesn't make the agent trustworthy. These risks remain with every setting above:

- **Exfiltration over an allowed host.** The agent can send what it can read, including its own model token, to any host on the allowlist, or hide traffic to another host behind an allowed one (domain fronting). Keep `allow` short.
- **Harness-written transcripts.** The Factory redacts everything it writes, but not the transcripts the Harness itself keeps.
- **A process escaping the kill on macOS.** A process that calls `setsid` leaves the process group the Factory kills when a Step ends.
- **Hooks planted in `.claude/`.** Claude Code runs hooks from the project's `.claude/` inside the Sandbox. Protect `.claude/` with `protected_paths`, as `init` suggests, so an agent can't plant one for a later Step.
- **Project secrets with scrubbing off.** The agent can read every project secret. See [`sandbox.scrub_credentials`](#sandboxscrub_credentials).
- **Shared Services.** Every Sandbox reaches the same copy, so an agent can read or change another Ticket's data.
- **`kind = "none"`.** The agent can read and change anything your user can.
