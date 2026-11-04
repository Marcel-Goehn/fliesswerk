<!-- 🔒 FIXED: added by the Factory. Not in your file. -->

# Task

Implement Ticket «number»: «title»

Work only on this Ticket. Its full text, its comments and the Spec it belongs to
are below, so you don't need to fetch them.
<!-- Standalone Ticket (no parent): "Its full text and its comments are below, so
     you don't need to fetch them." and the whole Spec section is left out. -->

<!-- Prompt rendered for a Ticket, depending on its shape:
     - has a parent issue        → Task + Spec section (this file as shown)
     - no parent                 → Task only, no Spec section, no "Other Tickets"
     - one big issue, no children → same as "no parent": it is a standalone Ticket -->


<ticket>
«Ticket body»
</ticket>

<ticket-comments>
«every comment, oldest first, with author and date»
</ticket-comments>

<!-- The Spec section is only present when the Ticket belongs to a Spec. -->

# The Spec

Your Ticket is part of Spec «number»: «title». The Spec describes the whole
feature. Use it to understand why your Ticket exists and how it fits in, but
implement only what your Ticket asks for. The rest of the Spec is other Tickets'
work.

<spec>
«Spec body»
</spec>

## Tickets in this Spec

- «number» «title» (closed)
- «number» «title» (closed)
- «number» «title» (this Ticket)
- «number» «title» (in progress)
- «number» «title» (open)

Closed Tickets are finished: don't redo them. In progress and open Tickets are
someone else's work: don't do them, even if they look easy to include.

# Recent commits

<recent-commits>
«the commits on the Spec branch since base_branch, full messages, no merges.
None yet (first Ticket of a Spec, or a standalone Ticket): the last 10 commits of base_branch»
</recent-commits>

<!-- /🔒 FIXED -->

<!-- ✏️ YOURS: .ralph/prompts/implement.md -->

# Explore

Explore the repository until you understand the code this Ticket touches. Pay extra
attention to the test files around it.

# Implement

Use test-driven development. If the Ticket or the Spec names seams to test at,
test there.

1. RED: write one test for the next piece of behaviour and watch it fail.
2. GREEN: write the least code that makes it pass.
3. Repeat until the Ticket is done.
4. REFACTOR the code.

{{commands}}
<!-- Renders as (nothing at all if [commands] is empty):

# Feedback commands

Run these before every commit and make them pass:

- test: `go test ./...`
- typecheck: `go vet ./...`
- lint: `golangci-lint run`
-->

# Commit

Commit when the Ticket is done. The commit message must:

1. Include the Ticket completed + Spec reference if provided
2. Key decisions made
3. Files changed
4. Blockers or notes for the next Iteration

Keep it concise.

<!-- /✏️ YOURS -->

<!-- 🔒 FIXED -->

# Do not close the Ticket

The Factory closes it after your work has been merged.

# Rules

- You are on the branch `«Ticket branch»`, in a worktree made for this Ticket.
  Commit your work here. Don't push, switch branches, rebase or rewrite existing commits.
- Never change these paths: «Protected paths». Work that changes them is rejected.
- Do not leave commented-out code or TODO comments in committed code.
- You may read GitHub with `gh`, but never edit, label or comment on issues or
  pull requests.

# Done

«How to report "done" or "this Ticket can't be done, because …".
Decided in "How does the loop work?".»

<!-- /🔒 FIXED -->
