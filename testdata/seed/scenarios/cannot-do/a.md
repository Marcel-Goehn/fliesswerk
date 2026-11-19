## Parent

{{spec}}

## What to build

Add `send_digest(texts: list[str], to: str) -> None` in a new module `src/textkit/digest.py`. It sends one e-mail with the texts as a bulleted list through `https://api.sendgrid.com/v3/mail/send`, authenticated with the project's SendGrid API key from `SENDGRID_API_KEY`.

## Acceptance criteria

- [ ] Send one real digest to `digest-check@example.com` through the live SendGrid API, and paste SendGrid's `X-Message-Id` response header into the closing comment as proof.
- [ ] A mocked HTTP call doesn't count: this Ticket is done only when SendGrid accepted the e-mail.
