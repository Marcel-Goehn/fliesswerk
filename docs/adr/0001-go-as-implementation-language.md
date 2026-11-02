# Go as the implementation language

The Factory is written in Go, even though its reference design, Sandcastle, is TypeScript and the maintainer's background is TypeScript. Sandcastle remains the reference design, but no code is ported from it. The Factory must ship as one executable that runs without a separately installed runtime on Windows, Linux and macOS, on x64 and arm64, and must cost nothing to build and distribute. Once user-authored code was ruled out (configuration is TOML plus shell Hooks), the main reason to choose a TypeScript runtime was gone. Go fits a tool that coordinates processes better: it cross-compiles to all six targets from one machine without a linker per target (`CGO_ENABLED=0`), produces small static executables (one Linux file runs on every distro), and has good built-in tools for running, timing out and killing child processes. Running parallel Iterations is straightforward, and TOML libraries are mature. The maintainer also wanted to learn Go.

## Considered Options

- **Bun / Deno (TypeScript)**: closest to Sandcastle and to the maintainer's experience. Rejected because executables are 30–50 MB, the runtime underneath is younger, and Bun's compiled executables run user `.ts` files by default, which works against the no-user-code extension model.
- **Rust**: the strongest correctness guarantees. Rejected because it is slower to write and needs a linker per target to cross-compile, which doesn't pay off for a tool that mostly coordinates other processes.
