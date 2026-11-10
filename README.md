# kraftwerk

Kraftwerk (short: `kwk`) is a software factory: it runs AFK agents over a Spec's ready Tickets in a loop.

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Marcel-Goehn/kraftwerk/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/Marcel-Goehn/kraftwerk/main/install.ps1 | iex
```

No admin rights needed. The [install guide](docs/install.md) covers browser downloads, verifying a release, uninstalling and building from source.

## Guides

- [Setting up a project](docs/setup.md): `kraftwerk init`, signing in, the GitHub token and choosing a Sandbox kind
- [Running the Factory](docs/usage.md): `kraftwerk run`, exit codes, logs, Claims and Blockers
- [Config reference](docs/config.md): every key, and the risks that remain
- [Services](docs/services.md): databases and other programs the agent needs
- [Behind a corporate proxy](docs/corporate-proxy.md)
- [Support matrix](docs/support-matrix.md): what is tested on which OS and engine

## No telemetry

Kraftwerk collects no telemetry, ever. It has no analytics, no crash reports and no usage statistics, and it sends nothing about you or your use of it anywhere.

The Factory itself connects only to:

- your Tracker, through `gh`, and your git remote, to do the work you asked for
- your Harness's endpoint, for one request that carries no credential, to detect a TLS-inspecting proxy. The Factory never sends your Claude credential anywhere: only Claude Code does, from inside the Sandbox
- AWS STS, with Bedrock, to turn your credentials into short-lived ones for the Sandbox
- GitHub's release list, at most once a day, to tell you about a newer version. Set `update_notice = false` to turn that off. `kraftwerk update` and the install scripts download from GitHub Releases

The agent reaches its model provider and the hosts on your allowlist, and nothing else.

## License

Kraftwerk is released under the [MIT License](LICENSE). The release archives also include [`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES): the licenses and notices of the Go modules compiled into the executable.

The files `kraftwerk init` writes into your project, in `.kraftwerk/`, are yours: use, change and share them without the license's conditions, including its notice.

## Trademarks

Kraftwerk is an independent project. It runs Claude Code, but isn't made, endorsed or supported by Anthropic. Claude and Claude Code are trademarks of Anthropic.

## Contributing

Kraftwerk is pre-v1 and isn't taking outside contributions yet. Issues from people outside the project aren't accepted before v1 and will be closed without review. Pull requests from outside the project are turned off.

Security reports are the exception: if you find a vulnerability, report it privately as [`SECURITY.md`](SECURITY.md) describes, never in a public issue.
