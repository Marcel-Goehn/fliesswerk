# What makes AI code review catch real bugs (as of 2026-11-27)

**Question** ([#155](https://github.com/Marcel-Goehn/fliesswerk/issues/155)): What makes AI code review catch real bugs rather than produce noise? This covers one reviewer vs. several, review axes, fresh vs. shared context, adversarial or verifying reviewers, deduplication and filtering, and review-then-fix loops.

**Method:**
- Read vendor docs and engineering blogs: Anthropic Code Review, `/code-review` and ultrareview, plus the open-source `code-review` plugin's command file. Also Cursor Bugbot, OpenAI Codex review, Greptile, CodeRabbit, Graphite and GitHub Copilot's ReviewBench.
- Read published benchmarks (Martian Code Review Bench, ReviewBench, CR-Bench-v2, CRBugBench, AACR-Bench) and empirical and method papers from arXiv.
- Nothing was tested hands-on.
- **Vendor claim** marks a number a vendor reports about its own product. **Independent** marks a third-party benchmark or a paper whose authors don't sell the tool. **Unverified** marks something I saw only in secondary write-ups or search snippets and couldn't confirm at the primary source.
- Most papers were read at abstract level. Where I read the full HTML, that's noted.

## Short answer

What separates signal from noise is one structure that every serious reviewer converges on:

1. **Find**: broad candidate generation, often in parallel. The reviewer has repo access and tools, not just the diff.
2. **Verify**: a separate step tries to confirm or falsify each candidate against the code, ideally by running something.
3. **Filter, dedupe and rank**: an explicit exclusion list, merging of duplicates, and severity ranking.
4. **Report**: only high-confidence, concrete, located findings.

Behind that structure, everyone is **precision-first** and defaults to **correctness**, with style left to linters and to explicitly quoted rules.

Even so, the best reviewers still miss a large share of real bugs: about 1/3 to 3/4 of known defects are caught, depending on the benchmark. Self-review by the authoring agent is close to chance. Review alone can't vouch for a PR. Independent, executable evidence (tests, running the code) is what makes a verdict trustworthy.

## 1. How the leading tools are built

### Anthropic: Code Review (managed), `/code-review` (local), ultrareview (cloud)

- **Managed Code Review.** It runs four stages: "Find: several agents examine the diff and the surrounding code in parallel. Verify: a verification step checks the candidate findings against the code and filters out false positives. Rank: the findings that remain are deduplicated and ranked by severity. Post." By default it "focuses on correctness: bugs that would break production, not formatting preferences or missing test coverage." It reads code and doesn't build or run it. It offers three review levels (Quick, Standard, Deep). ([docs](https://code.claude.com/docs/en/code-review))
- **Depth scales with PR size**: "Large or complex changes get more agents and a deeper read, while trivial ones get a lightweight pass." ([blog](https://claude.com/blog/code-review))
- **Vendor claims** (Anthropic internal, same blog):
  - PRs with substantive review comments rose from 16% to 54%.
  - Large PRs (over 1,000 lines) get findings 84% of the time, averaging 7.5 issues. Small PRs (under 50 lines) get findings 31% of the time, averaging 0.5.
  - "less than 1% of findings are marked incorrect."
  - A review costs $15–25 and takes about 20 minutes.
- **Local `/code-review`** runs as a forked subagent "with its own context window". `--fix` applies findings. Effort trades coverage for confidence: "At `low`, the review reports the findings it's most confident in". The report is capped at 32 findings by default. ([docs](https://code.claude.com/docs/en/code-review-local))
- **Ultrareview** runs a "fleet of reviewer agents" in a cloud sandbox. "every reported finding is independently reproduced and verified". It costs $5–25 and takes 5–15 minutes. ([docs](https://code.claude.com/docs/en/ultrareview))
- **The open-source `code-review` plugin** (`plugins/code-review/commands/code-review.md` in `anthropics/claude-plugins-official`, read locally) is the most concrete public recipe:
  - A cheap model gates first: skip closed, draft or trivial PRs, and PRs already reviewed.
  - A cheap model collects the relevant CLAUDE.md paths.
  - **Four parallel reviewers.** Two Sonnet agents check CLAUDE.md compliance. Two Opus agents look for bugs: one looks only at the diff, one at problems in the introduced code, including security. Each one gets the PR title and description so it knows the intent.
  - **Flag only** issues where the code won't compile or parse, will "definitely produce wrong results regardless of inputs", or makes "clear, unambiguous CLAUDE.md violations where you can quote the exact rule being broken."
  - **Don't flag** style, "potential issues that depend on specific inputs or state", subjective suggestions, pre-existing issues, anything a linter catches, general quality concerns unless CLAUDE.md requires them, or issues silenced in code.
  - **One validator subagent per issue**, in parallel. It gets only the PR description and the issue, and has to confirm the issue "with high confidence". Issues it doesn't validate are dropped. Bugs go to Opus validators and CLAUDE.md issues to Sonnet.
  - "Only post ONE comment per unique issue."

### Cursor Bugbot ([building Bugbot](https://cursor.com/blog/building-bugbot), [Autofix](https://cursor.com/blog/bugbot-autofix))

- **v1 pipeline:**
  - Eight parallel bug-finding passes, each seeing the diff in a **randomized order**.
  - Similar findings are clustered, and **majority voting** keeps only bugs that more than one pass found.
  - Each cluster is merged into one description, and unwanted categories (compiler warnings, docs) are filtered out.
  - A **validator model** removes likely false positives, and results are **deduplicated against earlier runs**.
- **The biggest gains came from going agentic.** The model reasons over the diff, calls tools and decides where to look. The agent was "too cautious" at first, so the prompts were made *more aggressive*. Context moved toward dynamic discovery, and small toolset changes had large effects.
- **Metric: resolution rate.** At merge time, an LLM classifies which flagged bugs the author actually fixed. Engineers spot-checked that classifier. Cursor hill-climbs on this metric online and on BugBench offline, an internal set of real diffs with human-annotated bugs. Many of the 40 experiments *regressed* the metrics.
- **Vendor claims:**
  - Resolution rate rose from 52% to over 70%. The later Autofix post says 76%.
  - Bugs flagged per run rose from 0.4 to 0.7, and resolved bugs per PR from about 0.2 to about 0.5.
  - It reviews over 2M PRs a month.
- **Autofix** spawns *separate* cloud agents that "work independently in their own virtual machines to test your software". Over 35% of Autofix changes are merged (vendor claim). Cursor lists "enabling Bugbot to verify its own findings" as *future* work, so Bugbot's findings themselves aren't yet verified by execution.

### OpenAI Codex review ([alignment blog](https://openai.com/safety-alignment/scaling-code-verification/), [GitHub integration docs](https://learn.chatgpt.com/docs/third-party/github))

- **A dedicated reviewer.** It's the same base model as the generator but trained separately for review, because training-time verifiers are tuned to catch everything while a human-facing reviewer must avoid false alarms.
- **Precision first:** "We optimize for signal-to-noise first, and only then push recall without compromising reliability." OpenAI accepted "modestly reduced recall".
- **Repo access and execution help both ways.** Giving the reviewer repo-wide tools and code execution "results in a stronger reviewer, catching more critical issues and raising fewer false alarms". Diff-only review "often misses important context".
- **Verification is cheap.** The reviewer catches "a large share of previously identified high-severity issues" at "a small fraction of the generator's token spend". Extra budget mostly improves calibration. Performance drops faster at low budget on *model-generated* code than on human code.
- **Vendor claims:**
  - It comments on 36% of Codex-generated PRs. 46% of those comments lead to a code change, against 53% on human PRs.
  - It handles over 100k external PRs a day.
- **On GitHub it flags only P0/P1 issues.** Repo rules live in `AGENTS.md` under `## Code Review Rules`, scoped per directory. The docs recommend starting with "two or three concise rules" and leaving formatting and lint to CI.

### Greptile ([make LLMs shut up](https://greptile.com/blog/make-llms-shut-up), [TREX](https://www.greptile.com/blog/trex-code-execution))

- **Baseline**: about 19% of comments were good, 2% incorrect and 79% nits. The address rate was 19%.
- **What failed:**
  - Prompting couldn't cut nits without also cutting critical comments, and few-shot examples made it worse.
  - LLM-as-judge severity scoring (1–10, drop anything under 7) failed: "The LLMs judgment of its own output was nearly random."
- **What worked**: each team's past comments are embedded, labeled by whether developers addressed, upvoted or downvoted them. A new comment is blocked if it's similar to three or more downvoted ones. The address rate went from 19% to 55+% in two weeks (vendor claim).
- **TREX** verifies by execution:
  - An orchestrator reviewer picks the issues worth testing and starts one subagent per issue, each with its own context and its own disposable sandbox.
  - Each subagent returns artifacts (logs, screenshots, traces) as evidence. "Bad evidence is worse than no evidence."
  - Greptile moved to this design after separate agents duplicated each other's work and a single agent got overloaded.
  - The harness is model-agnostic and can mix providers within one review.
  - Unverified: a Greptile report says TREX catches about 20% more bugs. A search snippet says Greptile deliberately routes review to a different model than the one that wrote the code. I didn't confirm either at the primary source.

### CodeRabbit ([context engineering](https://www.coderabbit.ai/blog/context-engineering-ai-code-reviews), [independent review](https://www.coderabbit.ai/blog/why-agentic-change-management-starts-with-independent-ai-code-review))

- It clones the repo into a sandbox, builds a code graph, and adds past PRs, **linked issues checked against the PR's requirements**, path rules and web lookups. It aims for a "1:1 ratio of code-to-context".
- It runs 40+ linters and SAST tools (50+ in the newer post), with a verification agent that de-noises their output.
- **Verification scripts** generated in the sandbox test each LLM comment, and low-value comments are dropped (vendor claim, no numbers).
- **Its argument for independence:** a reviewer that shares the authoring agent's context "carries the original assumptions straight into review". Its example is an authorization check that enforces the wrong account boundary, with tests that confirm the same mistake. The post has no outcome data.

### Graphite and GitHub Copilot

- **Graphite** uses comment acceptance rate as its main quality metric. Its 2024 post said retrieval and custom guidelines didn't fix signal-to-noise. That post now returns 404. Graphite is joining Cursor. Its numbers (about 52% action rate) appear only in secondary summaries and are **unverified**.
- **GitHub Copilot**, per [ReviewBench](https://github.blog/news-insights/research/reviewbench-an-open-benchmark-for-ai-code-review/):
  - Combining several independent model runs into one review (a "multi-model ensemble") raised the online addressed rate by 8.0% and recall by 13.6%, while cost per review fell 8.0% (vendor claim, A/B test).
  - Offline benchmark changes "have consistently pointed in the same direction" as production.

## 2. Independent evidence

**How good is AI review at all?**

- **CR-Bench-v2** ([arXiv 2611.02566](https://arxiv.org/abs/2611.02566)) rebuilds SWE-Bench defects as review tasks with full repo context. "Even the strongest systems detect only about one-third of known defects" (KDDR ≈ 33%). Gemini Code Assist detects 8.1%.
- **CRBugBench** ([arXiv 2610.13668](https://arxiv.org/abs/2610.13668), "(When) Should a Software Factory be Lights-off?") uses 1,000 OSS tasks plus 800 proprietary ones.
  - The best agent catches 71.9% of bugs within its top 5 comments, up from 47.7% five months earlier.
  - Agents are weaker on complex changes, non-local bugs and proprietary repos, where project context matters more.
  - Its recommendations: scale review effort to PR complexity, supply repository knowledge, dispatch models by PR characteristics, and monitor quality as models change.
  - "Exploration-nudging prompts" gain up to 5.8% for 10% more cost.
- **AACR-Bench** results ([arXiv 2608.09290](https://arxiv.org/html/2608.09290), Table 3 read) cover 200 PRs and 1,505 expert comments.
  - An open-ended coding agent used as reviewer (Claude Code with Claude-4.6-Opus) got 7.2% precision and 28.9% recall at about 5.7M tokens per run.
  - Codex with GPT-5.5 got 27.8% precision and 4.9% recall.
  - The paper's **OpenCodeReview** constrains the agent at three points: deterministic rule-guided dispatch of files and criteria, a curated tool set, and an *independent reflection* stage that filters comments by falsification while seeing only the diff. With the same model it reached 33.9% precision and 20.0% recall at 385K tokens, a **2.17x better F1 at 5–15x fewer tokens**. There's no ablation that isolates the reflection stage.
- **Martian Code Review Bench** ([repo](https://github.com/withmartian/code-review-benchmark), MIT) is the most-cited cross-vendor benchmark.
  - The offline track has 50 PRs and 173 golden comments, scored by three LLM judges. The top 5 tools are identical across all three judges.
  - The online track scores precision as the share of bot comments matched to fixes the developer actually made afterwards, which avoids training-data leakage.
  - I didn't read per-tool leaderboard numbers at the primary source. Figures such as "Greptile 84% precision" are **unverified**.
- **Vendor-run comparison benchmarks** (Tenki, CodeAnt, Macroscope and similar) were skipped as conflicted.

**What gets acted on in the wild?**

- **16 GenAI review GitHub Actions** were studied across 22,326 comments ([arXiv 2508.18771](https://arxiv.org/html/2508.18771v2), read in full).
  - Developers addressed about 60% of human comments, against 0.9–4.2% for file-level bots and 6.5–19.2% for hunk-level bots.
  - Predictors of a comment getting addressed: being tied to specific lines, being triggered by hand, containing code (23.2% when code is over half the comment, 4.2% with none), and being short (ρ = −0.24).
- **Code review agents in the AIDev dataset** ([arXiv 2604.03196](https://arxiv.org/abs/2604.03196)): PRs reviewed only by an agent merged 45.2% of the time, against 68.4% for human-only review. 12 of 13 agents had average signal ratios below 60%.
- **CodeRabbit in the wild** ([arXiv 2607.03316](https://arxiv.org/abs/2607.03316), seen as a search snippet only, **unverified**): 36.4% of 31,073 suggestions were accepted.
- **Google AutoCommenter** ([arXiv 2405.13565](https://arxiv.org/html/2405.13565v1), read in full) is the best evidence for a *standards* reviewer.
  - Every comment cites the URL of the best practice it enforces, and carries a confidence score.
  - Thresholds are **set per rule, not globally**. A single global threshold of 0.98 killed recall.
  - Comments on unmodified lines (80% of raw output) are dropped.
  - Rules found non-actionable are **suppressed one by one**, which raised the useful ratio from about 54% to over 80%. Raters also flagged comments that were "technically correct but low-value" as not useful.
  - About 40% of comments were actually resolved.

**Can a reviewer judge the author's work?**

- **Self-review is near chance.** In BOBA ([arXiv 2611.02207](https://arxiv.org/abs/2611.02207)), agents asked whether their own implementation was correct were right 53% of the time. Agents declare a request done once *their own* tests pass, and the missed bugs compound across chained requests: completion is about 95% on single requests and about 51% on chains. **One external failure signal plus one repair round** raised chain completion from 51% to 59%.
- **Intrinsic self-correction doesn't work.** Without external feedback, LLMs struggle to self-correct, and performance sometimes drops ([Huang et al., arXiv 2310.01798](https://arxiv.org/abs/2310.01798)).
- **Evidence matters more than reviewer size.** "Groundability, Not Scale Alone" ([arXiv 2610.01023](https://arxiv.org/abs/2610.01023)) found that reviewer size didn't predict review quality. With *execution* evidence, even weak reviewers judged patches well, and two of six classified every trace correctly. *Unchecked* structured evidence raised both catches and over-rejection. A deployable cascade built on generated failing tests and patch-caused static errors still over-rejected about 66% of the time. Producing reliable checks without official tests "remains the main bottleneck."
- **Same-model bias.** LLM evaluators recognize and favor their own generations ([arXiv 2404.13076](https://arxiv.org/abs/2404.13076)). The harmful part shows up mainly where the evaluator would itself have gotten the task wrong, and longer reasoning reduces it ([arXiv 2504.03846](https://arxiv.org/abs/2504.03846)). Neither paper is about code review specifically.
- **Multi-role reviewers.** RevAgent ([arXiv 2511.00517](https://arxiv.org/abs/2511.00517)) uses five category-specific commentators plus a critic and beat single-model baselines, but only on text-similarity metrics. A defect-focused industrial system on C++ ([arXiv 2505.17928](https://arxiv.org/abs/2505.17928)) combines code slicing for context, multi-role reasoning and a false-alarm filter. Its ablation credits all three, and it reports about 2x over plain LLMs.
- **Counterfactual verification.** CIVER ([arXiv 2610.36010](https://arxiv.org/abs/2610.36010)) separates real defects from false positives by applying a targeted repair and checking whether the critique responds. It identified 77% of genuine defects on its benchmark.

## 3. Answers to the ticket's sub-questions

- **One reviewer vs. several.** Several independent finders help recall, and agreement among them is a precision signal: Bugbot v1's majority voting, Copilot's ensemble (+13.6% recall, +8% addressed) and Anthropic's parallel finders. But the biggest jump Cursor reports came from a single *agentic* reviewer with tools, not from more passes. Unbounded agents are also expensive and noisy: on AACR-Bench, a coding agent used as reviewer had 7% precision. **Best supported:** a bounded number of finders, scaled by change size, plus a verifier.
- **Review axes.**
  - All vendors default to **correctness**, which includes security.
  - **Standards** are checked only against written rules, and the finding must quote or link the rule: Anthropic's plugin, Codex `AGENTS.md`, Google AutoCommenter.
  - **Spec conformance** gets the intent as input. Anthropic passes the PR title and description to every agent, and CodeRabbit checks linked issues against the PR.
  - **Style and lint** go to deterministic tools.
  - "General code quality" without a written rule is explicitly excluded as noise by Anthropic. Google found per-rule suppression necessary even for written rules.
- **Fresh vs. shared context.** Every vendor runs review in its own context, separate from the author: Anthropic runs `/code-review` as a forked subagent, and CodeRabbit argues for independence. Verifiers get *less* context still: Anthropic's validators see only the PR description and the issue, and OpenCodeReview's reflector sees only the diff. For finding bugs, more *repo* context helps (OpenAI, CRBugBench, CodeRabbit). What hurts is sharing the author's *reasoning*. Evidence that self-review is weak: BOBA (53%) and Huang et al.
- **Adversarial or verifying reviewers.** A per-finding verifier is now standard (Anthropic, Bugbot, CodeRabbit, OpenCodeReview, Greptile TREX). The verifier has to *ground* the claim, ideally by executing code (ultrareview, TREX, OpenAI's execution access, Groundability). Asking an LLM to rate severity without grounding was "nearly random" (Greptile).
- **Deduplication and filtering.**
  - Cluster and merge equivalent findings (Bugbot). Keep one comment per unique issue (Anthropic).
  - Dedupe against earlier runs (Bugbot).
  - Drop comments on unchanged lines and pre-existing issues (Google, Anthropic).
  - Apply a fixed exclusion list: linter-catchable issues, nitpicks, input-dependent speculation, silenced issues (Anthropic).
  - Rank by severity and report only the top severities (Codex P0/P1) or cap the count (Anthropic: 32).
  - Learn from what users ignore (Greptile embeddings, Google per-rule suppression).
- **Review-then-fix loops.** The products keep *finding* and *fixing* apart. Bugbot Autofix runs separate agents in VMs that test their fixes, with 35% of fixes merged. Anthropic's `--fix` applies a finished findings list. When findings are fixed, Claude Code re-labels each one as fixed, skipped or no change needed. What makes a repair round work is an *external* failure signal (BOBA), not a reviewer re-reading its own fix (Huang et al.). No vendor publishes an unbounded review-fix-review loop.

## 4. Implications for fliesswerk's review design

The current `review.md` has one session review along two axes *and* fix its own findings. Against the evidence above:

1. **Split find from fix, and make finding fresh and read-only.** The Factory can start a new Harness session with no transcript from the implement Step. That's Harness-agnostic, because the Factory owns it. The reviewer outputs a structured findings list (file, line, claim, axis, evidence) instead of editing code. Implementing and fixing can stay in one session, but judging shouldn't be done by whoever wrote the code (BOBA, Huang, CodeRabbit).
2. **Add a verify Step per finding.** Run a cheap verifier per finding that gets only the claim, the Ticket and the code, and must confirm or refute it. Prefer executable evidence: a failing test that reproduces the bug, or a Feedback command that fails. Where a failing test can be produced, the bug becomes an external signal for the fixer, and the test stays as a regression test. Ungrounded severity scoring shouldn't be used as a filter (Greptile).
3. **Do as much as possible in code.** These steps are deterministic and belong in Go, not in prompts:
   - gating trivial changes
   - choosing scope (diff, touched files, applicable Coding standards)
   - the exclusion list: pre-existing lines, findings on unchanged lines, findings a Feedback command already enforces
   - deduplicating findings across finders and across rounds
   - capping the count
   - bounding fix rounds

   OpenCodeReview shows that this kind of determinism *raises* F1 and cuts tokens 5–15x against an open-ended agent.
4. **Narrow the axes and make standards citable.** Correctness and Ticket conformance (including security and missing tests at the named seams) are the primary axis. A standards finding should count only if it quotes a rule from the Coding standards file (Anthropic plugin, Codex, Google). The built-in list of 12 code smells is exactly the "general code quality without a written rule" that vendors exclude as noise. Because fliesswerk's reviewer *edits*, that noise turns into churn and regression risk rather than ignored comments. Consider dropping the smells from the default prompt, or turning them into verified, low-priority findings.
5. **Scale rigor with the change, not with user settings.** Use one finder for small diffs and two or three for large or risky ones, each with a different diff order or focus. Keep findings that more than one finder reports or that a verifier confirms. This matches the map's "the Factory scales rigor itself" (Anthropic, Bugbot, CRBugBench). Running finders on a different model or Harness than the implementer is plausible but backed only by weak evidence (self-preference papers; Greptile's routing is unverified).
6. **A clean review isn't proof.** The best reviewers catch about 33% (CR-Bench-v2) to 72% (CRBugBench, top 5) of known bugs, and fewer on proprietary code. "Vouched for" has to rest mainly on executable evidence: tests at the seams, Feedback commands, verification beyond tests. Use review findings and their verification status as one input to **Risk reason** / **Human review required**.
7. **Measure it.** Copy Bugbot's resolution rate: did the fixer act on a verified finding, and did it survive to merge? Also track whether humans later change code the review passed. Keep these as run data so the maintainers can hill-climb the review prompts. That feeds the learning-loop item on the map.

## Caveats

- Most quality numbers are vendor self-reports on their own code or customers. The independent benchmarks disagree on definitions (golden comments, developer fixes, known defects), so their numbers can't be compared directly.
- Several 2026 arXiv papers were read at abstract level only, and the field moves monthly. The model names in those benchmarks (Claude-4.6/4.8-Opus, GPT-5.5, opus-5) are as reported by the papers.
- **Unverified**: Graphite's numbers, Martian per-tool leaderboard figures, the CodeRabbit-in-the-wild acceptance study, Greptile's +20% TREX figure, and Greptile's cross-model routing.
