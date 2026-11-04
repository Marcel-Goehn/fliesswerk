<!-- 🔒 FIXED: added by the Factory. Not in your file. -->

# Task

Review Ticket «number»: «title»

Another agent has just implemented this Ticket. Review and improve only this
Ticket's change. Its full text, its comments and the Spec it belongs to are below,
so you don't need to fetch them.
<!-- Standalone Ticket (no parent): "Its full text and its comments are below, so
     you don't need to fetch them." and the whole Spec section is left out. -->

<ticket>
«Ticket body»
</ticket>

<ticket-comments>
«every comment, oldest first, with author and date»
</ticket-comments>

<!-- The Spec section is only present when the Ticket belongs to a Spec. -->

# The Spec

The Ticket is part of Spec «number»: «title». The Spec describes the whole
feature. Use it to judge whether the change fits, but review only what this
Ticket asks for. The rest of the Spec is other Tickets' work.

<spec>
«Spec body»
</spec>

## Tickets in this Spec

- «number» «title» (closed)
- «number» «title» (closed)
- «number» «title» (this Ticket)
- «number» «title» (in progress)
- «number» «title» (open)

Closed Tickets are finished. In progress and open Tickets are someone else's
work: don't add them, even if they look easy to include.

# What changed

The work to review is every commit on this branch since `«base commit»`.
Read it with:

- `git log «base commit»..HEAD`
- `git diff «base commit»..HEAD`

<!-- /🔒 FIXED -->

<!-- ✏️ YOURS: .ralph/prompts/review.md -->

# Review

Go through the change in this order:

1. **Does it do what the Ticket asks, within the Spec?** Fix anything missing or
   wrong. Check that the tests cover the behaviour the Ticket describes, at the
   seams the Ticket or the Spec names, if any.

2. **Clarity.** Look for chances to:
   - reduce unnecessary complexity and nesting
   - remove redundant code and abstractions
   - use clear names for variables and functions
   - bring related logic together
   - remove comments that only restate the code
   - prefer explicit code over clever, compact code

3. **Keep the balance.** Don't:
   - over-simplify until the code is harder to read
   - merge unrelated concerns into one function
   - remove abstractions that help organise the code
   - break anything that already works

{{standards}}
<!-- Renders as (nothing at all if `standards` isn't set in config):

# Coding standards

Refactor the change until it follows the coding standards in
`docs/coding-standards.md`. Read that file before you start.
-->

{{commands}}
<!-- Renders as (nothing at all if [commands] is empty):

# Feedback commands

After your changes, run these and make them pass:

- test: `go test ./...`
- typecheck: `go vet ./...`
- lint: `golangci-lint run`
-->

# Commit

If you changed anything, commit with a message starting `Review:` that says what
you changed and why. If the change is already good, change nothing.

<!-- /✏️ YOURS -->

<!-- 🔒 FIXED -->

# Do not close the Ticket

The Factory closes it after this Ticket's work has been merged.

# Rules

- You are on the branch `«Ticket branch»`. Commit here. Don't push, switch
  branches, rebase or rewrite existing commits.
- Never change these paths: «Protected paths». Work that changes them is rejected.
- Do not leave commented-out code or TODO comments in committed code.
- You may read GitHub with `gh`, but never edit, label or comment on issues or
  pull requests.

# Done

«How to report "approved" (no commits) or "fixed" (commits).
Decided in "How does the loop work?".»

<!-- /🔒 FIXED -->
