## Parent

{{spec}}

## What to build

Add `format_block(text: str, width: int = 72, prefix: str = "> ") -> str` to `src/textkit/layout.py`. It normalises the whitespace of `text` first, then returns `indent_block` of it.

## Acceptance criteria

- [ ] `format_block("  the quick\n brown   fox ", 12)` is `"> the quick\n> brown fox"`.
- [ ] Tests in `tests/test_layout.py`.

## Blocked by

- {{b}}
