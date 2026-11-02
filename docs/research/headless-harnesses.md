# How do the target harnesses run headless?

Research for ticket #8 (map #1). Feeds the **harness boundary** (#17).
Researched 2026-11-02 from official docs, harness source, and Sandcastle v0.13.0 source (`mattpocock/sandcastle` @ `719347729bd0`), whose `AgentProvider.ts` already drives all six harnesses and whose recorded stream fixtures (captured 2026-10-31) show what each CLI actually emits.

Vocabulary: a **Step** is one harness invocation. What the harness calls a "session" (its persisted conversation, which can be resumed) is called a **harness session** below, so it does not clash with the glossary.

## Answer

- **All six have a one-shot headless mode that prints JSONL to stdout.** Claude Code: `claude -p --output-format stream-json --verbose`. Codex: `codex exec --json`. Pi: `pi --mode json`. Cursor: `agent -p --output-format stream-json`. OpenCode: `opencode run --format json`. Copilot: `copilot -p … --output-format json`. Each has its own event schema, so each adapter needs its own line parser. [1][4][7][10][12][15]
- **How the prompt gets in varies, and it matters on Windows.** Claude Code, Codex, Pi, OpenCode and Copilot read the prompt from stdin. Cursor documents only a positional argument [10][11]. Copilot ignores piped input if `-p` is also given [15]. An argv prompt is limited to about 128 KiB per argument on Linux (Sandcastle guards at 120 KiB) [S1], and to 32,767 characters for the whole command line on Windows [20]. Ticket and Spec context can easily exceed that, so a cross-platform Factory should use stdin wherever the harness allows it.
- **No harness has a "Ticket done" signal.** Each one tells you only that the process ended (exit code, terminal event). Two harnesses can return structured output natively: Claude Code (`--json-schema`, which fills `structured_output`) [1] and Codex (`--output-schema`) [4]. For the other four, the Factory has to look for a marker or tag in the final text, which is what Sandcastle does (`<promise>COMPLETE</promise>`, last `<tag>…</tag>`) [S2][S3]. Several harnesses exit 0 even when the run failed: Pi, OpenCode, and Codex `turn.failed` [S1]. The adapter has to read failures from the stream, not just from the exit code.
- **All six can resume a harness session by ID.** The ID comes from the stream for all six [S1]. Only Claude Code, Codex, Pi and OpenCode can fork a session. Cursor and Copilot resume only [S1][S4]. Harness sessions live in each harness's own store (JSONL files, SQLite, or an export/import JSON), so resuming one inside a Sandbox means copying harness-specific files in and out [S1].
- **Model and provider are configured differently by each harness.** Pi and OpenCode are multi-provider (OpenRouter and Bedrock are both documented) [8][9][13]. Claude Code supports Bedrock, Vertex and Foundry through env flags, and gateways through `ANTHROPIC_BASE_URL`, but Claude models only [2][3]. Codex supports custom `model_providers` (Bedrock is built in). Its wire API is `responses` only, so OpenRouter is unconfirmed [5][6]. Copilot has BYOK via `providers.json` (openai, azure or anthropic types) [16][17]. Cursor documents no BYOK [11].
- **Token usage is in the stream for every harness. Cost is reported directly by only three.** Claude Code reports cost (`total_cost_usd` plus per-model `costUSD`, as client-side estimates) [1]. Pi reports per-message `usage.cost` [7]. OpenCode reports per-step `part.cost` [S5]. Copilot reports premium requests and AI credits, not USD [S5]. Codex and Cursor report tokens only, so cost needs a price table [4][S5]. Claude Code's and Codex's terminal totals are summed over the whole turn, so they are not a context-window figure [S1].
- **Headless auth for subscription logins has a trap.** Claude Code takes `CLAUDE_CODE_OAUTH_TOKEN` (made with `claude setup-token`), but `--bare` ignores it, and the docs say `--bare` "will become the default for `-p` in a future release" [1][19]. Since v2.1.302, `claude -p` also starts in Manual permission mode unless a mode is set [1].

## Per-harness comparison

| | **Claude Code** | **Codex** | **Pi** | **Cursor CLI** | **OpenCode** | **Copilot CLI** |
|---|---|---|---|---|---|---|
| **Binary / headless cmd** | `claude -p` (`--print`) [1] | `codex exec` [4] | `pi -p` or `pi --mode json` (`--mode rpc` for a long-lived process) [7] | `agent -p` (`--print`) [10] | `opencode run` (also `opencode serve` + `--attach`) [12] | `copilot -p` [15] |
| **Prompt input** | arg, or stdin (`-p -`). Piped stdin is capped at 10 MB [1] | arg, or stdin when the arg is omitted or is `-` [4] | args, `@file`, piped stdin prepended to the first prompt [7] | positional arg only (stdin not documented) [10][11] | positional args. Source also merges non-TTY stdin [12][S6] | `-p PROMPT`, or piped stdin when there is no `-p` [15] |
| **Streaming format** | `--output-format stream-json --verbose` (`--include-partial-messages` for deltas). Events: `system/init`, `assistant`, `user`, `stream_event`, `system/api_retry`, `result` [1] | `--json`: `thread.started`, `turn.started`, `item.*`, `turn.completed`, `turn.failed`, `error` [4] | `--mode json`: `session` header, then `agent_start`, `turn_*`, `message_start/update/end`, `tool_execution_*`, `agent_end`, `agent_settled`, retry and compaction events [7] | `--output-format stream-json` (`--stream-partial-output` for deltas): `system/init`, `user`, `assistant`, `tool_call` started/completed, `result` [10][11] | `--format json`: `step_start`, `text`, `tool_use`, `step_finish`, `error` (observed. The schema is not on the docs page) [12][S5] | `--output-format json` (JSONL). Event schema undocumented. Observed: `assistant.message_delta`, `assistant.message`, `tool.execution_start`, `session.usage_checkpoint`, `result` [15][S5] |
| **End of run** | final `result` line (`subtype`, `is_error`, `result`, `terminal_reason`). Exit 0 / non-zero, 143 on SIGTERM [1] | `turn.completed` / `turn.failed`. `-o FILE` writes the last message. Exit codes not documented [4] | `agent_end`, then `agent_settled`. Exits 0 when the model call fails, with `stopReason:"error"` on the last message [7][S1] | `result` with `subtype:"success"`, `is_error` [11] | the last `step_finish` has `reason:"stop"`. Exits 0 after `error` events [S1][S5] | terminal `result` with `exitCode` (observed) [S5] |
| **Structured result** | `--json-schema` gives `structured_output` in the result (absent if the model declined) [1] | `--output-schema FILE` [4] | none native: parse the final text | none native | none native | none native (`copilot workflow run --result-file` is a separate feature) [18] |
| **Resume** | `--resume <id>` or `--continue`. `--fork-session`, `--session-id <uuid>`, `--no-session-persistence` [1][19] | `codex exec resume <id>` or `--last`. `codex exec fork <id>`. `--ephemeral` [4][S1] | `--session <id>`, `--continue`, `--fork <id>`, `--session-id`, `--session-dir`, `--no-session` [7] | `--resume=<chatId>`, `--continue`, `create-chat`. No fork [11][S1] | `--session <id>`, `--continue`, `--fork`. `opencode export` / `import` [12] | `--resume=<id>`, `--continue`, `--session-id`. No fork. Bare `--resume` fails under `-p` [17] |
| **ID in stream** | `system/init.session_id` [S1] | `thread.started.thread_id` [S1] | `session.id` (first line) [7] | `session_id` on every event [S5] | `sessionID` on every event [S5] | `result.sessionId` [S1] |
| **Model flag** | `--model` (alias or full ID), `ANTHROPIC_MODEL`, `--fallback-model`, `--effort` [19] | `-m`, `-c model_reasoning_effort=…`, `--profile` [4][5][S1] | `--model provider/id[:thinking]`, `--provider`, `--thinking` [7] | `--model`. `agent models` lists them [11] | `--model provider/model`, `--variant`, `--agent` [12] | `--model` or `COPILOT_MODEL`, `--effort` [17] |
| **Providers** | Anthropic API, Bedrock (`CLAUDE_CODE_USE_BEDROCK=1`, plus Mantle), Vertex, Foundry, or a gateway via `ANTHROPIC_BASE_URL`. Claude models only [2][3] | OpenAI or ChatGPT login. Built-in `amazon-bedrock`, `amazon-bedrock-runtime`, `gcp`, `ollama`, `lmstudio`, custom `[model_providers.<id>]` with `wire_api="responses"` only. OpenRouter unconfirmed [5][6] | many built-in, including `OPENROUTER_API_KEY` and Bedrock (`AWS_PROFILE` or `AWS_BEARER_TOKEN_BEDROCK`). Custom via `models.json` [8][9] | Cursor account models. No BYOK documented [11] | many (AI SDK), including OpenRouter, Bedrock, and OpenAI-compatible endpoints via `opencode.json` [13] | GitHub-hosted models, or BYOK via `providers.json` (openai, azure, anthropic). `COPILOT_OFFLINE=true` [16][17] |
| **Headless auth** | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `apiKeyHelper`, `CLAUDE_CODE_OAUTH_TOKEN`, cloud creds [19] | `CODEX_API_KEY` (inline per call) or saved login [4] | provider env vars, or `auth.json` [8] | `CURSOR_API_KEY` or `--api-key` [10][11] | provider env vars, or `~/.local/share/opencode/auth.json` [12][13] | `COPILOT_GITHUB_TOKEN`, then `GH_TOKEN`, then `GITHUB_TOKEN` [17] |
| **Skip approvals (AFK)** | `--dangerously-skip-permissions` or `--permission-mode …`, `--permission-prompts none` [1] | `--sandbox workspace-write` or `danger-full-access`, `--dangerously-bypass-approvals-and-sandbox` (used by Sandcastle, not on the docs page) [4][S1] | no approval system: Pi runs tools without asking. `--approve` covers project trust only [S7] | `--force` / `--yolo`, `--trust`, `--sandbox` [11] | `--auto` (docs). Source treats `--yolo` and `--dangerously-skip-permissions` as aliases [12][S6] | `--allow-all-tools` / `--allow-all` / `--yolo`, `--no-ask-user` [17] |
| **Tokens** | per call on `assistant.message.usage`. Turn total on `result.usage` [S1][S5] | `turn.completed.usage` (turn total). Per-request figures only in the rollout JSONL `token_count` events [4][S1] | per message on `message_end.message.usage` [7] | `result.usage` (turn total. Observed, not documented) [S5] | per step on `step_finish.part.tokens` [S1][S5] | `session.usage_checkpoint` (per-model metrics, last-call tokens). Observed [S5] |
| **Cost** | `total_cost_usd` + `modelUsage[*].costUSD` (estimate). `--max-budget-usd` cap [1][19] | none. Compute from tokens [4] | `usage.cost.{input,output,cacheRead,cacheWrite,total}` [7] | none [11] | `step_finish.part.cost` (observed). Also `opencode stats` [12][S5] | premium requests / AI credits (`result.usage.premiumRequests`, `requests.cost`, `totalNanoAiu`). Not USD. `--max-ai-credits` [17][S5] |

## What a common harness interface would need to abstract

Sandcastle's `AgentProvider` interface is a working example of this boundary for all six harnesses [S1]. Its shape:

- **`buildPrintCommand(options) → { command, stdin? }`**: the adapter returns argv plus optional stdin. The Factory owns spawning. Options are `prompt`, skip-permissions, `resumeSession`, `forkSession`. Model and effort are fixed when the adapter is constructed (`claudeCode(model, {effort})`).
- **`parseStreamLine(line) → ParsedStreamEvent[]`**: normalises each JSONL line into a small union: `text`, `tool_call {name,args}`, `result`, `session_id`, `usage`, `context_window`, `error`.
- **`sessionStorage`**: harness-owned capture-to-host and restore-into-Sandbox logic, plus `canFork`.
- Optional `parseSessionUsage` / `parseSessionContextWindow`, for harnesses whose stream lacks a per-call figure (Codex).

Beyond that, the six harnesses differ in these ways the Factory will need to deal with:

1. **Invocation**: binary name, flags, and *prompt transport* (stdin or argv). The adapter must say whether it needs argv so the Factory can enforce the OS limit (32,767 chars on Windows) or write the prompt to a file the agent is told to read.
2. **Event normalisation**: map each schema to one Factory event set (text, tool call, final text, error, harness-session ID, usage). For display, each harness needs its own tool-name and argument extraction.
3. **Outcome detection**: a uniform outcome (`succeeded` / `failed` / `cannot-do + reason`) has to be built from exit code, stream errors and the agent's final text. Exit 0 does not mean success for Pi, OpenCode or Codex. The completion signal must work for harnesses without native schema output. Native `--json-schema` / `--output-schema` is available as an optimisation for two.
4. **Hanging processes**: a child (MCP server, `gh`) can hold stdout open after the agent is done. Sandcastle adds a completion timeout once the signal appears [S2]. Claude Code itself can wait up to 10 minutes for background tasks after the turn [1].
5. **Harness sessions**: an ID from the stream, plus resume and optional fork. A capability flag (`canFork`), and storage that differs per harness (Claude/Codex/Pi JSONL with cwd rewriting, Cursor SQLite `store.db`, OpenCode export/import, Copilot `events.jsonl`) [S1].
6. **Model and provider config**: kept separate from the harness, as the map wants. Few of the options are shared: some are flags (`--model`), some env (`CLAUDE_CODE_USE_BEDROCK`, `OPENROUTER_API_KEY`), some config files (`config.toml`, `opencode.json`, `models.json`, `providers.json`). Not every harness can reach every provider (the Providers row). The adapter probably needs to declare which providers it supports and how to wire each one: env, flag, or a generated config file.
7. **Credentials**: the env var names each harness reads, so the Factory can pass exactly those into the Sandbox and redact them (Copilot redacts `GITHUB_TOKEN` and `COPILOT_GITHUB_TOKEN` itself [17]).
8. **Permissions**: "no prompts, the Sandbox is the boundary" maps to a different flag per harness, and Pi has none.
9. **Usage and cost**: normalise to `{input, cacheRead, cacheWrite, output, costUsd?, costUnit?}` per Step. Each harness differs on three points: whether figures are per call or a turn total, whether cached tokens are a subset of input (Codex) or separate buckets (Claude), and whether cost is USD, a provider estimate, credits, or missing. A price table is needed for Codex and Cursor. Copilot reports credits, not USD.

### Options and trade-offs for the downstream decision (#17)

- **Mirror Sandcastle's `AgentProvider`** (argv+stdin builder plus line parser plus session storage). It is proven across all six harnesses and keeps us close to the reference implementation. Its gaps: no first-class cost and no structured outcome. "Last result wins" is a Sandcastle heuristic, and ADR 0019 notes Codex and OpenCode emit a `result` per message, so there is no reliable terminal event [S2].
- **Add capability flags** (`promptViaStdin`, `nativeSchemaOutput`, `canFork`, `reportsCost`, `supportedProviders`). This lets the Factory pick the best path per harness, such as native schema for Claude Code and Codex with a tag fallback for the rest, at the cost of more interface surface.
- **Use long-lived protocols** (Pi `--mode rpc`, OpenCode `serve`, Claude `--input-format stream-json`). Steps would not pay startup cost each time, but these are not available everywhere, and each needs its own process lifecycle. That is probably beyond v1.

## Unconfirmed / open

- Cursor reading the prompt from stdin: not documented [10][11]. Sandcastle treats it as argv-only.
- Copilot `--output-format json` event schema: the docs say only "JSONL" [15][17]. Everything listed above is observed [S5].
- Cursor `result.usage` and OpenCode `part.cost` are seen in recordings but not documented [11][S5].
- Codex via OpenRouter: depends on OpenRouter speaking the Responses API, which this research did not verify [6].
- Claude Code via OpenRouter: the docs say any gateway exposing a supported API format works, but Anthropic "doesn't support routing Claude Code to non-Claude models" [3]. OpenRouter is not named.
- Codex `--dangerously-bypass-approvals-and-sandbox`: used by Sandcastle, not on the non-interactive docs page [4][S1].
- OpenCode `--dangerously-skip-permissions`: present in the `dev` branch source as an alias of `--auto`. Not checked against a release tag [S6].
- Exit codes for Codex, Pi, Cursor, OpenCode and Copilot `-p` are not documented.

## Surfaced for the map

- **`--bare` becoming the default for `claude -p`** would silently stop subscription-OAuth (`CLAUDE_CODE_OAUTH_TOKEN`) headless runs [1][19]. This affects the credentials and flexible-auth note on the map. The adapter should pin its flags explicitly.
- **The prompt-size limit on Windows** (32,767 chars) affects how Ticket and Spec context are passed. It argues for stdin or a prompt file, not argv.
- **Pi has no permission system** [S7], so with Pi the Sandbox is the only safety boundary. This matters for Sandbox and host mode.

## Sources

Official docs and specs:

- [1] Claude Code, *Run Claude Code programmatically*: https://code.claude.com/docs/en/headless
- [2] Claude Code, *Amazon Bedrock*: https://code.claude.com/docs/en/amazon-bedrock
- [3] Claude Code, *Other LLM gateways*: https://code.claude.com/docs/en/llm-gateway
- [4] Codex, *Non-interactive mode*: https://learn.chatgpt.com/docs/non-interactive-mode (redirected from developers.openai.com/codex/noninteractive)
- [5] Codex, *Advanced config*: https://learn.chatgpt.com/docs/config-file/config-advanced
- [6] Codex, *Config reference* (`wire_api`): https://learn.chatgpt.com/docs/config-file/config-reference
- [7] Pi, `packages/coding-agent/docs/cli.md` and `json.md`, badlogic/pi-mono @ `642e30b` (package `@earendil-works/pi-coding-agent` 1.5.0)
- [8] Pi, `packages/coding-agent/docs/providers.md`, same commit
- [9] Pi, `packages/coding-agent/docs/security.md`, same commit
- [10] Cursor, *Headless CLI*: https://cursor.com/docs/cli/headless
- [11] Cursor, *Output format* and *Parameters*: https://cursor.com/docs/cli/reference/output-format, https://cursor.com/docs/cli/reference/parameters
- [12] OpenCode, *CLI*: https://opencode.ai/docs/cli/
- [13] OpenCode, *Providers*: https://opencode.ai/docs/providers/
- [15] GitHub, *Run Copilot CLI programmatically*: https://docs.github.com/en/copilot/how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically
- [16] GitHub, *Adding LLM models to Copilot CLI*: https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-llm-models
- [17] GitHub, *Copilot CLI command reference*: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference
- [18] GitHub, *Copilot CLI programmatic reference*: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-programmatic-reference
- [19] Claude Code, *CLI reference* and *Authentication*: https://code.claude.com/docs/en/cli-reference, https://code.claude.com/docs/en/authentication
- [20] Microsoft, *CreateProcessW* (`lpCommandLine` max 32,767 chars): https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw

Source code (Sandcastle v0.13.0, `mattpocock/sandcastle` @ `719347729bd0`):

- [S1] `src/AgentProvider.ts`: print commands, stream parsers, session storage, argv guards for all six harnesses
- [S2] `src/Orchestrator.ts` (`DEFAULT_COMPLETION_SIGNAL`), `docs/adr/0019-completion-timeout-for-hanging-process.md`
- [S3] `src/extractStructuredOutput.ts`
- [S4] `docs/adr/0021-resume-without-fork.md`, `0022-opencode-resume-via-export-import.md`
- [S5] `src/__fixtures__/context-window/*.jsonl`: recorded real streams per harness (2026-10-31)
- [S6] OpenCode source, `anomalyco/opencode` branch `dev`: `packages/cli/src/run/run.ts`, `packages/cli/src/commands/handlers/run.ts`
- [S7] = [9] (Pi "does not ask for approval before every tool call")
