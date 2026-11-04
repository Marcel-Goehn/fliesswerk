<!-- 🔒 FIXED: added by the Factory. Not in your file. -->
<!-- Runs only in a parallel Run, when code finds a real conflict between two
     Tickets of one Spec. Clean merges never reach an agent. -->

# The conflict

Two Tickets of the Spec below changed the same code. You are on the Spec branch
`«Spec branch»`, with a merge of `«Ticket branch»` in progress. These files
conflict:

- «conflicting file»
- «conflicting file»

# The Ticket being merged

«Ticket number and title»

«Ticket body»

# Tickets merged into the Spec branch since that Ticket started

«number and title»

«body»

# The Spec

«Spec number and title»

«Spec body»

# Do not close the Ticket

The Factory closes it after the merge.

# Rules

- Resolve the conflicts only. Don't add features or refactor beyond what the
  resolution needs.
- Don't push, switch branches, rebase or abort the merge.
- Never change these paths: «Protected paths».
- You may read GitHub with `gh`, but never edit, label or comment on issues or
  pull requests.

<!-- /🔒 FIXED -->

<!-- ✏️ YOURS: .ralph/prompts/merge.md -->

# Resolve

For each conflicting file, read both sides and the Tickets they came from. Keep the
intent of both Tickets: the result must do what each of them asked for.

{{commands}}
<!-- Renders as (nothing at all if [commands] is empty):

# Feedback commands

After resolving, run these and make them pass:

- test: `go test ./...`
- typecheck: `go vet ./...`
- lint: `golangci-lint run`
-->

# Commit

Stage the resolved files and finish the merge with `git commit --no-edit`.

<!-- /✏️ YOURS -->

<!-- 🔒 FIXED -->

# Done

«How to report "resolved" or "can't resolve, because …".
Decided in "How does the loop work?".»

<!-- /🔒 FIXED -->
