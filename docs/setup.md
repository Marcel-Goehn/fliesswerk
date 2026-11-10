# Setting up a project

This guide takes a project from nothing to its first Run. It covers `fliesswerk init`, what a teammate does after cloning a project that is already set up, how to sign in to Claude, the fine-grained GitHub token, importing a Bedrock setup and choosing a Sandbox kind.

Install Fliesswerk first: see the [install guide](install.md).

## What you need

### What everyone needs

| Tool | Version | Used for |
| --- | --- | --- |
| git | 2.38.0 or newer | Branches, worktrees and pushes, with your own git credentials |
| `gh` | 2.94.0 or newer, logged in or with a token | Reading and writing your GitHub Issues |
| Claude Code (`claude`) | 2.1.319 or newer | The Harness. With a container Sandbox it is installed in the image, and you need it on your machine only for `claude setup-token` |

### What your Sandbox kind needs

Pick the row for your Sandbox kind. If you're not sure, take `container`. See [Choosing a Sandbox kind](#choosing-a-sandbox-kind) and the [support matrix](support-matrix.md).

| Sandbox kind | You need |
| --- | --- |
| `container` (the default) | Docker or Podman |
| `native` on Linux or WSL2 | bubblewrap (`bwrap`) |
| `native` on macOS | Nothing: Seatbelt is part of macOS 14 or newer |

If your project has Services, such as a database, they always run in Docker or Podman, whichever Sandbox kind you choose. A container Sandbox has that engine already. Services also need its compose tool: Docker Compose or `podman-compose`. The [Services guide](services.md#setting-them-up) has their versions.

You don't need to check these by hand. `fliesswerk check` tests what your setup uses and names the install command for your OS.

## Running `init`

Run `fliesswerk init` in your project's checkout. It looks at the repo, asks its questions and writes nothing until the last question has an answer. Press Enter to take the suggestion in brackets, or type `-` to leave it out.

The project questions come first, in this order:

1. **Where your Tickets live.** GitHub Issues is the only Tracker in v1.
2. **The repository**, as `owner/name`. Suggested from your git remote.
3. **The base branch** that Spec branches start from. Suggested from the remote's default branch.
4. **The ready label** that marks a Ticket Ready for agent. Default `ready-for-agent`.
5. **Languages.** Node, Go, Python and Rust have recipes. `init` suggests the ones it finds and names any language it found without a recipe.
6. **Feedback commands**: `test`, `typecheck`, `lint` and `format`, suggested from your project files. The ones you set become `verify`, which the Factory runs itself to check every change.
7. **The compose file** for the Services the agent needs, such as a database. See the [Services guide](services.md).
8. **Where the agent runs**: the Sandbox kind. See [Choosing a Sandbox kind](#choosing-a-sandbox-kind).
9. **The Coding standards file**, which only the review Step reads.
10. **Protecting `.claude/`**, if your project has one. Say yes: a hook planted there would run in a later Step.

Then come your own questions, described under [Signing in to Claude](#signing-in-to-claude) and [Reaching GitHub](#reaching-github). If something on the way inspects TLS, such as a corporate proxy, `init` also offers to export its root certificate for your Sandboxes. See [Behind a corporate proxy](corporate-proxy.md).

### What `init` writes

| File | Committed | Holds |
| --- | --- | --- |
| `.fliesswerk/config.toml` | Yes | The team's settings, with every default written out and commented |
| `.fliesswerk/Containerfile` | Yes | The image every container Sandbox starts from (container kind only) |
| `.fliesswerk/prompts/implement.md`, `review.md`, `merge.md` | Yes | The part of each prompt your team may edit |
| `.fliesswerk/.gitignore` | Yes | Keeps the files below out of git. Your root `.gitignore` is never touched |
| `.fliesswerk/config.local.toml` | No | Your own settings: how you sign in, your tokens' sources, and any `kind = "none"` |

Secrets you type go to your user-level credentials file, never into the project, unless you choose to keep one in `.fliesswerk/.env` (see [Signing in to Claude](#signing-in-to-claude)). It is `fliesswerk/credentials` in your user config directory (`~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows), readable only by you and shared by all your projects.

The `sandbox_created` Hook installs your dependencies from the lockfile in every fresh Sandbox, and the registries those installs need are on the network allowlist. Every setting is explained in the [config reference](config.md).

### Finishing the Containerfile

For a language without a recipe, the Containerfile has a block marked `TODO(fliesswerk)`. Install the language there and delete the marker line. `check` and `run` refuse to start while a marker is left. Add anything else your Feedback commands need under "system packages".

### The end of `init`

`init` ends by running `fliesswerk check --commands`. It builds the image, resolves every credential, tests the ones the Factory uses itself (GitHub, and AWS for Bedrock), does a dry-run push, starts a Sandbox, and runs your `sandbox_created` Hook and every Feedback command once on the base branch, with no agent and no tokens spent. A command that already fails on the base branch shows up here, so the agent isn't blamed for it.

It ends with either:

```
Ready: commit `.fliesswerk/`, label a Ticket, run `fliesswerk run`
```

or the exact lines to fix. Fix them and run `fliesswerk check --commands` again.

### Labels

A Run needs three labels in the repository: the ready label (default `ready-for-agent`), the `in_progress_label` (default `in-progress`) it adds to a Spec it claims, and the `cannot_do_label` (default `needs-investigation`) it swaps the ready label for on a Ticket it hands back. GitHub can only add labels that exist, so `init` creates those that are missing, by the names in `[tracker]`, and lists each one it created (`+ label in-progress`). It leaves a label that already exists exactly as it is, color and description included. GitHub compares label names without regard to case, so `Ready-For-Agent` counts as `ready-for-agent`.

When `init` can't create a label (your login or token can't write to Issues, or GitHub can't be reached), it still writes its files and prints the lines to run instead. With the default names, they are:

```sh
gh label create ready-for-agent --repo owner/name --description "Fliesswerk may work on it"
gh label create in-progress --repo owner/name --description "Claimed by a Fliesswerk Run"
gh label create needs-investigation --repo owner/name --description "Fliesswerk handed it back to a person"
```

Until the labels exist, `check` fails with exit code 3 and names each missing one, and a Run stops before it claims anything. Both say to run `fliesswerk init` again, or to create the label yourself with the `gh label create` line.

### Running `init` again

`init` never overwrites a file. Run it again at any time: it creates only what's missing (`.fliesswerk/.gitignore`, the Containerfile for the container kind, `config.local.toml`, and any of the Tracker's labels that went missing), says "Already set up" if nothing is missing, and runs `check`. To change a setting, edit the file.

## A teammate cloning a set-up repo

When `.fliesswerk/config.toml` is already committed, `fliesswerk init` asks only your own questions: how you sign in to Claude, where your secrets are kept and how you reach GitHub. It writes them to your `config.local.toml`, which is never committed, so you can't change the team's config by accident. Then it runs `check --commands`.

To use something other than what the team chose, override it in `config.local.toml`. It is merged over `config.toml` key by key, and CLI flags are merged over both. For example, a cheaper model for yourself:

```toml
model = "claude-sonnet-5-5"
```

If the team pinned a version with `factory = "…"`, a Fliesswerk outside that range fails validation and names the command that installs a matching one.

## Signing in to Claude

`init` asks how you sign in, then where the secret is kept.

| Method | `method` | You need | The agent may reach |
| --- | --- | --- | --- |
| A Claude subscription (Pro or Max) | `oauth_token` | A long-lived token from `claude setup-token` | `api.anthropic.com` |
| An Anthropic API key | `api_key` | The key | `api.anthropic.com` |
| Amazon Bedrock | `bedrock` | A region, and a profile or access keys | `bedrock-runtime.<region>.amazonaws.com` |
| A gateway that speaks Anthropic's API | `gateway` | Its URL and token | The gateway's host |

The host is added to the network allowlist for you. Every secret is resolved at `check` and at the start of every Run, so a missing environment variable, a failing `command` or a missing `.env` key stops the Run before any work starts.

Which method to use:

- **A subscription** is for one person's own Runs. The token is yours, made by you with `claude setup-token`; never share it or use someone else's. Anthropic's advertised usage limits assume ordinary, individual use, so long or parallel Runs may reach them.
- **An API key, Bedrock or a gateway** is for a team, a shared machine, CI or heavy use. Usage is billed to the owner of the key or account.

You are responsible for following Anthropic's terms for your own account. They are on Anthropic's [legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance).

The Factory never sends your Claude credential itself: only Claude Code does, from inside the Sandbox (see [ADR 0007](adr/0007-only-the-harness-sends-its-credential.md)). So `check` reports a subscription token, an API key or a gateway token as found but not tested ahead of time, and Claude Code checks it on the first Step. If Claude Code rejects it, the Run stops at once with exit code 2 and says how to fix it, such as `claude setup-token` for a subscription, and no Ticket is held against it. Bedrock is the exception: the Factory mints the Sandbox's short-lived AWS credentials from yours, so `check` and every Run start test those with AWS STS.

A secret can be kept in one of four places:

| Source | In the config | Notes |
| --- | --- | --- |
| Your credentials file | `{ stored = "claude_api_key" }` | Recommended, except for a subscription token, which it never holds. `fliesswerk auth set <key>` stores or replaces a secret from a hidden prompt |
| An environment variable | `{ env = "NAME" }` | |
| A command that prints it | `{ command = "op read op://…" }` | Such as a password manager. Its output is the secret |
| `.fliesswerk/.env` | `{ dotenv = "KEY" }` | The Factory refuses to start if the file isn't git-ignored |

A Claude subscription token is never kept in your credentials file: Anthropic's terms say developers ["may not collect, store, or intermediate Claude.ai credentials or session tokens"](https://code.claude.com/docs/en/legal-and-compliance). For a subscription, `init` offers only the other three, suggests `{ env = "CLAUDE_CODE_OAUTH_TOKEN" }` as Anthropic does, and never asks for the token itself. Set it where you'd set any environment variable, or have your password manager print it.

When you choose `.fliesswerk/.env`, `init` suggests the same name as for an environment variable, such as `CLAUDE_CODE_OAUTH_TOKEN`, and fills the file in once it has written `.fliesswerk/.gitignore`:

- **A subscription token**: `init` never asks for it. It adds the empty line `CLAUDE_CODE_OAUTH_TOKEN=` and names the file's full path. Paste the token from `claude setup-token` after the `=`, save the file and press Enter. `init` reads the token only at its check, as `check` and every Run start do.
- **Any other secret** (an API key, a gateway token, the GitHub token): `init` asks for it with a hidden prompt and adds it under the name you chose.

Either way, `init` never overwrites the file: it only appends a missing key, and a key that already has a value is used without asking. A new file is readable only by you. If `.fliesswerk/.env` isn't git-ignored, `init` writes no secret there and says why. Leave the secret empty, and `init` still finishes, but its check fails and names the file and the key until you add it.

The resulting `config.local.toml` looks like this:

```toml
[harnesses.claude_code]
method = "oauth_token"
secret = { env = "CLAUDE_CODE_OAUTH_TOKEN" }
```

Your secret reaches each Step through its environment when the Step starts. It is never on a command line, in a container's configuration or in the image, and it is masked as `***` in every log and Tracker comment.

### Importing a Bedrock setup

If your `~/.claude/settings.json` turns on Bedrock (`CLAUDE_CODE_USE_BEDROCK`), `init` offers to use the same region and profile, read from `AWS_REGION` and `AWS_PROFILE` in that file. Only where to sign in comes across, never a credential. Say no to enter the details yourself.

With Bedrock the agent never holds your own AWS credentials. On your machine the Factory turns them into short-lived credentials that can only call Bedrock, hands them to the Sandbox in a read-only file, and renews them before they expire. You can give either:

- a `profile`: SSO, `~/.aws/credentials` or `credential_process`. A Run warns when the SSO session ends within 2 hours.
- `access_key_id` and `secret_access_key`, each from a secret source, optionally with `role_arn` to assume a role.

To pass your own keys into the Sandbox unchanged instead, set `i_accept_long_lived_keys_in_sandbox = true` in `config.local.toml`. The agent can then read them, and every Run warns.

## Reaching GitHub

The Factory reads and writes your Issues with `gh`, on your machine only. The agent never gets a GitHub token or `gh`. If you are logged in to `gh`, `init` offers that login. Otherwise, or if you'd rather not hand the Factory your full login, it walks you through a fine-grained token for this one repository.

### Creating a fine-grained token

1. Open <https://github.com/settings/personal-access-tokens/new>.
2. Name it, and set **Resource owner** to the repository's owner.
3. Under **Repository access**, choose **Only select repositories** and pick the repository.
4. Under **Repository permissions**, set **Issues** to **Read and write**, and **Contents** and **Pull requests** to **Read-only**. **Metadata** is always read-only.
5. Generate the token and copy it.

`init` then asks where the token is kept, as for the Claude secret. It ends up in `config.local.toml`:

```toml
[tracker]
token = { stored = "github.owner.repo" }
```

The token goes to `gh` as `GH_TOKEN` in its environment, never on its command line. Pushing a Spec branch uses your own git credentials for the remote, not this token, and `check` makes sure they work with a dry-run push.

## Choosing a Sandbox kind

Every Step runs in a Sandbox. The network is denied by default for every kind except `none`: only the hosts on the allowlist and your Harness's own endpoints are reachable, through the Factory's egress proxy.

| Kind | Runs the agent | Needs | Pick it when |
| --- | --- | --- | --- |
| `container` (default) | In a container built from `.fliesswerk/Containerfile` | Docker or Podman | You can use a container engine. It is the strongest isolation and the same on every OS |
| `native` | On your machine, fenced in by bubblewrap (Linux, WSL2) or Seatbelt (macOS 14 or newer) | `bwrap` on Linux and WSL2, nothing on macOS | You can't or don't want to run containers, and your toolchain is on your machine |
| `none` | On your machine, not fenced in at all | Your acknowledgement | You accept that the agent can read and change anything your user can |

A native Sandbox reads only the system roots, your `PATH` toolchains and the OS trust store, and writes only to its own worktree. Your home directory, `~/.ssh`, `~/.aws` and your other projects stay out of reach, and so do the Docker and Podman sockets, D-Bus and the Keychain. On Ubuntu 24.04 and later, AppArmor blocks bubblewrap by default, and `check` prints the profile that fixes it.

On Windows, use `container`, or the native Sandbox inside WSL2. There is no native kind on Windows itself, and neither of these is tested in CI yet: see the [support matrix](support-matrix.md).

On macOS, the container Sandbox is the better choice when you have Services: a Seatbelt Sandbox reaches them only on random ports. See the [Services guide](services.md#native-sandboxes).

A missing prerequisite stops with the exact fix. The Factory never falls back to another kind.

### Running with no Sandbox

`kind = "none"` keeps the worktree isolation, the timeouts, the Protected path check and the non-interactive environment, but nothing else. A committed `config.toml` can't set it, so a change in the repo can never disarm the team's Sandbox. Set it for yourself in `config.local.toml`:

```toml
[sandbox]
kind = "none"
i_accept_unsandboxed_risk = true
```

or for one Run with `--sandbox none --i-accept-unsandboxed-risk`. Every Run warns while it is on. Choosing "not fenced in at all" in `init` writes the same lines to your `config.local.toml` and leaves the team on `container`.

## Teaching the agent your conventions

Each Step's prompt is a fixed part in the binary around your file in `.fliesswerk/prompts/`. Edit your part freely: the fixed part holds the Factory's protocol, so your edits can't break it. Two placeholders are available, each rendering its whole section or nothing: `{{commands}}` lists your Feedback commands, and `{{standards}}` points at your Coding standards file. Any other placeholder is a `check` error.

When a new release improves a built-in prompt, `check` tells you, and `fliesswerk prompts diff <step>` shows what changed, for you to take over as you like.

## Next

Label a Ticket `ready-for-agent`, commit `.fliesswerk/`, and read the [usage guide](usage.md).
