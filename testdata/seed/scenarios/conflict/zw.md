## Parent

{{spec}}

## What to build

Change `normalize_whitespace` in `src/textkit/__init__.py` so that it removes the zero-width characters U+200B, U+200C, U+200D and U+FEFF before collapsing whitespace. Change the function itself, not a new one.

## Acceptance criteria

- [ ] `normalize_whitespace("a​ b")` is `"a b"`.
- [ ] The existing tests still pass.
