# Lifecycle stages agent-driven teams cover (as of 2026-11-27)

**Question (#158):** Go through the software development lifecycle one stage at a time: requirements, design, implementation, testing, review, security, release, and monitoring and incident feedback. For each stage, what do agent-driven teams and factories put there today? Which stages or practices have no counterpart in fliesswerk at all? fliesswerk's current pipeline runs implement (TDD), then review (fix findings), then the merge queue, then opens a PR, with the Factory running Feedback commands itself as a gate.

**Method:**
- Primary sources first. These are first-party engineering posts from OpenAI, Anthropic, Stripe, Spotify, StrongDM and Meta, vendor docs from GitHub, Sentry, Cursor, Devin, Factory, Kiro, AWS and Microsoft, and arXiv papers.
- Claims that come only from secondary write-ups, or that I couldn't open in the original, are marked **(unverified)**.
- Vendor claims about effectiveness, such as MTTR or percentages, are reported as the vendor's own numbers, not as independent results.
- "fliesswerk counterpart" was checked against `GLOSSARY.md`, `internal/verify/verify.go` and the map #151.

## Stage by stage

### 1. Requirements and intake

| Practice | Who | Source |
|---|---|---|
| Requirements are written as structured acceptance criteria (EARS), and an analysis pass looks for "requirement bugs before they become code" | Kiro | [Specs docs](https://kiro.dev/docs/specs/), [Requirements analysis](https://kiro.dev/blog/deep-spec-analysis/) |
| The agent scopes a ticket against the codebase and gives it a **confidence score** before work starts | Devin (Jira/Linear) | [SDLC integration](https://docs.devin.ai/essential-guidelines/sdlc-integration) |
| Each phase asks clarifying questions, writes a plan, and "stops at approval gates before moving forward" | AWS AI-DLC | [awslabs/aidlc-workflows](https://github.com/awslabs/aidlc-workflows), [AWS blog](https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/) |
| "Shift work": separate interactive work from fully specified work, and run only the latter end to end | StrongDM | [Techniques](https://factory.strongdm.ai/techniques) |
| Automated issue dedupe and triage when an issue is opened (read-only tools, Sonnet) | Anthropic's own repo | [claude-dedupe-issues.yml](https://github.com/anthropics/claude-code/blob/main/.github/workflows/claude-dedupe-issues.yml) |

**fliesswerk:** Writing Specs and Tickets is out of scope (#151), and `ready-for-agent` is a human label. **Nothing checks whether a Ticket is fit for AFK work before an agent spends tokens on it.** Devin's confidence score and StrongDM's "fully specified" test both put a gate at this point. This is what #163 (intake gate) is about.

### 2. Design and planning

| Practice | Who | Source |
|---|---|---|
| Complex work gets checked-in **execution plans** "with progress and decision logs". Small changes get lightweight plans | OpenAI (Codex team) | [Harness engineering](https://openai.com/index/harness-engineering/) |
| `design.md` (architecture and algorithms) sits between requirements and tasks. A design-first variant also exists | Kiro | [Spec types](https://kiro.dev/blog/specs-bugfix-and-design-first/) |
| Construction starts with a proposed logical architecture and domain model, and a human approves it before code | AWS AI-DLC | [AWS blog](https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/) (article body not retrievable, **partly unverified**) |
| Configurable stopping points: root cause, then plan, then code, then PR | Sentry Seer | [Autofix docs](https://docs.sentry.io/product/ai-in-sentry/seer/autofix/) |
| An initializer session writes a feature list (all features start "failing"), a progress file and `init.sh` before any coding session runs | Anthropic | [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) |

**fliesswerk:** It has no plan Step and no persisted plan or decision log. The map already lists "A plan Step" as not yet specified. Others give the plan two separate jobs: something checked before implementing (Seer, AI-DLC), and a durable record a later session or reviewer reads (OpenAI, Anthropic).

### 3. Implementation

| Practice | Who | Source |
|---|---|---|
| A **blueprint** mixes agentic nodes ("Implement task") with deterministic nodes ("Run configured linters") that "don't invoke an LLM at all" | Stripe Minions | [Minions part 2](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents-part-2) |
| Context comes from rule files scoped to subdirectories or file patterns, plus a small curated subset of about 500 MCP tools | Stripe | same |
| "Gene transfusion": point the agent at concrete exemplars from another codebase | StrongDM | [Techniques](https://factory.strongdm.ai/techniques) |
| One feature per session, with a commit after each one so the agent can revert | Anthropic | [Effective harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) |

**fliesswerk:** The implement Step with TDD is the counterpart, and the Factory owning deterministic gates already matches Stripe's blueprint idea. Context comes from Coding standards and project docs. Scoped, per-path context (Stripe) is a refinement, not a missing stage.

### 4. Testing and verification

| Practice | Who | Source |
|---|---|---|
| Each change runs its own bootable app instance, so the agent can "reproduce bugs, validate fixes, and reason about UI behavior directly". Logs, metrics and traces come from a local observability stack, and performance targets become checks ("startup under 800ms") | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| End-to-end testing with browser automation is required before a feature counts as passing, because Claude tends "to mark a feature as complete without proper testing" | Anthropic | [Effective harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) |
| **Holdout scenarios**: end-to-end user stories kept *outside* the codebase so agents can't fit the code to them, scored as "satisfaction" rather than pass/fail | StrongDM | [factory.strongdm.ai](https://factory.strongdm.ai/) |
| **Digital Twin Universe**: behavioral clones of third-party services (Okta, Jira, Slack…), so thousands of scenarios can run per hour | StrongDM | [Techniques](https://factory.strongdm.ai/techniques) |
| **Test-integrity rule**: "It is unacceptable to remove or edit tests". Agents may only flip a `passes` field | Anthropic | [Effective harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) |
| Properties are extracted from requirements, then property-based tests are generated and their counterexamples reported | Kiro | [Property-based testing](https://kiro.dev/blog/property-based-testing/) |
| **Mutation-guided test generation**: every generated test is guaranteed to kill a mutant that no existing test catches | Meta ACH | [arXiv 2501.12862](https://arxiv.org/abs/2501.12862), [Meta engineering](https://engineering.fb.com/2025/02/05/security/revolutionizing-software-testing-llm-powered-bug-catchers-meta-ach/) |
| Verifiers run deterministically, are hidden from the agent ("the agent doesn't know what the verification does and how"), and are filtered by regex to the relevant errors. If one fails, the PR isn't opened | Spotify Honk | [Part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3) |
| Shift left: linters that CI would run also run locally, cached so fixes take "well under a second" | Stripe | [Minions part 2](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents-part-2) |

**fliesswerk:** TDD plus Feedback commands that the Factory runs itself (`internal/verify`: "success never rests on the agent's word") match Spotify's and Stripe's deterministic verifiers. Services cover part of what the DTU does. **Missing:**
- **Acceptance checks the implementing agent can't edit.** No holdout scenarios exist, and nothing guards tests: Protected paths cover Factory files, not the project's tests or acceptance criteria.
- **Running the app end to end and collecting evidence.** This is #159.
- **Test-adequacy signals** such as mutation testing or property tests. Test strength is never measured.
- **Verifier output filtering** (Spotify): failure output is cut to the relevant lines before the agent sees it. This is a cost lever that belongs with #157.

### 5. Review

| Practice | Who | Source |
|---|---|---|
| "almost all review effort" is agent to agent, and "Humans may review pull requests, but aren't required to" | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| An **LLM judge** reads the diff plus the original prompt after all verifiers pass, and catches scope creep such as unasked refactors or disabled flaky tests. It vetoed about 25% of sessions, and the agent course-corrected in half of those. It was later removed as models improved (**removal unverified**, reported by [InfoQ](https://www.infoq.com/news/2026/03/spotify-honk-rewrite)) | Spotify | [Part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3) |
| The coding agent runs Copilot code review on its own change and fixes the findings before finishing | GitHub Copilot | [Changelog 2025-10-28](https://github.blog/changelog/2025-10-28-copilot-coding-agent-now-automatically-validates-code-security-and-quality/) |
| Findings come prioritized P0 to P3, with style, micro-optimizations and "architectural opinions" excluded | Factory | [Automated code review](https://docs.factory.ai/guides/droid-exec/code-review) |
| The review reads `REVIEW.md` and `AGENTS.md` for team-specific bug patterns. Auto-fix pushes commits to the PR | Devin Review | [Devin Review autofix](https://docs.devin.ai/use-cases/gallery/devin-review-autofix) |
| Autofix is capped at three attempts per PR "to prevent loops" | Cursor Bugbot | [Bugbot docs](https://cursor.com/docs/bugbot) |

**fliesswerk:** The review Step exists. Two practices have no explicit counterpart: an **intent or scope check against the Ticket** that is separate from code quality (Spotify's judge), and a **hard cap on fix loops** (Cursor caps at 3, Stripe at 2 CI rounds). The design itself is #155 and #164.

### 6. Security

| Practice | Who | Source |
|---|---|---|
| Before finishing the PR, the agent runs **CodeQL** on new code, checks **new dependencies against the GitHub Advisory Database** (malware, High/Critical), runs **secret scanning**, and tries to fix what they find. These checks also apply to third-party agents (Claude, Codex) since 2026-06 | GitHub Copilot | [Changelog 2025-10-28](https://github.blog/changelog/2025-10-28-copilot-coding-agent-now-automatically-validates-code-security-and-quality/), [Risks and mitigations](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/risks-and-mitigations), [Changelog 2026-06-09](https://github.blog/changelog/2026-06-09-security-validation-for-third-party-coding-agents/) |
| Secret scanning on demand before merge, from the CLI and SDK (public preview) | GitHub | [Changelog 2026-11-17](https://github.blog/changelog/2026-11-17-secret-scanning-on-demand-for-developers-and-agents/) |
| An LLM security review of the PR diff (injection, auth, data exposure, crypto) with false-positive filtering. Its own warning: "not hardened against prompt injection" | Anthropic | [claude-code-security-review](https://github.com/anthropics/claude-code-security-review) |
| A repo-wide **threat model**, then every commit scanned against it, then the exploit **validated in a sandbox** to keep false positives low, then a patch attached. Now Codex Security | OpenAI Aardvark | [Introducing Aardvark](https://openai.com/index/introducing-aardvark/) |
| The user who asked for the PR can't approve it, and workflows don't run until a write-access user approves | GitHub Copilot | [Risks and mitigations](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/risks-and-mitigations) |

**fliesswerk:** The Sandbox and Egress proxy contain the *run*. **Nothing checks the *code*.** fliesswerk has no secret scan, no dependency or advisory check on newly added dependencies, and no SAST or security review. This is the clearest gap of any stage, and the parts that matter most are programmatic: secret scanning and dependency advisories are deterministic tools, not agent work, so they fit "programmatic first" and cost almost no tokens. Which tools to use (for example gitleaks, osv-scanner, govulncheck, Semgrep) is **unverified**, because none of the factories above name the open-source tools they use. GitHub's own checks are tied to GitHub.

### 7. Integration, CI and merge

| Practice | Who | Source |
|---|---|---|
| After pushing, CI runs and the agent gets two rounds: autofixes, then one more fix, then "we send the branch back to its human operator" | Stripe | [Minions part 2](https://stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents-part-2) |
| The agent pushes follow-up commits to fix failing CI and answers reviewer comments (`@sentry`) | Sentry Seer | [Autofix docs](https://docs.sentry.io/product/ai-in-sentry/seer/autofix/) |
| "minimal blocking merge gates": agents "often squash and merge their own pull requests" | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| Neither humans nor agents-on-behalf-of-humans write or review code; scenarios decide | StrongDM | [factory.strongdm.ai](https://factory.strongdm.ai/) |
| Acting on GitHub CI checks is planned "outer loop" work, not done yet | Spotify | [Part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3) |

**fliesswerk:** The merge queue, with agent conflict resolution, and opening the PR are the counterparts. Driving the PR to green (#168) and self-merge (#169) are planned. The practice to copy is the **round cap**, together with a hand-off to a human when the cap is hit.

### 8. Release and deploy

| Practice | Who | Source |
|---|---|---|
| The Operations phase: AI manages infrastructure as code and deployments "with team oversight" | AWS AI-DLC | [AWS blog](https://aws.amazon.com/blogs/devops/ai-driven-development-life-cycle/) (**unverified**: article body not retrievable, summary via search) |
| "It ships, deploys, breaks, and gets fixed": agents detect and fix build failures | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| AI analysis layers on Argo Rollouts canaries | Community talks | [rollouts-plugin-metric-ai](https://github.com/argoproj-labs/rollouts-plugin-metric-ai) (**unverified**, not studied) |

**fliesswerk:** None. That fits the Destination, which ends at a trusted PR. I found no factory that ties release mechanics (canaries, flags) specifically to *agent-written* code, and an [arXiv survey](https://arxiv.org/abs/2508.11867) notes that progressive delivery still relies on static rules and human approval. The one cheap, in-scope piece is **release-facing text in the PR**: what changed for users and the rollback risk. That belongs with #170 and #166.

### 9. Monitoring and incident feedback

| Practice | Who | Source |
|---|---|---|
| A production error leads to root cause from telemetry, then a plan, then a PR, then CI fixes. The plan can also go to Claude, Cursor or Copilot agents | Sentry Seer | [Autofix](https://docs.sentry.io/product/ai-in-sentry/seer/autofix/), [Coding agents](https://docs.sentry.io/integrations/coding-agents/) |
| Investigates against the commit deployed at incident time, mitigates within guardrails with human approval, then **opens a GitHub issue** that a coding agent picks up | Azure SRE Agent | [Overview](https://learn.microsoft.com/en-us/azure/sre-agent/overview), [AKS walkthrough](https://techcommunity.microsoft.com/blog/appsonazureblog/autonomous-aks-incident-response-with-azure-sre-agent-from-alert-to-verified-rec/4511343) |
| A Slack alert starts a Droid session (private preview). Vendor claims 30% of incidents auto-resolved (**vendor number**) | Factory | [Factory docs](https://docs.factory.ai/web/use-cases/overview), [Factory product page](https://factory.ai/product/software-factory) |
| Alert and backlog triage | Devin | [SDLC integration](https://docs.devin.ai/essential-guidelines/sdlc-integration) |

**fliesswerk:** None, and that's right for the Factory itself. The pattern everyone converges on is **incident to Ticket to coding agent**, so the only piece in scope is accepting such a Ticket once a human marks it `ready-for-agent`. A Ticket that comes with a reproduction (a failing test or a stack trace) is easier to verify. That ties into #163.

### 10. Maintenance and continuous work (not triggered by a Ticket)

| Practice | Who | Source |
|---|---|---|
| A recurring **doc-gardening** agent opens PRs that fix stale docs. Recurring **"garbage collection"** tasks scan for drift, update quality grades, and open refactors, most "reviewed in under a minute and automerged" | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| Large-scale migrations: LLMs plus a custom toolkit, because "simple prompting is not sufficient for anything but the simplest of migrations" | Google | [arXiv 2501.06972](https://arxiv.org/abs/2501.06972), [arXiv 2504.09691](https://arxiv.org/abs/2504.09691) |
| Fleet-wide migrations as Honk's main use case | Spotify | [Part 1](https://engineering.atspotify.com/2025/11/spotifys-background-coding-agent-part-1) |
| "Semport": ongoing semantic ports between languages and frameworks | StrongDM | [Techniques](https://factory.strongdm.ai/techniques) |
| Scans history on connect, then every commit | OpenAI Aardvark | [Introducing Aardvark](https://openai.com/index/introducing-aardvark/) |

**fliesswerk:** None. Every Run starts from a Spec's ready Tickets. Scheduled housekeeping (docs drift, dead code, dependency bumps) is a whole category fliesswerk doesn't cover. It partly overlaps the Learning loop (#160), but that is reactive, not scheduled.

### 11. Knowledge and learning

| Practice | Who | Source |
|---|---|---|
| The agent writes its own `AGENTS.md`, and linters and CI keep the knowledge base cross-linked | OpenAI | [Harness engineering](https://openai.com/index/harness-engineering/) |
| The agent *suggests* new Knowledge entries and humans review them | Devin | [Release notes 2024](https://docs.devin.ai/release-notes/2024) |
| Org-level memory of decisions, docs and runbooks, plus AutoWiki | Factory | [Factory docs](https://docs.factory.ai/web/use-cases/overview) |
| Progress file and git log as the hand-off between sessions | Anthropic | [Effective harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) |
| Judge veto rates are tracked as internal metrics | Spotify | [Part 3](https://engineering.atspotify.com/2025/12/feedback-loops-background-coding-agents-part-3) |

**fliesswerk:** The Learning loop is dim on the map (#160), and cost and run data are #161. One practice has no counterpart yet: **a hand-off record across Steps and Iterations** (plan, decisions, progress) that the review Step and the PR body can read.

## Practices fliesswerk has no counterpart for

Ordered by fit with the constraints: fixed pipeline, Harness-agnostic, programmatic first, low cost, ends at a PR.

1. **Code security checks.** These are secret scanning, advisory checks on newly added dependencies, and optionally SAST. GitHub runs all three on every coding-agent PR, including third-party agents. The checks are deterministic and cost no tokens, which suits programmatic first. No other map ticket covers them.
2. **Acceptance checks the implementing agent can't change.** StrongDM's holdout scenarios, Anthropic's "unacceptable to remove or edit tests", and Spotify's judge catching disabled tests all address the same failure: an agent games its own tests. fliesswerk's Protected paths only guard Factory files. A programmatic guard (for example "tests may be added, not deleted or weakened, without a Risk reason") would be cheap.
3. **A scope and intent check against the Ticket.** Spotify's diff-plus-prompt judge is a separate, cheap check that runs after the verifiers, not general code review. It may fold into the review Step (#164) or into Risk reasons (#166).
4. **Caps on fix loops, with an explicit hand-off when they're hit.** Stripe caps at 2 CI rounds and Cursor at 3 autofix attempts. This belongs in #168 and #157.
5. **A persisted plan and decision log** that later Steps and the PR read (OpenAI, Anthropic, Kiro, Seer). This sharpens the dim "plan Step".
6. **A Ticket readiness or confidence gate** before spending tokens (Devin, StrongDM "shift work"). This is #163.
7. **Test-adequacy signals**: mutation-guided tests (Meta ACH) and property tests from requirements (Kiro). These are more expensive and probably later, possibly scaled by rigor (#167).
8. **Out of the Destination but worth naming:** scheduled maintenance jobs (doc gardening, garbage collection, migrations), release and deploy verification, and incident-to-Ticket intake. fliesswerk ends at the PR and starts at a human-ready Ticket, so these stay outside. The incident tools (Seer, Azure SRE Agent) already *produce* issues that a coding agent consumes, so the in-scope part is only intake.

## Caveats

- The AWS AI-DLC blog body couldn't be fetched, and the phase details come from search summaries and the awslabs README, which only gives counts ("5 phases and 33 stages").
- The removal of Spotify's LLM judge comes from InfoQ and QCon coverage, not Spotify's own post.
- Stripe's "2 CI rounds" is from Stripe's own Part 2 post, which has been verified.
- Factory's incident figures, Aardvark's 92%, and Azure SRE Agent's roughly 50% autonomous mitigation are vendor-reported.
- I didn't find any factory that documents a release or canary stage specific to agent-written code. That may be an absence of evidence, not of practice.
