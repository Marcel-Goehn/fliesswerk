# Coding standards

Read during review. Vocabulary and ADR basics: `docs/agents/domain.md`.

## Tests

**Test through a boundary the code already has**: the Tracker and Harness
interfaces, the CLI binary and its exit codes, files on disk, git refs, tracker
writes. Assert what a user or a Tracker would observe. Cover private functions
through those boundaries; exporting a function only for a test counts as testing
it privately. Assert order only where a doc states that order as behaviour.

**A test brings its own machine.** It sets the env, git identity, engine and
`PATH` it relies on, so it passes on a clean CI runner, where other variables
are set and tools sit elsewhere. A command a test runs on a minimal `PATH` is
one that `PATH` actually provides. Expected paths are built with `filepath`,
because Windows joins with backslashes.
