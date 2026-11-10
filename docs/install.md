# Installing Fliesswerk

Fliesswerk is one executable with no runtime to install. It runs on Linux, macOS and Windows, on x64 and arm64. Git, your container engine, the Harness and `gh` are yours to install; `fliesswerk check` tells you which are missing and how to get them.

## Use the install script

This is the recommended way on every OS. It needs no admin rights.

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Marcel-Goehn/fliesswerk/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/Marcel-Goehn/fliesswerk/main/install.ps1 | iex
```

The script:

1. picks the archive for your OS and processor from the latest GitHub Release
2. checks its SHA-256 against the release's `checksums.txt`, and stops without installing anything if it differs
3. installs `fliesswerk` to `~/.local/bin`, or `%LOCALAPPDATA%\Programs\fliesswerk` on Windows
4. adds `fwk` as a short name, unless a `fwk` is already on your `PATH`
5. adds the directory to your `PATH` if needed: your shell's profile on Linux and macOS, your user `PATH` on Windows. Open a new terminal afterwards.

Run the script again to reinstall, or use [`fliesswerk update`](#updating). You can set these before running it:

| Variable                    | Effect                                                 |
| --------------------------- | ------------------------------------------------------ |
| `FLIESSWERK_VERSION`        | Install this release, such as `v1.2.3`, not the latest |
| `FLIESSWERK_DOWNLOAD_URL`   | Download from a mirror of the GitHub releases URL      |
| `FLIESSWERK_NO_MODIFY_PATH` | Leave your profile or user `PATH` alone                |

A script download doesn't trigger the SmartScreen or Gatekeeper warnings below, because those apply to files a browser saved.

## Updating

`fliesswerk update` installs the newest release of the major version you're on, after checking its SHA-256 against the release's `checksums.txt`. An update within a major never breaks your config. Run inside a project, it also stays within that project's `factory` pin. On Windows the running `fliesswerk.exe` is renamed to `fliesswerk.exe.old` first, and the next update removes that file.

`fliesswerk update --major` moves to the newest major version. Run `fliesswerk migrate` in each project afterwards.

| Variable                  | Effect                                                                                         |
| ------------------------- | ---------------------------------------------------------------------------------------------- |
| `FLIESSWERK_DOWNLOAD_URL` | Download from a mirror of the GitHub releases URL                                              |
| `FLIESSWERK_RELEASES_API` | Ask this URL for the release list, in the shape of GitHub's API, instead of GitHub             |
| `FLIESSWERK_GITHUB_TOKEN` | A token for the release list, for machines that share an address and reach GitHub's rate limit |

### The new-version notice

`check`, `run` and `release` say when a newer release is out, on stderr. They ask GitHub at most once a day and never in CI. To turn it off, set this in `.fliesswerk/config.toml` or `config.local.toml`:

```toml
update_notice = false
```

### Pinning the version for a team

`factory` in `.fliesswerk/config.toml` is a semver range the Factory's version must match, such as `"1"`, `"~1.2"` or `">=1.2 <2"`. A teammate on a version outside it gets a validation error that names the command to run.

### Renamed and removed config keys

A renamed or removed key keeps working within its major, with a deprecation warning. `fliesswerk migrate` rewrites `config.toml` and `config.local.toml`, keeps your comments and reports each change. If it can't rewrite a key by itself, for example one inside an inline table, it changes nothing and names the key to change by hand.

## Smart App Control

Windows 11's Smart App Control blocks programs that aren't signed, and Fliesswerk isn't signed yet. **Fliesswerk can't run on a PC with Smart App Control on until the binary is signed**, and there's no per-program exception.

`install.ps1` checks for this. If Smart App Control is on, it says so and installs nothing. If it's still evaluating your PC, the script warns you and installs, but Windows may turn it on later.

To check it yourself, open Windows Security > App & browser control > Smart App Control. Turning it off is your decision: it's a device-wide protection, and Windows may not let you turn it back on without resetting the PC.

## Downloading with a browser

Get the archive for your system from the [Releases page](https://github.com/Marcel-Goehn/fliesswerk/releases). Browsers mark what they save, so the OS warns you the first time it runs. The binary is unsigned, because signing costs money this project doesn't charge for.

### Windows: SmartScreen

Do one of these:

- Before extracting, right-click the ZIP, choose Properties, tick **Unblock** and click OK.
- After extracting, run `Unblock-File .\fliesswerk.exe` in PowerShell.
- When the "Windows protected your PC" dialog appears, click **More info**, then **Run anyway**.

### macOS: Gatekeeper

Do one of these:

- Run the binary once and dismiss the warning. Then open System Settings > Privacy & Security, and click **Open Anyway** next to the message about `fliesswerk`.
- Remove the quarantine mark in a terminal: `xattr -d com.apple.quarantine ./fliesswerk`

Don't re-sign, strip or compress the binary. Go gave it an ad-hoc signature at build time, and changing the file breaks it.

## Verifying a release

Download the archive and `checksums.txt` from the same release, then compare the checksum:

```sh
sha256sum --ignore-missing -c checksums.txt     # Linux
shasum -a 256 --ignore-missing -c checksums.txt # macOS
```

```powershell
Get-FileHash -Algorithm SHA256 .\fliesswerk_windows_amd64.zip # compare with checksums.txt
```

A checksum shows the download is intact. Published releases are immutable: once a release is out, nobody can replace its archives or `checksums.txt`, move or delete its tag, or reuse its version number. So the `checksums.txt` you check against is the one published with the archive.

To prove the archive was built by this repository's release workflow, verify its attestation with the [GitHub CLI](https://cli.github.com/):

```sh
gh attestation verify fliesswerk_linux_amd64.tar.gz --repo Marcel-Goehn/fliesswerk
```

## Uninstalling

Linux and macOS:

```sh
rm ~/.local/bin/fliesswerk
[ "$(readlink ~/.local/bin/fwk)" = fliesswerk ] && rm ~/.local/bin/fwk
```

If the script added a line to your profile (`.zshrc`, `.bashrc`, `.bash_profile`, `.profile`), you can delete it. For fish, delete `~/.config/fish/conf.d/fliesswerk.fish`.

Windows (PowerShell):

```powershell
$dir = Join-Path $env:LOCALAPPDATA 'Programs\fliesswerk'
Remove-Item -Recurse -Force $dir
# Read and written raw, so entries like %USERPROFILE%\bin stay as they are.
$userPath = (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
$userPath = ($userPath -split ';' | Where-Object { $_ -ne $dir }) -join ';'
New-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value $userPath -PropertyType ExpandString -Force | Out-Null
```

Secrets stored with `fliesswerk auth set` stay behind in a `fliesswerk` directory under your user config directory (`~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). Delete that directory to remove them. Each project keeps its own `.fliesswerk/` directory.

## Building from source

You need the Go version named in `go.mod`.

```sh
go install github.com/Marcel-Goehn/fliesswerk@latest
```

This build has no egress proxy for container Sandboxes, because those files are generated. To get a complete build, clone the repository and run:

```sh
go generate ./internal/proxyimage
CGO_ENABLED=0 go build -trimpath .
```
