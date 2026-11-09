# Sandboxes reach Services over a Factory-owned internal network, with egress through a proxy sidecar

A container Sandbox never joins the compose network: it is a normal bridge with a default route, so it would bypass the egress allowlist. Instead the Factory creates an `--internal` network for each Sandbox, connects every Service container to it with the service name as alias, and removes the network when the Sandbox is destroyed. Sandboxes therefore reach `postgres:5432`, never each other and never the internet directly. The only way out is one proxy sidecar per Run: the egress proxy from the Factory binary, built locally into an image with `docker|podman import` from a static Linux build embedded in every release, connected to every internal network and to one normal network. The native Sandbox reaches published Service ports instead. Under bwrap a Factory-side forwarder and an in-sandbox forwarder (the Factory binary itself) expose them as `<service>:<containerPort>` through a generated hosts entry. Under Seatbelt, per-port `localhost` rules allow the published host port. The Factory pins the Compose project name and one engine connection for compose, networks and Sandboxes alike, and finds Service containers with `compose ps`, so any compose provider works.

## Considered Options

- **Joining the compose network as it is**: the simplest option, but it gives the Sandbox direct internet access (tested on Docker and rootless Podman).
- **One internal network per Run**: fewer network operations, but parallel Sandboxes could reach each other, so one prompt-injected agent could reach into another Ticket's Sandbox.
- **Reaching the host proxy via the internal network's gateway IP**: works only on rootful Linux Docker. Under rootless Podman the gateway is in the rootless netns, not on the host.
- **A bind-mounted Unix socket to the host proxy**: works on Linux Docker and Podman, but is doubtful across the Docker Desktop, Colima and Podman machine VM boundary.
- **A proxy image on a registry (GHCR)**: a second release artifact and a pull at Run time, with a version that could drift from the binary's.

## Consequences

- One code path for every engine and OS: everything the sidecar needs is engine-local, so it lives inside the VM on macOS and Windows. Linux Docker and Podman are tested in CI. The Docker Desktop, Colima and Podman machine rows stay untested until someone confirms them, and the docs carry a support matrix that says so.
- `check` runs a live probe for every Sandbox kind: Service reachable, direct egress blocked, allowed host reachable through the proxy. Unconfirmed combinations fail loudly with a fix instead of silently.
- The embedded proxy has to match the engine's architecture (from `docker info`), not the host's. Docker Desktop in Windows-containers mode can't run it, and `check` says so.
- With a native Sandbox, a Service that publishes no port is a `check` and Run-start error naming the fix (`ports: ["127.0.0.1:<H>:<C>"]`). Each Sandbox reaches only the published ports of the configured Services, never other localhost ports.
- Under Seatbelt the agent uses the published host port, which may differ from the container port. So every Sandbox gets `KRAFTWERK_SERVICE_<NAME>_HOST` / `_PORT`, and the fixed prompt contract lists the reachable addresses.
- Under bwrap a container port below 1024 is forwarded on its published port instead, because AppArmor's `unpriv_bwrap` profile on stock Ubuntu denies capabilities inside the namespace. The prompt and the variables give that port.
- A corporate proxy listening on the host's own `localhost` can't be reached from the sidecar. Zscaler (transparent, or explicit at a remote address) is unaffected.
- Amended by [ADR 0005](0005-own-services-per-sandbox-with-shared-opt-out.md): each Sandbox gets its own Services by default, in its own Compose project, and only Services marked `shared` exist once per Run. Every Service is published on random ports, never on the ports in the user's compose file.
