# Which Steps other software factories and coding agents run (as of 2026-11-27)

Ticket: [#154](https://github.com/Marcel-Goehn/fliesswerk/issues/154), on map [#151](https://github.com/Marcel-Goehn/fliesswerk/issues/151).

**Question:** Which Steps do other software factories and autonomous coding agents run between "task is ready" and "PR merged"? Does any of them run an **intake gate** (judging a task clear and testable before implementing, and handing it back if not), a plan Step, a verification Step, a whole-branch review or a CI-fix loop? How opinionated or configurable is each?

**Method:**
- Sources are official docs, changelogs, repos and first-party engineering blogs. Each was read with a web fetch on 2026-11-27.
- Sandcastle comes from the earlier source read in `docs/research/sandcastle-templates.md` on branch `research/sandcastle-templates` (v0.13.0).
- Nothing was run hands-on.
- **Unverified** marks claims taken from a search-engine excerpt or secondary coverage because the primary page could not be fetched.

## Answer

- **Almost every system runs implement → deterministic checks → agent review → PR → CI fix. Only a few run an intake gate.**
  - Devin rates each task 🟢/🟡/🔴 and posts open questions *before* it implements. In bulk scoping, 🔴 tickets are "best assigned to a human engineer". Cognition reports that 🟢 tasks end in a merged PR about twice as often as 🔴.
  - Jules asks for feedback when a prompt is unclear. Kiro moves a task to "Needs attention", and Factory Missions push back during planning.
  - Symphony, OpenAI's ticket-driven factory, explicitly has **no** hand-back. It says to "proceed with the safest flow" and to stop only for missing tools, auth or secrets.
- **Plans are common, but in AFK systems nobody checks them.** Symphony, Open SWE, Jules, Kiro, Copilot, Devin, Factory Missions and Gas Town all plan. Plans go to a human for approval, with one exception: Jules added a **Planning Critic**, a second agent that reviews auto-approved plans, and reports **9.5% fewer task failures**.
- **Deterministic verification before review is universal among in-house factories.**
  - Stripe loops lint locally (<5 s) before pushing.
  - Spotify's "verifiers" switch on automatically from repo contents (e.g. `pom.xml`) and sit behind one MCP tool. They return only the relevant error lines. If a verifier fails, no PR is opened.
  - Copilot runs tests, linters, CodeQL, the advisory-database dependency check, secret scanning and its own code review before it hands over.
- **CI-fix loops are standard, and the good ones are bounded.**
  - Stripe allows **at most two CI rounds** and applies known autofixes deterministically before an agent sees a failure.
  - Cursor's Bugbot Autofix allows at most 3 attempts per PR.
  - Jules, Copilot Agent Merge, Devin Auto-fix, OpenHands `iterate`, Open SWE/Robin `/baby-sit` and Kiro `prepare-pr` all loop until green. Kiro adds the rule that "every expected lane ran and every lane passed".
- **Review is moving from one reviewer to several narrow reviewers whose findings are checked.**
  - Claude Code Review runs Find → **Verify** (drop false positives) → Rank → Post.
  - Anthropic's internal review agents must "write a proof that their finding is valid".
  - Kiro uses reviewers from **different model families** and requires every blocking finding to carry a location, quoted evidence, a trigger and a consequence.
  - Spotify's LLM judge compares the diff with the original prompt to catch scope creep, and vetoes about 25% of sessions.
- **Trusting a PR without a human read is a risk-routing gate, and the strongest designs keep that gate in code.**
  - Rewind: "The judgment is AI; the gate is not a vibe". A unit-tested verdict engine turns LLM findings into LOW/MEDIUM/HIGH. Protected-path rules in a CODEOWNERS-protected YAML file always force NEEDS_HUMAN.
  - Vercel (**unverified**): ≥100 files or CODEOWNERS paths are always high risk. 58% of PRs merge without human review.
  - OpenHands' review gives each PR a risk rating, and high-risk PRs go to a human instead of auto-merging.
  - Kiro auto-merges "simple-tier" PRs.
  - Intercom auto-approves >19% of PRs and refuses changes that are "too big, too complex, or too broad".
  - PagerDuty classifies the ticket *and plan* before implementation, then the diff after, and earns auto-approval per category from approval data. It isn't live yet.
- **Whole-branch review is rare as a named Step.**
  - Gas Town's Refinery runs verification gates on the *merged stack* of a batch and bisects on red, Bors-style.
  - Sandcastle's merge agent runs typecheck and tests after merging all branches.
  - Everyone else reviews at PR level, which is the whole branch only because they make one PR per task.
- **Vendors ship fixed pipelines with a few toggles, and frameworks ship configurable graphs.**
  - Vendors: Kiro autonomous mode has only an on/off switch. Copilot lets admins toggle validation tools. Claude Code Review offers Quick/Standard/Deep.
  - Frameworks: Fabro (DOT graphs), Gas Town (TOML formulas), Symphony (`WORKFLOW.md`) and Sandcastle (copied TypeScript).
  - Internal factories (Stripe, Spotify, Ramp, Kiro, Anthropic) are opinionated per company, with the pipeline owned by a platform team. That is the closest model to fliesswerk's.

## Comparison

Legend: **Y** = yes, a Step or feature; **~** = partial or human-driven; **–** = none found in the sources; **?** = not documented.

| System | Intake gate | Plan | Implement | Verify before PR | Agent review | Whole-branch review | CI-fix loop | Auto-merge / risk routing | Opinionated vs configurable |
|---|---|---|---|---|---|---|---|---|---|
| **Stripe Minions** (internal) | – (deterministic context hydration only) | ? | Y (agentic node) | Y deterministic lint loop; selective tests in CI | – (human) | – | **Y, max 2 CI rounds**; known autofixes applied by code | – (human-reviewed) | Opinionated: "blueprints" in code mix deterministic and agentic nodes |
| **Spotify Honk** (internal) | – | ? | Y | **Y** auto-selected verifiers via stop hook; LLM judge on diff vs prompt | Y (judge, ~25% veto) | – | – (planned as "outer loop") | – | Opinionated; verifiers hidden from the agent |
| **Ramp Inspect** (internal) | – | ~ (child sessions split work) | Y | Y tests, telemetry, screenshots, live previews | – | – | ? | – (PR opened with the user's token so they can't self-approve) | Opinionated |
| **OpenAI Symphony** | **– (explicitly proceeds)** | Y workpad plan + acceptance criteria + validation | Y | Y ticket's validation must pass before every push | ~ (PR feedback sweep of review comments) | – | Y (checks must be green) | – (Human Review state; Rework = full reset; `land` skill merges) | Configurable via `WORKFLOW.md` |
| **Sandcastle** | – | ~ (LLM planner picks Tickets, dependency graph) | Y | ~ (prompted typecheck/test) | Y one pass, commits fixes | ~ (merge agent tests after merging) | – | – | Fully configurable: templates are user-owned code |
| **Gas Town** | – | Y (Mayor decomposes) | Y (polecats) | ? | ? | **Y** Refinery gates the merged batch, bisects on red | ~ (failed MRs fixed or re-dispatched) | Y merges to main when green | Configurable TOML formulas |
| **Open SWE** | – | Y Planner, **human approval by default** (skippable) | Y | Y Reviewer runs tests and formatters | Y loops back to Programmer | – | Y `/baby-sit` (current README) | ~ ("expedited Slack review for small PRs") | Configurable (prompts, middleware, models) |
| **OpenHands** | – | ? | Y | Y critic model scores the run, QA agent runs the software | Y review skill with risk rating | – | **Y `iterate`**: draft PR until green, then ready | **Y** high-risk flagged for human, others may auto-merge | Configurable via skills files |
| **GitHub Copilot coding agent** | – | Y ("research, plan") | Y | **Y** tests, linters, CodeQL, dependency check, secret scanning | Y Copilot code review on its own diff | – | **Y Agent Merge** (CI + conflicts) | Y combinable with GitHub auto-merge | Opinionated; admins toggle validation tools |
| **Devin** | **Y** 🟢🟡🔴 confidence, open questions, waits when unsure | Y | Y | ? | Y Devin Review + Auto-fix findings | – | ~ (Auto-fix CI on non-Devin PRs) | – | Mostly fixed, a few settings |
| **Google Jules** | ~ (asks when prompt unclear) | Y human approval **or Planning Critic** | Y | ? | **Y critic** after patch, before submit | – | **Y CI Fixer** | – | Opinionated; setup script + AGENTS.md |
| **Factory.ai** | ~ (Missions planning pushes back, asks) | Y features + milestones | Y workers | **Y** validator workers per milestone; QA against running app | Y code review (severity threshold, `fix_mode`) | ~ (milestone validation spans features) | ? | ~ (`fail_on_severity` gate) | Configurable; review config read from base commit |
| **Cursor Bugbot** | – | – | – | – | Y | – | Y autofix, max 3 attempts | – | Configurable rules; config from base branch; learns rules |
| **Kiro** (product + own factory) | ~ ("Needs attention"; requests classified simple/medium/complicated) | Y requirements + acceptance criteria | Y sub-agents | **Y** test pod with screenshots/video; fast gate before slow | Y design, root-cause and UX reviewers from different model families | – | **Y `prepare-pr`** until PR Readiness green | **Y simple tier auto-merged** | Product: on/off only. Factory: opinionated |
| **Claude Code Review / Anthropic SDLC** | – | ~ (design security review) | – | – | **Y** Find → Verify → Rank → Post; proof required | – | Y ("first responder for CI/CD failures") | ~ risk-tiered codebase; humans sample auto-approvals; new reviewers in shadow mode | Review levels Quick/Standard/Deep; `REVIEW.md` |
| **Augment Cosmos** | – | – | – | **Y** Verifier: evidence, "never posts a pass-or-fail" | Y Deep Reviewer + Pair Reviewer | – | Y PR Fixer (findings, CI, conflicts) | ~ Risk Analyzer + `cosmos approve` policy; "None of these experts merges a PR" | Opinionated, internal |
| **StrongDM factory** | – | ? | Y | **Y** holdout scenarios + digital twins, "satisfaction" metric | – (no human review by rule) | ? | ? | Y (no human review) | Opinionated manifesto |
| **Fabro** | ~ (interview nodes) | ~ (example graph) | Y | Y user-added deterministic + LLM-judge nodes, fix loops | ~ | – | ~ | ~ human gates | Fully configurable DOT graph |
| **Rewind / Intercom / Vercel / PagerDuty** (approval layers) | ~ (Intercom: too-big changes must be split; PagerDuty: ticket+plan classifier) | – | – | – | Y specialist councils / sub-agents | – | – | **Y** deterministic gates over LLM findings; protected paths always human | Opinionated policy files |

## Detail per system

### Internal factories

**Stripe Minions.** Runs start from Slack, docs, feature-flag tools or tickets. CI opens tickets for flaky tests with a one-click fix button. Before the run, Stripe "deterministically run[s] relevant MCP tools over likely-looking links" to load context. Runs happen on a pre-warmed devbox, ready in 10 s.

Blueprints are workflows in code whose nodes are either deterministic ("Run configured linters", "Push changes") or agentic ("Implement task", "Fix CI failures"). The flow runs in this order:
1. Implement.
2. Lint locally in a loop.
3. Push.
4. CI round 1, where failing tests that have known autofixes are fixed automatically.
5. An agent fixes the remaining failures locally.
6. Push and CI round 2.
7. The branch goes back to the human operator.

Stripe's rule is "often one, at most two, CI runs—and only after we've fixed everything we can locally". It calls more rounds a trade-off with diminishing returns. Over 1,000 minion PRs merge each week, all human-reviewed. Sources: [part 1](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents), [part 2](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents-part-2).

**Spotify (background coding agent).** Verifiers for formatting, build and test switch on from repo contents, and the agent "doesn't know what the verification does and how". Verifiers extract only the relevant errors with regexes, and on success they return "a very short success message".

After the verifiers, an LLM judge sees the diff and the original prompt. It vetoes "about a quarter" of sessions, mostly for going beyond the prompt, such as refactoring or disabling flaky tests, and the agent recovers about half the time.

Verifiers run in Claude Code's stop hook. If one fails, no PR is opened. Spotify ranks "a PR that passes CI but is functionally wrong" as the most serious failure. Source: [Spotify Engineering, part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3).

**Ramp Inspect.** Inspect "closes the loop on verifying its work". For backend changes that means tests, telemetry and feature-flag queries. For frontend changes it means screenshots and live previews. PRs are opened with the user's own token so users can't approve their own changes: "You do not want to knowingly create a vector for unreviewed code". No plan or CI Step is described. Source: [builders.ramp.com](https://builders.ramp.com/post/why-we-built-our-background-agent).

**Kiro's own factory (1,000 PRs/week).**
- **Intake:** Requests are classified up front. A simple change is built directly, a medium one "needs a one-page design" and a complicated one needs "a review by the whole team".
- **Gates and verification:** A fast gate runs cheap checks before slow ones. A test pod attaches screenshots and video to the PR. Review rules load from main: "A PR never writes its own judge".
- **Review:** Separate design, first-principles and UX reviewers run on models from different families. A blocking finding needs a location, quoted evidence, a trigger and a consequence. Without them it is advisory, and nits become follow-up issues.
- **CI fix:** The `prepare-pr` skill loops until the PR Readiness verdict is green, because "'Nothing failed' is not 'everything ran'".
- **Merge:** Simple-tier PRs are "approved and merged automatically".

Source: [kiro.dev blog](https://kiro.dev/blog/software-factory-stability/).

**Anthropic.**
- **Plan:** An Opus-based security review checks design documents.
- **Implement:** Claude runs `/security-review` before it opens a PR.
- **Review:** Narrow review agents must "write a proof that their finding is valid". The codebase is tiered by risk.
- **Human oversight:** Humans review a risk-weighted sample of automated approvals. New reviewers run in shadow mode "until trust is earned".
- **CI:** Claude is the "first responder for CI/CD failures".

Sources: [SDLC blog](https://claude.com/blog/how-anthropic-secures-its-ai-native-software-development-lifecycle), [Code Review docs](https://code.claude.com/docs/en/code-review). Code Review itself is read-only, never builds the code and never blocks a PR. It reads `REVIEW.md` from the base branch, "never from the pull request's own changes".

**Augment Cosmos.** Cosmos splits the work across named roles:
- **Risk Analyzer:** classifies the change and applies the approval policy.
- **Deep Reviewer:** checks for correctness defects.
- **Pair Reviewer:** reconstructs intent and flags judgment calls for a human.
- **PR Fixer:** handles review findings, CI failures and conflicts.
- **Verifier:** deploys the change to an isolated instance, drives it end to end and posts evidence, including a Playwright trace. It "never posts a pass-or-fail result".

`cosmos approve` is off by default. It checks CODEOWNERS ownership, review status of the current head and whether the Verifier reported an unaddressed defect. "None of these experts merges a PR." Sources: [PR-to-merge loop](https://www.augmentcode.com/blog/optimizing-pr-to-merge-loop), [Verifier](https://augmentcode.com/blog/the-bottleneck-moved-to-verification-so-we-automated-that-too).

**StrongDM.** The factory's rules are that code "must not be written by humans" and "must not be reviewed by humans". End-to-end "scenarios" are stored outside the codebase as a holdout set so agents can't game them. Scenarios run against digital twins of Okta, Jira, Slack and others. Success is a probabilistic "satisfaction" score, not a green test suite. No Step list is given. Source: [factory.strongdm.ai](https://factory.strongdm.ai/).

### Approval and risk-routing layers

**Rewind.**
- **Judgment:** A council of specialist AI reviewers produces findings, each with a severity and a confidence.
- **Gate:** A verdict engine of "ordinary, unit-tested code, not a model" turns them into LOW/MEDIUM/HIGH and APPROVE, REQUEST_CHANGES or NEEDS_HUMAN.
- **Hard rules:** A CODEOWNERS-protected YAML file sends auth, authz, secrets, encryption and approval-rule changes to a human every time.
- **Other limits:** Diffs over about 10k lines skip the AI entirely. Non-human-authored PRs need a human to merge.

Source: [rewind.com](https://rewind.com/blog/ai-approve-pull-requests-safely/).

**Intercom.** Over 93% of PRs are agent-driven and over 19% are auto-approved. The review agent flags a change that is "too big, too complex, or too broad in scope" and requires it to be split. Separate sub-agents cover problem-description quality, diff-to-intent alignment, safety, correctness and best practices. AI-approved PRs revert at 0.53% (backend) and 0.22% (frontend), against 5.39% and 2.00% for human-authored PRs. Source: [intercom.com](https://intercom.com/blog/ai-is-approving-our-pull-requests-heres-how-we-made-it-safe).

**Vercel (unverified: search excerpt only, the page returned 404 to the fetcher).** 58% of PRs in its largest monorepo merge without human review. Two hard rules bypass the LLM: ≥100 files means high risk, and CODEOWNERS paths always need a human. High-risk PRs get a warning and need human approval. Source: [vercel.com](https://vercel.com/blog/58-percent-of-prs-in-our-largest-monorepo-merge-without-human-review).

**PagerDuty.** PagerDuty sorts work into 10 categories and 5 impact tiers. A classifier reads the **ticket and the agent's proposed plan** before implementation, and a second one reads the diff afterwards. A category becomes eligible for auto-approval only after its "changeless approval rate" stays above a threshold. "This system is not yet making merge decisions." Source: [pagerduty.com](https://www.pagerduty.com/eng/before-ai-ships-code-show-me-the-receipts/).

### Vendor agents

**GitHub Copilot coding agent.** It researches, plans and implements in a sandbox and runs tests and linters. Before it finishes, it also runs CodeQL, the GitHub Advisory Database check for new dependencies, secret scanning and Copilot code review on its own changes, then fixes what they find. All of these are on by default, and admins can toggle them. **Agent Merge** ("Watch and fix") fixes failing Actions workflows and/or merge conflicts, and it can be combined with GitHub auto-merge. Sources:
- [About coding agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent)
- [validation tools](https://github.blog/changelog/2026-03-18-configure-copilot-coding-agents-validation-tools/)
- [security validation](https://github.blog/changelog/2025-10-28-copilot-coding-agent-now-automatically-validates-code-security-and-quality/)
- [Agent Merge](https://github.blog/changelog/2026-10-28-agent-merge-watches-and-fixes-pull-requests-in-the-cloud) (from the changelog excerpt)

**Devin.** Devin reports 🟢🟡🔴 confidence at the start of a session, after planning and when answering code questions. It "will wait for user approval when it is unsure about its plan", and 🟢 gives "twice the likelihood of a merged PR compared to 🔴".

Bulk scoping posts, per Linear ticket, a confidence score, the relevant files, a plan and open questions. 🟡 tickets get answers and are re-evaluated. 🔴 tickets are "best assigned to a human engineer". A Linear automation can label tickets by score and start implementation only for 🟢 ones.

Devin Review can auto-fix findings on Devin-opened PRs. For other PRs it can take over to fix bugs and failing required checks. Sources: [Devin 2.1](https://cognition.com/blog/devin-2-1), [bulk scoping](https://docs.devin.ai/use-cases/gallery/bulk-plan-backlog.md), [Devin Review](https://docs.devin.ai/work-with-devin/devin-review).

**Google Jules.** Jules generates a plan, which a human approves. If the prompt is unclear, it asks before coding. A **Planning Critic** reviews "all plans that do not require human intervention", which cut task failures by 9.5%.

After the patch is generated and before submission, a **critic** performs an LLM-as-judge review without a reference implementation and loops until clean. The **CI Fixer** watches PRs Jules created and keeps "fixing, committing, and resubmitting". Sources:
- [running tasks](https://jules.google/docs/running-tasks/)
- [Planning Critic](https://jules.google/docs/changelog/2026-01-26-1)
- [critic](https://developers.googleblog.com/en/meet-jules-sharpest-critic-and-most-valuable-ally/)
- [CI Fixer](https://jules.google/docs/changelog/2026-02-19)

**Factory.ai.**
- **Missions:** Droid "will push back, ask questions, and iterate with you until the plan is solid", then plans features in milestones. Validator workers run at the end of each milestone, two runs per milestone, and do QA against the running app. The cost floor is "≈ #features + 2 × #milestones" runs.
- **Code review:** Focuses on clear bugs and skips style. It has `review_severity_threshold` and `fail_on_severity`, and `fix_mode` is commit, PR or off. `.factory/review.yaml` is read from the base commit.

Sources: [missions overview](https://docs.factory.com/missions/overview.md), [planning](https://docs.factory.com/missions/planning.md), [code review CI](https://docs.factory.com/automations/code-review-ci.md).

**Cursor Bugbot.** Reviews every PR update. Autofix starts a cloud agent that commits fixes to the branch, with at most 3 attempts per PR. Config is read from the base branch "so a PR can't change its own review settings". Bugbot learns rules from merged and closed PRs. Source: [cursor.com/docs/bugbot](https://cursor.com/docs/bugbot).

**Kiro autonomous mode.** The agent writes a plan with requirements and acceptance criteria, then sub-agents verify their changes before the work moves on. When stuck, the task goes to "Needs attention". The agent opens PRs and addresses comments via `/kiro fix` and `/kiro all`. The only control is an on/off toggle, and the model is chosen automatically. Source: [kiro.dev docs](https://kiro.dev/docs/web/autonomous-mode/).

### Open-source factories and frameworks

**OpenAI Symphony.** The Linear-driven flow runs:
1. Todo → In Progress.
2. A single workpad comment holds a hierarchical plan with acceptance criteria and validation, plus a reproduction signal recorded before any edit.
3. Implement and commit.
4. The ticket's Validation/Test Plan is mandatory before every push.
5. Push and open the PR. Checks must be green.
6. PR feedback sweep: every actionable comment is fixed or answered with justified pushback.
7. Human Review.
8. On changes requested, Rework: a **full reset** that closes the PR and starts a fresh branch.
9. The `land` skill merges.

There is no hand-back for unclear tickets. Sources: [repo](https://github.com/openai/symphony), [WORKFLOW.md](https://github.com/openai/symphony/blob/main/elixir/WORKFLOW.md).

**Open SWE.** Manager → Planner (plan approval by default, "if you're feeling bold … you can skip") → Programmer ⇄ Reviewer. The Reviewer "runs tests and formatters … before ever opening a PR", and the loop continues "until the code is perfect", with no stated cap. The current README describes a successor flow: plan and investigate, focused validation, opt-in automatic reviews that never push, and `/baby-sit` to watch CI and rerun only flaky jobs. Sources: [announcement](https://www.langchain.com/blog/introducing-open-swe-an-open-source-asynchronous-coding-agent), [README](https://github.com/langchain-ai/open-swe).

**OpenHands.**
- **Resolver:** The older resolver ran from a `fix-me` label and opened a draft PR on success or pushed a branch on failure. That comes from older package docs and is **unverified for current versions**.
- **Verification stack:** (1) A critic model scores the run while it works and stops or retries broken attempts. (2) On PR, a review skill works through a 10-scenario checklist and ends with a risk rating and verdict, and "high-risk PRs are flagged for human architect review rather than auto-merged". A QA agent runs the software. (3) An `iterate` skill opens a draft first "so auto-merge and deploy don't fire early", loops on CI and review comments, and marks the PR ready when green.

Sources: [verification stack](https://www.openhands.dev/blog/20260506-the-verification-stack), [resolver on PyPI](https://pypi.org/project/openhands-resolver/0.2.6).

**Sandcastle.** It has three shapes: a loop, implement → review per Ticket, and plan → implement in parallel → (review) → merge. In the plan shape an LLM picks the Tickets and an LLM merges and closes them. Review is a single pass that may commit fixes. There is no CI loop, and the limits are hard-coded constants. The templates are user-owned copied code. Source: `docs/research/sandcastle-templates.md` (Sandcastle v0.13.0).

**Gas Town.** The Mayor decomposes work into beads. Polecats implement and run `gt done`, which pushes and creates an MR bead. The Witness watches for stuck agents. The Refinery is a "Bors-style bisecting queue": it runs verification gates on the merged batch, merges all of it if green and bisects to isolate the failing MR if red. Workflows are customizable TOML formulas. Source: [README](https://github.com/gastownhall/gastown).

**Fabro.** Workflows are user-written Graphviz DOT graphs with human-gate nodes, for example plan → approve/revise → implement → simplify. Deterministic verifications and LLM-as-judge nodes can be layered in, and "failures trigger fix loops automatically". A CSS-like model stylesheet routes cheap models to cheap nodes. Every stage commits to git. Source: [README](https://github.com/fabro-sh/fabro).

**Intake for issues filed by humans.** Gemini CLI's own repo triages new issues with Gemini and labels them `status/need-information` when logs or repro steps are missing. GitHub has an AI-powered issue intake action that may ask for more info or mark an issue actionable. Sources: [geminicli.com](https://geminicli.com/docs/issue-and-pr-automation/), [GitHub docs](https://docs.github.com/en/enterprise-cloud@latest/issues/tracking-your-work-with-issues/administering-issues/triaging-an-issue-with-ai).

**Harnesses (Aider, SWE-agent, Claude Code, Codex CLI, opencode, goose).** These are single agents with no pipeline between "ready" and "merged". They are Step workers, not factories. Codex cloud's page describes environment, task, review and PR, with no automatic CI or review Step ([learn.chatgpt.com/docs/cloud](https://learn.chatgpt.com/docs/cloud)).

## Implications for fliesswerk's pipeline (input to the map, not decisions)

1. **An intake gate has precedent and a measured payoff.**
   - Devin gets about 2× the merge rate on 🟢 tasks. Intercom refuses over-broad changes. PagerDuty classifies ticket and plan for risk *before* implementing. Symphony, the system closest to fliesswerk, has no gate.
   - A Factory intake Step could return "clear / needs-info (with open questions) / too big" and hand the Ticket back. Its output could also seed the Risk reason early.
2. **Deterministic checks belong before any LLM review, behind one interface.**
   - Spotify's pattern fits "programmatic first": Feedback commands auto-selected and run by code, short output, no PR if they fail.
   - Stripe's local lint loop and its known-autofix-before-agent rule save CI rounds.
3. **Bound the CI-fix loop, and define green as "everything expected ran".**
   - Stripe allows 2 rounds and Bugbot 3. Kiro's "every expected lane ran" guards against vacuous green.
   - OpenHands keeps the PR a draft until green, which fits "drive it to green before In review".
4. **Make review findings evidence-based, verified and independent of the author.**
   - Use a different model family or at least a fresh context. Require location and evidence for blocking findings, as Kiro and Anthropic do. Verify findings before acting (Claude Code Review). Check diff-vs-Ticket scope creep (Spotify judge, Intercom intent alignment).
   - Load review rules from the base branch, never from the PR.
5. **"Human review required" with a Risk reason works best as code deciding over LLM findings.**
   - Rewind's verdict engine and Vercel's hard rules map directly onto Protected paths plus size thresholds forcing Human review required.
   - LLM findings can only *raise* risk. OpenHands, Kiro and Augment all route high-risk PRs to humans.
   - Anthropic's shadow mode and PagerDuty's per-category approval rates suggest logging run data to calibrate the gate later.
6. **Plan Step:** ubiquitous, but in AFK systems it is checked by a critic (Jules, −9.5% failures) or by nobody. A cheap critic over the plan against the Ticket's acceptance criteria is the only documented AFK check.
7. **Whole-branch review:** for a Spec branch carrying many Tickets, the only analogues are Gas Town's merged-batch verification gate and Sandcastle's post-merge tests. Nobody documents an LLM review of the whole multi-task branch, so this would be new ground for fliesswerk.
8. **Opinionated plus facts is the internal-factory norm.** Stripe, Spotify, Kiro and Anthropic fix the pipeline and let repos supply facts: lint and test commands, `REVIEW.md`/BUGBOT.md-style guidance, protected paths. This is consistent with the map's ADR 0011 stance.
