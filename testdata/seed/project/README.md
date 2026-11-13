# textkit

Small text helpers. This repository is a fixture for Fliesswerk's acceptance
pass: `testdata/seed/seed.sh` in the Fliesswerk repository resets it, and
everything here is replaced on the next reset.

```sh
uv sync --locked
uv run pytest
```

`compose.yaml` runs a Postgres for the Tickets that need a database.
