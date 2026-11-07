# Claims go through their own Tracker operations, and the claimer is an assignee who added the label

Spec #31 lists the Tracker's operations as "exactly these" (Snapshot, Comments, Comment, Close, SetLabels, Describe), with "no other writes in v1". It also says a Claim adds `in_progress_label` **and the token user as assignee**. SetLabels can't set an assignee, and the core can't tell its own Claims from other people's without knowing who the token acts as. So the Tracker boundary gains four operations: `User()` (the token's login), `Claims()` (every open issue carrying a Claim, for `check` and `release`), and the writes `Claim(id)` and `Release(id, by)`. The Snapshot carries each Spec's (and each standalone Ticket's) Claim, with claimer and date.

On GitHub, a Claim is the in-progress label. The claimer is the assignee who last added the label. If no assignee added it, the claimer is the first assignee. With no assignees at all, it is whoever last added the label. The date comes from that person's last `labeled` event (one `gh api` call per claimed issue). `Claim` runs `gh issue edit --add-label <label> --add-assignee @me`. `Release` removes the label and the claimer's assignment.

## Considered Options

- **Labels only, through SetLabels**: needs no new write. But a claimer that is only an event's actor is invisible in GitHub's UI, and the Spec asks for the assignee.
- **The claimer is the first assignee**: breaks for a Spec that already had an assignee (a lead) before someone's Run claimed it. That person's own Runs would then skip it.
- **The claimer is whoever last added the label**: breaks when a person or a bot re-adds the label, and `release` would then unassign the wrong person.

## Consequences

- `release <N>` and `check` read only the configured repo's claimed issues. A Spec claimed in another repo shows up in `--dry-run` and the Run summary, which read the Snapshot, but `release` can't find it.
- A Claim of unknown date sorts after the dated ones. A Claim with no claimer at all counts as someone else's, and is shown as "claimed by someone unknown".
