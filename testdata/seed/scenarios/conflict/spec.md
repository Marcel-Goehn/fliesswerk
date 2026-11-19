## Problem Statement

`normalize_whitespace` in `src/textkit/__init__.py` leaves zero-width characters in, and it flattens paragraphs into one line.

## Solution

Change `normalize_whitespace` itself in both ways. Don't add a second function: every caller should get the better behaviour.

## Testing Decisions

Unit tests with pytest in `tests/test_textkit.py`, through `normalize_whitespace` only.
