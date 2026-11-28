# What each Step costs today

Baseline for the cost decisions on the map "Wayfinder: the target pipeline from ready Ticket to trusted PR" (#151), from the ticket "Measure what each Step costs today" (#161).

## Source

These are the only Run logs that exist: `fliesswerk-test/.fliesswerk/logs`, two Runs and four Tickets.

- Run `2026-11-19T17-22-37`: Ticket #196.
- Run `2026-11-20T15-32-44`: Tickets #202, #203, #204, one after another.

The Harness was Claude Code 2.1.323 on `claude-sonnet-5-5`, using a subscription (OAuth). Every Ticket is a one-function addition (`char_count`, `reverse_words`, `truncate`) to a toy Python package. Every Step passed first time: no review rejected anything, no fix loop ran, no merge conflict came up.

Numbers come from each Step's `result` line, cross-checked against the per-call `assistant` lines (deduplicated by `message.id`). The scripts were throwaway and weren't kept.

## Per Step

"Input" counts every input token, split into fresh / cache read / cache write. Fresh input was 4–6 tokens in every Step. "Largest context" is the biggest single call's input. Cost is Claude Code's `total_cost_usd`, a list-price estimate: the subscription covered it.

| Ticket | Step | Turns | Model calls | Wall time | Input | cache read | cache write | Output | Largest context | Cost |
|---|---|---|---|---|---|---|---|---|---|---|
| #196 | implement | 4 | 3 | 11.7 s | 56,543 | 49,855 | 6,682 | 999 | 19,433 | $0.042 |
| #196 | review | 4 | 3 | 8.7 s | 58,479 | 51,322 | 7,151 | 475 | 19,902 | $0.038 |
| #202 | implement *(cold cache)* | 4 | 3 | 10.0 s | 57,350 | 37,544 | 19,800 | 877 | 19,802 | $0.092 |
| #202 | review | 3 | 2 | 4.9 s | 38,232 | 31,496 | 6,732 | 344 | 19,483 | $0.034 |
| #203 | implement | 4 | 3 | 9.5 s | 56,500 | 49,810 | 6,684 | 678 | 19,435 | $0.039 |
| #203 | review | 4 | 3 | 6.4 s | 57,281 | 50,684 | 6,591 | 387 | 19,342 | $0.035 |
| #204 | implement | 4 | 3 | 11.1 s | 57,190 | 50,087 | 7,097 | 1,095 | 19,848 | $0.044 |
| #204 | review | 4 | 3 | 7.6 s | 59,226 | 51,642 | 7,578 | 568 | 20,329 | $0.041 |
| all | merge | – | – | – | 0 | – | – | 0 | – | $0 |
| all | verify | – | – | ~1 s | 0 | – | – | 0 | – | $0 |

**Merge and verify** ran in code. The merge Step only starts an agent (`internal/prompt/templates/merge.md`) on a conflict, and none came up, so it cost nothing and no agent merge was ever measured.

**Per Ticket:** about $0.07–0.08 with a warm cache, or $0.13 when the Run's first Step is cold. Each Ticket used about 115K input tokens and 1.0–1.7K output tokens. A three-Ticket Run cost about $0.28.

**Subscription share:** Claude Code writes a `rate_limit_event` line with window utilization. Over the three-Ticket Run, the 5-hour window moved from 0.00 to 0.01 and the 7-day window sat at 0.07. That's the only subscription-share signal the logs carry, and it's only reported to 0.01.

## Where the tokens and dollars go

Fitting `total_cost_usd` against the four token counts reproduces every Step exactly at $2/M fresh input, $0.10/M cache read, $4/M cache write (the 2× one-hour TTL; every write was `ephemeral_1h`) and $10/M output.

- **Cache writes are most of the cost.** A typical warm Step costs about $0.027 in writes, $0.008 in output and $0.005 in reads, so writes are roughly 65%. In the cold Step they were 86%.
- **The context is almost all fixed overhead.** Every Step's first call reads exactly **12,749** tokens from cache, then writes about 5.4–6.1K. The 12,749 tokens are Claude Code's own system prompt and tools, shared across Steps, worktrees and both Step types. The ~6K is the Step's prompt, which holds the Ticket. The agent's actual work then adds only 0.9–1.6K tokens before the Step ends. So about two thirds of every context is Claude Code itself.
- **Steps share Claude Code's prefix but not the Step prompt.** This answers the open inference in "How agent pipelines keep token cost down" (#157). Only the Run's first Step after a long pause pays for the whole 18.4K. Every Step pays a fresh write for its own ~6K prompt, and with the one-hour TTL that write costs 2× input.
- **The base prefix includes about 25 tools the Steps never use** (Cron*, Workflow, RemoteTrigger, WebSearch, WebFetch, SendMessage, EnterWorktree, …), plus a list of skills and slash commands. Each Step used only Bash and StructuredOutput.
- **Turns:** 3 model calls per Step (read, write, report), 2 in the shortest review. A Step's turns don't grow with the Ticket at this size.

## How it varies with the Ticket

**These logs can't answer that.** All four Tickets are about the same trivial size. Apart from the cold-cache Step (2.2×), every Step costs $0.034–0.044 (±15%). Review cost about as much as implement, because both are dominated by the same fixed ~19K context. A baseline for real Tickets (multi-file, failing checks, review rejections, fix loops, an agent merge) still needs a Run against a realistic project.

## Does `parse.go` count everything correctly?

**The Step totals are correct.** When the `result` line is present, `stepUsage` takes the cumulative usage and `total_cost_usd` from it. The per-call input, cache read and cache write sums match it exactly in all eight Steps.

Gaps:

1. **Per-call output tokens are wrong.** The streamed `assistant` lines carry partial `output_tokens`: 3–42 per call, summing to 36–82 per Step against the `result`'s 344–1,095. `stepUsage` only uses per-call usage when no `result` line arrives (a killed, timed-out or out-of-memory Step). In that case output is undercounted about 10–20× and there's no cost. Input is fine.
2. **"context" is the last call's input, not the largest.** They're the same here because context only grows. They would differ after a compaction.
3. **Not read at all:**
   - `rate_limit_event`: 5-hour and 7-day utilization, the only measurable "share of the subscription".
   - `num_turns`, `duration_api_ms`.
   - `modelUsage`: per-model usage and cost. It matters once a Step mixes models or spawns subagents. Only one model appears here, so it's unverified whether `result.usage` includes subagent tokens.
   - `cache_creation.ephemeral_1h` vs `ephemeral_5m`: the TTL decides whether a write costs 1.25× or 2×.
4. **The run log's "tokens N in" sums fresh input, cache reads and cache writes.** It's a poor cost proxy: a cache read is 1/40 the price of a one-hour write, and in a warm Step the reads are 88% of the "in" count but only about 12% of the cost.
