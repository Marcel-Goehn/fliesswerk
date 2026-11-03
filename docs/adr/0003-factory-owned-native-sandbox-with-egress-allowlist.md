# A Factory-owned native Sandbox with deny-by-default network and filesystem

Besides the container Sandbox, the Factory offers a native Sandbox: the Step runs on the user's machine, wrapped as a whole process in an OS sandbox. That is bubblewrap on Linux and WSL2, and a `(deny default)` Seatbelt profile on macOS. All outbound traffic goes through an egress proxy built into the Factory's Go binary, which enforces a domain allowlist. Reads are closed except system roots and the toolchains on `PATH`, the agent gets a private home, and writes reach only the Iteration's worktree, minus git control files and Protected paths. Inside that wall the harness runs fully powerful, with prompts skipped and its own sandbox turned off, because Anthropic itself says permission modes and allow/deny rules are not a safety boundary. The same network policy applies to container Sandboxes, so switching kinds doesn't change what the agent can reach. A missing prerequisite stops the Run with the exact fix. The Factory never falls back to running unsandboxed.

## Considered Options

- **Permission rules only (`dontAsk`/`auto`)**: no dependencies and identical on every OS, but not a boundary, by Anthropic's own documentation.
- **Claude Code's built-in sandbox**: covers shell commands only (file tools, MCP and hooks run outside it), leaves reads open by default, has no native Windows support, and helps no other harness.
- **Delegating to Anthropic's `srt`**: an Anthropic-maintained policy, but it needs Node and npm on the host (we install without a package manager), its config is beta, reads are open by default, and its Windows backend is alpha with documented egress holes.
- **Sandcastle's `bwrap()`/`seatbelt()` as they are**: a good filesystem boundary, but egress is wide open, host localhost included.

## Consequences

- The Factory owns an egress proxy. The model token still has to reach the provider, so exfiltration over an allowed host, and domain fronting through broad domains, remain documented residual risks.
- Native Windows has no native Sandbox in v1: container or `none` only, and native works inside WSL2. An MXC-based backend may come later.
- Two escape hatches, `kind = "none"` and network `allow = ["*"]`, are accepted only from `config.local.toml` or a CLI flag, never from the committed config, so a committed change can't disarm the Sandbox for everyone. Every Run prints a warning when either is active.
