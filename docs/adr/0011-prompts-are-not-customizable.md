# Prompts are not customizable

Until now, `init` wrote a copy of each Step's built-in guidance to `.fliesswerk/prompts/<step>.md`, and the agent got the project's copy inside a fixed contract. The Factory no longer reads those files: every project gets the same prompts, compiled into the binary. A project's edits could weaken the prompts in ways the Factory can't detect. They worked against Fliesswerk's opinionated way of working, where TDD, the two review axes and the commit contents are the Factory's decisions, not the project's. And a project that copied the prompts once never got the improvements that followed. With one set of prompts, every improvement reaches every project. A project still feeds in its own facts through the Coding standards file, its Feedback commands and its Protected paths, and through the repo's own agent docs (`CLAUDE.md`, `AGENTS.md`), which the Harness reads itself.

With nothing left to edit, the split between a fixed contract in Go and an editable part in Markdown has no purpose. Each Step's prompt is now one Go `text/template` Markdown file that holds the whole prompt, conditions and loops included. Go only prepares the data. Each file stands alone, with no shared partials, so every prompt can be read and judged as a whole, even where that means repeating a Rule in all three. The docs tell users where in the source the prompts are. There is no command to show them, because the run logs already keep every attempt's exact prompt.

## Considered Options

- **Keep the split, with the editable part built in**: less to change, but no file shows the whole prompt, and the two halves have to agree without anyone seeing them together.
- **Shared partials for the blocks all Steps have** (Ticket, Spec, Services, Rules): no repetition, but no prompt can be read from top to bottom.

## Consequences

- `fliesswerk prompts diff`, the marker line, the built-in version history and `check`'s prompt warnings and placeholder errors are removed. Existing `.fliesswerk/prompts/` files are simply ignored, with no warning and no migration, since no release so far has had real users.
- Moving to the templates changes no rendered prompt by a single character. Improvements to what the prompts say come afterwards, as changes of their own.
