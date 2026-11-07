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

# Commit

If you changed anything, commit with a message that says what you fixed or
refactored, and why. If the change is already good, change nothing.
