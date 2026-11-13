## Parent

{{spec}}

## What to build

Change `normalize_whitespace` in `src/textkit/__init__.py` so that a blank line (two or more line breaks, with only whitespace between them) survives as exactly `"\n\n"`, while every other run of whitespace still becomes one space. Change the function itself, not a new one.

## Acceptance criteria

- [ ] `normalize_whitespace("one\n\n\n two\nthree")` is `"one\n\ntwo three"`.
- [ ] The existing tests still pass.
