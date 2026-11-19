## Problem Statement

textkit forgets every text it processed. Users want to save snippets and read them back later.

## Solution

Store snippets in the Postgres from `compose.yaml`, through `psycopg`, which is already in the lockfile.

## Testing Decisions

Test against the real database, not a mock: the Service `postgres` from `compose.yaml` (user, password and database `textkit`). Read its address from `FLIESSWERK_SERVICE_POSTGRES_HOST` and `FLIESSWERK_SERVICE_POSTGRES_PORT`, falling back to `localhost:5432`.
