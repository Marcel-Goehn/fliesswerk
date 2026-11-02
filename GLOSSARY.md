# ralph

A software factory that implements a spec's tickets with AFK agents, replacing the implement step of the grill → spec → tickets → implement → review → PR workflow.

## Language

**Factory**:
The tool as a whole: the thing that takes a spec's ready tickets and turns them into commits on a feature branch without a human in the loop.
_Avoid_: Ralph loop (as a name for the tool), software factory (in prose, fine; in code, use Factory)

**Sandbox**:
The isolated environment (a container) in which an agent works on a ticket.
_Avoid_: Container (when the concept is meant), VM

**Ready for agent**:
The state of a ticket that may be picked up by the Factory, expressed by the `ready-for-agent` label in whichever issue tracker is used.
_Avoid_: Sandcastle label, AFK-ready

**Spec**:
A parent issue describing a feature as a whole; its sub-issues are the Tickets. A Spec is never worked on directly, even if it carries the Ready-for-agent label.
_Avoid_: PRD (in Factory vocabulary), epic

**Ticket**:
An issue that is Ready for agent and is not a Spec: the unit of work an agent implements. When it belongs to a Spec, the Spec's content is part of its context.
_Avoid_: Task, issue (when the workable unit is meant)

**Run**:
One whole execution of the Factory, from the moment it is started until it stops.
_Avoid_: Session, job, loop (as a noun for the execution)

**Iteration**:
One pass of a Run's loop, working on exactly one Ticket. In a parallel Run, several Iterations happen at once.
_Avoid_: Attempt, ticket run

**Step**:
One agent session inside a Run, with one prompt (e.g. implement, review). Most Steps belong to an Iteration; some (e.g. planning, merging) belong to the Run as a whole.
_Avoid_: Phase, stage, agent run

**Harness**:
The agent program that carries out a Step, e.g. Claude Code, Codex, Pi. Distinct from the model provider it talks to (Anthropic, Bedrock, OpenRouter, …).
_Avoid_: Agent provider, agent CLI, model (when the program is meant)
