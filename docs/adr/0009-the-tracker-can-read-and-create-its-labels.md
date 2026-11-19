# The Tracker can read and create its labels

A Run adds three labels: the ready label, the `in_progress_label` and the `cannot_do_label`. GitHub can only add a label that exists, so a missing one only showed when a Run tried to claim a Spec or hand a Ticket back, after `check` had said everything was fine. Ticket #76 has `init` create the missing labels, and has `check` and every Run report them. Neither is possible through the operations ADR-0006 lists, so the Tracker boundary gains two more: `MissingLabels(names)`, a read, and `CreateLabel(name, description)`, a write that only `init` uses. Its Description gains `CreateLabelFix`, the line a person runs when `init` couldn't create a label (`gh label create …` on GitHub).

The adapter compares label names: GitHub compares them without regard to case, and another Tracker may not. `CreateLabel` never changes a label that already exists, because its color and description belong to the user.

## Considered Options

- **Keep label creation out of the Tracker; tell the user to run `gh label create`**: what the docs did. It fails late, and it never mentioned that the ready label has to exist too.
- **`EnsureLabels`, one operation that reads and creates**: `check` and a Run must only read, so the read has to stand alone anyway.

## Consequences

- The Tracker's writes are Comment, Close, SetLabels, Claim, Release and CreateLabel. A Run never calls CreateLabel.
- A missing label stops `check` and a Run with exit code 3, as a setup problem. A failure to read the labels stops a Run like any other Tracker failure.
