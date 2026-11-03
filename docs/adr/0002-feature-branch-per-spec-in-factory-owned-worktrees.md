# One feature branch per Spec, in Factory-owned worktrees

The Factory groups work as one feature branch per Spec (`<prefix>/spec-<N>-<slug>`, cut from the remote `base_branch`). A standalone Ticket gets a branch of its own and is treated as a Spec of one. This lets each feature go through `/code-review` and `/pr` on its own, and unrelated Specs never mix. Sandcastle does the opposite: it commits into, or merges into, whatever branch the user has checked out. The Factory **never touches the user's checkout**. Every branch is worked in a worktree the Factory owns, so the user can keep editing on any branch, even with a dirty tree, during a Run. In every Run mode, sequential or parallel, each Iteration commits on a short-lived Ticket branch cut from the Spec branch's tip. Code merges it into the Spec branch on success and leaves it aside on rejection. The Spec branch only ever moves forward, a failed Iteration never needs a destructive reset of the deliverable branch, and sequential and parallel Runs share one code path. These mechanics are deliberately not configurable. Only the grouping (the rule that maps a Ticket to its target branch) is a seam, and v1 ships just `per_spec`.

## Considered Options

- **Sandcastle-faithful (merge into the checked-out branch)**: simplest, but the user has to sequence Specs by hand, mixed Specs land on one branch, and the result depends on the state of the user's working tree.
- **One branch per Ticket, no merging (Copilot-style)**: a Ticket blocked by another starts from `base_branch` without its blocker's code, and the manual `/pr` step ends up with many PRs per Spec. It may return later as an optional `strategy` key.
- **Committing straight onto the Spec branch in sequential Runs**: one branch fewer, but rejection needs `reset --hard` on the deliverable, and parallel Runs need Ticket branches anyway, which would mean two code paths.

## Consequences

- Specs are self-contained. A blocker outside a Ticket's Spec is still respected for order, since the Ticket waits until the blocker is closed. Its code isn't on the Ticket's branch, though, until a human merges that branch into `base_branch`. `check` and the Run summary warn about such links rather than refusing them or working around them.
- The Factory never rebases a Spec branch or merges `base_branch` into it on its own. Spec branches are always kept. Ticket branches are deleted on success and kept on failure until the next attempt.
