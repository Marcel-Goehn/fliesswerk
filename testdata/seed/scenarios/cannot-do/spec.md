## Problem Statement

Subscribers want a weekly e-mail with the texts textkit processed.

## Solution

Send the digest through SendGrid's v3 Mail Send API, with the project's SendGrid account.

## Testing Decisions

The digest counts as working only once a real e-mail went out through SendGrid's live API. A mocked HTTP call isn't enough.
