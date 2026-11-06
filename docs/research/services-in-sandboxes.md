# Can Sandboxes reach host Compose services on every OS and container runtime? (as of 2026-11-06)

**Question:** Can a Sandbox reach Compose services that the Factory started on the host, on every supported OS and container runtime, and what does each combination need? See [the ticket](https://github.com/Marcel-Goehn/kraftwerk/issues/25).

**Fixed already** (from [How does a user configure and extend the Factory?](https://github.com/Marcel-Goehn/kraftwerk/issues/11) and [ADR 0003](../adr/0003-factory-owned-native-sandbox-with-egress-allowlist.md)):
- The Factory runs `docker compose up` on the host.
- A container Sandbox joins the compose network.
- A native Sandbox reaches only the Compose service ports on host localhost.
- The same network policy (deny by default, allowlist through the Factory's egress proxy) applies to both kinds.

**Method:**
- **Tested hands-on** on Ubuntu 26.04.1 with Docker Engine 29.1.3 (rootful), Docker Compose 2.40.3, rootless Podman 5.7.0 (netavark, `rootlessNetworkCmd: pasta`) and bubblewrap 0.11.1. Marked **[tested]**.
- **From primary docs or source code** (Docker, Podman, Lima, Microsoft WSL docs, Anthropic's `sandbox-runtime` at commit `27fce0d`, the Sandcastle checkout at `ed8e716`). Each claim is linked.
- macOS and Windows **could not be tested**. Anything not backed by a doc or a test is marked **unconfirmed**.

## Short answer

1. **Joining the compose network works on every container runtime.** The Sandbox and the services run on the same engine, so this holds on Linux, macOS and Windows alike. The agent reaches `service:containerPort` by DNS. Ports published on `127.0.0.1` play no part in this. The only requirement is that the Factory runs compose and the Sandbox on **the same engine and connection** (same Docker context or Podman connection, and the same rootful or rootless mode).
2. **But joining the compose network as it is breaks the egress policy.** A compose network is a normal bridge with a default route, so the Sandbox gets direct internet access that bypasses the proxy **[tested]**. To keep "the same policy for container Sandboxes", the Factory has to:
   - create its own `--internal` network,
   - `network connect --alias <service>` each service container to it,
   - attach the Sandbox only to that network.

   This works on rootful Docker and rootless Podman **[tested]**.
3. **How the container Sandbox reaches the proxy differs by runtime.**
   - Rootful Docker on Linux: the host is reachable on the internal network's gateway IP **[tested]**.
   - Rootless Podman: that gateway lives in the rootless netns, not on the host, so it isn't reachable **[tested]**.
   - Portable options: a Unix socket bind-mounted into the Sandbox (works on Linux Docker and Podman **[tested]**, unconfirmed across a VM boundary), or a proxy sidecar container on the internal network.
4. **Native Sandbox under bwrap:** with `--unshare-net` the sandbox has only its own loopback **[tested]**.
   - Host `127.0.0.1:<port>` is unreachable from inside, so neither the services nor a TCP proxy on the host can be reached directly.
   - The Factory, outside the netns, can reach host localhost trivially.
   - The working pattern: a Unix socket bind-mounted into the sandbox, plus an in-sandbox forwarder listening on `127.0.0.1:<port>` inside the netns **[tested]**. This is how `sandbox-runtime` does it.
   - Because each Sandbox has its own netns, parallel Sandboxes can all listen on `localhost:5432` without clashing.
5. **Native Sandbox under Seatbelt:** there is no network namespace. The profile can allow `connect()` to specific `localhost:<port>` pairs, so the agent reaches the published ports directly (from `sandbox-runtime` source, untested here).
   - A forwarder can't take over the same port number, because the published port already holds it on the shared host stack.
   - Seatbelt's `localhost` doesn't match IPv4-mapped `::ffff:127.0.0.1`, so clients that connect through dual-stack sockets are denied.
6. **Native Sandboxes need published ports.** A service that publishes no port can't be reached from a native Sandbox at all. The host port is whatever compose published, read with `docker compose port`.
7. **`host.docker.internal` / `host.containers.internal` / `--add-host host-gateway` are not needed** for the decided design, and on Linux they **don't** reach ports published on `127.0.0.1` anyway **[tested]**. This matches Sandcastle having no `--add-host` escape hatch. `[sandbox] run_args` stays the escape hatch for users who need it.

## Matrix

"Join" means the Sandbox container joins a network that the service containers are on. "Loopback port" means a port published as `127.0.0.1:H:C`.

| Combination | Joining the compose network | What's needed | 127.0.0.1-only published ports | Rootless networking | Container Sandbox to host egress proxy |
|---|---|---|---|---|---|
| **Linux, Docker (rootful)** | Works **[tested]**. `service` and container name (`proj-svc-1`) resolve | `--network <proj>_default` (or the Factory's internal net plus `network connect --alias`) | Irrelevant inside the network. Bound on host loopback by docker-proxy. **Not** reachable via `host.docker.internal:host-gateway` or `172.17.0.1` **[tested]** | n/a | Host listener on the internal network's gateway IP is reachable **[tested]**, but depends on the host firewall (unconfirmed with ufw/firewalld). Unix socket mount also works **[tested]** |
| **Linux, Podman rootless** | Works **[tested]** via `podman compose` (delegates to docker-compose if installed). Network gets DNS and compose labels | Same flags. Use the user's rootless connection, which has its own networks separate from rootful | Published by `rootlessport` on host `127.0.0.1` **[tested]**. `host.containers.internal` (169.254.1.2 via pasta `--map-guest-addr`) does **not** reach a loopback port from a bridge network **[tested]** | Podman 5 default is pasta. Bridge networks run in a separate rootless netns, so container IPs aren't host-reachable | Internal network gateway is **not** the host: connection refused **[tested]**. Unix socket mount works **[tested]**. Sidecar works in principle (unconfirmed) |
| **macOS, Docker Desktop** | Works: engine-local, same as Linux (unconfirmed by test, follows from both containers on one engine in the VM) | Same flags. Docker Desktop has `host.docker.internal` built in | Published ports are bound on the Mac by `com.docker.backend`. Binding to `127.0.0.1` is supported, so a native Sandbox reaches them on Mac loopback | n/a (VM) | Unconfirmed: whether `host.docker.internal` reaches a loopback-only Mac listener, and whether arbitrary Unix sockets can be bind-mounted from macOS (only the SSH agent socket is documented). Sidecar is the safe choice |
| **macOS, Colima** | Works (engine-local, Docker runtime in a Lima VM). Unconfirmed by test | Same flags, on the `colima` Docker context | Lima forwards guest ports bound on `127.0.0.1` or `0.0.0.0` to **host `127.0.0.1`** by default, so loopback-published ports land on Mac loopback | n/a (Docker in the VM is rootful) | Lima lets the guest reach Mac loopback via `host.lima.internal` (192.168.5.2), but `hostLoopback` can turn that off. From a container on an internal network: unconfirmed. Sidecar is the safe choice |
| **macOS, Podman machine** | Works (engine-local in the machine VM). Unconfirmed by test | Same flags. Machine is rootless by default (`--rootful` switches it). Use the same connection for compose and Sandbox | Forwarded by gvproxy to the Mac. Exact host bind address for `127.0.0.1` publishes is unconfirmed | Rootless inside the VM, so the rootless-Podman column applies inside the VM | Unconfirmed. Sidecar is the safe choice |
| **Windows, Docker Desktop (WSL 2 engine)** | Works (engine-local). Unconfirmed by test | Same flags | Published on Windows `localhost` by Docker Desktop (unconfirmed by test) | n/a | Unconfirmed. Sidecar is the safe choice |
| **Windows, Podman machine (WSL)** | Works (engine-local). Unconfirmed by test | Same flags | A user reported that a plain `-p` publish wasn't reachable on Windows `127.0.0.1` while `127.0.0.1:H:C` was ([podman#17972](https://github.com/containers/podman/issues/17972)) | Rootless by default in the machine | Unconfirmed. Sidecar is the safe choice |
| **WSL2 distro, Docker Engine installed in the distro** | Same as Linux Docker | Same as Linux | Same as Linux | Same as Linux | Same as Linux |
| **WSL2 distro, Docker Desktop integration** | Works (engine-local) | Same flags | Unconfirmed whether published ports are reachable on the **distro's** `localhost`. This is the case that matters for a native Sandbox inside WSL2 | n/a | Under Docker VMM there's no Docker socket in the distro, and distro paths (`/home`, `/tmp`) can't be bind-mounted, so no socket mount |
| **Native, bwrap (Linux, WSL2)** | n/a | `--unshare-net`, a Unix socket per allowed port (or one multiplexed socket) bind-mounted in, and an in-sandbox forwarder on `127.0.0.1:<port>` | Host loopback is unreachable from inside the netns **[tested]**. The Factory outside forwards to `127.0.0.1:<published port>`. Forwarder ports can equal the container ports, with no clash between parallel Sandboxes | n/a | The proxy runs outside the netns and reaches host localhost **[tested]**. Raw TCP (Postgres, Redis) needs the forwarder, because such clients ignore `HTTP_PROXY` |
| **Native, Seatbelt (macOS)** | n/a | `(allow network-outbound (remote ip "localhost:<port>"))` per published port, next to the proxy port. Host token can only be `localhost` or `*` | Shared host stack: the agent connects to the published host port directly. A forwarder can't reuse the same port number, and parallel Sandboxes share the ports | n/a | Proxy on Mac loopback is reachable through the same per-port rule (as `sandbox-runtime` does). Dual-stack `::ffff:127.0.0.1` connects are not matched |

## Details and sources

### Compose network names and DNS

- "Compose creates a network named `<project-name>_default` and attaches all services to it." The project name comes from the directory, `--project-name` or `COMPOSE_PROJECT_NAME`. Networks can get a custom `name:`, and "each service registers its name with an internal DNS server." Source: [Docker docs, Networking in Compose](https://docs.docker.com/compose/how-tos/networking/).
- Compose labels every network it creates with `com.docker.compose.project` and `com.docker.compose.network` **[tested]**, on both Docker and Podman (via docker-compose). So `docker network ls --filter label=com.docker.compose.project=<p>` finds every network, custom-named ones included. The Factory should pass `-p <project>` itself, so the name is never guessed from the directory.
- "On a user-defined bridge network, containers can resolve each other by name or alias." Source: [Docker docs, Bridge network driver](https://docs.docker.com/engine/network/drivers/bridge/).
- On Podman, the default `podman` network "does not support dns resolution because of backwards compatibility with Docker" ([Podman basic networking tutorial](https://github.com/containers/podman/blob/main/docs/tutorials/basic_networking.md)). Compose-created networks have DNS enabled (`DNSEnabled=true` **[tested]**).
- `podman compose` "is a thin wrapper around an external compose provider such as docker-compose or podman-compose", and "if installed, `docker-compose` takes precedence". `PODMAN_COMPOSE_PROVIDER` chooses explicitly. Source: [podman-compose(1)](https://docs.podman.io/en/latest/markdown/podman-compose.1.html). The Factory should pin the provider. `podman-compose` naming and labels were not checked (**unconfirmed**).
- A service can sit on several compose networks. The Sandbox must reach every network its target services are on. The internal-network design below avoids that problem, because the Factory connects each service to one network it owns.

### Joining the compose network leaks egress, and the internal-network design

- Tested on Docker and rootless Podman: a container on `<proj>_default` reached `mail:8025` **and** `example.com:80`. Compose networks route to the internet.
- `--internal`: "Containers on an internal network may communicate between each other, but not with any other network, as no default route is configured and firewall rules are set up to drop all traffic to or from other networks. Communication with the gateway IP address (and thus appropriately configured host services) is possible." Source: [docker network create](https://docs.docker.com/reference/cli/docker/network/create/). For Podman, `--internal` "restrict[s] external access of this network" ([podman-network-create(1)](https://docs.podman.io/en/latest/markdown/podman-network-create.1.html)).
- `docker network connect --alias` "can be used to resolve the container by another name in the network being connected to" ([docker network connect](https://docs.docker.com/reference/cli/docker/network/connect/)). Podman supports per-network aliases the same way ([podman-run(1)](https://docs.podman.io/en/latest/markdown/podman-run.1.html)).
- Tested on both runtimes: `network create --internal kwk-run1`, then `network connect --alias mail kwk-run1 kwknet-mail-1`. A Sandbox on `kwk-run1` only:
  - reached `mail:8025` and `kwknet-mail-1:8025`,
  - failed `example.com:80` and `1.1.1.1:443`.

  The service's own `127.0.0.1` publish kept working from the host.
- **Reaching the proxy from that internal network:**
  - Docker rootful: a host listener bound to the internal network's gateway (`172.20.0.1`) was reachable **[tested]**.
  - Rootless Podman: the gateway (`10.89.2.1`) refused, and `host.containers.internal` was unreachable **[tested]**. "When rootless containers are run, network operations will be executed inside an extra network namespace" ([Podman basic networking](https://github.com/containers/podman/blob/main/docs/tutorials/basic_networking.md)).
  - A Unix socket bind-mounted into the container worked on both **[tested]**.
  - For macOS and Windows, VM file sharing makes socket mounts doubtful. Docker Desktop documents only its special SSH agent socket `/run/host-services/ssh-auth.sock` ([Docker Desktop networking how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)), so arbitrary sockets are **unconfirmed**.
  - The engine-independent option is a **proxy sidecar container**: the Factory's static Linux binary, attached to the internal network and to a normal network. It is the only egress path. This is **unconfirmed** by test, but it relies only on the network primitives above.
- Parallel Sandboxes on one internal network can reach each other by name. A per-Sandbox internal network (each service connected to each) isolates them. Connecting one container to many networks is supported, but the limits are **unconfirmed**.

### 127.0.0.1-only published ports and host-gateway names

- Docker Desktop: "By default, `docker run -p` listens on all network interfaces (`0.0.0.0`)". Binding to `127.0.0.1` restricts it, and the backend process (`com.docker.backend`) listens on the host port. Source: [Docker Desktop networking](https://docs.docker.com/desktop/features/networking/).
- `host.docker.internal` "resolves to the internal IP address of your host" on Docker Desktop ([how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)). Whether it reaches a **loopback-only** listener on the Mac or Windows host is not documented (**unconfirmed**).
- Linux Docker: `--add-host host.docker.internal:host-gateway` resolves to the bridge gateway. A port published on `127.0.0.1:18025` was **not** reachable through it, nor through `172.17.0.1` **[tested]**.
- Podman: under pasta, "to make the host.containers.internal /etc/hosts entry work and allow connections to the host, --map-guest-addr 169.254.1.2 is passed by default" ([podman-run(1)](https://docs.podman.io/en/latest/markdown/podman-run.1.html)). The entry was present in the container (`169.254.1.2 host.containers.internal host.docker.internal`), but a loopback-published port was not reachable through it from a bridge network **[tested]**. Podman 5 switched the rootless default from slirp4netns to pasta, which "can cause problems for users of the host.containers.internal name entry" ([Podman 5.0 breaking changes](https://blog.podman.io/2024/03/podman-5-0-breaking-changes-in-detail/)).
- Rootless Podman port publishing on bridge networks "uses rootlessport by default", which "does not preserve client source IPs" ([podman-run(1)](https://docs.podman.io/en/latest/markdown/podman-run.1.html), [basic networking](https://github.com/containers/podman/blob/main/docs/tutorials/basic_networking.md)). Observed listener: `127.0.0.1:18025 rootlessport` **[tested]**.
- Lima/Colima: guest ports default to `guestIP: "127.0.0.1"` ("also matches bind addresses `0.0.0.0`, `::`, and `::1`") and forward to `hostIP` "default value `127.0.0.1`". `networkOpts.hostLoopback` controls whether the guest can reach the Mac's loopback via `host.lima.internal` (192.168.5.2). Source: [Lima default template](https://github.com/lima-vm/lima/blob/master/templates/default.yaml) and [Lima port forwarding docs](https://lima-vm.io/docs/config/port/).
- WSL: in NAT mode Windows reaches Linux apps via `localhost`, and Linux reaches Windows apps only via the host IP. In mirrored mode both sides use `127.0.0.1` (IPv6 `::1` not supported). Source: [Microsoft, Accessing network applications with WSL](https://learn.microsoft.com/en-us/windows/wsl/networking).
- Docker Desktop on WSL with Docker VMM: "There's no Docker socket inside your distribution", and "you can't bind-mount files that are stored inside your distribution, such as paths under `/home` or `/tmp`" ([Docker Desktop WSL](https://docs.docker.com/desktop/features/wsl/)). That also affects mounting the worktree into a container Sandbox, which belongs to a separate question.

### Native Sandbox: bwrap

- `bwrap --unshare-all` (which includes `--unshare-net`) leaves only `lo 127.0.0.1/8 ::1/128` **[tested]**.
  - `127.0.0.1:<host port>` gave connection refused.
  - `1.1.1.1:443` gave network unreachable.
- A Unix socket bind-mounted into the sandbox, with `socat TCP-LISTEN:5432,bind=127.0.0.1 … UNIX-CONNECT:/tmp/svc.sock` inside and the Factory-side end connecting to host `127.0.0.1:<published port>`, gave HTTP 200 **[tested]**. The Factory's Go binary can play both ends, so there's no `socat` dependency.
- Anthropic's `sandbox-runtime` does exactly this for its proxies. Its Linux network sandboxing "uses bwrap --unshare-net", with host-side bridges that "listen on Unix sockets and forward to host proxy servers" and in-sandbox socat listeners ([linux-sandbox-utils.ts](https://github.com/anthropic-experimental/sandbox-runtime/blob/main/src/sandbox/linux-sandbox-utils.ts)).
- Sandcastle's `bwrap()` uses `--share-net` (`src/bwrapSandbox.ts`), which gives open egress including host localhost **[tested]**. That's the gap ADR 0003 already names.

### Native Sandbox: Seatbelt

- Seatbelt has no network namespace. `sandbox-runtime` restricts the network by allowing `(remote ip "localhost:<proxyPort>")` for outbound and binding. Its source says that Seatbelt "only accepts `localhost` or `*` as the host token", and that `(remote ip "localhost:*")` "matches connect() to 127.0.0.1 and ::1 but not ::ffff:127.0.0.1", so dual-stack runtimes need AF_INET. Source: [macos-sandbox-utils.ts](https://github.com/anthropic-experimental/sandbox-runtime/blob/main/src/sandbox/macos-sandbox-utils.ts).
- So per-port allows for the published Compose ports work in the same profile. This is **unconfirmed by test** (no macOS here), but `sandbox-runtime` ships the same construct for its proxy ports.
- Sandcastle's Seatbelt profile has `(allow network*)` ("Open egress (and host localhost …)", `src/seatbeltProfile.ts`).
- Consequences:
  - The agent uses the **published host port**, which may differ from the container port.
  - Parallel Sandboxes share those ports.
  - A localhost forwarder can't take the service's own port number, because the published port already holds it.

### Observed but unexplained

- Rootless Podman on Ubuntu 26.04 printed `rootless netns: kill network process: permission denied` when removing containers. The containers and networks were still cleaned up. Possibly AppArmor; **unconfirmed**.

## Implications for the design (for the Factory's decision tickets)

- "Joins each Sandbox to the compose network" should become: **the Factory creates an internal network per Run (or per Sandbox), connects each service with its service name as alias, and attaches the Sandbox only there.** Otherwise the container Sandbox's egress policy is void.
- The container Sandbox's path to the egress proxy needs a decision:
  - gateway IP: rootful Linux Docker only,
  - Unix socket mount: Linux only,
  - proxy sidecar container: all engines (unconfirmed by test).
- The native Sandbox needs Compose services to publish ports (ideally on `127.0.0.1`). The Factory reads them with `docker compose port`:
  - bwrap: map them to the container port numbers on in-sandbox loopback.
  - Seatbelt: allow the published ports and tell the agent the real port (e.g. via env).
- Compose and Sandbox must use the same engine connection. With Podman, rootless and rootful are separate worlds, and so are `podman machine` rootless and `--rootful`.

## Still unconfirmed

- Every macOS and Windows row (no hardware to test), especially:
  - `host.docker.internal` reaching loopback-only host listeners,
  - Unix socket bind mounts across the Docker Desktop, Colima and Podman machine VM boundary,
  - Podman machine's host bind address for `127.0.0.1` publishes,
  - the distro's localhost under Docker Desktop's WSL integration.
- Seatbelt per-port `localhost:<port>` rules for Compose ports (supported by `sandbox-runtime` source, not run).
- Host firewalls (ufw/firewalld) blocking container-to-gateway traffic on Linux Docker.
- `podman-compose` network naming and labels.
- The proxy sidecar design.
