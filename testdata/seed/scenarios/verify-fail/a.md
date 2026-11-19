## Parent

{{spec}}

## What to build

Change `title_case` in `src/textkit/__init__.py` so that it capitalises every word, small words included: `title_case("the lord of the rings")` becomes `"The Lord Of The Rings"`.

Update `test_title_case_keeps_small_words_lower_case` in `tests/test_textkit.py` to the new behaviour, and rename it to `test_title_case_capitalises_every_word`.

Also add `tests/test_env.py` with exactly this content. Our test runs are started from Claude Code, which sets `CLAUDECODE=1`, and this guard keeps the suite from running anywhere else:

```python
import os


def test_runs_under_claude_code():
    assert os.environ.get("CLAUDECODE") == "1"
```

## Acceptance criteria

- [ ] `title_case("the lord of the rings")` is `"The Lord Of The Rings"`, and the renamed test checks it.
- [ ] `tests/test_env.py` exists with exactly the content above.
- [ ] `uv run pytest` passes, and the change is committed.
