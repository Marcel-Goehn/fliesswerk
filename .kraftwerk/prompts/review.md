<!-- 🔒 FIXED: added by the Factory. Not in your file. -->

# Task

Review the code changes for Ticket «number»: «title». Check that they do what the
Ticket asks, and improve how the code is written. Fix what you find yourself and
commit it.

The Ticket's full text, its comments and the Spec it belongs to are below, so you
don't need to fetch them.
<!-- Standalone Ticket (no parent): "The Ticket's full text and its comments are
     below, so you don't need to fetch them." and the whole Spec section is left
     out. Review then checks against the Ticket alone. -->

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

<!-- ✏️ YOURS: .kraftwerk/prompts/review.md -->

# Review

Review the change along two axes, one after the other. Keep them apart: code that
is well written but does the wrong thing fails, and so does code that does the
right thing but is badly written.

## 1. Does it do what the Ticket asks?

Compare the change with the Ticket, and with the Spec if there is one. Look for:

- requirements the Ticket asks for that are missing or only partly done
- requirements that look implemented, but where the implementation is wrong
- behaviour the Ticket didn't ask for: remove it
- edge cases that aren't handled
- new or changed behaviour without a test, or tests that aren't at the seams the
  Ticket or the Spec names
- security problems: injection, leaked credentials, unchecked input

Fix what you find.

## 2. Is it well written?

{{standards}}
<!-- Renders as (nothing at all if `standards` isn't set in config):

Follow the coding standards in `docs/coding-standards.md`. Read that file first.
Where they disagree with the smells below, the standards win.
-->

Check the change for these code smells. Each one is a judgement call, not a hard
rule. Skip anything the project's tooling already enforces.

- **Mysterious Name**: a function, variable or type whose name doesn't reveal what
  it does or holds. → Rename it.
- **Duplicated Code**: the same logic appears in more than one place in the
  change. → Extract it and call it from both places.
- **Feature Envy**: a function that reaches into another object's data more than
  its own. → Move it to the data it uses.
- **Data Clumps**: the same few fields or parameters keep travelling together.
  → Bundle them into one type.
- **Primitive Obsession**: a string or number standing in for a domain concept.
  → Give the concept its own small type.
- **Repeated Switches**: the same `switch` or `if` chain on the same type appears
  more than once. → Replace it with polymorphism or one shared map.
- **Shotgun Surgery**: one logical change needs scattered edits across many files.
  → Gather what changes together into one module.
- **Divergent Change**: one file is changed for several unrelated reasons.
  → Split it so each part changes for one reason.
- **Speculative Generality**: abstractions, parameters or hooks for needs the
  Ticket doesn't have. → Delete them.
- **Message Chains**: long `a.b().c().d()` navigation. → Hide it behind one
  function on the first object.
- **Middle Man**: a function or type that mostly just passes calls on.
  → Remove it and call the real target directly.
- **Refused Bequest**: a type that ignores most of what it inherits or implements.
  → Use composition instead.

Refactor what you find, without changing the behaviour the Ticket asks for.

## Keep the balance

Don't:

- over-simplify until the code is harder to read
- merge unrelated concerns into one function
- remove abstractions that help organise the code
- change code outside this Ticket's change
- break anything that already works

{{commands}}
<!-- Renders as (nothing at all if [commands] is empty):

# Feedback commands

After your changes, run these and make them pass:

- test: `go test ./...`
- typecheck: `go vet ./...`
- lint: `golangci-lint run`
-->

# Commit

If you changed anything, commit with a message that says what you fixed or
refactored, and why. If the change is already good, change nothing.

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
