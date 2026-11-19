## Problem Statement

textkit can't lay text out as an indented block of a given width, as a quote in a plain-text e-mail needs.

## Solution

Build it in three steps, each on top of the one before: wrapping, indenting a wrapped block, and one call that does both.

## Testing Decisions

Unit tests with pytest under `tests/`, through the public functions only.
