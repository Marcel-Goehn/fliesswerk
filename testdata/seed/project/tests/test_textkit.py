from textkit import normalize_whitespace, slugify, title_case, word_count


def test_normalize_whitespace_collapses_runs_and_trims():
    assert normalize_whitespace("  hello \t\n world  ") == "hello world"


def test_slugify_joins_words_with_hyphens():
    assert slugify("Hello, World!") == "hello-world"


def test_title_case_keeps_small_words_lower_case():
    assert title_case("the lord of the rings") == "The Lord of the Rings"


def test_word_count_counts_whitespace_separated_words():
    assert word_count("one two  three") == 3
