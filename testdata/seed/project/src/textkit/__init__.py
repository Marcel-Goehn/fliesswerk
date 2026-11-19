"""Small text helpers."""

import re

SMALL_WORDS = frozenset({"a", "an", "and", "in", "of", "on", "or", "the", "to"})


def normalize_whitespace(text: str) -> str:
    """Collapse every run of whitespace into one space, and trim both ends."""
    return " ".join(text.split())


def slugify(text: str) -> str:
    """Lower-case text, with each run of non-alphanumerics turned into one hyphen."""
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")


def title_case(text: str) -> str:
    """Capitalise each word, but keep small words lower-case unless they come first."""
    words = normalize_whitespace(text).lower().split(" ")
    return " ".join(
        word if i > 0 and word in SMALL_WORDS else word.capitalize()
        for i, word in enumerate(words)
    )


def word_count(text: str) -> int:
    """The number of whitespace-separated words in text."""
    return len(text.split())
