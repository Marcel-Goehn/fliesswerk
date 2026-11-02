# Can the parallel planner's decisions be computed instead of asked of an agent?

Research for [#4](https://github.com/Marcel-Goehn/ralph/issues/4). Blocks #14.
Reference: Sandcastle v0.13.0 (`f5c48e0`), plus a real eight-Run planner log from the `flashcards` project.

## Answer

1. **What the planner decides.** Only one thing: *which Tickets start in this round*. Its output is `{"issues": [{id, title, branch}]}` [S1, S2]. It does no grouping, ordering or priority beyond that. The branch name is already fixed as `sandcastle/issue-{id}` and only gets copied into the output. Sandcastle changed this to be deterministic after slug-based names lost progress [S4]. The orchestrating code then runs every selected Ticket at once, with no cap on concurrency [S2, S4].
2. **What it reasons from.** Issue `number, title, body, labels, comments` from `gh issue list` [S1, S3]. Three blocking criteria are written into the prompt: (a) B needs code that A introduces, (b) A and B touch overlapping files, (c) B depends on a decision or API that A establishes. There is also a fallback: "if every issue is blocked, include the single highest-priority candidate" [S1].
3. **The planner never sees the tracker's real blocking graph.** The default list command doesn't request `blockedBy`, `parent` or `subIssues` [S3]. `gh` 2.103 exposes all three as `--json` fields, including each blocker's `state` [S7]. So the planner rebuilds from prose a graph the tracker already holds. It also can't see a blocker that is open but not `ready-for-agent`, because the input is already filtered to ready issues [S1].
4. **Criteria (a) and (c) can be computed.** Once `to-tickets` records them as native "blocked by" links (or a parseable `## Blocked by` section), "ready" just means: open, `ready-for-agent`, not a Spec (`subIssuesSummary.total == 0`), and every blocker closed. Readiness detection in this style is standard in other tools. Beads' `bd ready` does exactly this, and Sandcastle already treats it as the *input* to the planner when Beads is the tracker [S3, S10]. Python's `graphlib.TopologicalSorter.get_ready()/done()` is the textbook API for the same "release successors as their predecessors finish" loop [S8].
5. **Only criterion (b), predicting file overlap, needs judgment, and in the real Run it didn't help.** Three rounds ran two Tickets in parallel. All three merges conflicted (`README.md`, `SKILL.md` files), and the merger agent resolved every one by keeping both sides [S6]. The planner had predicted "shouldn't clash" or "should merge cleanly" for two of the three pairs. Its one deliberate hold-back (#10 and #12, held behind #9 and #11) cost an extra plan→execute→merge cycle, and #10 and #12 still conflicted with each other in the next round [S5, S6].
6. **Overlap can be handled after the fact instead of predicted.** `git merge-tree --write-tree A B` reports a clean merge or a conflict (exit 0/1) without touching the working tree or index [S9]. Code can merge clean branches itself and spend agent tokens only on a real conflict.
7. **What is lost by dropping the agent:** reading prose dependencies that were never recorded as links, guessing semantic overlap, and one-off judgment calls. Examples from the log: refusing a Ticket because of a cost rule in `CLAUDE.md`, and noticing that five passes produced no commits [S5]. Each has a code-side substitute: a link-hygiene check, an optional `Touches:` hint, the "agent reports it can't be done" path from the map, and a "no commits after N passes" counter. Details below.

## Detail

### 1. Sandcastle's planner contract

| Aspect | What Sandcastle does | Source |
|---|---|---|
| Input | Shell-expanded `gh issue list --state open --label <L> --limit 100 --json number,title,body,labels,comments` (GitHub). With Beads it is `bd ready --json`, so readiness is already computed upstream | [S3] `src/InitService.ts:741,755` |
| Prompt | "build a dependency graph"; three blocking criteria; "Include only unblocked issues"; fallback to "single highest-priority candidate"; always emit `<plan>` | [S1] |
| Output | `<plan>` JSON, validated with Zod via `Output.object`. A missing or malformed plan throws `StructuredOutputError`, which aborts the loop | [S2], [S4] `6bcaa7a` |
| Branch | `sandcastle/issue-{id}`, fixed. The old `{id}-{slug}` form forked a new branch on every re-plan and lost progress | [S4] `d0afa21` |
| Execution | `Promise.allSettled` over all planned issues. The semaphore concurrency cap was removed | [S2], [S4] `0f8a99a` |
| Re-planning | The outer loop (`MAX_ITERATIONS`, 5–10) re-runs the planner after each merge, so newly unblocked Tickets get picked up | [S2] |
| Merge | One merger agent runs `git merge <branch> --no-edit` per branch, fixes conflicts and failing tests, and closes the issues | `src/templates/parallel-planner/merge-prompt.md` [S2] |

The planner's only real decision is a **subset of the ready list**. "Priority" appears only in the all-blocked fallback, and the input has no priority field [S1, S3].

### 2. What the planner actually decided (flashcards, 2026-11-01)

The tracker's native links, taken from the GitHub dependencies API [S7]: #7←#6; #8←#5,#6; #9←#4,#8; #10←#8; #11←#8; #12←#4,#7. #14 and #15 had no native links, but #15's body has a `## Blocked by` section listing #14 and #7–#13. Spec #1 has sub-issues #2–#15.

| Run | Ready list given | Planner chose | Pure graph rule would choose | Note |
|---|---|---|---|---|
| 09:04 | #1, #7–#12 | #7, #8 | #7, #8 | Same result. The planner derived #9←#8 and #12←#7 from prose; both were already native links. Prompt context: 30k |
| 09:31 | #1, #9–#12 | #9, #11 | #9, #10, #11, #12 | Planner held #12 (overlaps #9 in `sections.py`) and #10 (overlaps #9/#11 in `make-flashcards/SKILL.md`) |
| 10:17 | #1, #10, #12 | #10, #12 | #10, #12 | Planner warned that #10 and #12 might conflict and ran them anyway |
| 11:12 | #1 only | **#1 (the Spec)** | nothing (Spec excluded) | The planner scheduled a Spec. A `subIssues > 0` rule prevents that |
| 11:47 | empty | empty | empty | |
| 17:47 | #14, #15 | #14 | #14 (if the `## Blocked by` body section is parsed) | No native link existed. Prose only |
| 18:54 | #15 | #15 | #15 | Planner made its own `gh` query to check blocker states |
| 19:22 | #15 | **empty** | #15 | Planner refused: "$10 eval run, CLAUDE.md says ask above $2"; five earlier passes made no commits |

Sources: planner log [S5], merger log [S6], dependency API [S7].

**Merge outcome of the parallel rounds** [S6]:
- #7 + #8: `README.md` conflict (the planner had said "shouldn't clash").
- #9 + #11: conflicts in `README.md` and `skills/reviewing-notes/SKILL.md` (the planner had said "should merge cleanly").
- #12 + #10: conflicts in two `SKILL.md` files (predicted).

The merger agent resolved all of them, and the tests passed afterwards.

**Reading:** in this Run, the planner's file-overlap judgment (a) didn't prevent conflicts, because the hotspot files were docs and skill files every Ticket touches, and (b) cost one extra round. A graph-only scheduler would have run #9–#12 together at 09:31 and handed the merger one conflict-heavy merge instead of two. One project with 15 Tickets is too small a sample to generalise from. It is evidence, not proof.

### 3. A deterministic replacement, piece by piece

| Planner job | Computable? | How |
|---|---|---|
| Is it a Ticket, not a Spec? | Yes | `subIssuesSummary.total == 0` [S7]; this matches the map rule "a Spec is never worked on" |
| Blocked by an open issue? | Yes, if links exist | `blockedBy.nodes[].state` from `gh issue list --json blockedBy` [S7], or `GET .../dependencies/blocked_by` (paginated, max 100 per page) [S11]. Any *open* blocker counts, even one that isn't `ready-for-agent`, which the Sandcastle planner can't see |
| Dependencies written only in prose | Partly | Parse a fixed `## Blocked by` section with `#N` references (the format `to-tickets` already writes, e.g. flashcards #15). Free-form prose ("needs the Section Review that #8 builds") can't be computed. Better to require links at ticket-writing time and have the Factory warn when a body mentions `#N` with no matching link |
| Branch name | Yes | Already `prefix/issue-{id}` |
| Ordering / priority | Yes | Issue number ascending (creation order roughly matches `to-tickets` order), optionally a priority label. Sandcastle has no priority input at all |
| How many at once | Yes | A configured concurrency cap. Sandcastle has none [S4 `0f8a99a`] |
| All candidates blocked | Yes | Either a cycle (detect it, e.g. `graphlib` raises `CycleError` [S8], and report to the human) or blockers outside the ready set (report which ones). Sandcastle's "pick the highest-priority blocked one anyway" fallback runs work the graph says isn't ready; that is a design choice to revisit |
| Predict file overlap | No (only heuristics) | See section 4 |
| One-off judgment (cost rules, "pointless re-run") | No | Leave it to the implement Step: the agent reports "can't be done", and the Ticket stays open with a comment (map rule). Add a counter: skip a Ticket whose branch got N passes with no commits |

### 4. Options for file-overlap / merge conflicts

| Option | Tokens | Determinism | Trade-off |
|---|---|---|---|
| **A. Ignore overlap; resolve at merge** (graph only) | Merge Step only | Full | Maximum parallelism. Every conflict lands in the merge Step. Matches what actually happened in the real Run |
| **B. A `Touches:` / file-ownership hint per Ticket** (written by `to-tickets`) | 0 | Full, given the hints | Code builds a conflict graph and picks a non-overlapping set greedily (e.g. in issue-number order). Only as good as the hints. Hotspot files like `README.md` would serialise everything unless listed as shared or ignored. Precedent for path ownership: CODEOWNERS [S12] (used for reviewers, not scheduling) |
| **C. Merge with a pre-check** (`git merge-tree --write-tree`) | Merger agent only on conflict | Full | Code merges clean branches itself and calls an agent only for branches that actually conflict [S9]. Can be combined with A or B. Doesn't *prevent* conflicts; it makes clean merges free |
| **D. Keep an agent, advisory only** | One planning Step per round (about 20–30k context each in the log [S5]) | None | Keeps semantic overlap guessing. In the log it predicted 1 of 3 conflicts and cost one extra round |
| **E. Learn hotspots from history** (files that conflicted or changed together before) | 0 | Full | Needs data and configuration. Unconfirmed whether it's worth the complexity at this scale |

These options combine. "Graph-only readiness + concurrency cap + C" uses no tokens before the implement Steps and calls an agent only when git reports a real conflict.

### 5. Things to flag for downstream tickets

- **Tracker boundary:** the adapter must expose `blockedBy` (with state), `parent` and `subIssues` per Ticket, not just body and labels. GitHub provides all three through `gh` [S7]. Beads provides readiness directly (`bd ready`) [S10].
- **`to-tickets` convention:** deterministic planning only works if blocking is recorded as native links (or a strictly formatted `## Blocked by` section). In flashcards, #14/#15 used body text only while #7–#13 had native links. The convention was mixed.
- **Merge Step:** if conflicts are resolved at merge time rather than avoided, the merge Step matters more. See option C.
- Unconfirmed: whether GitHub limits the number of dependencies per issue or supports cross-repo links. The docs pages don't say [S11, S13]. Beads supports cross-repo blockers via `external:` refs [S10].

## Sources

- [S1] Sandcastle v0.13.0, `src/templates/parallel-planner/plan-prompt.md` (identical in `parallel-planner-with-review`). https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner/plan-prompt.md
- [S2] Sandcastle v0.13.0, `src/templates/parallel-planner{,-with-review}/main.mts`, `merge-prompt.md`; the local copy is `/home/marcel/dev/flashcards/.sandcastle/main.mts`.
- [S3] Sandcastle v0.13.0, `src/InitService.ts` lines 736–760 (`ISSUE_TRACKER_REGISTRY`: GitHub and Beads `LIST_TASKS_COMMAND`).
- [S4] Sandcastle `CHANGELOG.md`: entries `d0afa21` (deterministic branch names), `6bcaa7a` (`Output.object`), `0f8a99a` (semaphore removed), `0b74ab6` (`--limit 100`), `7400ead` (the "already filtered" hint).
- [S5] Planner log, `/home/marcel/dev/flashcards/.sandcastle/logs/main-planner.log` (8 Runs on 2026-11-01).
- [S6] Merger log, `/home/marcel/dev/flashcards/.sandcastle/logs/main-merger.log` (lines 63–78, 115–153, 179–244).
- [S7] `gh` 2.103.0, `gh issue list --json` field list (`blockedBy`, `blocking`, `parent`, `subIssues`, `subIssuesSummary`), and the live queries `gh api repos/Marcel-Goehn/flashcards/issues/{n}/dependencies/blocked_by` and `.../issues/1/sub_issues`.
- [S8] Python docs, `graphlib.TopologicalSorter` (`get_ready`, `done`, parallel example, `CycleError`). https://docs.python.org/3/library/graphlib.html
- [S9] Git docs, `git merge-tree --write-tree` (no working tree or index access; exit 0 clean, 1 conflict). https://git-scm.com/docs/git-merge-tree. The Git version that introduced it isn't stated on that page (believed to be 2.38; unconfirmed).
- [S10] Beads README (`bd ready`: "no open blockers", dependency types, `external:` refs, `--claim`). https://github.com/steveyegge/beads
- [S11] GitHub REST, Issue dependencies endpoints. https://docs.github.com/en/rest/issues/issue-dependencies
- [S12] GitHub docs, About code owners. https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners
- [S13] GitHub docs, Creating issue dependencies. https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/creating-issue-dependencies
