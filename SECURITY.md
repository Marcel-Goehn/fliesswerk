# Security policy

The Factory's job is containment: the Sandbox, the egress allowlist, Protected paths, keeping secrets out of logs and Sandboxes, and verified installs and updates. If you find a way around any of them, please tell us privately.

## How to report

Report a vulnerability through GitHub's private vulnerability reporting: [open a private report](https://github.com/Marcel-Goehn/fliesswerk/security/advisories/new).

Never open a public issue for a vulnerability. Public issues from outside the project are closed without review, and the report would be public before a fix exists.

## What to include

- The version, from `fliesswerk --version`
- Your OS and its version
- The Sandbox kind (`container` or `native`) and its engine: Docker or Podman for a container Sandbox, bubblewrap or Seatbelt for a native Sandbox
- Steps to reproduce, and what happened compared with what you expected

## Supported versions

Before v1, only the latest release gets fixes. Update with `fliesswerk update` before reporting, and check the problem is still there.

## In scope

- Escaping the Sandbox
- Reaching a host that isn't on the allowlist
- Changing a Protected path without the Iteration being rejected
- A secret reaching a log, a Ticket, a commit or a Sandbox it shouldn't
- One Sandbox reaching another Sandbox's own Services (a Service marked shared is reached by every Sandbox on purpose)
- The install scripts or `fliesswerk update` installing something whose checksum doesn't match

## Out of scope

- The risks already documented in the configuration reference's [Residual risks](docs/config.md#residual-risks) section
- Anything that happens with `sandbox.kind = "none"`, which runs the agent with no Sandbox by your choice
- Bugs in Claude Code, Docker, Podman, bubblewrap, Seatbelt or `gh` themselves. Please report those to their own projects

## What to expect

Fliesswerk is a best-effort volunteer project, and there is no bug bounty. A fix ships as a new release, and its notes credit the reporter unless you'd rather not be named.
