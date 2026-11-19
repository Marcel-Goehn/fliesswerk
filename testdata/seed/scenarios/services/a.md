## Parent

{{spec}}

## What to build

Add a new module `src/textkit/store.py` with:

- `connect() -> psycopg.Connection`, which connects to the database named in the Spec's Testing Decisions
- `save_snippet(conn, text: str) -> int`, which creates the table `snippets (id serial primary key, text text not null)` if it doesn't exist, stores `text` and returns its id
- `load_snippet(conn, id: int) -> str | None`, which returns the stored text, or `None` for an unknown id

## Acceptance criteria

- [ ] A test in `tests/test_store.py` saves a snippet in the real database and reads the same text back.
- [ ] A test proves an unknown id reads back as `None`.
- [ ] `uv run pytest` passes with the Service running.
