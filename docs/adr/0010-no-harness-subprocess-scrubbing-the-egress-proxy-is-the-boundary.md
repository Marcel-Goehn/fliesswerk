# No Harness subprocess scrubbing; the Egress proxy is the boundary

The Factory used to set `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` so that the agent's commands couldn't read the Harness's credential. Claude Code 2.1.322 does that by running every command in its own bubblewrap, and that doesn't work where Steps run. In a container Sandbox, rootless Podman refuses the `/proc` mount and Docker refuses the namespaces (#83). In a native Sandbox, it is bubblewrap inside the Factory's bubblewrap, and stock Ubuntu's `bwrap-userns-restrict` AppArmor profile strips the inner bwrap of the capabilities it needs (#85). Under scrubbing, Claude Code also forces its default permission mode and its own sandbox, and cuts its commands off the network. All of that goes against ADR 0003.

So the Factory never asks a Harness to scrub, for any Sandbox kind, `none` included. The Sandbox walls and the Egress proxy are the boundary. A command that goes looking can read the Harness's credential (e.g. from `/proc/<pid>/environ`), and that's accepted. It can leave only through allowed hosts, which ADR 0003 already lists as a residual risk. We loosen no Sandbox wall to protect the credential: an agent that can read the token is far less dangerous than one that can reach more of the network.

The Factory itself still withholds Project secrets from the agent and from its own Feedback-command runs (`sandbox.withhold_secrets`, default `true`; it was `scrub_credentials`). Only Hooks get them, unless the user turns that off. This doesn't depend on any Harness.

## Considered Options

- **Let nested bubblewrap work**: give container Sandboxes `/proc` mounts and new namespaces, and give native Sandboxes on Ubuntu an unconfined AppArmor profile for bwrap. It weakens the very walls the Sandbox is for, needs sudo or engine-specific flags per platform, and Claude Code would still need `socat`, explicit `allowedTools` and a way through its `--unshare-net`.
- **Pass the credential by file descriptor** (`CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR` and similar): keeps it out of every environment, but it relies on Harness internals that can change with any release, and it only stops the easy way of reading the token.
- **Keep scrubbing for `kind = "none"` only**: there it would work, but two behaviours for one setting is harder to explain, and `none` is already the unprotected escape hatch.

## Consequences

- No Sandbox kind needs bubblewrap for the Harness's sake, and `check` drops its bubblewrap fix.
- Don't bring back Harness scrubbing that needs a sandbox inside the Sandbox, or a loosened Sandbox wall to protect the credential.
