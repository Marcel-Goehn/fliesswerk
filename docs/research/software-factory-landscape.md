# Software factory landscape (as of 2026-11-02)

**Question:** Which open-source software factories / AFK coding-agent orchestrators exist, which have the most GitHub stars, and which are the most used?

**Method:**
- Star counts, last-push dates and descriptions come from the GitHub API (`gh api repos/OWNER/REPO`).
- Weekly npm downloads cover 2026-10-26 to 2026-11-01 and come from `api.npmjs.org`.
- PyPI figures are monthly downloads from `pypistats.org`.
- VS Code figures are cumulative installs from the Marketplace gallery API.
- None of the tools were tested hands-on. Notes on whether a tool can run AFK come from its docs, its description and its repo activity.

## Orchestrators: tools in Sandcastle's category

These tools run an existing agent harness (Claude Code, Codex, opencode, …) unattended, usually isolated, against a backlog of work.

| Project | Stars | Last push | What it is | How close to Sandcastle |
|---|---|---|---|---|
| [ruvnet/ruflo](https://github.com/ruvnet/ruflo) (ex claude-flow) | 80.7k | 2026-11-02 | Multi-agent swarms on top of Claude Code / Codex, with memory, federation and more | Same category but much heavier. Stars look high next to ~102k npm downloads/week (`ruflo`) |
| [BloopAI/vibe-kanban](https://github.com/BloopAI/vibe-kanban) | 28.6k | 2026-10-29 | Kanban board that hands each card to a coding agent in its own worktree | Close, but you supervise from a UI rather than leave it AFK. ~4k npm downloads/week |
| [openai/symphony](https://github.com/openai/symphony) | 27.9k | 2026-09-15 | "Turns project work into isolated, autonomous implementation runs" | **Very close**: isolated runs that work through the backlog |
| [snarktank/ralph](https://github.com/snarktank/ralph) | 22.1k | 2026-02-02 | Canonical Ralph loop: runs again and again until every PRD item is done | Same idea, but a script with no sandboxing. Looks stale |
| [gastownhall/gastown](https://github.com/gastownhall/gastown) (+ [beads](https://github.com/gastownhall/beads), 28.4k) | 18.7k | 2026-10-28 | Workspace manager for many agents, plus an issue tracker built for agents | Close, oriented toward running many agents in parallel |
| [langchain-ai/open-swe](https://github.com/langchain-ai/open-swe) | 11.2k | 2026-11-02 | Open-source asynchronous coding agent that runs in the cloud and works from issues | Close, but brings its own agent rather than wrapping a harness |
| [frankbria/ralph-claude-code](https://github.com/frankbria/ralph-claude-code) | 9.7k | 2026-10-31 | Ralph loop for Claude Code that detects when to stop | Close, only for Claude Code |
| [mattpocock/sandcastle](https://github.com/mattpocock/sandcastle) | 8.8k | 2026-11-02 | Sandboxed agent orchestration as a TypeScript library (`sandcastle.run()`) | Reference point. ~235k npm downloads/week (`@ai-hero/sandcastle`), the highest measurable usage in this group |
| [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) | 8.7k | 2026-08-20 | Terminal manager for parallel Claude / Codex / opencode sessions | Built for supervising sessions yourself, less AFK |
| [automazeio/ccpm](https://github.com/automazeio/ccpm) | 8.4k | 2026-10-19 | GitHub Issues plus git worktrees driving parallel agents | Close to an issue-driven factory |
| [dagger/container-use](https://github.com/dagger/container-use) | 4.1k | 2026-09-21 | Container sandboxes for coding agents | Covers only the sandboxing part |
| [mikeyobrien/ralph-orchestrator](https://github.com/mikeyobrien/ralph-orchestrator) | 3.2k | 2026-10-31 | Improved Ralph Wiggum loop implementation | Close, smaller |
| [fabro-sh/fabro](https://github.com/fabro-sh/fabro) | 1.8k | 2026-11-02 | "Open source dark software factory" | Uses the same name for itself, still small |
| [strongdm/attractor](https://github.com/strongdm/attractor) | 1.3k | 2026-03-17 | Spec for a non-interactive coding agent for use in a software factory | Design spec, not a tool |
| [ghuntley/how-to-ralph-wiggum](https://github.com/ghuntley/how-to-ralph-wiggum) | 1.8k | — | Geoffrey Huntley's guide to the Ralph technique | Reference write-up, not a tool |

**Most stars:** ruflo, but it's a heavyweight swarm framework. Among tools that are actually similar to Sandcastle, Vibe Kanban and Symphony lead at about 28k each.

**Most used:** Sandcastle has the highest measurable usage (npm downloads), with ruflo second. Symphony, Gas Town and Open SWE publish no download stats, so their usage can't be compared.

**Worth studying for this project:** Symphony, Sandcastle, ccpm and snarktank/ralph. They're the closest to picking up GitHub Issues and running an agent on them unattended.

## Harnesses: the workers inside the loop

These are single coding agents. A factory runs one of them headless inside its loop.

| Project | Stars | Usage signal | Headless / AFK mode | Maintained? |
|---|---|---|---|---|
| [anomalyco/opencode](https://github.com/anomalyco/opencode) | 224k | 5.16M/wk npm (`opencode-ai`) | `opencode run`, server mode | Yes |
| [anthropics/claude-code](https://github.com/anthropics/claude-code) (source-available) | 154k | 12.9M/wk npm; 29.3M VS Code installs | `claude -p`, Agent SDK, claude-code-action | Yes |
| [openai/codex](https://github.com/openai/codex) | 133k | 27.8M/wk npm (`@openai/codex`); 16.3M VS Code installs | `codex exec`, codex-action | Yes |
| [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli) | 109k | 445k/wk npm | `-p` non-interactive | Yes |
| [OpenHands/OpenHands](https://github.com/OpenHands/OpenHands) | 93k | 461k/month PyPI (`openhands-ai`) | Headless mode, GitHub issue resolver, SDK | Yes |
| [cline/cline](https://github.com/cline/cline) | 71k | 5.8M VS Code installs; 104k/wk CLI | Mainly IDE-based, has a headless CLI | Yes |
| [aaif-goose/goose](https://github.com/aaif-goose/goose) | 56k | n/a (Rust binary) | `goose run`, recipes | Yes |
| [Aider-AI/aider](https://github.com/Aider-AI/aider) | 50k | 261k/month PyPI (`aider-chat`) | Scriptable via `--message --yes` | Slowing (last push May 2026) |
| [SWE-agent/SWE-agent](https://github.com/SWE-agent/SWE-agent) | 21k | — | Batch runs from issue to patch, research-oriented | Yes |

**Most used harnesses:** Codex CLI and Claude Code are far ahead, followed by opencode.

## Caveats

- The usage numbers don't compare like for like. npm counts include CI runs and auto-updates. Claude Code also ships a native installer, so npm undercounts it. VS Code figures are cumulative installs, not active users.
- Stars can be inflated, and some projects (ruflo especially) have star counts out of proportion to their downloads.
- All figures are a snapshot from 2026-11-02.
