## Parent

{{spec}}

## What to build

Add `char_count(text: str, spaces: bool = True) -> int` in a new module `src/textkit/stats.py`. It returns the number of characters in `text`; with `spaces=False` it doesn't count whitespace.

## Acceptance criteria

- [ ] `char_count("a b")` is 3, and `char_count("a b", spaces=False)` is 2.
- [ ] An empty string counts 0.
- [ ] Tests in `tests/test_stats.py`.
