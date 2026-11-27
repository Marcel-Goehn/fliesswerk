# How agent pipelines keep token cost down (as of 2026-11-27)

**Question (#157):** What drives token cost in agent coding pipelines, and which techniques keep it down without losing quality? Covers prompt caching, context size and compaction, model tiering, doing work in code instead of agents, scaling effort to the change, and stopping rules for fix loops. Also: what subscription plans (e.g. Claude Max) actually allow per usage window, to judge what "doesn't drain a subscription" means in practice.

**Method:**
- Prices, caching rules and plan limits come from first-party docs: Anthropic (platform.claude.com, code.claude.com, support.claude.com, claude.com/pricing), OpenAI (developers.openai.com, learn.chatgpt.com) and Google (aistudio.google.com). They were fetched on 2026-11-27. Prices and plan limits change often, so re-check them before relying on a number.
- Evidence on agent behaviour comes from Anthropic engineering posts and arXiv papers. For the papers, only the abstract pages were read, not the full papers. Claims that only appear in the full text are marked *unverified*.
- fliesswerk's own per-Step costs are out of scope here (#161 measures them).

## Short answer

In an agent loop, cost is mostly **input tokens re-read on every turn**: the whole prefix (system prompt, tools, project context, history, tool results) is sent again with each request. That makes the price of input, and how much of it is served from the **prompt cache**, the main lever. After that come the **number of turns** (each turn re-reads the context) and **output and thinking tokens**, which are priced 5× input. Everything that works does one of five things:

1. Keeps the cached prefix stable.
2. Keeps context small.
3. Cuts turns. Better specs and doing work in code both do this.
4. Uses a cheaper model or lower effort where quality holds.
5. Stops loops that have stopped paying off.

Subscriptions publish no absolute token numbers, only multiples of Pro per 5-hour and weekly window. So "doesn't drain a subscription" can only be judged by measuring a run's share of the window (#161).

## 1. What drives cost

- **Input dominates the token count in agentic coding.** On SWE-bench Verified across eight frontier models, agentic tasks consume "1000x more tokens than code reasoning and code chat", with "input tokens rather than output tokens driving the overall cost". Repeat runs of the same task "can differ by up to 30x in total tokens". "Higher token usage does not translate into higher accuracy": accuracy "often peaks at intermediate cost and saturates at higher costs". Models "systematically underestimate" their own token use (correlation up to 0.39). Source: [Bai et al., arXiv:2604.22750](https://arxiv.org/abs/2604.22750).
- **Why input dominates:** "Claude Code sends your full conversation with every request, and each time Claude uses tools it sends another request carrying that batch of tool results" ([Claude Code: costs](https://code.claude.com/docs/en/costs#why-usage-climbs-in-a-long-session)). So cost per turn grows with context, and total cost grows roughly with turns × context.
- **With high cache hit rates, output can still be half the dollars.** One search snippet said that with Kimi K3 at a 96.3% cache hit rate, output was ~2.7% of tokens but ~51% of dollars. It also said cutting *turns* matters more than shortening inputs. That snippet came from [Smékal, arXiv:2608.25399](https://arxiv.org/abs/2608.25399), but it is *unverified*: the abstract doesn't contain it. The abstract does say that cutting a full task spec down to a bare user story "raises token spend by 29.7%".
- **Agents and multi-agent setups multiply cost.** "Agents typically use about 4× more tokens than chat interactions, and multi-agent systems use about 15× more tokens than chats." In their eval, "token usage by itself explains 80% of the variance" in performance, yet "upgrading to Claude Sonnet 4 is a larger performance gain than doubling the token budget on Claude Sonnet 3.7" ([Anthropic: multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)). Claude Code's agent teams use "approximately 7x more tokens than standard sessions" when teammates run in plan mode ([costs](https://code.claude.com/docs/en/costs#manage-agent-team-costs)).
- **Waste is common and measurable.** Three behaviours ("subsumed retrieval, similar script generation, and test re-execution") show up in 79–98% of tasks and account for "up to 22.75% of task cost". Developer-written skills (high-level guidance) cut cost by up to 41.73%, about twice what agent-written skills achieved. "Structure-aware retrieval" sometimes *raised* cost by up to 28.14% ([Hu et al., arXiv:2609.30725](https://arxiv.org/abs/2609.30725)).
- **Fixed per-request overhead is small but adds up.** Any request with tools gets a tool-use system prompt of ~286–675 tokens depending on the model. The bash tool adds 325 tokens and the text editor 974 on 4.7+ models ([pricing: tool use](https://platform.claude.com/docs/en/about-claude/pricing#tool-use-pricing)). Each MCP server adds context to every message ([OpenAI Codex pricing tips](https://learn.chatgpt.com/docs/pricing)). "Tools like `gh` … are still more context-efficient than MCP servers" ([costs](https://code.claude.com/docs/en/costs#reduce-mcp-server-overhead)).
- **Tokenizer change:** Claude 4.7+ models produce "approximately 30% more tokens for the same text" than Sonnet 4.6 and earlier ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)). Per-token prices aren't comparable across that boundary.

### Current list prices (USD per million tokens, Claude API, 2026-11-27)

| Model | Input | 5m cache write | 1h cache write | Cache hit | Output |
|---|---|---|---|---|---|
| Opus 5.5 | 4 | 5 | 8 | 0.20 (0.05×) | 20 |
| Sonnet 5.5 | 2 | 2.50 | 4 | 0.10 (0.05×) | 10 |
| Haiku 5.5 (≤100k prompt) | 0.10 | 0.125 | 0.20 | 0.01 | 0.50 |
| Haiku 5.5 (>100k prompt) | 0.50 | 0.625 | 1 | 0.05 | 2.50 |
| Fable 5.2 | 8 | 10 | 16 | 0.20 (0.025×) | 40 |

Source: [Anthropic pricing](https://platform.claude.com/docs/en/about-claude/pricing).

- Batch API: −50%, but it is asynchronous and of little use for an agent loop.
- Fast mode: 2× the price (Opus 5.5 is $8/$40).
- US-only inference: ×1.1.
- 1M-token context is billed at the standard rate (all except Haiku 5.5).
- Haiku 5.5 is 20× cheaper than Sonnet 5.5 on input and output, but it switches to the higher tier for any prompt over 100k tokens, even if most of that prompt is a cache hit.

## 2. Prompt caching: the biggest lever

**How it works (Anthropic).**
- Prices: a cache write costs 1.25× input (5-minute TTL) or 2× (1-hour TTL). A cache hit costs 0.1× input on most models, 0.05× on Opus/Sonnet 5.5 and 0.025× on Fable 5.1+. The 5-minute write pays off "after one cache read" and the 1-hour write "after two".
- Matching: caching is a **prefix match** in the order `tools` → `system` → `messages`, and a hit needs "100 percent identical prompt segments".
- Lifetime: the TTL is refreshed free on each hit. It is measured "from the start of the request", so a 4-minute response leaves about 1 minute for the next call on the 5-minute TTL.
- Limits: up to 4 breakpoints, each with a 20-block lookback. Prompts shorter than 512 tokens (5.x models) up to 4,096 tokens (Haiku 4.5, Opus 4.5/4.6) are silently not cached.

Sources: [prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching), [pricing](https://platform.claude.com/docs/en/about-claude/pricing#prompt-caching).

**What breaks the cache.**
- Changing tool definitions invalidates everything.
- Changing tool choice, images or dropped thinking blocks invalidates system and messages.
- Changing the model invalidates everything, because each model has its own cache.
- Changing thinking or effort parameters is model-specific. On Opus 5.5, Sonnet 5.5, Haiku 5.5 and Fable 5.1+, effort changes keep the cache when made via the API or a subscription.
- Turning fast mode on or off.

Sources: [prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching), [Claude Code: prompt caching](https://code.claude.com/docs/en/prompt-caching#actions-that-invalidate-the-cache).

**How Claude Code's own team designs around it** ([Shihipar, "Prompt caching is everything", 2026-04-30](https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/)):
- "We build our entire harness around prompt caching … we run alerts on our prompt cache hit rate and declare SEVs if they're too low." Also: "A few percentage points of cache miss rate can dramatically affect cost and latency."
- "Static content first, dynamic content last." Past breakages came from a timestamp in the system prompt and from non-deterministic tool ordering.
- Send updates as messages (`<system-reminder>`), never by editing the system prompt.
- **Don't switch models mid-session.** "If you're 100k tokens into a conversation with Opus … it would actually be more expensive to switch to Haiku than to have Opus answer." To use another model, hand off to a subagent with a short brief instead.
- Never add or remove tools mid-session. Defer them with stubs instead. Plan mode is a tool, not a swap of the toolset.
- Compaction reuses the parent's exact prefix and appends the summarization prompt. If it doesn't, the whole history is billed uncached.

**TTL in Claude Code** ([prompt caching: cache lifetime](https://code.claude.com/docs/en/prompt-caching#which-ttl-each-request-gets)):
- The main conversation (including `-p` runs and Agent SDK turns) gets the **1-hour TTL by default on a subscription within plan usage**, and **5 minutes on an API key**, cloud provider or usage credits.
- Subagents, workflows and compaction get 5 minutes everywhere by default.
- Controls: `CLAUDE_CODE_PROMPT_CACHE_TTL` / `promptCacheTtl` for the main conversation, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL` for everything else, and `ENABLE_PROMPT_CACHING_1H` / `FORCE_PROMPT_CACHING_5M`.

**Cache scope in Claude Code.** "The cache is effectively scoped to one machine and directory." The system prompt embeds the auto-memory paths, and the conversation opens with the working directory and a git-status snapshot. "Sequential sessions share the prefix only when the git status snapshot taken at startup matches." The Agent SDK docs show how to move the auto-memory location out of the system prompt to share caches across a fleet ([prompt caching: cache scope](https://code.claude.com/docs/en/prompt-caching#cache-scope)). Inference for fliesswerk: each Step runs in a fresh `-p` process in a per-Ticket worktree with a different git status, so **Steps almost certainly don't share Claude Code's prefix with one another**. Caching mainly pays off *within* a Step's turns. This is not measured; #161 should check `cache_creation` against `cache_read` per Step.

**Other providers (relevant for Harness-agnosticism):**
- **OpenAI:** caching is automatic. The minimum is 1,024 tokens on GPT-5.6+. A cache read costs 0.1× input (0.05× on some GPT-6.x). A cache write costs 1.25× with a 30-minute default TTL, or 2.5× with a 12-hour TTL. Advice: "Put stable developer instructions and shared reference material first", append instead of rewriting, and keep tools consistent ([OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)).
- **Google Gemini:** implicit caching is on by default for 2.5+ models, with a 4,096-token minimum on current models. The discount isn't stated on the page ([Gemini caching](https://aistudio.google.com/docs/caching)).
- The design rule is the same everywhere: stable prefix, append-only history, fixed tool set.

## 3. Context size and compaction

- **Smaller context is both cheaper and better.** Recall degrades as context grows ("context rot"). Recommended fixes:
  - Compaction.
  - Tool-result clearing, called "the safest lightest touch forms of compaction".
  - Structured notes kept outside the context.
  - Just-in-time retrieval by path or query.
  - Subagents that explore with tens of thousands of tokens but return "a condensed, distilled summary", "often 1,000-2,000 tokens".

  Source: [Anthropic: effective context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents).
- **Compaction is itself a big request.** When the cache is warm it costs a fraction of the context size. When cold, it reprocesses the whole history uncached. "When you want a fresh start instead of continuity, `/clear` costs nothing." Rewinding to an earlier turn hits an existing cache entry. Compacting builds a new one ([prompt caching: compaction](https://code.claude.com/docs/en/prompt-caching#compacting-the-conversation), [costs](https://code.claude.com/docs/en/costs#why-usage-climbs-in-a-long-session)).
- **Trim what enters context.**
  - Preprocess tool output in code. A hook that greps a log can reduce context "from tens of thousands of tokens to hundreds".
  - Keep CLAUDE.md "under 200 lines" and move workflow-specific instructions into on-demand skills.
  - Prefer CLIs over MCP.
  - Use code-intelligence (LSP) tools instead of grep-and-read.

  Source: [costs: reduce token usage](https://code.claude.com/docs/en/costs#reduce-token-usage).
- **Specific prompts cut exploration.** "Vague requests … trigger broad scanning", while specific ones allow "minimal file reads" ([costs](https://code.claude.com/docs/en/costs#write-specific-prompts)). A full spec instead of a user story saved 29.7% ([arXiv:2608.25399](https://arxiv.org/abs/2608.25399)).

## 4. Model tiering and effort

- **Tier by Step, not within a session.** Anthropic's guidance is "Haiku for simple tasks, Sonnet for most production workloads, and Opus for the most complex reasoning" ([pricing](https://platform.claude.com/docs/en/about-claude/pricing#cost-optimization-strategies)). Claude Code's guidance is "Sonnet handles most coding tasks well … Reserve Opus for complex architectural decisions", with `model: haiku` for simple subagents ([costs](https://code.claude.com/docs/en/costs#choose-the-right-model)). Because a model switch rebuilds the cache, the switch belongs at a session boundary. In fliesswerk, the natural boundary is a Step.
- **Effort is the main cost knob on current models.** It controls all output tokens: thinking, tool calls and text. "Lower effort also means fewer and terser tool calls". Opus 5.5 and Haiku 5.5 default to `medium`, other models to `high`.
  - Sonnet 5.5: for agentic coding, "start with `medium` for well-specified tasks and move to `high` for harder or longer ones".
  - `low` suits "simpler tasks … such as subagents".
  - `max` "adds significant cost for relatively small quality gains".
  - "Consider dynamic effort: Adjust effort based on task complexity."
  - Thinking can't be turned off on 5.5 models, so effort is the lever.

  Source: [effort](https://platform.claude.com/docs/en/build-with-claude/effort).
- **Model quality can beat token budget.** A model upgrade gave more than doubling the token budget ([multi-agent post](https://www.anthropic.com/engineering/multi-agent-research-system)). Cheaper isn't automatically better per unit of quality: a weak model that loops costs more than a strong one that finishes.

## 5. Doing work in code instead of agents

- The research supports fliesswerk's "programmatic first" rule on cost grounds, too:
  - Every deterministic action (running Feedback commands, parsing results, git, opening PRs, checking CI) costs tokens when an agent does it and nothing when code does.
  - "Test re-execution" is one of the three main waste behaviours ([arXiv:2609.30725](https://arxiv.org/abs/2609.30725)).
  - Hooks that filter output before the model sees it are Anthropic's own recommendation ([costs](https://code.claude.com/docs/en/costs#offload-processing-to-hooks-and-skills)).
- **Pass facts in, don't make the agent discover them.** Developer-written skills and specs cut cost (−41.73% and −29.7% in the studies above). Giving the agent the failing command's trimmed output beats letting it re-run and read the full log.

## 6. Stopping rules for fix loops

- **Self-repair has limited returns.** "Performance gains are often modest, vary a lot between subsets of the data, and are sometimes not present at all" once the cost of repair is counted. Gains improve with a *stronger* feedback model ([Olausson et al., ICLR 2024, arXiv:2306.09896](https://arxiv.org/abs/2306.09896)). Combined with "accuracy … saturates at higher costs" ([arXiv:2604.22750](https://arxiv.org/abs/2604.22750)), this argues for a **small fixed cap on fix rounds**, with escalation to a human (fliesswerk's *Human review required*) rather than unbounded retries.
- **Hard caps available in the Harness:**
  - Claude Code has `--max-budget-usd` / `maxBudgetUsd`. It counts the call's own spend and is based on client-side estimates.
  - It also has caps on subagent depth, concurrency and spend.
  - It reports `total_cost_usd` and per-model `modelUsage` (including cache read/creation) in the `-p --output-format json/stream-json` result.
  - These are "client-side estimates, not authoritative billing data".

  Sources: [Agent SDK cost tracking](https://code.claude.com/docs/en/agent-sdk/cost-tracking), [headless](https://code.claude.com/docs/en/headless).
- A cheap, Harness-agnostic stopping signal: stop when a round makes no progress, for example when the same Feedback command fails with the same output twice, or the diff doesn't change. *(Design inference, not from a source.)*

## 7. Subscription plans: what a usage window allows

### Claude (checked 2026-11-27 on [claude.com/pricing](https://claude.com/pricing) and the [Claude Code plan article](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-max-or-ultra-plan))

| Plan | Price | Limit as published |
|---|---|---|
| Pro | $20/mo ($17 annual) | Baseline: "every 5 hours and every week" |
| Max | $100/mo | "5x Pro usage every 5 hours and every week", plus a separate weekly Fable limit |
| Ultra | $200/mo | "10x Pro usage every week"; "No 5-hour limit" for **verified** accounts only |
| Team standard / premium | $20–25 / $100–125 per seat | "More usage than Pro" / "5x more usage than standard seats" |

- **No absolute numbers are published.** Not in tokens, messages or hours. The pages only give multiples of Pro, and "How fast you reach a limit depends on the model you use, how long your chats are, and how large your files are." Prompt counts on third-party sites are estimates and are left out here. "Max 20x" appears to have been renamed "Ultra" (the page links to a `max_20x` URL). The date of the rename is *unverified*.
- Limits are "shared across Claude and Claude Code". The weekly limit "resets at a fixed time each week that is assigned to your account" ([Max plan](https://support.claude.com/en/articles/11049741-what-is-the-max-plan)). "Additional usage limits may apply to manage capacity."
- Past the limit you can turn on **usage credits** at standard API rates. Claude Code can also wait for the reset and continue automatically (`autoContinueAtUsageLimit`) ([costs](https://code.claude.com/docs/en/costs#when-a-developer-asks-about-a-limit)). Monthly API credits included with Max "can't be used in Claude Code".
- Anthropic's own sizing hint: Claude Code costs on average "around $13 per developer per active day and $150-250 per developer per month", and stays "below $30 per active day for 90% of users" (enterprise, at API rates) ([costs](https://code.claude.com/docs/en/costs)).
- Subscriptions get the **1-hour cache TTL by default** within plan usage. Anthropic says a high cache hit rate "helps us create more generous rate limits for our subscription plans" ([blog](https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/)). Whether cache reads count against plan limits at the reduced rate is not published (*unverified*). The costs page only says history is re-read "at the cached token rate".

### OpenAI Codex (checked 2026-11-27 on [Codex pricing](https://learn.chatgpt.com/docs/pricing))

- Estimated local messages per 5 hours on Plus range from **5–45** (GPT-6 Astra) to **350–3,000** (GPT-6 Luna), for example 17–180 on GPT-6.2 Sol.
- "Pro plans currently have no five-hour limit", but "Weekly limits may also apply".
- The figures are "not fixed message limits".
- API-key sessions are billed separately from the ChatGPT allowance.

### Rules that affect a tool like fliesswerk

- **Third-party products and subscription logins.** "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods … instead" ([Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)). fliesswerk runs the *user's own* installed `claude` CLI with the user's own login. It doesn't embed the SDK or offer a login itself. Whether that counts as "offering claude.ai login" is **not settled by the docs** (*unverified, open question*). Worth a deliberate decision and maybe asking Anthropic.
- **`--bare` disables the subscription.** "Bare mode doesn't use your subscription login": it needs `ANTHROPIC_API_KEY` ([headless](https://code.claude.com/docs/en/headless#start-faster-with-bare-mode)). The docs say `--bare` "will become the default for `-p` in a future release". fliesswerk deliberately never passes `--bare` (`internal/harness/claudecode/claudecode.go`). If the default flips, subscription users would break unless fliesswerk opts out explicitly. Watch the release notes.

### What "doesn't drain a subscription" can mean in practice

Since only relative limits exist, the workable definition is **a measured share of the user's own window**. Claude Code surfaces this as plan usage bars in `/usage`, and the `-p` result reports per-run token counts. A reasonable target is something like "a run of N Tickets uses at most X% of a Pro/Max 5-hour window". That target can only be set once #161 has per-Step numbers. On an API key, Anthropic's $13/active-developer-day average is a sanity anchor.

## Implications for fliesswerk's pipeline design

1. **Treat cache hit rate as a first-class metric.**
   - Record `cache_read_input_tokens` against `cache_creation_input_tokens` per Step from the stream-json result.
   - Inside a Step: keep the prompt template static first and dynamic last (Ticket and diff at the end), keep the tool set fixed, and don't change model or effort mid-Step.
   - Across Steps the Claude Code prefix probably isn't shared, so each Step pays one cache write of the base prefix. That makes **many tiny Steps relatively expensive**. Adding Steps (more reviewers, verification) has a fixed per-Step floor that #161 should quantify.
2. **Tier model and effort per Step, decided by the Factory.** Review and verification of a small diff, classification, and summarizing CI logs suit Sonnet or Haiku at low or medium effort. Implementation on a well-specified Ticket suits Sonnet or Opus at medium, rising for larger changes. fliesswerk already passes `--model` and `--effort` per Step. The rigor ticket can drive effort from the size of the change, which keeps the "Factory scales rigor itself" Note.
3. **Programmatic first is also the cost strategy.** Run Feedback commands, CI polling, diff stats, the Protected-paths check and similar in Go. Feed agents only trimmed failures (the tail of the output, the failing test names), never full logs. Pass facts into the prompt so the agent doesn't rediscover them.
4. **Cap fix loops hard.** Allow a small fixed number of fix rounds, stop early when a round makes no progress, then hand over as *Human review required*. Consider using the Harness's budget cap (`--max-budget-usd` on Claude Code) as a per-Step safety net, keeping in mind it's a client-side estimate. Keep the cap Factory-owned so it stays Harness-agnostic.
5. **Fresh context per Step beats long sessions.** Starting each Step clean with a distilled brief is the "subagent returns 1–2k tokens" pattern applied at pipeline level. It avoids compaction cost and context rot. Don't carry whole transcripts between Steps; carry structured reports (which fliesswerk already does via `--json-schema`).
6. **Subscription risk is mostly policy, not tokens.** Settle the "third-party claude.ai login" question deliberately, and pin the non-`--bare` behaviour before Claude Code flips the default.

## Sources

- Anthropic, [Pricing](https://platform.claude.com/docs/en/about-claude/pricing)
- Anthropic, [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
- Anthropic, [Effort](https://platform.claude.com/docs/en/build-with-claude/effort)
- Claude Code docs, [Manage costs effectively](https://code.claude.com/docs/en/costs)
- Claude Code docs, [How Claude Code uses prompt caching](https://code.claude.com/docs/en/prompt-caching)
- Claude Code docs, [Run Claude Code programmatically](https://code.claude.com/docs/en/headless)
- Claude Code docs, [Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)
- Claude Code docs, [Track cost and usage](https://code.claude.com/docs/en/agent-sdk/cost-tracking)
- T. Shihipar, [Lessons from building Claude Code: Prompt caching is everything](https://claude.dev/blog/lessons-from-building-claude-code-prompt-caching-is-everything/) (2026-04-30)
- Anthropic Engineering, [How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- Anthropic Engineering, [Effective context engineering for AI agents](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
- Claude, [Plans & pricing](https://claude.com/pricing)
- Claude Help Center, [Use Claude Code with your Pro, Max, or Ultra plan](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-max-or-ultra-plan)
- Claude Help Center, [What is the Max plan?](https://support.claude.com/en/articles/11049741-what-is-the-max-plan)
- OpenAI, [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- OpenAI, [Codex pricing](https://learn.chatgpt.com/docs/pricing)
- Google, [Gemini context caching](https://aistudio.google.com/docs/caching)
- Bai et al., [How Do AI Agents Spend Your Money?](https://arxiv.org/abs/2604.22750) (arXiv:2604.22750)
- Smékal, [Can your AI agent be cheaper?](https://arxiv.org/abs/2608.25399) (arXiv:2608.25399)
- Hu et al., [Analyzing and Mitigating Cost-Inefficient Behaviors in Coding Agents](https://arxiv.org/abs/2609.30725) (arXiv:2609.30725)
- Olausson et al., [Is Self-Repair a Silver Bullet for Code Generation?](https://arxiv.org/abs/2306.09896) (ICLR 2024)
