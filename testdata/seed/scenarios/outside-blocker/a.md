## Parent

{{split}}

## What to build

Add `split_sentences(text: str) -> list[str]` in a new module `src/textkit/sentences.py`. It splits `text` after each `.`, `!` or `?` that is followed by whitespace, and trims each sentence.

## Acceptance criteria

- [ ] `split_sentences("Hi there. How are you? Fine!")` is `["Hi there.", "How are you?", "Fine!"]`.
- [ ] Tests in `tests/test_sentences.py`.
