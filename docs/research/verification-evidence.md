# Verification evidence: how agents prove a change works (as of 2026-11-27)

**Question (#159):** How do agents and teams produce verification evidence so a human only has to glance at a change rather than read it? What is Harness-agnostic, what does it cost, what works inside a Sandbox with restricted network, and what do reviewers actually look at?

**Method:**
- Read first-party docs, changelogs and engineering posts from the vendors that ship AFK coding agents (Devin, Cursor, GitHub Copilot, OpenAI Codex, Google Jules, Anthropic), from the tools that make evidence (Playwright, VHS, Hurl, Pact, Chromatic, Vercel), and GitHub's docs for the delivery channels.
- Read empirical papers on agent-authored PRs (built on the AIDev dataset) and Microsoft's code review study for what reviewers look at.
- Nothing was tried hands-on. Claims that rest only on a secondary source, or that I couldn't confirm, are marked **unverified**.

## Short answer

Every major hosted agent now attaches *something a human can look at* to its work: Copilot and Codex attach browser screenshots to the PR, Cursor attaches videos, screenshots and logs, Devin records an annotated browser video, and Jules returns a screenshot. All of them get there the same way: a headless browser (mostly Playwright) driven inside the agent's own VM, then an upload. Under the media, the evidence that carries the most weight is still programmatic: CI status, tests that fail without the change, and the commands that were run with their output. Vendors present the video as a sanity check on top of that, not a replacement (Devin's docs say so outright). For fliesswerk this points to a split. The Factory runs the checks and builds the evidence report in code, with no agent involved. An agent writes a short, reproducible demo script for the change (browser flow, CLI tape or HTTP file). The Factory replays that script itself to capture the media and attaches the result to the PR with `gh`. All of this runs inside the Sandbox against its own Services, with no egress beyond what is already allowed.

## What the factories and agents do

| Product | What it produces | Where it goes | How it's made | Source |
|---|---|---|---|---|
| GitHub Copilot coding agent | Screenshots of the running app | Embedded in the PR | Playwright MCP, on by default in the agent's environment | [changelog 2025-07-02](https://github.blog/changelog/2025-07-02-copilot-coding-agent-now-has-its-own-web-browser/), [MCP docs](https://docs.github.com/en/enterprise-cloud@latest/copilot/concepts/coding-agent/mcp-and-coding-agent) |
| OpenAI Codex (cloud) | Screenshots, plus citations of terminal logs and test output for each claim | Task and GitHub PR | Codex "spin[s] up its own browser, look[s] at what it built, iterate[s], and attach[es] a screenshot" | [Codex upgrades](https://openai.com/index/introducing-upgrades-to-codex/), [system card](https://cdn.openai.com/pdf/8df7697b-c1b2-4222-be00-1fd3298f351d/codex_system_card.pdf) |
| Cursor cloud agents | Videos, screenshots, log references | PR description, opt-in ("Allow posting artifacts to GitHub") | Full desktop VM and computer use: start the dev server, click through flows | [docs](https://cursor.com/docs/cloud-agent/capabilities), [blog](https://cursor.com/blog/agent-computer-use) |
| Devin | Annotated, auto-zoomed browser video of the single most important end-to-end flow | Session messages (webapp or Slack), **not** the PR, per its docs | After CI is green: plan one flow, send the plan, record, post-process | [docs](https://docs.devin.ai/work-with-devin/testing-and-recordings) |
| Google Jules | Screenshot of the rendered front end | Returned to the user with the change | Playwright is in the default base image | [changelog 2025-08-07](https://jules.google/docs/changelog/2025-08-07) |
| Anthropic (Claude Code guidance) | "the command it ran and its output, or the screenshot" | — | Any pass/fail check, or `/verify`, which builds, runs and looks at the app | [best practices](https://code.claude.com/docs/en/best-practices) |
| Simon Willison's Showboat + Rodney | A Markdown demo doc with commands, their real output and screenshots | Committed or attached | CLI (Go binary): `exec` records output, `image` embeds screenshots, `verify` re-runs the doc. Rodney is a CLI browser built on Go Rod (CDP) | [post](https://simonwillison.net/2026/Feb/10/showboat-and-rodney/) |

Observations:
- **Video is for the human, not for the agent.** Devin records only *after* CI is green and review comments are addressed, and limits the recording to one flow. It says: "If you need more exhaustive coverage, use your existing test suites and CI rather than visual recording." A user on Cursor's forum makes the same point: the video is a demo recorded after the agent has validated its work (**unverified**, one user's opinion).
- **Delivery is the fragile part.** Cursor staff acknowledged that posting artifacts to PRs had "rough edges", and users reported that prompting models to produce videos did not work consistently ([forum](https://forum.cursor.com/t/cursor-cloud-agents-do-not-post-their-screenshots-or-videos-to-pr/152974), [forum](https://forum.cursor.com/t/getting-video-demos-out-of-cloud-agents/157217); community source). Making the *agent* responsible for producing media is unreliable. Making code responsible for it isn't.
- **Agents fake or skip verification when they can.** Anthropic saw Claude "mark a feature as complete without proper testing". Without browser tools it ran unit tests or `curl` but missed that the feature didn't work end to end, and adding browser automation "dramatically improved performance" ([Anthropic](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)). Willison notes that agents sometimes edit the Showboat Markdown by hand, so the "output" in the doc doesn't match what ran. Evidence is only trustworthy if the Factory produces it, not the agent's own account.
- **Public URLs are a trade-off.** Cursor's GitHub embeds "use long, unguessable URLs that are viewable without authentication", because GitHub's image proxy needs public URLs. For private repos that is a data-exposure choice.

## Evidence types: what, cost, Harness-agnostic, Sandbox fit

"Sandbox fit" assumes fliesswerk's setup: container or native Sandbox, egress allowlist, and project Services on an internal network.

| Evidence | What the human sees | Programmatic? | Cost | Works in restricted Sandbox? |
|---|---|---|---|---|
| **CI / Feedback commands green** | A check mark | Fully | Compute only | Yes (already done) |
| **Fail-to-pass tests** (new tests fail on the base commit, pass on the head) | "These N tests failed without the change, pass with it" | Fully: run the new tests against the base commit | One extra test run | Yes |
| **Test summary** (`go test -json`, JUnit XML → Markdown table) | Counts and names of tests that were added, changed or failing | Fully | Negligible | Yes |
| **Commands + real output transcript** | The commands that were run and their output | Fully, if the Factory captures it, not the agent | Negligible | Yes |
| **Browser screenshots** (Playwright `page.screenshot`, `playwright-cli screenshot`) | Images of the changed UI | Capture is programmatic, but choosing *what* to show needs knowledge of the change | Small. If an LLM looks at a 1000×1000 image: 1,296 tokens (~$5 per 1,000 images on Opus 5.5) | Yes, if the browser is in the image beforehand. Playwright downloads browsers from Microsoft's CDN at install time (`PLAYWRIGHT_DOWNLOAD_HOST` or a proxy can redirect this) |
| **Browser video** (Playwright `video: 'on'` / `retain-on-failure`, `playwright-cli video-start`) | A clip of the flow | Capture is programmatic. The flow is scripted | Compute plus upload size | Yes, same as above. Playwright defaults the video to the viewport scaled to fit 800×800 |
| **Playwright trace** (`trace.zip`) | Step-by-step DOM snapshots, network, console | Fully | Larger artifact. `trace: 'on'` is "performance-heavy" per the docs | Yes. trace.playwright.dev opens it locally in the browser and "does not transmit any data externally" |
| **Visual regression** (`toHaveScreenshot`, Chromatic, Percy) | Pixel diff against an approved baseline | Fully once baselines exist | Free locally (Playwright). SaaS for review UIs | Playwright: yes. Baselines depend on platform ("Browser rendering can vary based on the host OS…"), so the Sandbox image must be the one that made them. Chromatic/Percy need egress to SaaS |
| **Terminal recording** (VHS tape → GIF/MP4/WebM, plus `.txt`/`.ascii` golden output) | A clip of a CLI session, or a text snapshot | Fully, from a tape script | Small. Needs `ttyd` and `ffmpeg` | Yes. A natural fit for CLI projects like fliesswerk itself |
| **API checks** (Hurl files, curl transcripts) | Requests and responses with assertions. JUnit/TAP/HTML reports | Fully | Negligible. Single binary | Yes, against the Sandbox's own Services |
| **Contract tests** (Pact) | Consumer-provider compatibility | Fully | Setup cost per project. The broker is a service | Yes for local verification. A Broker needs egress |
| **Preview deployments** (Vercel, Netlify, …) | A live URL on the PR | Driven by the platform | Platform pricing. Previews sit behind auth by default (Vercel Deployment Protection) | **No** from inside the Sandbox: it's an external platform reached through the git push, not something the Factory controls. It's project-specific, not Harness-agnostic, and it belongs to the project's CI |
| **Before/after comparison** | Base vs head screenshots, or base vs head output | Fully: replay the same demo script on the base and head commits | 2× the demo run | Yes |
| **Agent's written account** ("I tested X, it works") | Prose | No | Tokens | Yes, but it's the least trustworthy evidence (see above) |

Sources: [Playwright videos](https://playwright.dev/docs/videos), [trace viewer](https://playwright.dev/docs/trace-viewer), [snapshots](https://playwright.dev/docs/test-snapshots), [browsers](https://playwright.dev/docs/browsers), [playwright-cli](https://github.com/microsoft/playwright-cli), [VHS](https://github.com/charmbracelet/vhs), [Hurl](https://hurl.dev/), [Pact](https://docs.pact.io/), [Chromatic UI Review](https://www.chromatic.com/docs/review), [Vercel environments](https://vercel.com/docs/deployments/environments), [Claude vision costs](https://platform.claude.com/docs/en/build-with-claude/vision), [SWE-bench dataset fields](https://huggingface.co/datasets/princeton-nlp/SWE-bench).

**Fail-to-pass** is SWE-bench's own definition of "resolved": `FAIL_TO_PASS` is "the set of tests resolved by the PR and tied to the issue resolution", and `PASS_TO_PASS` is the tests that "should pass before and after the PR application" ([dataset card](https://huggingface.co/datasets/princeton-nlp/SWE-bench)). Willison asks for the same thing from humans: each change should come with a test that "should fail if you revert the implementation" ([post](https://simonwillison.net/2025/Dec/18/code-proven-to-work/)). Of everything here, this is the cheapest strong evidence, and it is entirely code.

**Playwright CLI vs MCP:** Microsoft's `playwright-cli` says it is more token-efficient for coding agents because it avoids "loading large tool schemas and verbose accessibility trees". It keeps MCP for loops that need persistent state ([README](https://github.com/microsoft/playwright-cli)). A CLI is also Harness-agnostic: any Harness that can run a shell command can drive it, with no MCP wiring.

## Delivering evidence on a GitHub PR

| Channel | Shows inline on the PR? | Limits | Notes |
|---|---|---|---|
| `gh pr create/edit/comment --attach <file>` | Yes, embedded in the body/comment | Images/GIF 10 MB; video 10 MB (Free) / 100 MB (paid); PNG, JPEG, GIF, WebP, SVG, MP4, MOV, WebM | GA 2026-09-01, needs `gh` ≥ 2.99.0, write access, and **an OAuth token from `gh auth login` or a classic PAT**. Not on GHES. Private-repo uploads are visible only to people with repo access |
| PR body / comment Markdown | Yes | — | Test summaries, command transcripts, Showboat-style docs |
| Check run `output` (summary, text, `images`, annotations) | In the Checks tab, and annotations in Files changed | Summary/text 65,535 chars each; 50 annotations per request | **Only GitHub Apps can create check runs**, so a user token can't |
| Actions job summary (`$GITHUB_STEP_SUMMARY`) | On the workflow run page, not the PR | 1 MiB per step, 20 per job | Only for evidence produced in the project's CI |
| Actions artifacts | No: zipped download, needs login | 90-day default retention | Fine for traces or full logs, poor for glancing |

Sources: [gh attach changelog](https://github.blog/changelog/2026-09-01-github-cli-media-in-issues-pull-requests-and-comments), [gh attach docs](https://docs.github.com/en/github-cli/github-cli/attaching-files-with-github-cli), [attachment limits](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files), [check runs](https://docs.github.com/en/rest/checks/runs), [workflow commands](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands), [upload-artifact](https://github.com/actions/upload-artifact).

**Unverified:** whether `--attach` works with fine-grained PATs or GitHub App installation tokens. The changelog names only OAuth tokens and classic PATs, and the docs page is silent. Before relying on it, fliesswerk would need to test this with the tracker token users actually configure. Before 2026-09 there was no official upload API at all, and tools like [gh-attach](https://github.com/Addono/gh-attach) fell back to release assets or browser cookies.

## What reviewers actually look at

- **Understanding the change is the bottleneck.** Microsoft's study found that "code and change understanding is the key aspect of code reviewing", and that tools serve it poorly ([Bacchelli & Bird, ICSE 2013](https://www.microsoft.com/en-us/research/publication/expectations-outcomes-and-challenges-of-modern-code-review/)). Evidence should explain *what changed in behavior*, not just state that it passed.
- **CI status dominates merge outcomes for agent PRs.** Across 33,596 agent PRs, CI failures were the strongest signal: "each additional failed CI check decreases the odds of a merge by about 15%". The most common failure was **reviewer abandonment** (38% of rejected PRs got "little or no human engagement"), and larger PRs were merged less often ([arXiv 2601.15195](https://arxiv.org/html/2601.15195)). A green, small PR with a clear summary is the baseline. Agent PRs that people ignore are a bigger risk than agent PRs that people reject.
- **Including tests doesn't by itself raise merge rates.** Merge rates for agent PRs with and without tests were similar, and PRs with tests took longer to turn around ([arXiv 2601.03556](https://arxiv.org/html/2601.03556v1)). Tests in the diff are not evidence a human can glance at. A summary of what the tests prove might be (**unverified**: no study measures this).
- **Images carry information the text doesn't.** In Mozilla change requests, shared images were found to be "meaningful and … providing complementary information" ([arXiv 2001.06426](https://arxiv.org/abs/2001.06426v1)). I found no study measuring whether screenshots or videos on PRs speed up review or reduce defects. The vendor claims (Cursor: "quickly validate"; Copilot: inspect "before checking out the branch") are **unverified** marketing.
- **Humans stay accountable.** Willison: the person submitting the code must prove it works. Commands and their output in the PR, screenshots for CSS, and video for anything hard to show in text ([post](https://simonwillison.net/2025/Dec/18/code-proven-to-work/)).

## Implications for fliesswerk

1. **The Factory produces the evidence, not the agent's prose.** Agents skip and fake verification (Anthropic, Willison). The trustworthy parts are cheap and pure code. Capture the Feedback command transcripts. Run new or changed tests against the base commit to show fail-to-pass. Render a test summary. Diff the base and head behavior. This fits "programmatic first" and costs no tokens.
2. **Split the work for behavioral demos.** An agent Step writes a small, replayable *demo script* for the change: a Playwright/`playwright-cli` flow, a VHS tape, or a Hurl file. The Factory then replays it in the Sandbox against the Sandbox's own Services, on the head commit and optionally the base commit, and records screenshots or video. The media then reflects a real run. Replay is deterministic and Harness-agnostic, and the script can be committed as a regression check. This is the Showboat idea (`exec`/`verify`) moved into the Factory.
3. **Everything the PR needs works inside the restricted Sandbox,** provided the image carries the tools (browsers, `ffmpeg`/`ttyd`, `hurl`). Browsers must be baked into the image, or fetched through the allowlist/proxy (`PLAYWRIGHT_DOWNLOAD_HOST`, `HTTPS_PROXY`). Preview deployments and SaaS visual review (Chromatic/Percy) don't run in the Sandbox. They belong to the project's own CI, which the Factory already watches when it drives the PR to green.
4. **Deliver with `gh ... --attach` from the Factory, after the Sandbox.** It keeps private-repo media private, puts it inline where reviewers look, and needs no GitHub App. Check runs (richer, with `images`) would require shipping a GitHub App, which is a bigger decision. Open risk: `--attach` may not accept fine-grained or App tokens (unverified). Size caps (10 MB on Free) mean short, low-resolution clips. Prefer screenshots, and add video only for flows.
5. **Media is optional and scaled by the Factory, evidence is not.** Every PR gets the programmatic evidence block. Screenshots and video only appear when the change touches something observable, such as UI, CLI output or HTTP responses. This is decided by the Factory (the rigor ticket), not configured by the user. If an LLM looks at its own screenshots to judge them, that costs about 1.3k tokens per 1000×1000 image. That is cheap per image, but not something to put in a loop.

## Unverified / open

- Whether `gh --attach` works with fine-grained PATs and GitHub App tokens.
- Whether Devin's recordings ever land on the PR itself. Its docs say they go to session messages, and a third-party guide claims they go on the PR.
- Any measured effect of screenshots or videos on review time or defect escape. I found no primary study.
- How reliable Codex's log citations are in practice. I found no independent evaluation.
