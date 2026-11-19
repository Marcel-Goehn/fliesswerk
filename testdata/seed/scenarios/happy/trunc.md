## Parent

{{spec}}

## What to build

Add `truncate(text: str, width: int, ellipsis: str = "…") -> str` in a new module `src/textkit/preview.py`. Text up to `width` characters comes back unchanged. Longer text is cut so that the result, ellipsis included, is exactly `width` characters long.

## Acceptance criteria

- [ ] `truncate("hello world", 8)` is `"hello w…"`.
- [ ] `truncate("hello", 8)` is `"hello"`.
- [ ] A `width` shorter than the ellipsis raises `ValueError`.
- [ ] Tests in `tests/test_preview.py`.
