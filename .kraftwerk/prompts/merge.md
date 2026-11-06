<!-- 🔒 FIXED: added by the Factory. Not in your file. -->
<!-- Runs only in a parallel Run, when code finds a real conflict between two
     Tickets of one Spec. Clean merges never reach an agent. A standalone Ticket
     has its own branch and never conflicts, so merge always has a Spec. -->

# Task

Resolve the merge conflict between Ticket «number»: «title» and the work already
on the Spec branch. The result must do what every Ticket involved asks for.

You are on the Spec branch `«Spec branch»`, with a merge of `«Ticket branch»` in
progress. These files conflict:

- «conflicting file»
- «conflicting file»

The Tickets involved and their Spec are below, so you don't need to fetch them.

# The Ticket being merged

<ticket number="«number»" title="«title»">
«Ticket body»
</ticket>

# The Tickets it conflicts with

<!-- Only the Tickets merged into the Spec branch since this Ticket started whose
     commits touched the conflicting files. Code finds them with git log. -->

<ticket number="«number»" title="«title»">
«Ticket body»
</ticket>

# The Spec

The Tickets are part of Spec «number»: «title». Use it to understand how their
changes should fit together.

<spec>
«Spec body»
</spec>

<!-- /🔒 FIXED -->

<!-- ✏️ YOURS: .kraftwerk/prompts/merge.md -->

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

# Do not close the Ticket

The Factory closes it after the merge.

# Rules

- Resolve the conflicts only. Don't add features or refactor beyond what the
  resolution needs.
- Don't push, switch branches, rebase or abort the merge.
- Never change these paths: «Protected paths». Work that changes them is rejected.
- Do not leave commented-out code or TODO comments in committed code.
- You may read GitHub with `gh`, but never edit, label or comment on issues or
  pull requests.

# Done

«How to report "resolved" or "can't resolve, because …".
Decided in "How does the loop work?".»

<!-- /🔒 FIXED -->
