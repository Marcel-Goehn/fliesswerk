## Parent

{{spec}}

## What to build

Add `wrap(text: str, width: int) -> list[str]` in a new module `src/textkit/layout.py`. It splits `text` into lines of at most `width` characters, breaking only between words. A word longer than `width` gets a line of its own.

## Acceptance criteria

- [ ] `wrap("the quick brown fox", 10)` is `["the quick", "brown fox"]`.
- [ ] Tests in `tests/test_layout.py`.
