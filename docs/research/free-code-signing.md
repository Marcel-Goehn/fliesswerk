# Research: free code signing for the Factory's Windows and macOS executables

Ticket: [#29](https://github.com/Marcel-Goehn/kraftwerk/issues/29), "Is there a free way to code-sign the Factory's Windows and macOS executables?" (map [#1](https://github.com/Marcel-Goehn/kraftwerk/issues/1)).
Researched 2026-11-07 against SignPath Foundation's terms (signpath.org, undated "Draft"), SignPath's GitHub docs, Microsoft Learn (SmartScreen page dated 2026-05-04, code signing options page 2026-08-29), the Smart App Control FAQ, Apple's Big Sur universal apps release notes, Apple Support 102445 (2026-05-27), Apple DTS forum posts, PowerShell `master` source and Go `master` linker source.
Nothing was built or run. The claims below come from docs and source only, and claims nobody tested are marked **unconfirmed**.

## Answer

- **Windows: one free route exists, SignPath Foundation, but kwk can't use it before it is public, released and known.** It is free for OSS, and Microsoft's own docs point open-source projects to it. [M2][S1][S2] Blockers for kwk:
  - The terms don't say "public repository". But SignPath verifies every signed binary against "the noted source code repository", requires an OSI license, and says "we cannot sign binaries based on source code that nobody knows". **A private repo will in practice not qualify.** [S2]
  - "The project must already be released in the form that should be signed", and executables need "a certain verifiable reputation". So v1.0.0 ships unsigned, and kwk applies once it has users. Approval is at their discretion, with no appeal. [S2]
  - Signing doesn't remove the SmartScreen warning on day one either. OV/EV-signed files still show "unrecognized" until reputation builds, and EV stopped bypassing SmartScreen in 2024. [M1][M2]
- **Other Windows options are not free.** Azure Artifact Signing (formerly Trusted Signing) starts at about $9.99/month and has no free tier. Individuals can only use it in the USA and Canada. OV certificates cost $150–300/year, and self-signed certificates count as unsigned. [M2][M3]
- **macOS: no free route gets past Gatekeeper.** Passing Gatekeeper needs a Developer ID signature plus notarization. A Developer ID needs the Apple Developer Program, which costs 99 USD a year. The fee waiver covers only nonprofits, schools and government bodies. [A1][A5][A6] What *is* free, and already happens:
  - Apple silicon refuses to run unsigned arm64 code. An ad-hoc signature is enough to run, but "binaries signed this way cannot pass through Gatekeeper." [A2]
  - Go's linker ad-hoc signs every darwin/arm64 executable it links, including cross-compiled ones (`NeedCodeSign()` is `IsDarwin() && IsARM64()`). kwk needs no extra signing step. Post-processing the binary (for example with UPX) would break that signature. [G1]
- **The install scripts avoid both warnings.**
  - **macOS:** "Gatekeeper usually only kicks in [if] the app is quarantined", and "most Unix-y tools don't quarantine their downloads, including curl". [A3]
  - **Windows:** PowerShell's `Invoke-WebRequest -OutFile` writes a plain `FileStream`, with no Zone.Identifier (Mark-of-the-Web). Downloads through Windows' bundled `curl` and through .NET from PowerShell get no MotW either. [P1][E1]
  - Even with a MotW, files started from `cmd.exe`/PowerShell skip the SmartScreen check, because only Explorer/ShellExecute launches trigger it. [E1]
- **One Windows warning the install script does not avoid: Smart App Control.** On Windows 11 machines with Smart App Control on, an unsigned file with no reputation is "untrusted" and blocked, "not just those downloaded from the Internet". There is "no way to bypass Smart App Control protection for individual apps", short of turning the feature off. [M1][M4] Only a valid signature (SignPath, later) fixes this.
- **Sigstore/cosign doesn't help with OS warnings.** It proves where a file came from to someone who runs a Sigstore verifier (`cosign`, `gh attestation verify`). Neither SmartScreen nor Gatekeeper reads it, and kwk's planned GitHub build attestations are already Sigstore-based. [X1][X2]

## What changes

- **install.sh**: nothing is needed for Gatekeeper, because curl doesn't quarantine. [A3] It must not run `xattr`, and it must not post-process the macOS binary after the Go link, which would break the arm64 ad-hoc signature. [G1]
- **install.ps1**: nothing is needed for SmartScreen. Keep using `Invoke-WebRequest` (or `curl.exe`), and don't add `Start-BitsTransfer` or a browser hand-off. If it ever extracts a ZIP that carries a MotW, `Unblock-File` removes the mark. [P1][P2] Optional: detect Smart App Control and print a clear message instead of failing with a bare error.
- **Docs ("Installing kwk")** need to explain:
  - The install scripts are the recommended path, and why: no OS warnings, checksums verified.
  - **Windows, browser download**: "Windows protected your PC" means: click **More info**, then **Run anyway**. Or right-click the ZIP, open **Properties**, tick **Unblock** *before* extracting (Explorer copies the mark into extracted files). Or run `Unblock-File .\kwk.exe`. [M1][E1][P2]
  - **Windows, Smart App Control**: kwk can't run while it is on, and there is no per-app allow. The user's options are to wait for a signed release or to turn SAC off in Windows Security. [M4]
  - **macOS, browser download**: on macOS 15+ the Control-click shortcut is gone. Run kwk once, then go to **System Settings > Privacy & Security > Open Anyway**. Or run `xattr -d com.apple.quarantine ./kwk`. [A4][A7][A3]
  - **Linux**: no OS warning.
  - Verification: checksums plus `gh attestation verify` are how users check authenticity, since the OS signature can't.
- **Later, once the repo is public and v1.x has users**: apply to SignPath Foundation for Windows. The prerequisites are listed below.

## Detail

### SignPath Foundation

What it is:
- "For OSS projects, our services are free of charge." The private key lives in SignPath's HSM. [S1]
- The certificate is issued to "SignPath Foundation", so SignPath Foundation, not kwk, shows up as the publisher. "SignPath Foundation is the publisher of the OSS project." [S2]
- Microsoft describes it as "OV-level certificate signing through a managed pipeline". [M2]

Eligibility, quoted from the terms (the page itself is marked "Draft"): [S2]
- "OSI-approved Open Source license without commercial dual-licensing for all components"
- No proprietary components, no malware or PUPs, "actively maintained"
- "must already be released in the form that should be signed"
- functionality described on the download page

Constraints when the Foundation's certificate is used: [S2]
- The signing team owns the repo and signs only its own builds.
- MFA for SignPath and GitHub.
- Named roles: Authors, Reviewers ("each change proposed by people who are not committers … must be reviewed"), Approvers.
- "Every release needs manual approval for signing."
- A "Code signing policy" section on the home page and release pages, with the attribution text "Free code signing provided by SignPath.io, certificate by SignPath Foundation", team roles and a privacy statement.
- File metadata restrictions: "product name" equal to the project name, and the same "product version" across a build. A plain Go `.exe` has no version resource, so kwk would need a build step that embeds one. That such a tool works with SignPath's checks is **unconfirmed**.
- "Software that includes instructions or automated facilities for installation must also include instructions or automated facilities for uninstallation". The docs need uninstall steps.
- No "hacking tools". kwk doesn't scan for vulnerabilities, so this shouldn't apply.

Private repo and reputation:
- The terms never say "public". But "SignPath Foundation performs initial verification of project reputation and control", "we cannot sign binaries based on source code that nobody knows", and "For executable programs … we require a certain verifiable reputation." [S2]
- "We're under no obligation to accept your project, and there is no independent arbitration mechanism." [S2]
- Conclusion: apply only after the repo goes public and a release has users. How much reputation is enough is **unconfirmed**, because it isn't stated anywhere.

GitHub Actions fit: [S3]
- Signing is a step in the release workflow:
  1. `actions/upload-artifact` (v4+)
  2. `signpath/github-action-submit-signing-request@v3` with the artifact id
  3. download of the signed result after manual approval
- Use `archive: false` to avoid wrapping the file in a ZIP.
- "For OSS projects: All jobs of the GitHub workflow leading up to the signing request were executed on GitHub-hosted agents", so no self-hosted runners.
- Origin metadata "is provided by GitHub, not the build script, and can therefore not be forged."
- Go cross-compiles Windows binaries on a Linux runner, which fits the GitHub-hosted rule. That SignPath accepts PE files built on Linux is **unconfirmed**, though Authenticode signing doesn't care where a file was built.

Apple: SignPath can drive `codesign`, but it needs "an 'Apple Developer Application' certificate" that you supply yourself. The Foundation doesn't hand out Apple certificates. [S4]

### Windows: SmartScreen, Mark-of-the-Web, Smart App Control

- SmartScreen weighs "publisher reputation" and "file hash reputation". Unsigned files start "with zero reputation" on every new version. There is "no need (or mechanism) to manually submit a file for SmartScreen reputation review", and clearing the prompt "can take several weeks and hundreds of clean installs". [M1]
- Unsigned result: "'Windows protected your PC'; User must choose 'Run anyway'". Enterprise policy may forbid continuing. [M1]
- EV: "EV certificates no longer bypass SmartScreen … this behavior no longer exists". Since 2024, EV behaves the same as OV. [M1][M2]
- What writes a MotW: browsers do (Chrome via `IAttachmentExecute`, Firefox writes `Zone.Identifier` directly). The bundled `curl`, bitsadmin and WinINET/WinHTTP/System.NET callers "including from PowerShell" don't, by design. Explorer's ZIP extraction carries the mark into extracted files. SmartScreen AppRep runs on Explorer/ShellExecute launches; "The `CreateProcess` API does not care about the MotW". [E1] (E1 is Eric Lawrence, a former Microsoft IE/Edge engineer, on his blog. It is not a Microsoft doc.)
- PowerShell 7 source: `StreamHelper.SaveStreamToFile` does `new FileStream(filePath, fileMode, FileAccess.Write, FileShare.Read)` and copies. There is no Zone.Identifier and no attachment API. [P1] Windows PowerShell 5.1 is closed source. That it behaves the same is reported by E1 but **unconfirmed** from source.
- `Unblock-File` removes the Zone.Identifier stream, the same as the Unblock box in Explorer's Properties dialog. [P2]
- Smart App Control: "Smart App Control will block execution of unsigned files unless the file has a positive reputation. Smart App Control signature checks apply to all executable files, not just those downloaded from the Internet." [M1] The FAQ says it is off on enterprise-managed devices, in developer mode and in S mode, and with optional diagnostic data off. It has no per-app exceptions, and a validly signed app is allowed. [M4] So kwk users on SAC machines get blocked even through `install.ps1`. How many developer machines have SAC on is **unconfirmed**. Developer mode turns it off, and many developers have that on.

### macOS: Gatekeeper, quarantine, ad-hoc signing

- Gatekeeper checks "downloaded" software: identified developer, notarized, not altered. "users can override Gatekeeper policies to open any software unless restricted by a device management service." [A1]
- Quarantine is the trigger. Apple DTS (Quinn) says "Gatekeeper usually only kicks in [if] the app is quarantined" and "Most Unix-y tools don't quarantine their downloads, including curl and scp". The fix for a quarantined file is to remove `com.apple.quarantine` with `xattr`. [A3]
- Override UI: "Click Privacy & Security, scroll down, and click the Open Anyway button". [A4] macOS Sequoia removed Control-click as a way to override Gatekeeper. [A7]
- Apple silicon: "the operating system enforces that any executable must be signed before it's allowed to run … a simple ad-hoc signature is sufficient … These new signatures are not bound to the specific machine … However, given that these signatures do not bear any valid identity, binaries signed this way cannot pass through Gatekeeper." [A2]
- Go: `func (ctxt *Link) NeedCodeSign() bool { return ctxt.IsDarwin() && ctxt.IsARM64() }`. When it returns true, the linker runs `machoCodeSign`, which "code-signs Mach-O file fname with an ad-hoc signature" using `cmd/internal/codesign`, which uses "the same ad-hoc signing algorithm as the Darwin linker". [G1] This landed for Go 1.16 (golang/go#42684). [G2] darwin/amd64 binaries stay unsigned, and Intel Macs don't require a signature. [A2]
- Developer ID: "you must be the Account Holder of your development team in the Apple Developer Program". [A5] The program costs 99 USD per year, and fee waivers are limited to nonprofits, accredited schools and government entities. [A6]

### Not useful for OS warnings

- **Sigstore/cosign** signs artifacts and verifies them against Fulcio identity and the Rekor log, through a Sigstore client. [X1] SmartScreen reads Authenticode signatures and Gatekeeper reads Apple code signatures. Neither consumes Sigstore bundles. Sigstore's own docs don't claim they do, and no Microsoft or Apple doc above mentions it. [X1][M1][A1]
- **GitHub artifact attestations** are Sigstore under the hood. Public repos use the Public Good instance and private repos use GitHub's instance. They are meant for people who run `gh attestation verify`. [X2]
- **Self-signed Authenticode**: "Same behavior as no signature". [M1]
- **Microsoft Store**: removes SmartScreen warnings for free, but it is for MSIX/installer apps, not a CLI fetched with curl. [M2] It doesn't fit kwk's distribution, so it wasn't researched further.

## Sources

- [S1] SignPath Foundation home, https://signpath.org/
- [S2] SignPath Foundation, "Conditions for Open Source projects" (code of conduct, Draft), https://signpath.org/terms
- [S3] SignPath docs, Trusted Build Systems: GitHub, https://docs.signpath.io/trusted-build-systems/github
- [S4] SignPath docs, macOS crypto provider, https://docs.signpath.io/crypto-providers/macos
- [M1] Microsoft Learn, "SmartScreen reputation for Windows app developers" (2026-05-04), https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation
- [M2] Microsoft Learn, "Code signing options for Windows app developers" (2026-08-29), https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options
- [M3] Azure Artifact Signing pricing, https://azure.microsoft.com/en-us/pricing/details/artifact-signing/
- [M4] Microsoft Support, Smart App Control FAQ, https://support.microsoft.com/windows/smart-app-control-faq-285ea03d-fa88-4d56-882e-6698afdb7003
- [P1] PowerShell source, `WebCmdlet/StreamHelper.cs` (`master`), https://github.com/PowerShell/PowerShell/blob/master/src/Microsoft.PowerShell.Commands.Utility/commands/utility/WebCmdlet/StreamHelper.cs
- [P2] Microsoft Learn, `Unblock-File`, https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.utility/unblock-file
- [E1] Eric Lawrence, "Downloads and the Mark-of-the-Web" (updated 2026-09-01), https://textslashplain.com/2016/04/04/downloads-and-the-mark-of-the-web/
- [A1] Apple Platform Security, "Gatekeeper and runtime protection in macOS", https://support.apple.com/guide/security/gatekeeper-and-runtime-protection-sec5599b66df/web
- [A2] Apple, "macOS Big Sur 11.0.1 Universal Apps Release Notes", https://developer.apple.com/documentation/macos-release-notes/macos-big-sur-11_0_1-universal-apps-release-notes
- [A3] Apple Developer Forums, Quinn (DTS), "Avoiding notarisation & Gatekeeper for development builds & testing" (Nov 2020), https://developer.apple.com/forums/thread/666452
- [A4] Apple Support 102445, "Safely open apps on your Mac" (2026-05-27), https://support.apple.com/en-us/102445
- [A5] Apple, Developer ID, https://developer.apple.com/developer-id/
- [A6] Apple Developer Program, what's included, https://developer.apple.com/programs/whats-included/
- [A7] Apple Developer News, "Updates to runtime protection in macOS Sequoia" (2024-08-06), https://developer.apple.com/news/?id=saqachfa
- [G1] Go linker source (`master`): `src/cmd/link/internal/ld/lib.go` (`NeedCodeSign`), `macho.go` (`machoCodeSign`), `src/cmd/internal/codesign/codesign.go`, https://go.googlesource.com/go/+/refs/heads/master/src/cmd/link/internal/ld/
- [G2] golang/go#42684, "cmd/link: code-sign on darwin/arm64", https://github.com/golang/go/issues/42684
- [X1] Sigstore overview, https://docs.sigstore.dev/about/overview/
- [X2] GitHub Docs, artifact attestations, https://docs.github.com/en/actions/concepts/security/artifact-attestations
