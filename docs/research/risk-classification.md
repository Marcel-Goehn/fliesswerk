# How teams decide which changes need a human (as of 2026-11-27)

**Question (#156):** How do teams and tools decide which changes need a human reviewer? This covers risky-path rules (CODEOWNERS-style), migrations and schema changes, auth, payments and data deletion, blast radius and diff size, dependency changes, and agent self-assessment. Which signals are programmatic, which need judgment, how reliable is each, and how is the reason shown to the human?

**Method:**
- Sources are vendor docs, tool READMEs and papers.
- Claims I took only from an abstract or a search snippet, without reading the full text, are marked *(abstract only)*.
- Claims I could not check against a primary source are marked *(unverified)*.
- Nothing was tested hands-on.

## Short answer

Industry uses two layers that complement each other:

1. **Deterministic rules decide that a human must look.** These are path ownership (CODEOWNERS, Google's OWNERS, GitHub ruleset file patterns), migration linters (Atlas, Squawk, strong_migrations), dependency and security scanners (dependency-review-action, GitLab approval policies) and semver-based automerge policies (Renovate). They are cheap, explainable and auditable: each one names the rule, the file and the line. Every widely deployed "a human must approve" mechanism I found is of this kind.
2. **Statistical risk scores rank and gate the rest.** Examples are Meta's Diff Risk Score, classic just-in-time (JIT) defect prediction, Microsoft CRANE and Mozilla bugbug. They are used to concentrate scrutiny on the top few percent of changes, not to certify that a change is safe. Their recall is moderate. Even Meta's production model only catches a minority of incidents at small gating fractions, and the scores need historical incident data that a single project usually doesn't have.

**Agent self-assessment is the weakest signal.** Models can't reliably self-correct without external feedback. Frontier models rewrite tests and scoring code to "pass", and they then sometimes believe the result matched intent. An agent's statement that "this is safe" should never be what clears Human review required. At most it can *add* a Risk reason.

## 1. Risky-path rules (ownership)

**GitHub CODEOWNERS** ([docs](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners))
- Each line is a gitignore-style pattern followed by owners, and "the last matching pattern takes the most precedence".
- `!` negation, `[ ]` ranges and escaped `#` are not supported.
- Owners are requested automatically, and "Require review from Code Owners" in branch protection makes their approval mandatory. Any one listed owner is enough.
- The file under the base branch's `.github/`, root or `docs/` applies, and it must be under 3 MB.
- **How the reason is shown:** a tooltip saying "Owned by USER or TEAM (from CODEOWNERS line NUMBER)". The reason is the rule's location, not its intent.

**GitHub rulesets** ([docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets))
- Push rules can *block* changes to file paths, file extensions or file sizes, using `fnmatch` patterns with exceptions.
- A required-reviewers rule takes per-team file patterns, with `!` to exclude, for up to 15 teams. It isn't available on user-owned repositories.

**Google OWNERS** ([Software Engineering at Google, ch. 9](https://abseil.io/resources/swe-book/html/ch09.html))
- OWNERS files are "hierarchically additive", and "an owner of the directory in question must also approve" any change to that part of the tree.
- Approval is split into three kinds: LGTM (correctness), owner approval and readability approval.
- Even "machine-generated changes require review" (large-scale changes), but reviewers of those changes limit themselves to concerns local to their own code.
- Some documentation and config changes "might not require a code review".
- Google recommends keeping changes to about 200 lines.

**Reliability:** deterministic and explainable, with no false negatives *for the paths that are listed*. Its blind spot is risk that lives in an unlisted file, for example an auth check inlined into a handler. Coverage is only as good as the list.

## 2. Migrations, schema changes and data deletion

These linters are mature and deterministic, and they are the strongest programmatic signal for "DB migration" and "data deletion" Risk reasons.

**Atlas** ([analyzers](https://atlasgo.io/lint/analyzers))
- Stable check codes, each tied to the statement that triggered it:
  - **Destructive** (fail by default): `DS101` schema dropped, `DS102` table dropped, `DS103` column dropped.
  - **Data-dependent** (warn): `MF101` to `MF104`, for example adding a unique index or making a column NOT NULL.
  - **Backward-incompatible** (warn): `BC101` to `BC104`, for renames and drops. These fire even when the object is empty, because running clients still reference it.
  - **Locking and rewrites** (Pro): `PG1xx` and `PG3xx`, `MY1xx`.
- Atlas's review policy "auto-approves only when linting passes and requires manual approval otherwise". That is the same gate fliesswerk wants.

**Squawk** ([repo](https://github.com/sbdchd/squawk))
- A Postgres migration linter that aims to prevent downtime. Its rules include `ban-drop-table`, `ban-drop-column`, `require-concurrent-index-creation` and `renaming-table`.
- Suppression is per statement or per file, with `-- squawk-ignore`.
- It ships as a CLI, a pre-commit hook and a GitHub Action.

**strong_migrations** ([repo](https://github.com/ankane/strong_migrations), Rails)
- Flags these operations: removing a column, changing a type, renaming, adding a foreign key or check constraint, raw SQL, and non-concurrent indexes on Postgres.
- An operation counts as "dangerous" if it holds a lock for more than a few seconds or has a good chance of causing app errors.
- **How the reason is shown:** the risk plus a safer multi-step recipe.
- The opt-out (`safety_assured {}`) is explicit and visible in the diff.
- It admits that it can't reliably detect backfills, and it blocks `execute` because it can't inspect it.

**Reliability:**
- High for DDL in recognised migration files.
- Blind to data deletion done in application code, such as `DELETE FROM` in a job or an ORM `.delete_all`, and to raw SQL that it can't parse.
- Each tool is tied to one database or framework.
- **For fliesswerk:** "a migration file was added or changed" is detectable by path alone, and that path signal is language-agnostic. The per-statement detail needs a per-ecosystem linter as an optional fact source.

## 3. Dependency changes

**Renovate automerge** ([docs](https://docs.renovatebot.com/key-concepts/automerge/))
- The policy layer: automerge is opt-in per rule (update type, dependency type, package name). Renovate recommends it "for any dependency update where you would select 'merge' anyway".
- Typical rules cover minor and patch updates, devDependencies and lock-file maintenance.
- Pre-1.0 packages (`!/^0/`) are excluded, because under semver they can break at any time.
- It never merges without passing status checks, unless `ignoreTests` is set, which the docs discourage.
- CODEOWNERS-required reviews block automerge.

**dependency-review-action** ([repo](https://github.com/actions/dependency-review-action))
- Compares the dependency diff for newly introduced vulnerable versions, with `fail-on-severity`.
- Also checks license allow and deny lists, OpenSSF Scorecard levels, and denied packages.
- Reports in the job summary or a PR comment, and exposes outputs (`vulnerable-changes`, `invalid-license-changes`, …).

**Reliability:**
- "A new or major-bumped dependency" is fully programmatic: diff the manifest and lock file.
- Vulnerability and license checks depend on an external advisory database, which means network access and a GitHub-specific service.

## 4. Security findings as an approval trigger

**GitLab merge request approval policies** ([docs](https://docs.gitlab.com/user/application_security/policies/merge_request_approval_policies/))
- Approval is required when security scanners report findings that are `newly_detected`, meaning present on the MR branch but not on the target branch.
- **Fail closed:** "if the rules are invalid, or can't be evaluated, approval is required" (`fallback_behavior`). A missing scanner report still enforces approval.
- GitLab notes that it doesn't verify the integrity of the scan artifacts.

The fail-closed default and the "new compared with the base branch" framing both carry over directly to fliesswerk.

## 5. Statistical change-risk scoring (blast radius, size, history)

**JIT defect prediction**
- [Kamei et al., TSE 2013](https://sail.cs.queensu.ca/data/pdfs/TSE_ALarge-ScaleEmpiricalStudyOfJust-in-TimeQualityAssurance.pdf) *(abstract only)*: 14 change metrics in five groups: diffusion (subsystems, directories and files touched, entropy), size (lines added and deleted), purpose (is it a fix), history and developer experience.
- Average accuracy was 68% and recall 64% across 11 projects.
- These are the classic "blast radius / diff size" features, and they are cheap to compute from git.

**Shihab et al., "An industrial study on the risk of software changes", FSE 2012** ([PDF](https://www.eecs.yorku.ca/~zmjiang/publications/FSE2012_shihab.pdf)) *(snippets only, unverified detail)*
- Developers marked their own changes as risky, and models predicted those marks.
- Developers became less reliable at judging risk when a change had many related changes.

**Meta Diff Risk Score (DRS)**
- Sources: the [engineering blog, 2025-08](https://engineering.fb.com/2025/08/06/developer-tools/diff-risk-score-drs-ai-risk-aware-software-development-meta/) and [Abreu et al., arXiv 2410.06351 (ICSE SEIP 2025)](https://arxiv.org/abs/2410.06351v1) *(abstract only)*.
- A fine-tuned Llama model predicts how likely a diff is to cause a SEV, a production incident. Its inputs are the title, test plan and code changes.
- It's used for gating during code freezes: Meta reports 10,000+ diffs landed during a 2024 freeze that "previously could not have landed".
- It also feeds reviewer selection and 18 other uses.
- **Gating levels:** none, weekend (top 5%), yellow (top 10%) and red (top 50%).
- **Baseline recall:** the logistic-regression baseline catches 18.7% of SEVs at the top 5%, 27.9% at the top 10% and 84.6% at the top 50%. The LLMs improve on this by roughly 1.05× to 1.5×.
- **Explanation:** the model highlights risky snippets. Natural-language explanation of the score is listed as *future work*, and LLM explainability is called an open problem.

**DRS-OSS** ([arXiv 2511.21964](https://arxiv.org/abs/2511.21964v2)) *(abstract only)*
- An open-source version: Llama 3.1 8B fine-tuned on ApacheJIT.
- F1 is 0.64 and ROC-AUC 0.89. Gating the riskiest 30% of commits would block up to 86.4% of defect-inducing changes (simulated).
- Its GitHub App posts a risk label and a confidence score.

**Microsoft CRANE** ([ICST 2011](https://www.microsoft.com/en-us/research/wp-content/uploads/2011/03/ICST2011_CRANE_CameraReady.pdf)) *(abstract only)*
- Windows change-risk analysis used to decide how risky a fix is and which tests to run.
- A related Microsoft study found that [change bursts](https://www.microsoft.com/en-us/research/publication/change-bursts-as-defect-predictors/) were the strongest predictors of defect-prone components.

**Mozilla bugbug** ([repo](https://github.com/mozilla/bugbug)) *(unverified)*
- Search snippets describe a "regressor" model for flagging patches likely to cause regressions. I did not confirm its current status.

**Change size and review effectiveness**
- Google's guideline is about 200 lines (above).
- The SmartBear/Cisco study of 2,500 reviews ([SmartBear](https://smartbear.com/learn/code-review/best-practices-for-peer-code-review/)) found that defect discovery drops sharply beyond 200 to 400 lines. This is vendor-reported, and the 70–90% figure is a vendor claim *(unverified against the original data)*.

**Reliability:**
- These are probabilistic rankers. They need labelled incident history per project, which a fresh project or a factory run doesn't have.
- Even Meta's best model misses most SEVs at a 5–10% gating budget.
- Size and diffusion metrics are programmatic and transferable, but on their own they only weakly predict defects.
- **Use:** a heuristic Risk reason such as "large or scattered change", not a pass/fail certification.

## 6. Agent self-assessment

**Kadavath et al., "Language Models (Mostly) Know What They Know"** ([arXiv 2207.05221](https://arxiv.org/abs/2207.05221)) *(abstract only)*
- Large models are fairly well calibrated on multiple-choice questions in the right format.
- Calibration degrades on new distributions.
- That is short-answer QA, not judging the safety of a code change.

**Huang et al., "Large Language Models Cannot Self-Correct Reasoning Yet"** ([ICLR 2024](https://arxiv.org/abs/2310.01798))
- Without external feedback, self-correction doesn't help reasoning and sometimes hurts it.
- Earlier gains depended on oracle labels.

**METR, "Recent Frontier Models Are Reward Hacking"** ([2025-06](https://metr.org/blog/2025-06-05-recent-reward-hacking))
- Models modify tests and scoring code, monkey-patch evaluators and override equality operators to pass.
- o3 did this in 30.4% of RE-Bench runs and 0.7% of HCAST runs. METR says these counts are likely underestimates.
- "Please do not cheat" barely changed the rate.
- Asked afterwards, the model sometimes admits the plan didn't match intent, and on other tasks "reliably thinks" its hack did match.

**AIDev** ([arXiv 2507.15003](https://arxiv.org/abs/2507.15003)) *(abstract only)*
- Across about 456k agent-authored PRs, agents are faster than humans, but their PRs are accepted less often.

**Reliability:**
- An agent's claim "no risk here" is not evidence.
- An agent volunteering "this deletes user data" is useful as an *additional* Risk reason: low cost, and it can only add, never clear.
- Checks that compare against the base branch, such as "were tests modified or deleted?" or "were assertions weakened?", are programmatic and catch the reward-hacking pattern directly.

## 7. Signal summary

| Signal | Programmatic? | Reliability | Reason shown as | Real-world mechanism |
|---|---|---|---|---|
| Path matches a project's risky-path list | Yes (glob) | Exact for listed paths, blind to unlisted ones | Pattern + file (+ line of rule) | CODEOWNERS, OWNERS, ruleset file patterns |
| Migration file added or changed | Yes (path convention) | High | File | Atlas review policy, framework conventions |
| Destructive or incompatible DDL | Yes (per-DB linter) | High for parseable DDL, misses app-level deletes | Check code + statement | Atlas DS/BC, Squawk, strong_migrations |
| New dependency or major bump | Yes (manifest and lock-file diff) | High | Package + old → new version | Renovate rules, dependency-review |
| New vulnerability or license violation | Yes (needs advisory DB) | Depends on the DB | Advisory ID, severity | dependency-review, GitLab policies |
| Tests or CI config deleted or weakened | Yes (diff vs base) | Medium-high | File + hunk | (reward-hacking defence, METR) |
| Diff size and diffusion | Yes (git stats) | Weak predictor alone | Numbers vs threshold | JIT metrics, Google's ~200 LOC |
| Learned risk score | Model + history | Moderate recall, needs incident data | Score (+ highlighted snippets) | Meta DRS, DRS-OSS, CRANE |
| Auth, payment or crypto semantics in non-listed files | Partly (grep/AST patterns), else judgment | Medium | Matched pattern + location | (Semgrep-style rules, *not researched here*) |
| Agent self-assessment | No | Low, cannot clear risk | Agent's sentence | Not used as a gate by anyone found |

## Implications for fliesswerk

1. **Rules decide, the agent can only add.** Human review required should be triggered by deterministic rules over the Spec branch's diff against the base branch. Agent statements may add Risk reasons but never remove them. This matches every production gate found (CODEOWNERS, Atlas, GitLab, Renovate) and the evidence on self-assessment.
2. **Ship built-in, project-independent rules, and let projects add facts.** Built-ins detected from the diff alone, all cheap and language-agnostic:
   - migration-directory changes, using common conventions
   - dependency manifest and lock-file changes, flagging new dependencies and major bumps
   - deleted or modified tests, CI config, or the Factory's own config
   - CODEOWNERS or `.github/` changes
   - secrets and env files
   - large or highly diffuse diffs

   The project contributes *facts* only: a protected/risky paths glob list (CODEOWNERS syntax, last match wins), and optionally which migration linter command to run. This fits ADR 0011 and the map's "projects contribute facts" note.
3. **Fail closed.** As in GitLab, if a check can't be evaluated (a linter crashed, or the diff couldn't be computed), mark the PR Human review required with that as the Risk reason.
4. **Reason format:** each Risk reason should carry `{rule id, human-readable why, files (and line/statement where known), Tickets whose commits introduced them}`. That combines the CODEOWNERS "from line N" provenance with the Atlas/strong_migrations "code + statement + why it's dangerous" style. Attributing a reason to a Ticket is possible because each Ticket lands as its own commits on the Spec branch. Map files to the commits that touched them.
5. **Skip learned risk scores for now.** They need per-project incident history and still miss most incidents at small gating budgets. Size and diffusion metrics are worth emitting as a soft Risk reason ("large change") rather than a model.
6. **Cost:** everything above except the optional agent-added reasons is a git diff plus glob matching, so it costs zero tokens.

## Not covered or open

- Semgrep/CodeQL-style detection of auth and payment code in files a project didn't list. This is the main gap between path rules and judgment.
- Kubernetes Prow's `approve` plugin, which explains its approvals in PR comments, was not researched.
- Full texts of the Kamei, Shihab, Meta DRS and CRANE papers were not read. Their numbers come from abstracts.
