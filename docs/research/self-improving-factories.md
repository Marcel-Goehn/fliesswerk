# How self-improving factories learn from their mistakes (as of 2026-11-27)

**Question** (#160, map #151): how do agent workflows learn from what went wrong and improve their surroundings? Does Matt Pocock's `/retro` skill match the idea of the Factory improving a project's Coding standards, agent docs and Feedback commands from review findings and CI failures? How do others close the loop, and what keeps such loops from drifting or bloating the docs?

**Method:**
- Primary sources only: skill source files cloned from GitHub (commit SHAs given), vendor docs, vendor engineering posts and arXiv papers.
- Nothing was run hands-on. Numbers are the sources' own claims.
- Claims I could not check against a primary source are marked **unverified**.

## Summary

- **`/retro` matches what the idea changes, but not how it runs.** It sorts what went wrong into the right homes: an automated check first, a Coding-standards rule for the reviewer second, and a navigation pointer in `AGENTS.md`/`CLAUDE.md` only as a last resort. That is the order fliesswerk wants. But it is a human-invoked, interactive skill. It reads one session log and returns a list of candidates. It does not open a PR, does not combine evidence across Runs, and does not prune what it added earlier.
- **The strongest pattern across sources is "promote the lesson into code".** `/retro`, Every's `ce-compound` and OpenAI's harness-engineering post all prefer a failing check over a sentence of prose. They keep prose for judgement calls, and they keep always-loaded agent docs short, as a table of contents.
- **Always-loaded context files are not free and often don't help.** A 2026 ETH study found that `AGENTS.md`-style files do not generally improve task success and raise cost by more than 20%. Loops that append to `CLAUDE.md` by default work against fliesswerk's cost goal.
- **The anti-drift mechanisms that hold up are evidence-based:** a lesson enters only on a verified signal, and leaves when evidence says it is stale. Examples are citations checked against the current branch (Copilot Memory), expiry without reinforcement (Copilot Memory), disabling on negative signal (Cursor Bugbot), `retire_when` conditions and evidence-gated delete (`ce-compound` refresh), and small itemised edits instead of whole-file rewrites (ACE).
- **Learning from noisy signals pollutes the context** (ACE). Only verified outcomes should teach. Examples are a review finding that was fixed, or a CI failure that later went green.

## 1. Matt Pocock's `/retro`

Source: [`skills/engineering/retro/SKILL.md`](https://github.com/mattpocock/skills/blob/bbadaa631c37af594e5a05c9cefacd9956136b18/skills/engineering/retro/SKILL.md) and its user docs [`docs/engineering/retro.md`](https://github.com/mattpocock/skills/blob/bbadaa631c37af594e5a05c9cefacd9956136b18/docs/engineering/retro.md), both at commit `bbadaa6` (2026-11-27). The same skill ships in the `mattpocock-skills` 1.3.1 plugin.

### What it does

- **Trigger:** typed by the user only (`disable-model-invocation: true`; `allow_implicit_invocation: false` in `agents/openai.yaml`). The docs say: "You invoke this by typing `/retro`, and the agent won't reach for it on its own."
- **Input:** "the primary sources for the session the user specifies", which may mean "searching through session logs on this machine", and defaults to the current session.
- **Output:** a list of candidates "in order of severity". It "only proposes. Nothing changes until you pick a candidate". It "edits nothing by hand and installs no hook automatically."
- **Target:** the agent's *environment*, not the code: "It changes the environment, not the code."
- **Categories:**

| Category | When | Fix |
|---|---|---|
| Navigation | Finding a file or fact took long | A navigation pointer from a file the agent already reads |
| Automated checks | A tool could have caught the mistake, or the repo has no guardrail at all | Lint rule, type, test, pre-commit hook, CI job. It first reads the repo's own check command and CI, "so a check that already exists but sits unwired or silently broken is the finding" |
| Coding standards | The reviewer agent missed a mistake | A **mechanical** violation "gets a deterministic check, full stop". `CODING_STANDARDS.md` is only for "genuine judgement calls" |
| Global AGENTS.md | `AGENTS.md` is large | Move steering out into standards or checks |
| Tool economy | A tool call was expensive | Streamline the tool |
| No-ops | Steering files are large | Delete instructions that change nothing |
| Information access | The agent lacked information | Widen access, e.g. tee dev-server logs |

- **The reasoning behind it:** standards belong to the reviewer, because "the review agent has the least context pressure", while the implementer has the most. `CLAUDE.md`/`AGENTS.md` "should be used incredibly sparingly, usually only for navigation pointers". `retro` "is not a memory system. It does not store what happened. It changes the environment so the mistake cannot happen again."
- **Weaknesses its own docs admit:**
  - It "does not audit the lint rules, hooks, or CI jobs it proposed last month", so "you must still prune checks yourself".
  - It can "invent generic advice to satisfy the retro categories", so you should discard any candidate you can't trace to a moment in the session.
  - Running it after every session "mostly produces rules nobody needed".
  - It says success means "your `AGENTS.md` gets shorter over time, not longer".

### Does it match the fliesswerk learning loop?

| Aspect | fliesswerk idea | `/retro` | Match |
|---|---|---|---|
| What gets improved | Coding standards, agent docs, Feedback commands, glossary | Checks, Coding standards, `AGENTS.md`, navigation pointers, tools, information access | **Yes.** Its "Coding standards only seen by the reviewer" is exactly fliesswerk's **Coding standards** definition (`GLOSSARY.md`) |
| Order of preference | (open) | Check > reviewer rule > pointer, and never a default line in `CLAUDE.md` | A good default to adopt |
| Trigger | The Factory, AFK, after a Run | A human, by hand | **No** |
| Evidence | Review findings and CI failures across Runs | One session transcript | **Partly.** Fliesswerk has structured, cross-Run evidence that `retro` lacks |
| Output | A PR, never silently | A ranked list in chat | **No**, though "proposes only" fits "never silently" |
| Pruning | Needed against bloat | Prose only, judged from one session. Checks are not audited | **Gap** |
| Pipeline lessons | Go to maintainers as run data | "Tool economy" and "information access" mix harness and pipeline problems into project advice | Needs splitting: in fliesswerk those are mostly maintainer lessons |

**Verdict:** `/retro` gets the *taxonomy and the order* right, and fliesswerk can reuse them. It does not supply the *loop*: an automatic trigger, evidence across Runs, a PR as output, and pruning. Those have to come from fliesswerk's own Steps and run data.

## 2. How others close the loop

### Every: Compound Engineering (`ce-compound`)

Source: [EveryInc/compound-engineering-plugin](https://github.com/EveryInc/compound-engineering-plugin) at commit `ee3695a` (2026-11-26), files [`skills/ce-compound/SKILL.md`](https://github.com/EveryInc/compound-engineering-plugin/blob/ee3695a24ae1ccb205e4334b79799cb25c060247/skills/ce-compound/SKILL.md) and [`docs/guides/ce-compound.md`](https://github.com/EveryInc/compound-engineering-plugin/blob/ee3695a24ae1ccb205e4334b79799cb25c060247/docs/guides/ce-compound.md).

This is the closest match to a factory learning loop. It is the last step of brainstorm → plan → work → compound, and it can run unattended (`mode:non-interactive`, also used by its `/lfg` pipeline).

- **Entry bar (the counterfactual):** "without a record of this lesson, would a future engineer working from the current tree still be likely to repeat the mistake or redo substantial investigation?" If not, it writes nothing. "Completion, effort, and diff size do not establish eligibility."
- **Cheapest durable form, in order:**
  1. One owner and one way: reported to the code's owner, never written.
  2. A test or lint rule. It is kept "only after seeing it pass on the current tree and fail on the defect in a throwaway copy".
  3. A rule in an *existing* `CODING_STANDARDS.md`. It "never creates that file".
  4. A learning doc in `docs/solutions/`, which later `ce-plan` runs read.
- **Write boundary:** at most one test or lint rule or standard, and at most one learning, per run. "One learning per run", never batched. It never changes product code. Edits to `AGENTS.md`/`CLAUDE.md` happen only interactively with consent. Unattended runs only report "gap noted, not applied". "The commit or pull request that carries the write is its review."
- **Dedup:** it scores overlap with existing learnings on five dimensions. High overlap updates the existing doc instead of adding a new one.
- **Grounding:** a deterministic script checks cited paths, line numbers, SHAs and links against the tree before the learning is written.
- **Refresh (pruning):** Keep, Update, Consolidate, Replace or Delete, each with an evidence bar. "Age alone is not staleness." Auto-delete needs the implementation gone, the problem domain gone, and no substantive inbound citations. Each refresh also cuts claims "recoverable from a named in-repo artifact". Learnings can declare `retire_when`, the outside change that retires them. "Archive folders accumulate and pollute search", so it deletes, and git history is the archive. An unattended refresh opens a PR.
- **Unverified:** how `ce-plan` retrieves from `docs/solutions/` (the README says only that `ce-plan` "reads" it).

### OpenAI: harness engineering

Source: Ryan Lopopolo, ["Harness engineering: leveraging Codex in an agent-first world"](https://openai.com/index/harness-engineering/), OpenAI, 2026-02-11.

- One big `AGENTS.md` failed: "give Codex a map, not a 1,000-page instruction manual". The final file is about 100 lines and works as a table of contents into a structured `docs/` directory (progressive disclosure). A monolithic file "rots instantly".
- Review comments, refactor PRs and bugs become doc updates or tooling: "When documentation falls short, we promote the rule into code." Lint error messages "inject remediation instructions into agent context".
- A recurring **doc-gardening** agent "scans for stale or obsolete documentation that does not reflect the real code behavior and opens fix-up pull requests". Linters and CI check that docs are current and cross-linked.
- "Golden principles" are enforced by background tasks that open small refactor PRs, and most "can be reviewed in under a minute and automerged". Cleanup is garbage collection, paid "continuously in small increments", after a manual Friday cleanup (about 20% of the week) failed to scale.

### Cursor Bugbot: learned review rules

Source: Cursor, ["Bugbot learning"](https://cursor.com/blog/bugbot-learning), 2026-04-08.

- Signals from merged PRs: reactions to its comments, replies to them, and human review comments on issues it missed.
- These become **candidate rules**, tested against incoming PRs. A candidate is **promoted to active** as signal builds up, and an active rule is **disabled** when it "starts generating consistent negative signal". Humans can edit or delete rules.
- Reported scale: more than 110,000 repos with learning on and more than 44,000 learned rules. Resolution rate went from 52% (July 2025) to about 78%.
- **Unverified:** the promotion and disabling thresholds, which the post doesn't give. Learned rules live in Cursor's dashboard, not in the repo. A [forum request](https://forum.cursor.com/t/api-or-git-based-workflow-to-promote-bugbot-learned-rules-into-repository-rules/171018) asks for a Git workflow (not checked beyond the search snippet).

### GitHub Copilot Memory

Source: [GitHub Docs, "Copilot Memory"](https://docs.github.com/en/copilot/concepts/agents/copilot-memory).

- Copilot writes memories itself "as a byproduct of working with you". Repository facts are scoped to one repository.
- **Citations:** every repository fact cites code, and Copilot "checks the fact's citations against the current branch" before relying on it. It upvotes memories it can validate and downvotes wrong ones.
- **Expiry:** memories expire without reinforcement. "The default expiry period is 90 days" (an admin can set 7 to 180). An upvote resets the clock, but retrieval alone does not.
- Memories live in GitHub, not in the repo, so they are not reviewed as a PR.

### Claude Code memory (`CLAUDE.md`, rules, auto memory)

Source: [Claude Code docs, "How Claude remembers your project"](https://code.claude.com/docs/en/memory).

- `CLAUDE.md` files are context, not enforcement: "To block an action regardless of what Claude decides, use a PreToolUse hook instead."
- Size: "target under 200 lines per CLAUDE.md file. Longer files consume more context and reduce adherence." Imports don't reduce the cost. Path-scoped `.claude/rules/` and skills load only when relevant.
- Auto memory is written by Claude, and only the first 200 lines or 25KB of `MEMORY.md` load. It is "machine-local" and "not shared across machines or cloud environments", so it is no fit for a factory whose lessons must reach the repo.
- `/doctor prompt-audit` looks for outdated instructions, references to files that don't exist, and contradictions. It proposes edits and "nothing in your files changes until you ask". This is a Harness-specific feature.

### Devin Knowledge

Source: [Devin docs, "Knowledge"](https://docs.devin.ai/product-guides/knowledge).

- Devin auto-suggests Knowledge from chat feedback, and a human edits, accepts or dismisses it. Each item has a trigger and is retrieved when relevant, not loaded always. The docs advise items "targeted at one workflow or action".
- The page says Knowledge is being deprecated in favour of Skills.

### Ralph (snarktank/ralph)

Source: [`prompt.md`](https://github.com/snarktank/ralph/blob/6c53cb0b831ebe8739c6a003e22af14902d8b0b5/prompt.md) at commit `6c53cb0`.

- This is the naive loop. Every iteration appends "Learnings for future iterations" to `progress.txt`, consolidates "general and reusable" patterns at its top, and updates nearby `AGENTS.md` files "if you discover reusable patterns". Nothing is reviewed, deduplicated or pruned. This is the bloat path the other sources warn against.

### Research: memory, reflection and prompt optimisation

- **Reflexion** ([arXiv:2303.11366](https://arxiv.org/abs/2303.11366), Shinn et al., 2023): agents store verbal reflections on task feedback in an episodic memory and improve without weight updates (91% pass@1 on HumanEval in the paper). This is the origin of "learn from feedback in words".
- **ACE, Agentic Context Engineering** ([arXiv:2510.04618](https://arxiv.org/abs/2510.04618), [HTML](https://arxiv.org/html/2510.04618), Zhang et al., ICLR 2026):
  - It names the two failure modes of self-edited contexts: **brevity bias** (insights get summarised away) and **context collapse** ("repeated rewriting gradually erodes details"). In the paper's example, a full rewrite shrank a context from about 18,282 to 122 tokens, and accuracy fell from 66.7 to 57.1, below the 63.7 no-adaptation baseline.
  - Its fix: itemised bullets with IDs and helpful/harmful counters. An LLM proposes *deltas*, non-LLM code merges them, and embeddings remove duplicates.
  - Its warning: without reliable feedback "the constructed context can be polluted by spurious or misleading signals".
- **GEPA** ([arXiv:2507.19457](https://arxiv.org/abs/2507.19457), Agrawal et al., ICLR 2026): an optimiser that reflects on execution traces and evolves the prompts of a *fixed* program. It beats GRPO by 6% on average with up to 35x fewer rollouts. This is relevant to the maintainer side: how fliesswerk's fixed prompts could be tuned offline from run data.
- **Evaluating AGENTS.md** ([arXiv:2602.11988](https://arxiv.org/abs/2602.11988), Gloaguen et al., ETH, v3 2026-09-29):
  - "providing context files does not generally improve task success rates", although agents follow their instructions.
  - Inference cost rises by more than 20%.
  - "repository overviews … are not helpful".
  - Context files help mainly for non-standard coding practices, and changes should be "rigorously evaluated".
  - Per the arXiv HTML v1 (seen only via the search snippet, **unverified** in the current version): LLM-generated files did worse than developer-written ones.
- **Anthropic** on evals and context:
  - ["Skill authoring best practices"](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices): "Create evaluations BEFORE writing extensive documentation". Build three scenarios, measure a baseline, then write "minimal instructions".
  - ["Effective context engineering for AI agents"](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents) (2025-09-29): find "the smallest set of high-signal tokens", and keep notes outside the context window.

## 3. What keeps loops from drifting or bloating

| Mechanism | Who | What it does |
|---|---|---|
| Prefer a check over prose | `/retro`, `ce-compound`, OpenAI | A check fails loudly and can't go stale silently. Prose is only for judgement calls |
| Entry bar / counterfactual | `ce-compound` | Write only if the lesson can't be recovered from the code, tests or docs, and losing it would cause a repeat |
| Verified signal only | ACE (warning), `ce-compound` ("solved and verified"), Bugbot (merged PRs) | Noisy feedback pollutes the context |
| Recurrence before promotion | Bugbot (candidate → active) | One incident is not a rule |
| Prove a new check | `ce-compound` | It passes on the current tree and fails on the reintroduced defect |
| One lesson per change | `ce-compound` | Small, reviewable diffs. Batching breaks review |
| Update in place, dedup | `ce-compound` overlap scoring, ACE embeddings | No near-duplicate rules |
| Delta edits, not rewrites | ACE | Avoids context collapse |
| Size budget / table of contents | Claude Code (<200 lines), OpenAI (~100 lines), `/retro` (pointers only) | Agent docs stay short. Details are loaded on demand |
| Citations checked against the tree | Copilot Memory, `ce-compound` grounding script | Stale claims are caught by code, not by an LLM |
| Expiry / retirement | Copilot (90 days without reinforcement), `ce-compound` `retire_when` | Lessons have to keep earning their place |
| Disable on negative signal | Bugbot | A rule that fires on good code gets switched off |
| Periodic gardening PRs | OpenAI doc-gardening, `ce-compound` refresh | Pruning is a regular, reviewable job |
| Human gate | `/retro`, Devin, `ce-compound` (via PR), OpenAI (PR) | Nothing changes silently |
| Measure | Anthropic evals-first, AGENTS.md paper | Context changes can make things worse, so check against a baseline |

## 4. Implications for fliesswerk (input for the map, not decisions)

Mapped onto the Notes in #151 (harness-agnostic, programmatic first, low cost):

1. **Collect the evidence in code, not by an agent.** The Factory already sees structured signals: review findings per Iteration, Feedback-command failures, and CI failures after push. It can compare "CI failed while every Feedback command passed", which shows a Feedback command is missing or wired wrong. Counting how often a signal recurs across Tickets and Runs is plain run data. No transcript-reading agent is needed, which is cheaper and harness-agnostic, unlike `/retro`'s session logs.
2. **Call an agent only on recurrence,** Bugbot-style. Run one cheap agent call when a signal recurs (e.g. the same review-finding class on two or more Tickets, or a CI-only failure). Its job is to pick the cheapest form, in `/retro`'s order: Feedback command or lint check > Coding-standards rule > pointer in agent docs or a `GLOSSARY.md` term.
3. **Gate the proposal programmatically.** A new or changed check must pass on the current tree and fail on the commit that had the defect (`ce-compound`). Cited paths must exist. One lesson per change. The result lands as its own PR, separate from the Spec's PR, so it is never silent.
4. **Prune with evidence, in the same loop.** Track which Coding-standards rules and checks actually fire, using review findings that cite a rule and Feedback-command failures. Propose removing rules that never fire or that fire on code later accepted (Bugbot's disable, Copilot's expiry). Keep agent docs on a size budget. `/retro` admits it lacks this part.
5. **Split project lessons from pipeline lessons by where the fix lives.** "Tool economy" and harness-behaviour findings go to maintainers as run data. GEPA-style offline tuning of the fixed prompts against an eval set built from real Runs is a maintainer tool, consistent with ADR 0011.
