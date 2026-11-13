## Problem Statement

textkit can normalise and title-case text, but it can't count characters, reverse word order or shorten text for a preview.

## Solution

Three small helpers, each in a module of its own with its own tests.

## Testing Decisions

Each helper gets unit tests with pytest under `tests/`, through its public function only.
