# Only the Harness sends its Claude credential; the Factory never tests it

`check` and every Run start used to prove the Harness's credential with one free test call from the Factory's own code: listing models at Anthropic, or at the gateway, with the credential. For `oauth_token` that sent the user's Claude subscription token from a third-party program. Anthropic's terms forbid exactly that: developers may not "route requests through Free, Pro, Max, or Ultra plan credentials", and Anthropic has blocked tools that did. A user who signs in to the unmodified Claude Code binary with their own subscription is explicitly allowed (https://code.claude.com/docs/en/legal-and-compliance).

So the Factory sends no request carrying a Claude credential, for `oauth_token`, `api_key` or `gateway`, at `check`, at `init` or at a Run start. Only the unmodified `claude` binary does, inside the Sandbox. The call is dropped for every Anthropic-style method, not only subscriptions, so all of them behave the same.

The Factory still resolves every secret up front: a missing env var, a failing `command` or a missing `.env` key stops the Run before work starts. A bad or expired credential shows at the first Step instead. Claude Code's authentication failure stops the Run at once, once for the whole Run, with no retry, nothing handed back as `needs-investigation`, and a message naming the fix (`claude setup-token`, check the API key, check the gateway token).

## Considered Options

- **Keep the test call for `api_key` and `gateway` only**: their terms allow it, but two behaviours for one Harness is harder to explain, and a gateway may front a subscription.
- **Test through `claude` itself at `check`**: a real Step spends tokens, and `check` promises none.

## Consequences

- Unchanged: Bedrock's STS call, which the host needs to mint the Sandbox's short-lived credentials; GitHub's `GET /repos/…` with the Tracker token; and the TLS-interception probe, which carries no credential. The probe finds a gateway's host, scheme and port from `base_url`.
- `check` reports these credentials as found but not tested ahead of time.
- Don't bring back a request the Factory makes with a Claude credential, for any method.
