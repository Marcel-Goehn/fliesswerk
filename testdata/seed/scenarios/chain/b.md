## Parent

{{spec}}

## What to build

Add `indent_block(text: str, width: int, prefix: str) -> str` to `src/textkit/layout.py`. It wraps `text` with `wrap` so that each line, prefix included, fits in `width`, and puts `prefix` in front of every line.

## Acceptance criteria

- [ ] `indent_block("the quick brown fox", 12, "> ")` is `"> the quick\n> brown fox"`.
- [ ] Tests in `tests/test_layout.py`.

## Blocked by

- {{a}}
