# How Sandcastle handles branches, worktrees and merging

Research for issue #5 (map #1). Blocks #13. All Sandcastle references are to `mattpocock/sandcastle` at v0.13.0 (commit `f5c48e0`). Line numbers refer to that commit. Vocabulary follows `GLOSSARY.md` (Factory, Sandbox, Spec, Ticket, Run, Iteration, Step).

## Answer

- **Three branch strategies, chosen per `run()` call.** `head` means the agent writes straight into the host working directory, with no worktree. `merge-to-head` creates a temp branch `sandcastle/[<name>/]<YYYYMMDD-HHMMSS>-<hex>` in a worktree and merges it into the host's checked-out branch when the call ends. `branch` puts commits on a branch the caller names, in a worktree, and never merges it. The default depends on the Sandbox kind: `head` for bind-mount (Docker, Podman) and no-sandbox providers, `merge-to-head` for isolated providers. [S1, S9]
- **Worktrees live in `.sandcastle/worktrees/<branch-with-/-as->`, branched from host `HEAD`** (or from `baseBranch`, for a new named branch). A named-branch worktree is **reused** on a re-run. It fast-forwards from `origin` only when it is clean and strictly behind. A PID lock file in `.sandcastle/locks/` stops two processes from using it at once. [S2, S3, S4]
- **Sandcastle's code merges in exactly one place:** the `merge-to-head` merge back, a plain `git merge -- <temp>` run in the host repo. On a conflict it runs `git merge --abort` (the host branch is unchanged), **keeps the temp branch** and fails with retry instructions. It never resolves conflicts itself. [S5]
- **Parallel work is merged by an agent, not by code.** The parallel templates give each Ticket a deterministic named branch (`sandcastle/issue-<id>`) and run the Tickets concurrently. A final "merger" Step then runs `git merge <branch> --no-edit` for each branch, **resolves conflicts as an LLM**, runs the tests and closes the issues. Because the merger uses the bind-mount default (`head`), its merge commits land directly on whatever branch the host has checked out. [S6, S7]
- **Sandcastle has no Spec concept and no feature branches.** The issue list is "every open issue with the label" (default label `Sandcastle`). The planner agent, an LLM, decides which Tickets run together. If several unrelated Specs have ready Tickets, **all of them land on the one branch the host has checked out** (usually `main`). Grouping by Spec is up to the user: check out a feature branch first, or filter the list command. The real flashcards setup shows this: per-Ticket merges went straight onto `main`, and the `sandcastle/issue-*` branches were left behind, never deleted. [S6, S8, S10]
- **Other AFK tools work per task: one branch and one PR per Ticket, with a human merging.** GitHub Copilot's cloud agent pushes only to its own new `copilot/` branch (or to the PR branch that triggered it) and cannot merge its own PRs. claude-code-action creates `claude/<entityType>-<number>-<timestamp>` from `base_branch` for issues, and pushes to the existing branch for open PRs. Aider simply commits on the current branch. None of them groups several Tickets into a feature branch. [S11, S12, S13]

## Detail

### Branch strategies

| Strategy | Where the agent works | Where commits end up | Branch afterwards | Notes |
|---|---|---|---|---|
| `head` | Host working directory | Host's current branch | n/a | Bind-mount and no-sandbox only. `copyToWorktree` is unusable because there is no worktree [S9 simple-loop comment] |
| `merge-to-head` | Worktree on temp branch `sandcastle/<ts>-<hex>` | Merged into the host's current branch at the end of the call | Deleted. **Kept** if it holds commits found on no other branch (a failed run or a failed merge) | Default for isolated providers. Not safe to run concurrently: concurrent calls "race `git merge` against the same HEAD" [S1, S5, S10b] |
| `branch` | Worktree on the named branch | The named branch only | Kept. Sandcastle never merges or deletes it | Created from `baseBranch ?? "HEAD"` if the branch is missing. Reused if it exists [S2, S4] |

- `createWorktree()` with `merge-to-head` differs: each `wt.run()` merges into host HEAD but **keeps** the source branch for the next call. `wt.close()` deletes it unless it holds unmerged commits. [S1 l.143]
- `{{SOURCE_BRANCH}}` and `{{TARGET_BRANCH}}` are built-in prompt arguments, so prompts can diff against the target, for example the flashcards review prompt's `` !`git diff {{TARGET_BRANCH}}...{{BRANCH}}` ``. [S1 l.223, S8]

### Worktree lifecycle

1. **Create** (`WorktreeManager.create`, [S2 l.259–392]). Path: `.sandcastle/worktrees/<branch.replaceAll("/","-")>`.
   - A temp branch is created with `git worktree add -b <temp> <path> HEAD`.
   - A named branch is created with `git worktree add <path> <branch>`. If the branch doesn't exist yet (`invalid reference`), it falls back to `-b <branch> <path> <baseBranch ?? HEAD>`.
   - git flags suppress upstream config writes (`branch.autoSetupMerge=false`, …) [S2 l.11–18].
2. **Collision.** If the branch is already checked out in a worktree under `.sandcastle/worktrees/`, that worktree is **reused**. A dirty worktree is reused with a warning. A clean one gets `git fetch origin <branch>` + `git merge --ff-only`, and the refresh is skipped if that fails or the branches diverged. If the branch is checked out in the main checkout or an unmanaged worktree, Sandcastle errors, because git forbids the same branch in two worktrees. [S2 l.311–337, S3]
3. **Lock.** `.sandcastle/locks/<name>.lock` holds `{pid, branch, acquiredAt}` and is created atomically. A live PID makes the call fail fast with no retry. A dead PID's lock is taken over. The lock is released when the scope ends. [S4]
4. **Prune.** Before each creation, `pruneStale` runs `git worktree prune` and removes orphaned directories and stale locks. [S2 l.484–565; SandboxFactory l.408–419]
5. **Close.** A clean worktree is removed. A dirty or aborted one is kept, and its path is printed for review. [S1 l.141; SandboxFactory l.260–300]
6. **Isolated Sandboxes** (no shared filesystem). Commits leave the Sandbox as `git format-patch base..HEAD` and are applied on the host with `git am --3way`, which gives them **new SHAs**. The base is tracked in a sandbox-local ref, `refs/sandcastle/sync-base`. [S14]

### Merge back (`merge-to-head`) and conflicts

`withSandboxLifecycle` [S6b l.216–300]:

1. Read the host's current branch with `git rev-parse --abbrev-ref HEAD`. This is skipped for a named branch.
2. Record `baseHead` in the worktree.
3. Run the agent and sync to the host.
4. For a named branch, collect `baseHead..refs/heads/<branch>` and stop there.
5. Otherwise `mergeTempBranch`: detach the worktree, `git merge -- <temp>` in the **host repo directory**, then `git branch -D <temp>`.

The comment there explains why it uses merge and not cherry-pick: "cherry-pick breaks when the temp branch contains merge commits (e.g. a merge agent merging multiple parallel branches)" [S5 l.159–167].

On failure [S5 l.61–105]: if `MERGE_HEAD` exists, run `git merge --abort`. Then raise `SyncError`: "Merge of 'X' onto 'Y' failed and was aborted, so 'Y' is unchanged … The temporary branch 'X' has been preserved. To retry: git merge X …". The merge also has a timeout (`mergeToHostMs`, `MergeToHostTimeoutError`).

Consequence: the merge target is **the branch currently checked out in the user's main working directory**. Sandcastle never checks out or switches branches in the host checkout.

### Parallel Runs (templates `parallel-planner` and `parallel-planner-with-review`)

Each outer loop pass goes through three phases [S6]:

1. **Plan Step.** An agent reads the output of `LIST_TASKS_COMMAND` (GitHub: `gh issue list --label Sandcastle …` [S10]). It builds a dependency graph, counting "B and A modify overlapping files" as blocking, and emits `<plan>{"issues":[{id,title,branch}]}</plan>` with `branch = sandcastle/issue-{id}`. The name is deterministic "so that re-planning the same issue always produces the same branch name and accumulated progress is preserved" [S7]. That relies on worktree reuse (above).
2. **Execute.** `Promise.allSettled` runs one `run({branchStrategy:{type:"branch",branch}})` per Ticket. The with-review variant uses `createSandbox({branch})` and runs implement, then review, in the same Sandbox. Each Ticket branch starts from host `HEAD` at that moment.
3. **Merge Step.** One agent run with the default strategy (`head` on Docker/Podman), prompted to `git merge <branch> --no-edit` for each branch that has commits. It resolves conflicts "intelligently by reading both sides", runs typecheck and tests, fixes any failures, makes "a single commit summarizing the merge", and closes the issues with `CLOSE_TASK_COMMAND` [S7].

What this means for the questions in #5:

- **Which Tickets run together** is decided by an LLM, not by code. That conflicts with the map's principle that "code decides everything that isn't writing code".
- **Conflicts between parallel Tickets** are left to the LLM merge Step. Sandcastle's own code only aborts.
- **Several unrelated Specs:** every Ticket from every Spec is planned together and merged onto the single checked-out branch. Nothing separates them.
- **Per-Ticket branches are never cleaned up.** The `branch` strategy does not delete them.
- The **implement prompt** tells the agent to `gh issue view` the Ticket and "If it has a parent PRD, pull that in too". The agent fetches Spec context itself; code does not inject it [S6c].
- The **sequential-reviewer** template creates a fresh `sandcastle/sequential-reviewer/<Date.now()>` branch for each Iteration from host `HEAD` and **never merges it**. A later Iteration therefore does not see an earlier Iteration's work [S9b]. (This is inferred from the code. It does not affect correctness only if the Tickets are independent.)

### Observed in a real setup (`~/dev/flashcards/.sandcastle`, read-only inspection)

- `main.mts` is the `parallel-planner-with-review` template with `podman()` (bind-mount, so the merger runs as `head`). `merge-prompt.md` adds project-specific test commands and `gh issue close <ID>`.
- `git log --graph` on `main` shows merge commits such as "Merge Slide-deck Sources (#12) and Calibration and Preferences (#10)", plus single-Ticket merges, directly on `main`. `git branch` still lists `sandcastle/issue-{1,7,8,9,10,11,12,14,15}`. These are the leftover Ticket branches.
- Logs are named per worktree and Step (`sandcastle-issue-10-implementer.log`, `main-merger.log`).

### How other AFK tools group work

| Tool | Branch per | Base | Merge | Source |
|---|---|---|---|---|
| GitHub Copilot cloud agent | Task: "a new `copilot/` branch is created … the agent can only push to that branch". It writes to the PR branch when an existing PR triggers it | The branch the user picks | Human only: it "cannot approve or merge a pull request" | [S11] |
| claude-code-action | Issue: `{{prefix}}{{entityType}}-{{entityNumber}}-{{timestamp}}`, prefix `claude/`. Open PR: checks out the PR's branch | `base_branch` input, default is the repo default branch | Human, via PR | [S12] |
| Aider | None: commits each edit on the current branch, and the user manages branches | Current | n/a | [S13] |
| OpenAI Codex cloud | **Unconfirmed.** The docs page fetched says only that each task has "its own workspace" and that you can "commit or open a pull request" | ? | Human | [S15] |

The common pattern is one branch per unit of work, a PR, and a human merge. None of the tools checked groups several Tickets of one Spec onto a feature branch automatically. That grouping would be the Factory's own addition.

## Options for the Factory (input for #13, not a decision)

1. **Sandcastle-faithful.** Merge into whatever branch the host has checked out. The user checks out a feature branch and starts a Run limited to one Spec.
   - For: simplest, closest to Sandcastle.
   - Against: the user must sequence Specs by hand. A Run that picks Tickets from several Specs mixes them. The Factory writes into the user's checkout.
2. **One feature branch per Spec, owned by code.** For example, `factory/spec-<N>` is created from a configured base the first time the Spec is seen. Each Ticket branches from its Spec's feature branch and is merged back into it. Tickets without a Spec go to their own branch or a configurable default.
   - For: matches `Factory → /code-review → /pr` per feature. Unrelated Specs never mix, and code decides the grouping.
   - Against: merging into a branch that isn't checked out needs a worktree for the feature branch (git can only merge in a checked-out tree). Parallel Tickets in one Spec still conflict with each other.
3. **One branch (and PR) per Ticket** (Copilot / claude-code-action style).
   - For: no merging at all inside the Factory.
   - Against: dependent Tickets in one Spec can't build on each other unless they are stacked. Many PRs per Spec clash with the manual `/pr` step.

Sub-choices that hold whatever the option:

- **Who merges:**
  - Code runs `git merge` and falls back to an LLM merge Step only on conflict. This is cheaper and fits "code decides".
  - An LLM merge Step always runs (the Sandcastle templates). The tests also run after every merge.
- **Conflict policy:**
  - Abort and keep the branch, as Sandcastle code does.
  - Resolve with an LLM merge Step.
  - Re-queue the Ticket onto the updated feature branch, so it is re-implemented instead of merged.
- **Which Tickets run in parallel:** an LLM planner as in Sandcastle, or code from tracker "blocked-by" links plus label/Spec filters. The second fits the map.
- **Branch hygiene:** delete Ticket branches after a successful merge, as `merge-to-head` already does. The `branch` strategy and the templates leave them behind.
- **Re-runs:** deterministic branch names plus worktree reuse (ADR 0003) carry progress across Runs. Timestamped names do not.

## Newly surfaced questions

- Should the merge back touch the user's main checkout at all? Sandcastle runs `git merge` in the host repo directory, so a dirty or differently checked-out working tree affects the result. Detached-HEAD behaviour is **unconfirmed** (`rev-parse --abbrev-ref HEAD` returns `HEAD`).
- Isolated Sandboxes rewrite SHAs (`git am`). Does any Factory feature (cost per commit, closing Tickets by SHA) depend on stable SHAs?

## Sources

- [S1] Sandcastle `docs/guide/branch-strategies.md` (v0.13.0): https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/branch-strategies.md
- [S2] `src/WorktreeManager.ts`: temp branch naming l.57–71, `create` l.259–392, `deleteTempBranch…` l.434–479, `pruneStale` l.484–565: https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/WorktreeManager.ts
- [S3] ADR 0003 "Reuse existing worktree by default": https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/adr/0003-reuse-worktree-by-default.md
- [S4] ADR 0007 "Worktree locking"; `NamedBranchStrategy.baseBranch` in `src/SandboxProvider.ts` l.270–316: https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/adr/0007-worktree-locking.md
- [S5] `src/lifecycleHostGit.ts`: `mergeToHost` l.61–105, `mergeTempBranch` l.159–203: https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/lifecycleHostGit.ts
- [S6] `src/templates/parallel-planner/main.mts` and `parallel-planner-with-review/main.mts`; [S6b] `src/SandboxLifecycle.ts` l.216–300; [S6c] `src/templates/parallel-planner/implement-prompt.md` l.5
- [S7] `src/templates/parallel-planner/merge-prompt.md`, `plan-prompt.md`
- [S8] Local setup `/home/marcel/dev/flashcards/.sandcastle/{main.mts,merge-prompt.md,plan-prompt.md,review-prompt.md}` plus `git branch` / `git log --graph` in that repo (inspected 2026-11-02)
- [S9] `src/run.ts` l.400, l.701–702 (default strategy). [S9b] `src/templates/sequential-reviewer/main.mts` l.50–130, `simple-loop/main.mts` l.29–39
- [S10] `src/InitService.ts` l.741–743 (GitHub `LIST_TASKS_COMMAND` / `CLOSE_TASK_COMMAND`). [S10b] `docs/guide/agent-sessions.md` l.103 and ADR 0018 (concurrency of `head` / `merge-to-head`)
- [S11] GitHub Docs, "Risks and mitigations for Copilot cloud agent": https://docs.github.com/en/copilot/concepts/agents/coding-agent/risks-and-mitigations, and "About Copilot coding agent" ("can only work on one branch at a time and can open one pull request per task"): https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent
- [S12] `anthropics/claude-code-action` `action.yml` (`base_branch`, `branch_prefix`, `branch_name_template`) and `src/github/operations/branch.ts` `setupBranch` (commit `d535f51`): https://github.com/anthropics/claude-code-action
- [S13] Aider docs, "Git integration": https://aider.chat/docs/git.html
- [S14] Sandcastle ADR 0017 "Sync-out tracks its patch base in a sandbox-owned ref": https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/adr/0017-sandbox-owned-sync-base-ref.md
- [S15] OpenAI Codex cloud docs: https://learn.chatgpt.com/docs/cloud (redirected from developers.openai.com/codex/cloud). Branch behaviour not stated there.
