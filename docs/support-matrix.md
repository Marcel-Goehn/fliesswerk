# Support matrix

Which operating systems, container engines and Sandbox kinds the Factory is tested on. **Tested in CI** means automated tests run the setup on real machines for every change, or every night where marked. **Untested** means it should work, but no test proves it: please [report an issue](https://github.com/Marcel-Goehn/fliesswerk/issues) if it doesn't.

Whatever your setup, `fliesswerk check` starts a real Sandbox of your kind and probes it: a Service is reachable, direct internet access is blocked, and an allowed host is reachable through the egress proxy. An untested setup that doesn't work fails there, before any Run.

## Sandboxes and engines

| OS | Setup | Status |
| --- | --- | --- |
| Linux x64 | Container Sandbox with Docker Engine | Tested in CI |
| Linux x64 | Container Sandbox with rootless Podman | Tested in CI |
| Linux x64 | Native Sandbox (bubblewrap), on stock Ubuntu 24.04 | Tested in CI |
| Linux arm64 | Container Sandbox with Docker Engine | Tested in CI, nightly |
| Linux arm64 | Container Sandbox with rootless Podman | Tested in CI, nightly |
| Linux arm64 | Native Sandbox (bubblewrap) | Tested in CI, nightly |
| macOS | Native Sandbox (Seatbelt), with Services stood in by a local TCP listener | Tested in CI |
| Windows | `kind = "none"` | Tested in CI |
| Windows | `install.ps1` | Tested in CI |
| Any | Docker Desktop | Untested |
| macOS | Colima | Untested |
| macOS, Windows | Podman machine | Untested |
| Windows | Docker Desktop's WSL2 integration | Untested |
| macOS | Container Sandbox | Untested |
| Windows | Container Sandbox | Untested |
| Windows | Native Sandbox inside WSL2 | Untested |

The whole unit and end-to-end suite also runs on every change on Linux x64, macOS arm64 and Windows x64. Every release is installed with its install script and smoke-tested on the same three.

## Not supported

- **A native Sandbox on Windows itself.** Use the container Sandbox, or the native Sandbox inside WSL2. Both should work, but neither is tested in CI.
- **The native Sandbox on macOS before 14.**
- **Docker Desktop in Windows-containers mode.** `check` detects it and says so.
- **Proxies that need a login** (Basic, NTLM, Kerberos) and PAC files. See [Behind a corporate proxy](corporate-proxy.md).
- **Trackers other than GitHub Issues and Harnesses other than Claude Code.** v1 has one of each.

## Tool versions

`check` requires at least these, and names the install command for your OS when one is missing or too old:

| Tool | Minimum |
| --- | --- |
| git | 2.38.0 |
| `gh` | 2.94.0 |
| Claude Code | 2.1.319 |
| Docker Compose | 2.24.4 |
| `podman-compose` | 1.4.0 |
