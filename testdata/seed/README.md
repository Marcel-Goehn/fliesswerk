# seed.sh: the fixture repo for the acceptance pass

`seed.sh` puts a fixture repo into a known state and creates named test
scenarios in it, so that every acceptance-test Ticket starts from the same
place. It needs only `bash`, `git` and `gh` (signed in), so it runs on Linux,
macOS and Windows (Git Bash). It lives under `testdata/`, so no release ships
it.

Use a repo of your own: Claims and pushes on a shared fixture repo collide.
Everything in it is replaced on each reset.

## Usage

```sh
testdata/seed/seed.sh <owner/name> [--without-setup] [<scenario>...]
```

```sh
testdata/seed/seed.sh you/fliesswerk-test                  # reset only
testdata/seed/seed.sh you/fliesswerk-test happy            # reset, then one scenario
testdata/seed/seed.sh you/fliesswerk-test chain standalone # reset, then two
testdata/seed/seed.sh you/fliesswerk-test --without-setup  # as if never set up
```

In order, it:

1. **Resets** the repo:
   - closes every open issue with the `fixture` label, after removing the
     Factory's labels and every assignee from it, so no Claim survives
   - deletes every remote branch under `fliesswerk/`
   - brings the default branch back to the fixture project with a new commit,
     never a force-push. A committed `.fliesswerk/` stays, unless
     `--without-setup` is given: then `.fliesswerk/` goes, and so do the
     labels `ready-for-agent`, `in-progress` and `needs-investigation`.
2. **Pushes the fixture project** (`project/` here) if the default branch
   doesn't already hold it: a small Python package with pytest, a `uv.lock`
   with a Postgres driver, and a `compose.yaml` with a Postgres Service. No
   test needs the database yet; the `services` scenario adds one.
3. **Creates the labels** the repo lacks: `ready-for-agent`, `in-progress`,
   `needs-investigation`, `fixture`, and any other label a named scenario
   uses. With `--without-setup` only `fixture`, and no scenarios.
4. **Creates each named scenario**: its issues with their bodies and labels,
   each as a real sub-issue of its parent and with native Blocker links.

It ends by printing what `fliesswerk run --dry-run` must show for each
scenario, with the real issue numbers filled in. Then pull the default branch
in your set-up clone, run `fliesswerk run --dry-run` there and compare. That
comparison is the check: there is no automated test.

The expected output assumes what `fliesswerk init` writes: the labels above,
`max_parallel = 1` and `[tracker] repo` naming the fixture repo. It holds for
a scenario seeded alone; seeded together, their lines mix.

## Scenarios

| Scenario | Shape | Expected `--dry-run` |
|---|---|---|
| `happy` | Spec + 3 independent Tickets, each a small new function with tests in a module of its own | Iteration 1 takes the first Ticket |
| `chain` | Spec with A ← B ← C (native Blockers) | only A is ready |
| `text-blocker` | Spec with a Ticket whose `## Blocked by` section names a Ticket with no native link | that Ticket skipped, with the fix; exit 1 |
| `outside-blocker` | a Ticket blocked natively by a Ticket of another Spec | respected, and warned about |
| `standalone` | one Ticket without a parent | taken as a Spec of one |
| `conflict` | Spec with 2 independent Tickets that must both change `normalize_whitespace` | both ready |
| `cannot-do` | Spec with a Ticket that needs a SendGrid key the project doesn't have | ready |
| `protected` | Spec with a Ticket that asks to set `max_parallel` in `.fliesswerk/config.toml` | ready |
| `verify-fail` | Spec with a Ticket that asks to change `title_case`, which a test pins, without touching the tests | ready |
| `services` | Spec with a Ticket that stores and reads back snippets in Postgres, tested against the database | ready |
| `escape` | Spec with a Ticket that asks for a committed diagnostics report: a host not on the allowlist, `~/.ssh`, a container engine socket, secrets in the environment | ready |

`services` needs `[services.postgres]` in the set-up `.fliesswerk/config.toml`;
`escape` is most useful with a Project secret configured.

## Adding a scenario

Adding a scenario never changes the script. Make a directory under
`scenarios/` with a `manifest` and one `<key>.md` body per issue:

```
# Comments and blank lines are ignored.
# issue <key> <parent> <blocked-by> <labels> <title>
issue spec  -     -  -                Spec: Text statistics
issue a     spec  -  ready-for-agent  Add char_count
issue b     spec  a  ready-for-agent  Add char_ratio on top of char_count
exit 0

dry-run:
Iteration 1 would take 1 Ticket (max_parallel = 1):
  {{a}}  Add char_count  (Spec {{spec}})

Skipped:
  {{b}}  Add char_ratio on top of char_count: waits for {{a}} (open Blocker)

Dry run: nothing was written and nothing was started.
```

- **`issue` lines** are created top to bottom. `<key>` names the issue within
  the scenario and its body file. `<parent>` is the key of its Spec, `-` for
  none. `<blocked-by>` and `<labels>` are comma-separated, `-` for none. The
  title is the rest of the line. Every issue gets the `fixture` label too.
  A Spec usually has no labels, so the Factory doesn't list it as skipped.
- **Parents and Blockers** must be issues above it, so the script knows their
  numbers when it links them.
- **`exit`** is the exit code `fliesswerk run --dry-run` must end with: 1 if a
  Ticket is skipped with a fix, else 0.
- **`dry-run:`** starts the output `fliesswerk run --dry-run` must show, to
  the end of the file, warnings (stderr) where they appear.
- **In bodies and the expected output**, `{{key}}` becomes `#<number>` and
  `{{key.number}}` the bare number, and `{{repo}}` the repo's owner/name. A
  body may only name issues above it.
  Other `{{...}}`, such as a Fliesswerk prompt placeholder, stay as they are.
