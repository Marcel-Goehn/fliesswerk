# How Anthropic and OpenAI ship agent-written PRs at volume (as of 2026-11-27)

**Question (#152):** How do the Claude Code team at Anthropic and the Codex team at OpenAI get to very high volumes of agent-written pull requests while keeping quality? Which Steps does a change go through (planning, implementation, verification, review, CI, merge), what is automated vs. human, what gets merged without a human read, and what tooling does the reviewing?

**Method:** Read first-party sources: Anthropic's blog and Claude Code docs, OpenAI's engineering blog, alignment blog, developer docs and cookbook. Secondary coverage (InfoQ, podcasts, write-ups) was used only to find the primary posts. Anything I could only find in secondary sources is marked **unverified**. All numbers are self-reported by the companies. Nobody has audited them.

## Short answer

Both teams got to volume the same way. They moved *reading* from humans to machines, and they made being wrong cheap. They did not make agents more careful one PR at a time.

- **Anthropic (Claude Code team)** runs a layered, risk-tiered pipeline. The agent proves its work before the PR. A fresh-context self-review runs, then a multi-agent bug review whose findings are verified by other agents. A separate blocking security review and blocking CI follow. Next comes human approval scaled by a **classifier-assigned risk tier** (0, 1 or 2 humans), then a merge queue, then flagged and staged rollout with one-step rollback. **Low-risk changes merge with no human approval.** An agent's approval never fills a human slot. The stated optimization target is "the minimum amount of human review time that doesn't raise reverts or incidents."
- **OpenAI (Codex)** runs Codex review on every internal PR. On the "harness engineering" team, humans "may review pull requests, but aren't required to". Agents review each other until satisfied, and they often squash-merge their own PRs. Quality comes from the *environment*: mechanical linters and structural tests with remediation messages, repo-local docs as the system of record, per-worktree app and observability for self-verification, and background "garbage collection" agents. OpenAI's public review product stays deliberately advisory and tuned for precision.

## Anthropic: the Claude Code team

Primary source: [How the Claude Code team does code review](https://claude.dev/blog/solving-code-review-at-anthropic/) (Addy Osmani, Morgan Westlee Lunt, Qing Wang, 2026-10-15). Unless noted, everything in this section comes from that post.

### Scale and outcomes

- Claude writes about 90% of Claude Code's own code. Company-wide, "Claude authors about 80% of the code merged into our codebase today" ([Anthropic secure SDLC post](https://claude.com/blog/how-anthropic-secures-its-ai-native-software-development-lifecycle), Jason Clinton, 2026-07-21).
- Between June and September 2026, merged PRs to the Claude Code repos more than doubled, while the share of changes that needed a revert fell by over 40%.
- Monorepo changes per day went from about 2,500 (May) to more than 30,000 (September).
- PRs passing after one review-and-fix round went from 65% to 78%. PRs needing five or more rounds went from 8.9% to 1.5%. Mean reviews to pass went from 2.08 to 1.32.
- Goal: most PRs merge with **at most one human in the loop**, with no rise in reverts or incidents.

### The Steps a change goes through

| # | Step | Automated or human | Blocks merge? |
|---|---|---|---|
| 0 | **Before the PR:** the agent runs something (tests, build, screenshot against the design) and must *show the output*. A subagent reviews the diff in a fresh context. Optional skills hunt bugs, simplify the diff, and check behavior end to end. | Agent | n/a |
| 1 | **AI bug review on every PR:** several agents hunt bugs in parallel, a separate group votes on each finding, and a finding is posted only if enough agree. Style and cleanup-only findings are dropped. Survivors are tagged **Important**, **Nit** or **Pre-existing**. Repos are set to Quick, Standard or Deep. | Agent | Every finding must be answered (fixed, or thread resolved with a reason) |
| 2 | **Security review:** a separate automated pass. | Agent | Yes, while any finding is open |
| 3 | **Automated checks:** lint, types, tests on every platform, build. "A typical CLI PR may run dozens of checks." The PR template asks *how it was tested* and *how to undo it*. | CI | Yes |
| 4 | **Human approval scaled by risk:** a classifier assigns the tier. A second classifier asks whether this change needs a person regardless. | Classifier, then 0–2 humans | Yes |
| 5 | **Merge queue:** reruns all checks against main with the change applied. | CI | Yes |
| 6 | **Rollout:** behind a flag, employees first. Then the `latest` channel, with `stable` trailing. | Automated | n/a |
| 7 | **Detection and rollback:** flags turn off without a deploy, broken builds get reverted, weekly on-call. | Humans plus dashboards | n/a |

Risk tiers:

| Tier | Examples | Human approvals |
|---|---|---|
| Low | Docs, copy, internal-only features, additive changes behind a flag | **None**, once automated and security checks are green |
| Medium | Ordinary product changes; sensitive areas such as caching, auto-updater, system prompts | One |
| High | Highest-risk code plus a small set of always-two-people paths | Two |

Rules behind the tiers (quoted or close paraphrase):

- "The change sets the tier, not the author."
- Owner-listed sensitive paths and **any CI workflow file** always need a person.
- Only trusted authors (mostly the owning team) get the lighter paths.
- "An agent's approval never counts as a person's." Automated review can clear low-risk changes, but it can't fill a human slot.
- "Whoever prompted the change is on the hook for it." On agent-written PRs, the person who directed the agent is a required reviewer.
- "An approval covers one specific commit." New commits the system can't vouch for, such as an agent's push or a hand-resolved merge, need re-approval.

### Keeping the reviewer good (the feedback loop)

- **Incidents become evals.** Past revert- and incident-causing PRs form a dataset, and the reviewer runs against it daily. Every change to the reviewer's prompts, tools or model is scored against it. If the score drops, the change is dropped.
- **Noise is measured.** Each comment is classified by the author's response (fixed / wrong / intentional / didn't matter). The reviewer is also run on known-clean PRs to count false alarms. Priority: "catch real bugs first, cut review rounds second."
- **Convergence controls.** Reviews are capped at **three rounds**, after which the author is asked to reconsider the approach. One CLI PR once went through more than 50 overnight rounds. After the first few passes only the new diff is re-reviewed. The reviewer flags the bug *class* and never repeats a finding.
- **Recurring findings are fixed at the source:** in `CLAUDE.md` (short, loaded by every review), in `REVIEW.md` (written by code owners: "what failure looks like in that repo", capped nits, skip paths, file and line evidence required), or as a new lint rule or skill.
- Review agents also run nightly over existing code.
- Acknowledged gap: there are "no circuit breakers yet" that tighten approvals when revert or incident numbers move.

### What humans still own

Whether to build it at all, risky code (billing, auto-updater, CI workflows), architecture others will copy, reading the *evidence* (test output, commands, screenshots, findings), and accountability: "A person is accountable for every change that ships, whoever typed it." Code understanding comes from on-demand Claude explainers, not from reading trivial diffs line by line.

### Company-wide (security view)

From the [secure SDLC post](https://claude.com/blog/how-anthropic-secures-its-ai-native-software-development-lifecycle):

- "We tier our codebase by risk, and make deliberate decisions on what parts to automate." "Entire codebases have strict human approval processes."
- Claude-reviewed code merges, and "a risk-weighted sample is reviewed by humans." "Agents aren't merging code to production unchecked."
- "Each review agent is designed and scoped to a specific, narrow focus", "requiring the agents to write a proof that their finding is valid."
- The hard gate sits at test/CI, not in the agent's session (hooks are optional extra gates).
- New AI reviewers run in **shadow mode**: "New agents post comments for human approval until trust is earned." "Every approval is logged with the signals and reasoning behind it." A sample of automated approvals is audited.
- "More than half of all code is being merged by our internal version of Claude Tag" (Claude in Slack).

### The productized tooling

- **Code Review** ([docs](https://code.claude.com/docs/en/code-review), [launch post](https://claude.com/blog/code-review)) runs four stages: *Find* (parallel agents), *Verify* (filter false positives), *Rank* (dedupe, severity), *Post*. It reads the diff and the surrounding code **without building or running it**. It posts a check run that "doesn't conclude as a failure", and by itself it neither approves nor blocks. It focuses on correctness, not style or test coverage. Review levels are Quick, Standard and Deep. `REVIEW.md` is read from the base branch, "never from the pull request's own changes." Cost averages "$15–25" per review and about 20 minutes. Fewer than 1% of findings are marked incorrect. Large PRs (>1,000 lines) get findings 84% of the time (7.5 on average); small PRs (<50 lines) 31% of the time (0.5 on average).
- **`/code-review`** ([docs](https://code.claude.com/docs/en/code-review-local)) is a local, bundled skill that runs as a fresh-context subagent. `--fix` applies findings. Effort `low` reports only high-confidence findings. It reads `CLAUDE.md`, not `REVIEW.md`. `/code-review ultra` / `claude ultrareview` escalates to a cloud review. Neighbors: `/simplify`, `/verify`, `/security-review`.
- **Claude Ship** ([docs](https://code.claude.com/docs/en/claude-ship.md)) is the productized pipeline. It has three agents:
  - **Code Review** finds problems.
  - **PR Steward** ([docs](https://code.claude.com/docs/en/pr-steward.md)) pushes fixes for failing checks, merge conflicts, Important findings and human comments. It "is designed not to approve or merge."
  - **Claude Approvals** ([docs](https://code.claude.com/docs/en/approvals.md)) is a check driven by `.claude/approvals.yml`. Per path it sets `seats` (number of humans) and whether `claude: approves`. Conditions can read PR facts (lines changed, author, target branch) and **classifiers** ([docs](https://code.claude.com/docs/en/approvals-classifiers.md)): `claude/code_review` = pass/fail on open Important findings, `claude/diff` = trivial/same_intent/different since the last approval, and custom `repo/<name>` classifiers (a prompt plus an enum output, such as `change_risk: low|medium|high`). The policy starts in **observe mode**. The docs warn: "With `seats: 0`, a pull request that a bot wrote can merge when no person has looked at it." A condition "can't read CI results, so keep CI as its own required check." On injection: a classifier "reads text that the pull request's author wrote", so on paths that matter, ask for a person in a rule that reads no classifier.
- **Claude Code GitHub Action** ([docs](https://code.claude.com/docs/en/github-actions.md)) runs Claude in your own CI. The Claude Code team's post names it as the alternative AI reviewer.
- **Boris Cherny's personal workflow** (via [InfoQ](https://www.infoq.com/news/2026/01/claude-code-creator-workflow), from his [X thread](https://x.com/bcherny/status/2007179832300581177)): plan mode first ("A good plan is really important!"), many parallel sessions, and "give Claude a way to verify its work" (2–3x better results). Teammates tag `@claude` on PRs to add learnings to `CLAUDE.md`, and a PostToolUse hook formats code. *Unverified:* the per-day PR counts ("22 PRs yesterday") attributed to him in secondary write-ups.

## OpenAI: Codex

### Harness engineering (an internal product team building with zero hand-written code)

Primary source: [Harness engineering](https://openai.com/index/harness-engineering/) (Ryan Lopopolo, 2026-02-11).

- About 1M lines and about 1,500 merged PRs in about five months. Three engineers grew to seven, averaging **3.5 PRs per engineer per day**. Single Codex runs lasted "upwards of six hours."
- **Planning:** humans set priorities and acceptance criteria. Small changes get lightweight plans. Complex work gets versioned **execution plans** checked into `docs/exec-plans/` (active, completed, tech-debt tracker) with progress and decision logs. "Plans are treated as first-class artifacts." See also the [PLANS.md cookbook](https://developers.openai.com/cookbook/articles/codex_exec_plans).
- **Verification:** each git worktree boots its own app instance. Codex drives Chrome DevTools to reproduce bugs and validate fixes, and queries an ephemeral per-task logs, metrics and traces stack (LogQL/PromQL). It can record videos of failures and fixes.
- **Review:** Codex reviews its own changes locally, requests additional agent reviews locally and in the cloud, and loops until all agent reviewers are satisfied. "Humans may review pull requests, but aren't required to."
- **CI and merge:** minimal blocking gates. Custom linters and structural tests enforce a fixed layering (Types → Config → Repo → Service → Runtime → UI), and **their error messages include remediation instructions for the agent**. CI checks that docs are current and cross-linked. Flaky tests usually get a rerun rather than blocking. Agents often squash-merge their own PRs. Corrections are cheap and waiting is expensive. The post says this "would be irresponsible in a low-throughput environment."
- **Entropy control:** "golden principles" are encoded as mechanical rules. Background Codex tasks scan for drift, update quality grades, and open small refactor PRs, "most of which can be reviewed in under a minute and automerged". A doc-gardening agent fixes stale docs. This replaced a manual "AI slop" cleanup that took every Friday (20% of the week) and did not scale.
- `AGENTS.md` is about 100 lines and works as a table of contents. `docs/` is the system of record.

### Review at OpenAI scale

- [A Practical Approach to Verifying Code at Scale](https://openai.com/safety-alignment/scaling-code-verification/) (OpenAI Alignment, 2025-12-01):
  - Every internal PR is automatically reviewed.
  - The reviewer gets **repo access and code execution**, which made it catch more critical issues and raise fewer false alarms.
  - "We optimize for signal-to-noise first, and only then push recall."
  - It comments on 36% of PRs fully generated by Codex cloud, and 46% of those comments lead to a code change (53% for human-written PRs). Authors address a comment with a change 52.7% of the time, and over 80% of reactions are positive.
  - Review quality "drops more rapidly with thinking budget on reviewing model generated code".
  - "The reviewer is a support tool, not a replacement for careful judgment", and the post warns against treating a clean review as a guarantee.
- [Codex code review in GitHub](https://learn.chatgpt.com/docs/third-party/github.md) docs:
  - Automatic reviews, or `@codex review`.
  - In GitHub it flags only **P0/P1** issues.
  - Rules live in an AGENTS.md "Review guidelines" section, nested per directory.
  - Follow-up fixes via `@codex fix …`.
- [Custom code review rules for Codex](https://developers.openai.com/blog/custom-code-review-rules-for-codex):
  - The Codex repo keeps review rules in its own AGENTS.md, for example protecting the `rawResponseItem/*` integration surface from silent breaking renames.
  - Small, scoped rules with an explicit safe alternative beat broad instructions. "If removing a rule would not change the review, leave it out."
  - Rule-guided review recovered 98% of required custom findings vs. 58.3% for the baseline.
  - "Keep formatting and mechanical checks in CI."
  - OpenAI's weekly PR volume "has more than doubled since Q4."
- [OpenAI Forum write-up](https://forum.openai.com/home/blogs/how-openai-engineers-use-codex-to-tackle-big-projects-with-rigor-2025-12-04) (2025-12-04, talk by Romain Huet and Aaron Friel):
  - Codex reviews every internal PR.
  - More than 92% of technical staff use Codex, and they ship about 70% more merged PRs.
  - Long runs keep a continuously updated plan file, sometimes with a "watchdog" agent.
  - "Keeping humans fully accountable for every line that ships."
- [Auto-review](https://openai.com/safety-alignment/auto-review/) (OpenAI Alignment, 2026-04-30) covers sandbox-escalation approvals, not PRs:
  - A *separate* reviewer agent replaces human approval for actions outside the sandbox. It is separate because the main agent is "optimized to finish tasks".
  - The result is about 200x fewer human stops. Red-teamers could still mislead it.
  - The pattern carries over: a separate judge with a narrow mandate replaces a human gate.
- [Pragmatic Engineer with Tibo Sottiaux](https://newsletter.pragmaticengineer.com/p/building-codex-with-tibo-sottiaux) (2026-09-09): intent discussions belong *before* the code, and correctness and security review get automated. The page doesn't include the transcript's "SDLC behind Codex" section, so **the Codex team's own merge and approval rules are unverified**.
- **Unverified** (secondary only):
  - No PR goes to a human until Codex has reviewed it in a separate conversation and every comment is resolved (Render write-up of a Dominik Kundel talk).
  - The Codex product team uses three specialized AI "police" reviewers instead of one (Every article, paywalled).

## Common patterns across both

1. **Proof before the PR.** The agent must run things and show the output: tests, builds, screenshots, browser runs, observability queries. "Give Claude a way to verify its work" (Anthropic). Per-worktree app and observability stacks (OpenAI).
2. **Fresh-context self-review before the PR, then an independent reviewer on the PR.** Separation matters because the author agent is biased toward finishing (OpenAI Auto-review rationale; Anthropic fresh-context subagent).
3. **Precision over recall, with verification of findings.** Several finders plus a verify/vote step (Anthropic). P0/P1 only, with repo access and execution (OpenAI). Style is pushed to linters and CI on both sides.
4. **Mechanical rules beat prose.** Recurring findings become lint rules, structural tests, or short scoped rule files (`REVIEW.md`, AGENTS.md "Review guidelines"). Linter errors carry remediation text for the agent.
5. **Risk, not authorship, decides human involvement** (Anthropic, explicitly). Low risk merges on green automation. Sensitive paths (CI workflows, billing, auth, installers) always need a person. Approval is tied to a commit and invalidated by unvouched pushes.
6. **Cheap undo instead of exhaustive pre-merge reading.** Small PRs, merge queue, feature flags, internal-first and staged rollout, one-step revert.
7. **Bounded loops.** Review-fix rounds are capped (three at Anthropic), with diff-only re-review and no repeated findings.
8. **The reviewer is itself tested.** Incident and revert datasets as evals, false-positive measurement on clean PRs, shadow mode before a new reviewer gets authority, a human audit of a risk-weighted sample.
9. **Continuous cleanup agents.** Nightly review over old code (Anthropic). Garbage-collection and doc-gardening agents opening tiny automerged PRs (OpenAI).

## Implications for fliesswerk's pipeline

These are suggestions for the map (#151), not decisions.

- **Steps line up with today's pipeline, with gaps.** fliesswerk has implement → review (fix) → merge queue → PR. Both companies add: a *verification-evidence* Step before review (run the Feedback commands, attach output), a *separate security review* pass, a *risk classification* Step that produces Human review required plus a Risk reason, and *drive to green* on CI.
- **Human review required should be decided by the change, not the author.** That means a programmatic layer (Protected paths, CI workflow files, diff size, files touched) plus an agent classifier with an enum output (low/medium/high) and a reason. Programmatic rules must win over the classifier: the classifier can only raise risk on protected paths, never lower it, because classifiers read attacker-controllable PR text.
- **The review Step should verify findings before acting on them.** Use separate finder and verifier calls, precision first, findings tagged Important / Nit / Pre-existing, and only Important ones fed to the fixer. Nits and style go to Feedback commands (linters), not the reviewer. These are plain agent invocations the Factory orchestrates, so they stay Harness-agnostic.
- **Cap the review-fix loop at about three rounds**, re-review only the new diff after round one, and escalate to Human review required (Risk reason: "did not converge") instead of looping.
- **An agent never counts as a human approval.** The Factory may vouch for (and opt-in auto-merge) only low-risk PRs. Any agent commit after vouching (conflict resolution in the merge queue, CI fixes) invalidates the vouch and needs a re-check. This matches Anthropic's "approval covers one specific commit".
- **Cost:** Anthropic's managed review averages $15–25 per PR at Standard depth. fliesswerk should scale review depth with risk (cheap pass for low-risk diffs, deep pass only where the risk classifier says so) instead of running the most expensive review everywhere.
- **Run data as the learning loop:** record each finding's outcome (fixed / dismissed) and any later revert, so maintainers can build a regression set for the reviewer prompt, the way Anthropic's incidents become evals.

## Sources

- Anthropic, [How the Claude Code team does code review](https://claude.dev/blog/solving-code-review-at-anthropic/) (2026-10-15)
- Anthropic, [How Anthropic secures its AI-native software development lifecycle](https://claude.com/blog/how-anthropic-secures-its-ai-native-software-development-lifecycle) (2026-07-21)
- Anthropic, [Bringing Code Review to Claude Code](https://claude.com/blog/code-review) (2026-03-09)
- Claude Code docs: [Code Review](https://code.claude.com/docs/en/code-review), [/code-review](https://code.claude.com/docs/en/code-review-local), [Claude Ship](https://code.claude.com/docs/en/claude-ship.md), [PR Steward](https://code.claude.com/docs/en/pr-steward.md), [Claude Approvals](https://code.claude.com/docs/en/approvals.md), [Approvals classifiers](https://code.claude.com/docs/en/approvals-classifiers.md), [GitHub Actions](https://code.claude.com/docs/en/github-actions.md)
- InfoQ, [Boris Cherny's workflow](https://www.infoq.com/news/2026/01/claude-code-creator-workflow) (secondary, citing [his X thread](https://x.com/bcherny/status/2007179832300581177))
- OpenAI, [Harness engineering](https://openai.com/index/harness-engineering/) (2026-02-11)
- OpenAI Alignment, [A Practical Approach to Verifying Code at Scale](https://openai.com/safety-alignment/scaling-code-verification/) (2025-12-01)
- OpenAI Alignment, [Auto-review](https://openai.com/safety-alignment/auto-review/) (2026-04-30)
- OpenAI docs, [Codex code review in GitHub](https://learn.chatgpt.com/docs/third-party/github.md); [Custom code review rules for Codex](https://developers.openai.com/blog/custom-code-review-rules-for-codex); [Using PLANS.md for multi-hour problem solving](https://developers.openai.com/cookbook/articles/codex_exec_plans)
- OpenAI Forum, [How OpenAI engineers use Codex to tackle big projects with rigor](https://forum.openai.com/home/blogs/how-openai-engineers-use-codex-to-tackle-big-projects-with-rigor-2025-12-04) (2025-12-04)
- Pragmatic Engineer, [Building Codex with Tibo Sottiaux](https://newsletter.pragmaticengineer.com/p/building-codex-with-tibo-sottiaux) (2026-09-09)
