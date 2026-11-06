# Draft prompts (for "How do prompts take in a project's conventions?")

These are drafts to read and react to, not final wording.

Each file shows the **whole prompt as the agent receives it**, so you can read it
in one piece. Two kinds of block are marked:

- `🔒 FIXED`: added by the Factory's code. In the real setup this text is **not in
  your file** and you can't edit it. Text in `«guillemets»` is filled in by code
  at the start of each Step.
- `✏️ YOURS`: the content of `.kraftwerk/prompts/<step>.md`. You can edit all of it.

## Placeholders in your part

When a Step starts, the Factory reads your file, replaces each placeholder, adds
the fixed blocks and pipes the result to the harness. Only simple replacement: no
logic and no shell commands. An unknown placeholder is an error in `check`.

| Placeholder | Becomes |
|---|---|
| `{{commands}}` | the whole Feedback commands section: heading, one sentence saying when to run them (worded per Step) and the `[commands]` map from `config.toml` as a list, e.g. ``- test: `go test ./...` ``. Nothing at all if `[commands]` is empty |
| `{{commands.test}}` | just that one command (one placeholder per name in `[commands]`) |
| `{{standards}}` | only if `standards = "<path>"` is set in config: a short paragraph telling the agent to read that file and that it wins over the built-in code-smell list. Without it: nothing |

## One loop, three prompts

A sequential Run and a parallel Run use the **same** implement and review prompts.
Every Iteration works alone on its own Ticket branch, whether or not other
Iterations are running. `merge` runs only when two parallel Tickets in one Spec
conflict, which can't happen in a sequential Run.

## Still open in these drafts

- The "Done" footer: decided in "How does the loop work?".

## To revisit after real Runs

- When to run Feedback commands: "before every commit" may be too slow for big test
  suites. Matt Pocock's `/implement` runs typecheck and single test files regularly
  and the full suite once at the end.

- No "you are working alone, nobody will answer" line. If Iterations end because
  the agent stops to ask a question, add one line to the fixed Rules: "Don't stop
  to ask questions: nobody will answer. Where the Ticket is unclear, decide, and
  note the decision in your commit message."
