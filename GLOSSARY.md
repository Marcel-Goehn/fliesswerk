# kraftwerk

A software factory that implements a spec's tickets with AFK agents, replacing the implement step of the grill → spec → tickets → implement → review → PR workflow.

## Language

**Factory**:
The tool as a whole: the thing that takes a spec's ready tickets and turns them into commits on a feature branch without a human in the loop.
_Avoid_: Kraftwerk (when the concept is meant; Kraftwerk is the product name), Ralph loop (as a name for the tool), software factory (in prose, fine; in code, use Factory)

**Sandbox**:
The isolated environment in which a Step runs, walling the agent off from everything it doesn't need. It comes in two kinds: a **container Sandbox** (the Step runs in a container) and a **native Sandbox** (the Step runs directly on the user's machine, fenced in by the operating system).
_Avoid_: Host mode (implies no isolation), container (when the concept is meant), VM

**Ready for agent**:
The state of a ticket that may be picked up by the Factory, expressed by the `ready-for-agent` label in whichever issue tracker is used.
_Avoid_: Sandcastle label, AFK-ready

**Spec**:
A parent issue describing a feature as a whole; its sub-issues are the Tickets. A Spec is never worked on directly, even if it carries the Ready-for-agent label. A Spec is self-contained: its Tickets depend only on each other and on what is already in the base branch, never on another Spec.
_Avoid_: PRD (in Factory vocabulary), epic

**Ticket**:
An issue that is Ready for agent and is not a Spec: the unit of work an agent implements. When it belongs to a Spec, the Spec's content is part of its context.
_Avoid_: Task, issue (when the workable unit is meant)

**Run**:
One whole execution of the Factory, from the moment it is started until it stops.
_Avoid_: Session, job, loop (as a noun for the execution)

**Blocker**:
An issue that must be closed before a Ticket can be worked on, as recorded by the tracker's own blocking relationship. Text in a Ticket's description never makes an issue a Blocker.
_Avoid_: Dependency, prerequisite

**Iteration**:
One pass of a Run's loop: it takes the Tickets that are ready (as many as the Run may work on at once), works each through its Steps, and ends when all of them have finished. Only then does the next Iteration look for ready Tickets again.
_Avoid_: Round, batch, attempt

**Step**:
One agent session inside an Iteration, with one prompt (implement, review, merge).
_Avoid_: Phase, stage, agent run

**Harness**:
The agent program that carries out a Step, e.g. Claude Code, Codex, Pi. Distinct from the model provider it talks to (Anthropic, Bedrock, OpenRouter, …).
_Avoid_: Agent provider, agent CLI, model (when the program is meant)

**Tracker**:
The issue tracker the Factory reads Specs and Tickets from and reports back to (GitHub Issues in v1). Like the Harness, it can be swapped for another one.
_Avoid_: Issue source, backend, provider

**Feedback command**:
A project command that tells the agent whether its change works (e.g. tests, type check, lint). Declared once per project and made available to every Step.
_Avoid_: Check, script, validation

**Coding standards**:
A project file describing how code should be written. Only the review Step checks against it; the implement Step doesn't see it.
_Avoid_: Style guide, conventions (when the file is meant)

**Hook**:
A shell command the Factory runs at a named moment of a Run (e.g. when a Sandbox is created), either on the host or inside the Sandbox.
_Avoid_: Callback, lifecycle script

**Protected path**:
A file or directory in the project that the agent may not change, because the Factory itself relies on it (its own configuration, the services it starts). An Iteration that changes one is rejected.
_Avoid_: Read-only file, locked file
