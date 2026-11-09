# Services

A Service is a supporting program, such as a database, that the Factory starts from your project's compose file. This guide covers own Services in container and native Sandboxes.

## Setting them up

Name each Service the agent needs in `.kraftwerk/config.toml`:

```toml
[services]
compose_file = "compose.yaml"

[services.postgres]
shared = false
```

Only the Services named here start, along with whatever they `depends_on`. The compose file must be committed, because each Sandbox reads it from its own worktree.

You need one of these, and `kraftwerk check` enforces it:

| Engine | Compose tool                                  |
| ------ | --------------------------------------------- |
| Docker | Docker Compose 2.24.4 or newer                |
| Podman | `podman-compose` 1.4.0 or newer, on the `PATH` |

With Podman the Factory always calls `podman-compose` directly. It does not use `podman compose`, which picks whichever provider it finds first.

## Own Services

Each Sandbox gets its own copy of every Service, with fresh data, so parallel Tickets can't see each other's rows.

- The copy runs in Compose project `kraftwerk-<repo dir>-<hash>-<sandbox>`.
- It is healthy before the `sandbox_created` Hook runs, so migrations and seeding belong in that Hook.
- It is removed, volumes included, when the Sandbox is destroyed.

Your compose file, your running containers and your volumes are never touched. The Factory layers its own override on your file, which:

- publishes every declared port on a random `127.0.0.1` port instead of the one you chose
- removes `container_name`, which stays usable as a network alias
- renames named volumes and networks into the Factory's project

Relative paths in the compose file resolve inside the Sandbox's worktree, next to the compose file.

## Reaching a Service from the agent

Declare ports with `ports:` or `expose:`, or let the image's `EXPOSE` say. If a Service declares ports, those are used and the image's are ignored.

For a Service `postgres` on port 5432, every command in the Sandbox can use:

- `postgres:5432`, and its old `container_name` if it had one
- `localhost:5432`
- `KRAFTWERK_SERVICE_POSTGRES_HOST` and `KRAFTWERK_SERVICE_POSTGRES_PORT`, which hold the first port

The agent's prompt lists the same addresses. If two Services listen on the same port, the first by name keeps `localhost:<port>`, and the other is reachable by name only.

Set addresses your code reads with `env` templates, which get the Service's host and first port in every Sandbox:

```toml
[services.postgres]
env = { DATABASE_URL = "postgres://app@{host}:{port}/app" }
```

## Native Sandboxes

A native Sandbox still runs its Services in Docker or Podman, so it needs one of the compose tools above. It reaches a Service through a port published on your machine, and only the Services named in `.kraftwerk/config.toml`. A Service they `depends_on` runs, but the Sandbox can't reach it.

Every Service you name needs a TCP port under `ports:`. `expose:` or the image's `EXPOSE` isn't enough:

```yaml
services:
  postgres:
    ports:
      - "127.0.0.1:5432:5432"
```

The Factory still replaces the host side with a random port, so yours stays free.

**On Linux and WSL2 (bwrap)** it works as in a container Sandbox: `postgres:5432`, `localhost:5432` and the variables. The one exception is a container port below 1024, which a bwrap Sandbox can't listen on. It's at `<service>:<published port>` instead, and the prompt, the variables and `{port}` all give that port.

**On macOS (Seatbelt)** a Sandbox has no network of its own, so there are no Service names and no container ports. Commands reach `127.0.0.1:<published port>`, a random number that differs per Sandbox. Anything your code reads must come from `env` templates or `KRAFTWERK_SERVICE_<NAME>_HOST` and `_PORT`: a port hard-coded in the project can't work.

We recommend the container Sandbox on macOS: it gives you the names and the container ports, so your project's addresses work as written.

## What `check` rejects

`kraftwerk check` and the start of a Run stop on these, for the Services that would start:

| In the compose file                                       | Why                                               | Fix                                                        |
| --------------------------------------------------------- | ------------------------------------------------- | ---------------------------------------------------------- |
| A volume with `external: true`                            | Every Sandbox would share it, and it is real data | Use a volume the project defines                           |
| A writable bind mount from an absolute path, `~` or outside the project | It is real data on your machine                   | Add `:ro`, or use a named volume or a path in the project  |
| A volume with `driver_opts.device`                        | It is real data on your machine or network        | Use a volume without a device                              |
| A `[services.<name>]` the file doesn't define             | There is nothing to start                         | Fix the name, or enable the Service's profile by default   |
| With `kind = "native"`, a named Service with no TCP port under `ports:` | A native Sandbox reaches a Service only through a published port | Add `ports: ["127.0.0.1:<H>:<C>"]`            |
