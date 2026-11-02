# Research: language and runtime for a single-executable Factory

Ticket: [#6](https://github.com/Marcel-Goehn/ralph/issues/6), "Can one dependency-free executable be cross-platform, configurable and extensible?" (map [#1](https://github.com/Marcel-Goehn/ralph/issues/1)).
Researched 2026-11-02 against Bun v1.4.4 (docs at `oven-sh/bun` main, 2026-10-31), Deno v2.9.8, Go and Rust current docs, and Sandcastle v0.13.0 (`f5c48e0`).
Nothing was built or run: the claims below come from docs, source and issue trackers only. Untested claims are marked **unconfirmed**.

## Answer

- **Yes, all four candidates can ship one executable with no runtime on the host for Windows, Linux and macOS (x64 and arm64), and all four can cross-compile from one machine.** Go, Bun and Deno need no extra toolchain to do it. Rust needs a linker for each target. [G1][G2][B1][D1][R1][R2]
- **Bun `bun build --compile`: yes, it can import a user's `.ts` file from disk at runtime, by default.** The executable contains the full Bun runtime, and "Bun reads and transpiles JavaScript and TypeScript files on `import` and `require`". The docs list computed-specifier imports of module files as something that keeps working, and refusing "a JavaScript or TypeScript file on disk" happens only when you opt into `--disallow-code-generation-from-strings=strict`. [B2][B3][B4] Limits:
  - A relative path resolves against the process's **current working directory**, not the executable's location. [B4]
  - Imports of `.json`, `.toml` and `.yaml` from disk work natively, even under `=strict`. [B3]
  - How a disk-loaded file resolves *its own* imports in standard (JIT) mode is not documented: npm packages next to it, or importing the Factory's own API by name. **Unconfirmed.** `tsconfig.json`/`package.json` autoload is off by default in compiled executables, and `--compile-autoload-tsconfig` / `--compile-autoload-package-json` turn it on. [B2]
  - The new `bun compile` / `--aot` mode refuses code from disk unless you pass `--aot-allow-code-from-disk`, and it then interprets that code. In that mode a disk file *can* import the compiled program and its bundled packages by name. AOT is experimental and only on `main` (merged 2026-10-27, after v1.4.4). [B5][B6][B7]
- **Deno `deno compile`: yes, since v2.8.2 (2026-06-03), and only with an absolute path.**
  - Before v2.8.2, a compiled binary passed disk files to V8 as plain JavaScript, so TypeScript syntax threw `SyntaxError`. [D3]
  - PR #34616 now transpiles `.ts` files that were not embedded ("local files read from disk that weren't embedded at compile time"), at no extra size, because `deno_ast` is already linked in. [D3][D4]
  - The runtime `file:` import "needs a real on-disk absolute path (e.g. derived from `Deno.cwd()`), not a specifier relative to `import.meta.url`". [D3]
  - Remote `https:`/`jsr:` imports at runtime are out of scope. [D3]
  - Permission flags are fixed at compile time. [D1]
  - Deno's maintainers say they have no plans to support TypeScript imports out of the box. Runtime TS support arrived as a bug fix ("fix(compile)"), so treat it as newer and less documented than Bun's: the official `deno compile` page doesn't mention it. [D1][D3]
- **Go cannot load user code natively in a portable way.** The `plugin` package is "supported only on Linux, FreeBSD, and macOS", and plugins must be built with exactly the same toolchain. [G3] A pure-Go embedded interpreter does work everywhere:
  - goja: ES5.1+, partial ES6. [G4]
  - Starlark, a Python dialect. [G5]
  - User `.ts` could be transpiled in-process with esbuild's Go API (`api.Transform`, `LoaderTS`) and run on goja. This is plausible, but **unconfirmed end to end**: goja documents no ES-module support. [G4][G6]
- **Rust is in the same position as Go:** no native plugins, so user code means embedding an engine. Candidates are QuickJS, Boa or Lua bindings, which were **not researched in depth here**.
- **Binary size runs against the JS runtimes.**
  - Bun's own release zip is 49 MB (linux-x64) and 52 MB (windows-x64). A compiled executable is "a copy of the Bun runtime with your program attached". The `cli` feature alone is about 47 MB on Linux, and `--trim` exists to cut it down. [B8][B9]
  - Deno's `denort` base zip is 27–33 MB, depending on target. [D2]
  - Go and Rust binaries are much smaller. No figure was measured here (**unconfirmed**).
- **Sandcastle is the friction we are removing:** user loop code is `.sandcastle/main.ts` (templates ship `main.mts`), run with `npx tsx`, importing `@ai-hero/sandcastle` and Zod from the host project's `node_modules`. [S1][S2] With Bun or Deno `compile`, the Factory could keep a TypeScript loop file *without* Node, npm or `tsx` on the host. Go and Rust can keep a loop file only by changing its language (Starlark, Lua) or by embedding a JS engine with a transpile step.

## Comparison

| Criterion | Go | Go + embedded engine | Rust | Bun `build --compile` | Deno `compile` |
|---|---|---|---|---|---|
| Single file, no host runtime | Yes | Yes (pure-Go engines: goja [G4], starlark-go [G5]) | Yes | Yes, runtime embedded [B1] | Yes, `denort` embedded [D1] |
| Targets (Win/Linux/macOS, x64+arm64) | All; `GOOS`/`GOARCH` [G1] | Same as Go | All Tier 1 except x86_64-apple-darwin (Tier 2) [R1] | All, plus musl [B1] | All; Windows arm64 since 2.9.3 [D1] |
| Cross-compile from one host | Built-in; cgo is off when cross-compiling [G2] | Built-in if the engine is pure Go | `rustup target add` plus a linker per target [R2] | `--target=bun-<os>-<arch>` [B1] | "regardless of the host platform" [D1] |
| Binary size | Small (**unconfirmed**, not measured) | Small + engine (**unconfirmed**) | Small (**unconfirmed**) | ~50 MB class (runtime zip 49–52 MB [B8]); `--trim` reduces it [B9] | ~30 MB class (`denort` zip 27–33 MB [D2]) |
| Subprocesses on Windows | `os/exec`; Windows gets one command-line string; `cmd.exe`/batch files unquote differently; no cwd lookup since 1.19 [G7] | Same as Go | `std::process::Command`; batch-file escaping CVE fixed in 1.77.2, now errors on unsafe args [R3] | `Bun.spawn` / `node:child_process` (Windows-specific behaviour not researched) | `Deno.Command` / `node:child_process` (not researched) |
| Containers | Shell out to `docker`/`podman` CLI (as Sandcastle does [S3]) | Same | Same | Same | Same |
| YAML | `go-yaml/yaml` archived, unmaintained (2025-04-01) [G8]; `goccy/go-yaml` is the stated replacement [G9] | Same | `serde_yaml` archived, unmaintained (2024-03-25) [R4] | Built in: `Bun.YAML`, YAML 1.2, `import` of `.yaml` [B10] | Via npm/JSR package (not researched) |
| TOML | Third-party (not researched) | Same | `toml` + serde (not researched) | Built-in `.toml` import [B10] | Via package (not researched) |
| Schema validation | Third-party JSON Schema libs (not researched) | Could also validate in the script | serde types + schema crates (not researched) | Zod bundled into the exe; `z.toJSONSchema()` [Z1] | Same as Bun |
| Adding config options | Struct + tags; recompile | Struct, or let scripts read options | Struct + serde; recompile | One Zod schema gives types, validation and JSON Schema [Z1] | Same as Bun |
| User loop code at runtime | `plugin` not on Windows [G3]; separate-process IPC only | Yes, in the engine's language (JS ES5.1+ / Starlark / Lua) | Embed an engine (not researched) | **Yes, `.ts` from disk, transpiled** [B3][B4] | **Yes since 2.8.2, absolute path** [D3] |
| Maturity of the compile path | Mature | goja/starlark are established; the esbuild + goja combination is unproven | Mature | `--compile` stable; `--aot` experimental, unreleased [B5] | Stable; `--bundle`/`--engine quickjs` experimental [D1] |

## Detail

### Bun: runtime import from disk

- The default `bun build --compile` executable "bundles all imported files and packages into the executable, along with a copy of the Bun runtime". [B1]
- In the Workers section: "If you use a relative path to a file not included in the standalone executable, Bun loads that path from disk relative to the process's current working directory, and errors if it doesn't exist." [B4]
- Under the opt-in `--disallow-code-generation-from-strings=strict`, the docs' table for compiled executables says `import()`/`require()` of "a JavaScript or TypeScript file on disk" is "refused, including a file that the program just wrote". JSON/JSONC/JSON5/TOML/YAML/XML imports from disk still work. [B3] So without `=strict`, loading code from disk is the normal behaviour. The same page lists "Importing module files, with literal or computed specifiers" among the things that keep working. [B3]
- A community example from 2024 shows a compiled binary running `await import(\`./${givenPath}\`)` on a user's `example.ts`. The reporter noted that some scripts "behaved differently" than under `bun run`, and no cause was found. [B11] This is secondary evidence only.
- `BUN_BE_BUN=1` (since v1.2.16) makes a compiled executable act as the full `bun` CLI. The Factory could use this to run a user's script in a *separate process* with the same binary. That gives isolation, but the embedded entrypoint and its flags don't apply then. [B2][B3]
- Config autoload at runtime: `.env` and `bunfig.toml` load by default, and the docs say this "may" change. `tsconfig.json` and `package.json` don't load by default. [B2] For a Factory, `--no-compile-autoload-dotenv` deserves a look, so that the host project's `.env` isn't silently read. This is a credentials concern.
- AOT mode (`bun compile`, experimental, on `main` only): a disk file "can also import your program and the packages compiled into it by name, like `import "my-app"`". Code from disk is interpreted, not JIT-compiled. [B6][B7] For standard `--compile`, whether a disk file can `import "ralph"` and get the embedded module is **unconfirmed**. If it can't, a user's loop file would need its API injected, for example a default-exported function that receives the Factory API as an argument.

### Deno: runtime import from disk

- Timeline:
  - Before 2.8: anything not in the module graph or `--include` failed with "Module not found". Statically analyzable dynamic imports were embedded automatically. [D5]
  - 2.8.0 (2026-05-22): `module.registerHooks()` works in compiled binaries, so a program can bring its own transpiler. [D4][D6]
  - 2.8.2 (2026-06-03): built-in transpilation of runtime-imported TS from `data:`, `blob:` and non-embedded local files (#34616). [D3]
- The official `deno compile` page still describes only `--include` for non-analyzable imports. It says nothing about loading files that aren't embedded. [D1] The runtime-TS behaviour is documented only in the PR and the issue thread. **Not verified by running.**
- Which permission flag a runtime `file:` import needs in a compiled binary (`--allow-read`, `--allow-import`, or none) is **unconfirmed**. Runtime flags have to be baked in at compile time. [D1]

### Go

- Cross-compiling: "In effect, you are always cross-compiling". [G1] cgo is disabled by default when cross-compiling, so a Factory has to stay pure Go to keep that property. [G2]
- The `plugin` package isn't an option: Windows is unsupported, and its docs recommend IPC instead. [G3]
- Engines:
  - goja is pure Go with no cgo. It targets ES5.1 and most of ES6 is "in progress". Its README mentions no ES modules, a `Runtime` isn't goroutine-safe, and it has no `setTimeout`. [G4]
  - starlark-go is a Python dialect "intended for use as a configuration language". It offers step limits and cancellation for untrusted code. [G5]
  - esbuild's Go API transforms TypeScript in-process (`Transform`, `LoaderTS`, targets down to ES5, CommonJS/IIFE output). [G6]
  - A TS-loop-file path in Go would therefore be esbuild then CJS then goja, with the Factory API injected as globals. This is **unconfirmed** in practice.

### Rust

- Rust is Tier 1 on every target except `x86_64-apple-darwin`, which is Tier 2 with host tools. [R1] Cross-compiling needs "other tools ..., particularly a linker". [R2]
- `std::process::Command` on Windows: before 1.77.2, batch-file arguments were escaped badly (CVE-2024-24576). Since then it returns `InvalidInput` when an argument can't be escaped safely. [R3] Go documents the same class of problem for `cmd.exe` and batch files. [G7] A Factory that spawns `claude.cmd`-style shims on Windows has to deal with this in any language.

## Options and trade-offs (for the downstream decision)

1. **Bun `--compile`, TypeScript loop file kept.** This is closest to Sandcastle's `orchestrate()` model and its users.
   - It has the built-in YAML/TOML parsers, Zod for schemas, and the most mature runtime `.ts` import.
   - Costs: a binary of about 50 MB, cwd-relative resolution, an undocumented import story for disk files in JIT mode, and default `.env` autoload to switch off.
2. **Deno `compile`, TypeScript loop file kept.**
   - Smaller base and a permission model.
   - Runtime TS import is five months old and documented only in a PR. Absolute paths only. No built-in YAML.
3. **Go, declarative config only (YAML/TOML), no user code.**
   - Smallest binary and simplest cross-compiling.
   - Extensibility only through config options the Factory defines. This needs a maintained YAML library choice.
4. **Go plus an embedded engine** (Starlark for config-as-code, or goja with esbuild for TS).
   - Keeps the single small binary and allows user logic.
   - Costs: a second language, or a JS engine without ES modules or modern-runtime parity.
5. **Rust:** similar to Go, with more cross-compile setup and no stronger ecosystem story for this use. Embedded engines were not researched.

New questions raised:
- Does the Map need user-authored loop code at all, or are config plus Hooks enough? This decides between options 1–2 and 3.
- Spawning harness CLIs that are batch shims (`.cmd`) on Windows is a known hazard in every language. It may deserve a note in the harness-boundary ticket.

## Sources

- [B1] Bun docs, Single-file executable: <https://bun.com/docs/compiler/executables> (source `docs/compiler/executables.mdx`, oven-sh/bun @ a47a315, 2026-10-31)
- [B2] Same page, sections "Act as the Bun CLI" (BUN_BE_BUN, v1.2.16) and runtime config autoload; API reference `CompileBuildOptions`
- [B3] Bun docs, Code generation from strings: <https://bun.com/docs/runtime/code-generation-from-strings> (sections "Files on disk are not part of the program", "What each level refuses")
- [B4] Bun docs, executables, Workers section: "If you use a relative path to a file not included in the standalone executable, Bun loads that path from disk relative to the process's current working directory"
- [B5] Bun docs, Compiler overview (table `--compile` vs `--aot`, Stable vs Experimental): <https://bun.com/docs/compiler>; AOT merged in oven-sh/bun#45631 (commit 8d5672c, 2026-10-27), after release bun-v1.4.4 (2026-10-22)
- [B6] Bun docs, Compatibility (dynamic imports, `--aot-allow-code-from-disk`): <https://bun.com/docs/compiler/compatibility>
- [B7] Bun docs, Troubleshooting ("Code is loaded from disk ... Bun interprets that too"): <https://bun.com/docs/compiler/troubleshooting>
- [B8] Bun v1.4.4 release assets (bun-linux-x64.zip 49 MB, bun-windows-x64.zip 52 MB, bun-darwin-aarch64.zip 38 MB): <https://github.com/oven-sh/bun/releases/tag/bun-v1.4.4>
- [B9] Bun docs, Smaller executables: <https://bun.com/docs/compiler/size>
- [B10] Bun docs, YAML (YAML 1.2, `.yaml` imports) and TOML: <https://bun.com/docs/runtime/yaml>, <https://bun.com/docs/runtime/toml>
- [B11] oven-sh/bun discussion #12235 (2024-06-28): <https://github.com/oven-sh/bun/discussions/12235>
- [D1] Deno docs, `deno compile`: <https://docs.deno.com/runtime/reference/cli/compile/> (source denoland/docs `runtime/reference/cli/compile.md`)
- [D2] Deno v2.9.8 release assets (denort-*.zip 27–33 MB; deno-*.zip 36–40 MB): <https://github.com/denoland/deno/releases/tag/v2.9.8>
- [D3] denoland/deno#34616 "fix(compile): transpile TypeScript imported at runtime" (merged 2026-06-01, in v2.8.2), and the closing comment on issue #27945: <https://github.com/denoland/deno/pull/34616>, <https://github.com/denoland/deno/issues/27945>
- [D4] denoland/deno#33853 "feat(compile): support module.register() and module.registerHooks() in compiled binaries" (merged 2026-05-06, in v2.8.0): <https://github.com/denoland/deno/pull/33853>
- [D5] denoland/deno#18327 "`deno compile` output can't dynamically import files that were not present at compilation time" (closed as completed by #33853): <https://github.com/denoland/deno/issues/18327>
- [D6] Deno v2.8.0 release notes: <https://github.com/denoland/deno/releases/tag/v2.8.0>
- [G1] Go, Installing from source (`$GOOS`/`$GOARCH`, valid combinations): <https://go.dev/doc/install/source>
- [G2] Go, cmd/cgo ("disabled by default when cross-compiling"): <https://pkg.go.dev/cmd/cgo>
- [G3] Go, package plugin: <https://pkg.go.dev/plugin>
- [G4] dop251/goja README: <https://github.com/dop251/goja>
- [G5] google/starlark-go README: <https://github.com/google/starlark-go>
- [G6] esbuild Go API: <https://pkg.go.dev/github.com/evanw/esbuild/pkg/api>
- [G7] Go, package os/exec (Windows command lines, ErrDot, PATHEXT): <https://pkg.go.dev/os/exec>
- [G8] go-yaml/yaml (archived 2025-04-01, "THIS PROJECT IS UNMAINTAINED"): <https://github.com/go-yaml/yaml>
- [G9] goccy/go-yaml README: <https://github.com/goccy/go-yaml>
- [R1] rustc platform support: <https://doc.rust-lang.org/nightly/rustc/platform-support.html>
- [R2] rustup book, Cross-compilation: <https://rust-lang.github.io/rustup/cross-compilation.html>
- [R3] Rust security advisory CVE-2024-24576: <https://blog.rust-lang.org/2024/04/09/cve-2024-24576/>
- [R4] dtolnay/serde-yaml (archived 2024-03-25, "no longer maintained"): <https://github.com/dtolnay/serde-yaml>
- [Z1] Zod 4, JSON Schema: <https://zod.dev/json-schema>
- [S1] Sandcastle v0.13.0 `docs/guide/init.md` (`npx tsx .sandcastle/main.ts`, host installs Zod): <https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/init.md>
- [S2] Sandcastle v0.13.0 `package.json` (name `@ai-hero/sandcastle`), README ("A TypeScript library"), `src/templates/parallel-planner/main.mts` (imports `@ai-hero/sandcastle` and `zod`): <https://github.com/mattpocock/sandcastle/tree/v0.13.0>
- [S3] Sandcastle v0.13.0 `src/DockerLifecycle.ts` (uses `execFile` from `node:child_process`): <https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/DockerLifecycle.ts>
