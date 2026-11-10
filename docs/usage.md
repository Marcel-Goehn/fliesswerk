# Running the Factory

This guide covers what happens in a Run and how to steer it: `kraftwerk run` and `--dry-run`, stopping with Ctrl-C, exit codes, the terminal view, the logs and `summary.json`, Blockers, Claims, and what to do after a rejected push.

It assumes a project that `kraftwerk init` has set up. See the [setup guide](setup.md).

## Before a Run: Specs, Tickets and labels

The Factory works Tickets: open issues with the ready label (default `ready-for-agent`) that are not Specs. A Spec is any issue with sub-issues. It is never worked on itself, even if it has the ready label: its sub-issues are its Tickets, and its body is part of every one of its Tickets' prompts. An issue with no parent is a standalone Ticket, worked like a Spec with one Ticket.

A Ticket is ready when it is open, has the ready label, is not a Spec, and every Blocker is closed, whatever the reason it was closed for.

### Setting native blocking links

Blockers come only from GitHub's own issue dependencies, never from text. Set them on the Ticket that waits:

- on github.com: open the Ticket, then under **Relationships** choose **Mark as blocked by** and pick the Blocker
- or with `gh`: `gh issue edit 43 --add-blocked-by 42`

A Ticket whose body names a Blocker in a `## Blocked by` section or on a `Blocked by:` line, with no matching link, is skipped. The dry run, `check` and the Run's summary name it and print the `gh issue edit … --add-blocked-by …` command that fixes it.

A Blocker outside the Ticket's Spec is respected: the Ticket waits for it to close. The Run warns, though, because that Blocker's code is on another branch and not on this Spec's branch. Keep a Spec's Tickets depending only on each other and on the base branch.

There is no cycle detection: Tickets that block each other never become ready.

## Starting a Run

```sh
kraftwerk run          # every eligible Spec and standalone Ticket
kraftwerk run 31       # only Spec #31, or standalone Ticket #31
```

A Run checks the config and refuses to start on an error. Then it takes the checkout's Run lock, prunes old logs, cleans up what a crashed Run left behind, replays any Tracker writes that failed last time, resolves every credential and tests the ones the Factory uses itself (the Tracker's, and AWS for Bedrock), runs the `run_start` Hook and starts shared Services. Then it works Iteration after Iteration, and finally runs the `run_end` Hook.

Only one Run at a time works in a checkout. A second one is refused with the PID of the first. A Run lock left by a Run that is no longer running is taken over.

### An Iteration

An Iteration takes up to `max_parallel` ready Tickets (default 1), in this order:

1. Specs you have claimed, in the order you claimed them, then the rest by Spec number
2. Tickets by number within a Spec

That is "finish what's started": a Spec gets finished before the next one starts. Each Ticket goes through its Steps on its own:

1. A fresh Sandbox, with its own worktree on a Ticket branch cut from the tip of the Spec branch, and its own Services
2. The `sandbox_created` Hook
3. **implement**: the agent writes the code, commits, and reports `done` or `cannot_do`
4. **review**, in a fresh session, if `[steps.review] enabled` and implement made commits: it either approves or commits fixes
5. **verify**: the Factory runs the Feedback commands in `verify` itself, after the last Step that changed code
6. The Spec's merge queue: merged into the Spec branch by code, or by the **merge** Step on a real conflict, then pushed, then the Ticket is closed with a comment that names the summary, the commits and the branch

After every committing Step, the Factory checks that no Protected path was changed. When every Ticket in the Iteration has finished, the Factory reads the Tracker again and the next Iteration takes the Tickets that are ready now. A Run ends when nothing is ready, after `max_iterations` (default 10), or when it is stopped.

### Branches

| Branch | Name | Kept |
| --- | --- | --- |
| Spec branch | `kraftwerk/spec-<N>-<slug>` | Always. It is the deliverable |
| Standalone Ticket's branch | `kraftwerk/ticket-<N>-<slug>` | Always |
| Ticket branch inside a Spec | `kraftwerk/ticket-<N>` | Until the Ticket lands. It is never pushed |

`kraftwerk` is `[branches] prefix`. A Spec branch is cut from the remote `base_branch` the first time, and pushed after every Ticket that lands. Closed always means the code is on GitHub. An existing Spec branch, local or remote, is found by its number and continued, so a Spec can be handed over or moved to another machine.

All the Factory's work happens in its own worktrees under `.kraftwerk/state/`. Your checkout is never touched: keep working on any branch, even with uncommitted changes. The Factory never rebases a Spec branch, never merges `base_branch` into it, never force-pushes and never pushes `base_branch`.

The Factory doesn't open pull requests. Open one when a Spec is finished, or have a `spec_finished` Hook do it. Host Hooks run with your own `gh` login:

```toml
[[hooks.spec_finished]]
run = 'gh pr create --fill --head "$KRAFTWERK_BRANCH"'
```

### When a Ticket fails

| What happened | What the Factory does |
| --- | --- |
| The agent reports `cannot_do` | Swaps the ready label for `cannot_do_label` (default `needs-investigation`), comments with the reason, and discards the work |
| An error, no report, a timeout, a failed `verify` or a failed `sandbox_created` | Retries the Step once in a fresh session. After a failed `verify`, implement retries with the failing output in its prompt. If that fails too, it hands the Ticket back as for `cannot_do` |
| implement reports `done` with no commits | Hands the Ticket back at once, as "reported done without changes" |
| A Protected path was changed | Hands the Ticket back at once, with a comment naming the files |
| The merge failed, or `verify` failed on the merged result | Discards the work and re-implements the Ticket fresh from the updated Spec branch in the next Iteration. A second failure hands it back |
| A transient error, such as an outage | Retries the Step 3 times with backoff, then stops the Run and leaves the Ticket untouched |
| An authentication error | Stops the Run at once, with no retry and once for the whole Run, says how to fix the credential, and leaves the Ticket untouched. Claude Code's credential is first checked here, at the first Step |

A handed-back Ticket's comment ends with the last 30 lines of its log in a collapsed **details** block, or the failing command's last 30 lines after a failed `verify`, plus the Run id and the local log path. The Tickets it blocks stay blocked, and the rest of the Spec goes on. To try again, fix the cause, then swap `needs-investigation` back for `ready-for-agent`.

A Tracker write that still fails after its retries is queued in `.kraftwerk/state/` and replayed at the start of the next Run, so a Ticket that was closed but not recorded never runs twice.

## Previewing with `--dry-run`

```sh
kraftwerk run --dry-run
kraftwerk run --dry-run 31
```

The dry run checks the config, reads the Tracker and prints:

- the Tickets Iteration 1 would take
- the Tickets that are ready, but for a later Iteration
- every skipped Ticket with the reason and, where there is one, the fix
- the Specs other people have claimed

It writes nothing and starts nothing: no Sandbox, no branch, no label and no token spent. It exits with 1 if a skipped Ticket needs a fix, otherwise 0.

## Stopping a Run

- **The first Ctrl-C** stops new work. The Tickets in flight finish, including their merge and push, and the Run ends.
- **The second Ctrl-C** kills every Step now. The work in flight is discarded and its Tickets are left exactly as they were, still ready. Claims stay. A Ticket that has already been pushed still gets closed, so its code never sits on GitHub with the Ticket open.

Either way the `run_end` Hook runs and the summary is written. The next Run cleans up the Sandboxes, worktrees and Ticket branches left behind.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Nothing is left that needs a person: nothing more was ready and nothing failed, or the Run reached `max_iterations` |
| 1 | Some Tickets failed, were handed back with `cannot_do`, or were skipped and need a fix |
| 2 | The Run was stopped: an authentication error, a transient error that outlasted its retries, a failed `run_start` Hook, Ctrl-C, an unreadable Tracker, or another Run holding the checkout's Run lock |
| 3 | The config is invalid, `check` failed, or the project isn't set up for a Run |

`kraftwerk check` uses 0 and 3.

## Watching a Run

A Run starts with a banner: the version, the Run id and log directory, the Harness and auth method, the Sandbox kind and the limits. Then it prints plain lines, one per event and one per tool call the agent makes:

```
14:03:41  #42  implement  started on kraftwerk/ticket-42
14:03:52  #42  implement  ▸ Read src/cart.go
14:07:10  #42  implement  finished in 3m29s, tokens 184210 in, 6120 out, context 52344, $0.84
```

Each Step's end line shows its duration, tokens, its context window (the input tokens of its last model call) and its cost as the Harness reports it. A Step that has been silent for half its idle timeout gets a warning. Lines are coloured only on a terminal, and never when `NO_COLOR` is set.

| Flag | Effect |
| --- | --- |
| `--verbose` | Also shows the agent's own text |
| `--json` | Prints the summary as JSON to stdout when the Run ends, and the live lines to stderr |

Local-only settings can also come from flags, which override both config files for one Run: `--sandbox`, `--i-accept-unsandboxed-risk`, `--allow` (repeat it for more hosts), `--model` and `--max-parallel`.

### The summary

When a Run ends it prints a summary, leaving out empty sections: the duration, Iterations and exit code, then the Tickets closed (with commits, largest context window and cost), failed (with the reason), stopped and skipped (with the fix), how many Tickets were still ready at the limit, the Specs finished, memory, writes queued for replay, the tokens and cost per Ticket and for the Run, and the log directory.

Cost is shown in the unit the Harness reports and never converted. Mixed units are summed separately, such as `$3.10 + 12 credits (2 Steps reported no cost)`. A cost the Harness doesn't report is never made up.

## The log directory

Every Run gets its own directory, `.kraftwerk/logs/<run-id>/`, named after its start time, such as `2026-11-07T14-03-12`. `kraftwerk logs` prints the latest one.

| File | Holds |
| --- | --- |
| `run.log` | Every line, always verbose, without colour |
| `<ticket>/<step>-<attempt>.jsonl` | The Harness's raw output of one Step attempt, such as `42/implement-2.jsonl` |
| `<ticket>/<step>-<attempt>.prompt.md` | The exact prompt of that attempt |
| `summary.json` | The summary |

A merge Step's files go under the Ticket whose merge it resolved. Everything in the directory passes through the redactor first: every known secret, also in its base64 and URL-encoded forms, becomes `***`. The same goes for the terminal and every Tracker comment. Transcripts the Harness writes elsewhere by itself are not covered.

### Keeping logs

Each Run, when it starts, removes the Run directories older than `[logs] keep_days`, 30 by default. Set it to `"forever"` to keep everything:

```toml
[logs]
keep_days = "forever"
```

Only directories named like Run ids are removed. Anything else in `.kraftwerk/logs/` is left alone.

### `summary.json`

`summary.json`, and the output of `run --json`, has `"schema": 1`. Releases only ever add fields to it. A field is never renamed or removed, and its meaning never changes, while `schema` stays 1. Every list is present, empty if there is nothing in it.

| Field | Holds |
| --- | --- |
| `schema` | `1` |
| `run_id`, `started_at`, `duration_seconds`, `iterations` | The Run |
| `exit_code`, `exit_meaning` | How it ended |
| `closed` | `id`, `title`, `spec` (empty for a standalone Ticket), `commits`, `largest_context_window`, `cost_display` |
| `failed` | `id`, `title`, `reason`, `kind`, `largest_context_window` |
| `stopped` | `reason` (empty unless the whole Run stopped) and `specs`: `id`, `branch` and `fix` for each Spec a rejected push stopped |
| `skipped` | `text_only_blockers` (`id`, `title`, `reason`, `fix`), `claimed_by_others` (`id`, `title`, `by`, `at`) and `outside_blockers` |
| `still_ready` | How many Tickets were still ready when the Run reached `max_iterations` |
| `specs_finished` | `id`, `title`, `branch` |
| `memory` | `peak_estimate_mb`, `waits`, or `null` when nothing was measured |
| `queued_writes` | Tracker writes waiting to be replayed |
| `tickets` | `id` and `usage` for each Ticket that ran a Step |
| `usage` | The Run's `input`, `output`, `cache_read`, `cache_write`, `cost` (by unit), `steps_without_cost` and `cost_display` |
| `log_dir` | The Run's log directory |

A failure's `kind` is one of `cannot_do`, `no_changes`, `no_report`, `harness_error`, `verify`, `merge`, `protected_path`, `sandbox_created`, `push_rejected`, `out_of_memory` or `factory`.

## Claims and `release`

The first time a Run takes a Ticket from a Spec, it claims the Spec: it adds the `in_progress_label` and assigns the Spec to the user of the GitHub token. A standalone Ticket is claimed itself. The Claim lasts across Runs until the Spec's last Ticket closes.

Everyone else's Runs skip a claimed Spec. `check`, the dry run and the summary list it with the claimer and the date. Your own Runs take your claimed Specs first.

To hand a Spec over on purpose, release it:

```sh
kraftwerk release 31
```

That removes the label and the assignee, and the next Run of whoever starts one takes it, continuing the Spec branch where it is. There is no timeout: a Claim whose owner has gone stays until someone releases it.

## Never push to a Spec branch the Factory owns

While a Spec is claimed, its Spec branch belongs to the Factory. Don't push to it, and don't rebase or force-push it. To change what a Ticket did, comment on the Spec, open a new Ticket for the fix, or wait until the Spec is finished and change it in its pull request.

The Factory never overwrites anyone's work. If someone pushes to the Spec branch anyway, the next push of that branch is rejected, and:

- that Spec stops for this Run. Its Tickets in flight are killed and left untouched, still ready
- the summary lists the Spec under Stopped, with the fix
- the other Specs go on

To continue:

1. Decide whether the commits that were pushed should stay. If not, remove them from the remote branch yourself.
2. Bring your local copy of the Spec branch up to date, as the summary says: `git fetch origin <branch>:<branch>`
3. Start a new Run. It continues from the branch as it is on GitHub.
