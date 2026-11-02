# Sandcastle's five templates

Ticket: [#3](https://github.com/Marcel-Goehn/ralph/issues/3). Source: Sandcastle **v0.13.0** (commit `f5c48e0`), read from source. `S/` below is short for `https://github.com/mattpocock/sandcastle/blob/v0.13.0/`.

Terms: a Sandcastle `run()` call with its own prompt and agent is what this project calls a **Step** (implement, review, plan, merge). Sandcastle's own "iteration" is one agent invocation inside a `run()` ([S/docs/guide/iterations.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/iterations.md#L3)). Each template's outer `for` loop is the **Run**-level loop. To keep the two apart, this note says "agent iteration" for the first and "outer pass" for the second.

## Answer

- **The templates are only example `main.mts` scripts.** Each one is a single TypeScript file plus some prompt `.md` files. `sandcastle init` copies the folder as-is into `.sandcastle/`. The flow lives in user-owned code, not in the engine, and the templates are kept deliberately small "to demonstrate the orchestration shapes" ([S/.out-of-scope/bundled-workflow-templates.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/.out-of-scope/bundled-workflow-templates.md), [S/docs/adr/0009-templates-no-shared-code.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/adr/0009-templates-no-shared-code.md)).
- **There are three shapes:** (1) a single `run()` that loops agent iterations (`blank`, `simple-loop`); (2) a sequential implement-then-review pass per Ticket (`sequential-reviewer`); (3) plan, then implement in parallel, then merge (`parallel-planner`, which `-with-review` extends with a review Step on each branch).
- **Code decides almost nothing.** In shapes 1 and 2 the *agent* chooses the Ticket from a prompt that lists open issues, and the *agent* closes it. In shape 3 an LLM planner chooses the Tickets and the order (a dependency graph, as `<plan>` JSON), and the LLM merger closes the issues. Code only does the looping, the fan-out, the "were there commits?" gate and argument passing. This is the opposite of the map's rule that code decides everything except writing code.
- **Limits are hard-coded constants.** `simple-loop`: 3 agent iterations. `sequential-reviewer`: 10 outer passes, with 1 agent iteration per Step. Parallel templates: 10 outer passes, planner/reviewer/merger 1 agent iteration each, implementer 100. **There is no cap on parallelism**: one sandbox per Ticket the planner returns, all started together with `Promise.allSettled`.
- **Branch strategies differ by template, and some results are surprising.** `sequential-reviewer` leaves **one unmerged branch per Ticket** (`sandcastle/sequential-reviewer/<timestamp>`), so a human has to merge them. The parallel templates' planner and merger use the default `head` strategy with Docker, so **the merger merges straight into the host's checked-out branch**.
- **Prompts are copied, not shared.** Within the same version, `simple-loop/prompt.md` is byte-identical to `sequential-reviewer/implement-prompt.md`. The review prompt and `CODING_STANDARDS.md` are identical in both review templates. The plan, implement and merge prompts are identical in both parallel templates. They are duplicated on purpose (ADR 0009).
- **Init flattens the models.** The templates use opus for the planner and sonnet for everything else, but `init` replaces *every* `claudeCode("…")` model with the one model you chose (default `claude-opus-4-8`) ([S/src/InitService.ts L1122-L1132](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/InitService.ts#L1122-L1132), [L446](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/InitService.ts#L446)). Choosing a model per Step is lost after init.

## Comparison

| | `blank` | `simple-loop` | `sequential-reviewer` | `parallel-planner` | `parallel-planner-with-review` |
|---|---|---|---|---|---|
| Steps | 1 (whatever the prompt says) | implement | implement, then review | plan, then implement ×N, then merge | plan, then (implement, then review) ×N, then merge |
| Prompts | `prompt.md` (empty skeleton) | `prompt.md` | `implement-prompt.md`, `review-prompt.md`, `CODING_STANDARDS.md` | `plan-prompt.md`, `implement-prompt.md`, `merge-prompt.md` | the planner set plus `review-prompt.md` and `CODING_STANDARDS.md` |
| Who picks the Ticket | n/a | the agent | the agent | planner LLM (`<plan>` JSON, Zod-validated) | same |
| Who closes the Ticket | n/a | the agent (implement) | the agent (implement, *before* review) | merger LLM | merger LLM |
| Outer loop | none | none (one `run()`) | `MAX_ITERATIONS = 10` | `MAX_ITERATIONS = 10` | `MAX_ITERATIONS = 10` |
| Agent iterations per Step | default 1 | 3 | 1 / 1 | plan 1, implement 100, merge 1 | plan 1, implement 100, review 1, merge 1 |
| Parallelism | 1 | 1 | 1 | unbounded (one per planned Ticket) | unbounded |
| Branch strategy | default (`head` with Docker) | `merge-to-head` | `createSandbox` on a new branch per pass, **never merged** | plan/merge: default `head`; implement: `branch` `sandcastle/issue-{id}` | same, with implement and review sharing one `createSandbox` on `sandcastle/issue-{id}` |
| Stop condition | `<promise>COMPLETE</promise>` | COMPLETE or 3 agent iterations | implement makes no commits, or 10 passes | plan returns `[]`, or 10 passes | same |
| Models in source | opus | sonnet | sonnet / sonnet | opus / sonnet / sonnet | opus / sonnet / sonnet / sonnet |
| Extra host dependency | none | none | none | `zod` | `zod` |

## Detail per template

### `blank`
A single `run({ agent, sandbox: docker(), promptFile })` call with no other options ([S/src/templates/blank/main.mts](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/blank/main.mts)). The prompt is an empty Context / Task / Done skeleton that explains `` !`command` `` interpolation and the COMPLETE signal ([prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/blank/prompt.md)). `maxIterations` defaults to 1 ([S/src/run.ts L95](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/run.ts#L95)). The branch strategy defaults to `head` for bind-mount providers and `merge-to-head` for isolated ones ([S/src/run.ts L696-L702](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/run.ts#L696-L702)). `docker()` is a bind-mount provider ([S/src/sandboxes/docker.ts L158](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/sandboxes/docker.ts#L158)). Init's next steps for `blank` tell the user to write their own prompt and orchestration ([S/src/InitService.ts L958-L967](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/InitService.ts#L958-L967)).

### `simple-loop`
A single `run()` with `maxIterations: 3`, `branchStrategy: merge-to-head`, `copyToWorktree: ["node_modules"]` and an `npm install` `onSandboxReady` hook ([main.mts L8-L50](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/simple-loop/main.mts#L8-L50)). The prompt is re-expanded on every agent iteration ([main.mts L20-L22](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/simple-loop/main.mts#L20-L22)), so the Ticket list is always current. The prompt ([prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/simple-loop/prompt.md)) injects `{{LIST_TASKS_COMMAND}}` output and the last 10 `RALPH` commits. It asks the agent to choose by priority (bug > tracer bullet > polish > refactor) and to skip blocked issues, then explore, plan, use red-green-refactor, run `npm run typecheck && npm run test`, make one `RALPH:` commit and close the issue with `{{CLOSE_TASK_COMMAND}}`. One issue per agent iteration. When blocked, the agent comments and moves on. It emits COMPLETE when nothing is left. `merge-to-head` merges the temporary branch back to HEAD when the run ends ([S/docs/guide/branch-strategies.md L38](https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/branch-strategies.md#L38)).

### `sequential-reviewer`
An outer loop of `MAX_ITERATIONS = 10` passes ([main.mts L33-L117](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/sequential-reviewer/main.mts#L33-L117)). Each pass:
1. Creates a branch `sandcastle/sequential-reviewer/${Date.now()}` and one `createSandbox({ branch, … })` that both Steps share (L54-L63). The worktree is branched from `HEAD` ([S/src/WorktreeManager.ts L353](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/WorktreeManager.ts#L353)).
2. **Implement**: `sandbox.run({ maxIterations: 1, promptFile: implement-prompt.md })`. It is limited to 1 on purpose, because more would "drain the whole backlog onto this one branch … which defeats the per-issue review" (L75-L84). The implement prompt is identical to `simple-loop/prompt.md`, so the agent also **closes the issue before the review**.
3. If `implement.commits.length === 0`, the loop stops (L86-L91).
4. **Review**: `sandbox.run({ maxIterations: 1, promptFile: review-prompt.md, promptArgs: { BRANCH } })` (L103-L111). The review prompt injects `git diff {{TARGET_BRANCH}}...{{BRANCH}}` and the commit log. It asks for simplifications that keep behaviour intact plus correctness, test and security checks, loads `@.sandcastle/CODING_STANDARDS.md`, and lets the reviewer commit fixes on the branch or do nothing ([review-prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/sequential-reviewer/review-prompt.md)). `TARGET_BRANCH` is a built-in prompt argument: the host's active branch ([S/src/PromptArgumentSubstitution.ts L21](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/PromptArgumentSubstitution.ts#L21)).
5. `sandbox.close()`. Nothing merges the branch. A top-level `createSandbox()` "is always explicit-branch" ([S/src/createSandbox.ts L318-L324](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/createSandbox.ts#L318-L324)). The result is one review-gated branch per Ticket, each based on the original HEAD. That every pass starts from the unmerged HEAD is my reading of the code, not something the docs state.

### `parallel-planner`
An outer loop of `MAX_ITERATIONS = 10` with three phases ([main.mts](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner/main.mts)):
1. **Plan** (L68-L90): `run({ maxIterations: 1, output: Output.object({ tag: "plan", schema }) })`, with schema `{ issues: [{ id, title, branch }] }` (L27-L31). Structured output requires `maxIterations: 1` ([S/src/run.ts L562-L566](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/run.ts#L562-L566)). An invalid plan throws and aborts the loop (L78-L81). The plan prompt asks the LLM to build a dependency graph. "Blocked" covers code dependency, *overlapping files* (to avoid merge conflicts) and pending API decisions. The LLM returns only unblocked issues, using the deterministic branch `sandcastle/issue-{id}` "so … accumulated progress is preserved". If everything is blocked it returns the single best candidate, and it always emits `<plan>` (`[]` exits) ([plan-prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner/plan-prompt.md)). An empty plan breaks the loop (L86-L90).
2. **Implement** (L108-L132): `Promise.allSettled(issues.map(run({ branchStrategy: { type: "branch", branch }, maxIterations: 100, promptArgs: { TASK_ID, ISSUE_TITLE, BRANCH } })))`. There is no concurrency limit. Re-using a named branch re-uses its worktree ([S/docs/guide/branch-strategies.md L39](https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/branch-strategies.md#L39)). The implement prompt names one issue (`{{VIEW_TASK_COMMAND}}`), asks for red-green-refactor, typecheck/test and a `RALPH:` commit. It also says "Do not close the issue - this will be done later", and to comment on the issue if the work is incomplete ([implement-prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner/implement-prompt.md)). Rejected promises are logged. Only branches with commits move on (L137-L150). If there are none, the template `continue`s to the next pass (L161-L165).
3. **Merge** (L176-L190): one `run({ maxIterations: 1, promptArgs: { BRANCHES, ISSUES } })` with no `branchStrategy`, so on Docker it uses `head` and runs in the host working directory ([S/docs/guide/branch-strategies.md L37](https://github.com/mattpocock/sandcastle/blob/v0.13.0/docs/guide/branch-strategies.md#L37)). The merge prompt runs `git merge <branch> --no-edit` for each branch, resolves conflicts, runs typecheck/test and fixes failures, makes one summary commit, then closes each merged issue with `{{CLOSE_TASK_COMMAND}}` ([merge-prompt.md](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner/merge-prompt.md)).

### `parallel-planner-with-review`
The same as `parallel-planner` (plan, implement and merge prompts are byte-identical), except phase 2 ([main.mts](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/templates/parallel-planner-with-review/main.mts)). Each issue gets `createSandbox({ branch: issue.branch })`, then implement (100 agent iterations), then, **only if it produced commits**, review (1 agent iteration, the same review prompt as `sequential-reviewer`) in the same sandbox. The template concatenates both Steps' commits so the merge gate sees them, because "each sandbox.run() only returns commits from its own run". Each Ticket's pipeline runs concurrently under `Promise.allSettled`.

## Shared structure (candidate primitives)

The five templates combine a small set of pieces:

- **The "Step" call**: `run`/`sandbox.run({ name, agent, promptFile, promptArgs, maxIterations, output? })`.
- **Sandbox per Ticket**: `createSandbox({ branch })`, so several Steps can share a branch and worktree.
- **Gate**: `result.commits.length > 0` decides whether the next Step runs and whether to stop.
- **Fan-out**: `Promise.allSettled` over planned Tickets, so one failure doesn't cancel the others.
- **Structured hand-off**: `Output.object({ tag, schema })` (only the planner uses it).
- **Signals and setup**: `<promise>COMPLETE</promise>` for early stop; `copyToWorktree: ["node_modules"]` and the `npm install` hook in every non-blank template.
- **Init-time template args**: `{{LIST_TASKS_COMMAND}}`, `{{VIEW_TASK_COMMAND}}` and `{{CLOSE_TASK_COMMAND}}` come from the tracker registry. For GitHub they expand to `gh issue list --label Sandcastle …`, `gh issue view <ID>` and `gh issue close <ID> --comment "Completed by Sandcastle"` ([S/src/InitService.ts L741-L743](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/InitService.ts#L741-L743)). Init also replaces the agent factory and sandbox provider names in the template text with regexes (L1110-L1150).
- **Prompt-time args**: `{{BRANCH}}`, `{{TASK_ID}}`, `{{ISSUE_TITLE}}`, `{{BRANCHES}}`, `{{ISSUES}}`, plus the built-in `{{TARGET_BRANCH}}`.

TypeScript defaults are baked into the prompts and hooks: `npm run typecheck`, `npm run test`, `npm install`, `node_modules`. Init only *tells* users of other package managers to edit these by hand ([S/src/InitService.ts L979-L984](https://github.com/mattpocock/sandcastle/blob/v0.13.0/src/InitService.ts#L979-L984)).

## Options and trade-offs for v1 (input to #14 / #18, not a decision)

- **Ship the shapes as configuration, not as copied code.** All five reduce to a Step list (plan? implement, review?, merge?), a parallelism setting and a few limits. Sandcastle copies code on purpose (ADR 0009), and the cost is drift. A config-driven Factory could offer `sequential` and `parallel` modes, each with optional review.
- **Move Ticket selection and closing into code.** Every template leaves these to the LLM. The parallel planner's dependency graph could be built in code from tracker data (GitHub "blocked by"/sub-issue links), but the planner also judges *file overlap*, which code can't easily do. That is an open question.
- **Branch outcome per shape.** There are three behaviours: merge to HEAD (`simple-loop`), one branch per Ticket left unmerged (`sequential-reviewer`), and LLM merge into the host branch in place (parallel). This needs an explicit, consistent choice. Closing the issue before review (`sequential-reviewer`) and merging in place on the host (parallel) are both risks for AFK use.
- **Bound the parallelism.** Sandcastle has no limit, so v1 likely wants a `maxParallel` setting.
- **Per-Step models.** The source intends different models per Step (opus to plan, sonnet to execute), but init collapses them to one. Per-Step model config would keep that intent.
- **Candidate new shapes** suggested by the gaps: review that can loop back to implement (Sandcastle's review only does one pass of edits), a merge done in code (`git merge` with a fallback to an LLM merge only on conflict), and a single-Ticket run.

## Sources

All at tag v0.13.0 of https://github.com/mattpocock/sandcastle:
- `src/templates/{blank,simple-loop,sequential-reviewer,parallel-planner,parallel-planner-with-review}/` (all files)
- `src/InitService.ts` (template registry L35-L61, tracker template args L736-L778, next steps L927-L1000, main rewrite L1110-L1150, init order L1561-L1578)
- `src/run.ts` (L95 default maxIterations, L562-L566 output requires 1 iteration, L696-L702 default branch strategy)
- `src/sandboxes/docker.ts` L158 (bind-mount provider); `src/WorktreeManager.ts` L353 (branch from HEAD); `src/createSandbox.ts` L318-L324; `src/PromptArgumentSubstitution.ts` L21
- `docs/guide/branch-strategies.md`, `docs/guide/iterations.md`, `docs/guide/init.md` (Templates section)
- `docs/adr/0009-templates-no-shared-code.md`, `.out-of-scope/bundled-workflow-templates.md`
