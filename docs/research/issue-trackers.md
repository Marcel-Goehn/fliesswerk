# Research: how GitHub, local-markdown and Jira trackers expose what the Factory needs

Ticket: [#9](https://github.com/Marcel-Goehn/ralph/issues/9). Feeds the **tracker boundary** and feature-branch grouping (#13).
Researched 2026-11-02. `GLOSSARY.md` doesn't exist yet, so this note uses the map's vocabulary: **Factory**, **Spec**, **Ticket**, **Ready for agent**, **Run**, **Iteration**, **Step**.

## Answer

- **GitHub has everything the Factory needs as native, machine-readable data.** That covers labels, parent/sub-issues (Spec → Ticket), native blocking with an open-blocker count, comments, close with a reason, and milestones/Projects/issue types for grouping. One `gh issue list/view --json` call returns `labels`, `parent`, `subIssues`, `blockedBy` (each blocker's `state` included) and `comments`. So the Factory can compute the frontier in code with no extra round trips. Live-verified against this repo with `gh` 2.103.0. [G1–G6, L1]
- **The local-markdown convention covers the same concepts as text, but nobody has specified a parser for it, and it has gaps.** There is one directory per Spec (`.scratch/<slug>/spec.md`) and one file per Ticket (`issues/NN-slug.md`). A `Status:` line acts as the label, a `Blocked by:` line as blocking, and a `## Comments` heading holds comments. However, **no "closed/done" state is defined for implementation Tickets**: `/to-tickets` writes `Status: ready-for-agent`, and only `/wayfinder` defines `resolved`. The line formats also differ between skills (`Status:` vs `**Status:**`, and `Blocked by:` takes "numbers/titles"). The Factory would have to fix a strict grammar of its own. [P1–P3]
- **Jira exposes everything except a native "has open blockers" filter, and closing depends on the workflow.** Labels, the unified `parent` field (Epic Link retired Feb 2024) and the `Blocks` link type ("is blocked by") are all readable through JQL. Closing goes through a workflow *transition* whose id must be discovered per issue. "Done" means `statusCategory = Done`. Comments are ADF JSON, not Markdown. JQL can find linked issues, but it can't filter on whether those blockers are still open. So the frontier has to be computed in code after fetching the links. [J1–J6]
- **Grouping primitive for a feature branch:**
  - GitHub: the **parent issue (Spec)**; milestones, Projects v2 (GraphQL only, needs the `project` scope) and issue types (organisations only, so unavailable on this personal-account repo) as optional extras.
  - Local: the **directory**.
  - Jira: the **Epic / `parent`**, with `fixVersion`, sprint or component as alternatives.

  All three trackers have "parent = Spec", which makes it the only grouping primitive common to all of them. [G4–G6, P1, J3]
- **Sandcastle has no tracker boundary to copy.** Its "issue tracker" is three shell-command strings (`LIST_TASKS_COMMAND`, `VIEW_TASK_COMMAND`, `CLOSE_TASK_COMMAND`) substituted into prompts, and the *agent* runs them. Sandcastle is "not in the loop at runtime". Its GitHub list command filters on a label and ignores blocking and parents. That conflicts with "code decides everything that isn't writing code", so the Factory's boundary has to be richer than Sandcastle's. [S1, S2]
- **`gh` isn't guaranteed on the host**, and the map says the host shouldn't have to install anything. Everything `gh` does here is plain REST/GraphQL plus a token, so a GitHub adapter can call the API directly. The catch is database ids: REST endpoints for sub-issues and dependencies take the integer `id`, not the `#number`. [G2, G3, P4]

## Capability tables

Legend: **native** = first-class field or API; **convention** = text in a body or file; **n/a** = doesn't exist.

### GitHub Issues

| Need | Read | Write | Notes |
|---|---|---|---|
| Ready for agent | `labels` in `gh issue list --label ready-for-agent --json labels`; REST `labels[].name` | `gh issue edit N --add-label/--remove-label`; REST `POST /issues/{n}/labels` | native [G1] |
| Spec → Ticket | `parent`, `subIssues`, `subIssuesSummary` in `gh --json`; REST `GET /issues/{n}/parent`, `GET /issues/{n}/sub_issues`, `parent_issue_url` on every issue | `gh issue create --parent N`, `gh issue edit P --add-sub-issue C`, `--remove-parent`; REST `POST /issues/{p}/sub_issues {sub_issue_id: <db id>}`, `replace_parent`, `PATCH .../sub_issues/priority` for ordering | native. Max **100 sub-issues per parent**, **8 levels** of nesting, cross-repo allowed [G2, G4] |
| Blocking | `blockedBy` / `blocking` in `gh --json` (each node has `state`); REST `GET /issues/{n}/dependencies/blocked_by`; `issue_dependencies_summary.blocked_by` = **open** blockers only, `total_blocked_by` = all | `gh issue create --blocked-by`, `gh issue edit N --add-blocked-by/--remove-blocked-by`; REST `POST /issues/{n}/dependencies/blocked_by {issue_id: <db id>}` | native. No documented max. Adding edges quickly can trigger secondary rate limits / 422 [G3, P4] |
| Comments | `comments` in `gh --json`; REST `GET /issues/{n}/comments` | `gh issue comment N --body`; REST `POST /issues/{n}/comments` (Markdown) | native [G1] |
| Close | `state`, `stateReason`, `closedAt` | `gh issue close N --reason completed\|"not planned"\|duplicate --comment`; REST `PATCH` `state=closed`, `state_reason` | native. "Can't be done, stays open" = just comment, don't close [G1] |
| Claim / lock | `assignees` | `gh issue edit N --add-assignee @me` | native |
| Grouping | parent issue; `milestone`; `projectItems`; `issueType` | `--milestone`, `--add-project` (needs `project` token scope), `--type` | Projects v2 is GraphQL-only (`addProjectV2ItemById`, `updateProjectV2ItemFieldValue`). Issue types are org-level only (max 25) [G5, G6] |
| Search | `is:blocked`, `has:parent-issue`, `no:parent-issue` worked live. `parent-issue:owner/repo#1` returned 0 results (**unconfirmed** syntax) | | Prefer the direct endpoints. Search qualifiers for these aren't in the docs [L1] |

### Local markdown (Pocock skills convention)

| Need | Read | Write | Notes |
|---|---|---|---|
| Ready for agent | `Status:` line near top of `issues/NN-slug.md`, values from `triage-labels.md` | rewrite that line | convention. `/to-tickets` template writes `**Status:** ready-for-agent` (bold); tracker doc says `Status:` line [P1, P2] |
| Spec → Ticket | directory membership: `.scratch/<slug>/spec.md` + `.scratch/<slug>/issues/*.md` | create file in the directory | convention. One level only, no cross-feature parents [P1] |
| Blocking | `Blocked by: NN, NN` line (wayfinder); `**Blocked by:** numbers/titles` or "None (can start immediately)" (to-tickets) | edit line | convention. Free text in to-tickets form, so a parser needs a strict grammar. "Unblocked when every listed file is `resolved`" is defined only for wayfinder [P1, P2] |
| Comments | text under `## Comments` at bottom | append | convention, Markdown [P1] |
| Close | **not defined** for implementation Tickets. Wayfinder uses `Status: resolved` + `## Answer` | | gap: Factory must define a done value (and a "can't be done" marker) [P1, P2] |
| Claim / lock | wayfinder `Status: claimed` | rewrite line | no owner field. Parallel Runs race on the same file |
| Grouping | the directory (`<feature-slug>`) | | maps 1:1 to a feature branch |
| Ordering | file number `NN`, "first by number wins" | | numbered in dependency order [P1, P2] |
| Sandbox reach | files must be visible to wherever the tracker adapter runs; Sandcastle notes worktree sandboxes get git-tracked files only [S1] | | if `.scratch/` is gitignored, only the host side can read it |

### Jira Cloud (REST v3)

| Need | Read | Write | Notes |
|---|---|---|---|
| Ready for agent | `labels = ready-for-agent` in JQL; `fields.labels` | `PUT /rest/api/3/issue/{key}` with `update.labels [{add}/{remove}]` | native. A status ("Ready for dev") is an alternative signal [J1, J2] |
| Spec → Ticket | `parent = KEY` in JQL; `fields.parent`; issue type `hierarchyLevel` (-1 subtask, 0 base, 1 epic, higher = premium levels) | `fields.parent = {key}` on create/edit; remove with `update.parent.set.none` | native. Epic Link retired Feb 2024 in favour of Parent [J2, J3] |
| Blocking | `fields.issuelinks` (each with `type.inward/outward` and linked issue's status); JQL `issue in linkedWorkItems(KEY, "is blocked by")`, `workItemIsBlockedBy = KEY`, `issueLinkType = "is blocked by"` | `POST /rest/api/3/issueLink {type:{name:"Blocks"}, inwardIssue, outwardIssue}` | native, but site must have issue linking enabled. **No JQL for "has an open blocker"**, so compute in code. Direction of `inwardIssue`/`outwardIssue` for "Blocks" isn't stated plainly in the spec (it only says the comment goes on the "from (outward)" issue), so **verify by round-trip** [J3, J4] |
| Comments | `GET /rest/api/3/issue/{key}/comment` | `POST .../comment`, body in **Atlassian Document Format** (JSON) | native. Needs Markdown↔ADF conversion [J5] |
| Close | `fields.status.statusCategory` ("a work item is resolved when statusCategory = Done"); `resolution` | `GET .../transitions` to find a transition into Done, then `POST .../transitions {transition:{id}, fields:{resolution}}` | native but **workflow-specific**. Edit issue can't transition. Transition screens may require fields [J2, J5] |
| Claim / lock | `assignee` | edit `fields.assignee {accountId}` | native |
| Grouping | Epic (`parent`), `fixVersion`, sprint (Agile API), components, labels | | several options. Epic is the closest equivalent to a Spec [J2] |
| Search | `GET/POST /rest/api/3/search/jql` with `nextPageToken` paging; JQL must be **bounded** | | old `/rest/api/3/search` is "Currently being removed". Search is eventually consistent (`reconcileIssues` param, max 50 ids) [J1] |
| Auth / CLI | Basic `email:api_token` (passwords deprecated) or OAuth 2.0; official `acli` exists for Win/macOS/Linux | | `acli`'s Jira subcommands and JSON output **unconfirmed** [J6, J7] |

## Options and trade-offs for the tracker boundary (not decided here)

1. **Shape of the boundary**
   - *Option A:* graph-level reads. `listReadyTickets()` returns Tickets with `parent`, `blockers[{id, open}]` and `labels`, and the Factory core computes the frontier. All three trackers can supply this. GitHub does it in one call, while Jira and local need extra reads.
   - *Option B:* push "frontier" into the adapter. Simpler for the core, but each adapter re-implements the ordering rules.
   - Either way, Sandcastle's 3-command model isn't enough, because it has no blocking and no parent.
2. **Close semantics**
   - GitHub has close with a reason, Jira has workflow transitions, and local has nothing defined.
   - The boundary needs `complete(ticket, comment)` and `reportBlocked(ticket, reason)`, the latter meaning comment and stay open. For Jira, config has to name the target transition or status category. For local, the Factory has to define a done value.
3. **GitHub transport: `gh` vs direct REST/GraphQL**
   - `gh` is convenient and returns the full graph in one JSON call.
   - The feature flags used here (`--parent`, `--add-sub-issue`) need `gh` 2.94+ [P4]. The version that added `--json blockedBy` is unconfirmed.
   - Direct HTTP avoids a host dependency, but has to map `#number` to the database `id` for writes.
4. **Grouping primitive for feature branches**
   - The parent Spec is universal.
   - Milestone, Project, fixVersion and sprint are tracker-specific and would need adapter-specific config.
   - Tickets with no parent ("any `ready-for-agent` issue that isn't a Spec is a Ticket") need a default group, for example one branch per Ticket.
5. **Local-markdown strictness**
   - Either adopt Pocock's convention and fix a strict grammar (and upstream the missing done state), or define a Factory-owned front-matter format.
   - Pocock lists Jira as out of scope for first-class support [P3], so there is no upstream Jira convention to align with.

## Sources

- **G1** GitHub CLI manual: `gh issue list/view/edit/close/comment` (live help output of `gh` 2.103.0, incl. `--json` fields `blockedBy blocking parent subIssues subIssuesSummary issueType projectItems milestone stateReason`; `close --reason {completed|not planned|duplicate}`). https://cli.github.com/manual/gh_issue
- **G2** GitHub REST: Sub-issues. https://docs.github.com/en/rest/issues/sub-issues
- **G3** GitHub REST: Issue dependencies. https://docs.github.com/en/rest/issues/issue-dependencies
- **G4** GitHub Docs: Adding sub-issues ("up to 100 sub-issues per parent issue", "up to eight levels"). https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues
- **G5** GitHub Docs: Using the API to manage Projects (GraphQL, `read:project`/`project` scopes). https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/using-the-api-to-manage-projects
- **G6** GitHub Docs: Managing issue types in an organization (up to 25; defaults task/bug/feature). https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/managing-issue-types-in-an-organization
- **L1** Live checks on `Marcel-Goehn/ralph`, 2026-11-02: `gh api repos/Marcel-Goehn/ralph/issues/9` (`issue_dependencies_summary`, `sub_issues_summary`, `parent_issue_url`, `type: null`); `gh issue view 13 --json blockedBy,parent`; `.../issues/1/sub_issues` (19 items); search qualifier tests.
- **P1** Pocock skills 1.3.1, `skills/engineering/setup-matt-pocock-skills/issue-tracker-local.md`.
- **P2** Pocock skills 1.3.1, `skills/engineering/to-tickets/SKILL.md` (local ticket template, publish rules).
- **P3** Pocock skills 1.3.1, `.out-of-scope/mainstream-issue-trackers-only.md`.
- **P4** Pocock skills 1.3.1, `skills/engineering/setup-matt-pocock-skills/issue-tracker-github.md` (`gh` 2.94+ for `--parent`/`--add-sub-issue`; database id for REST; `blocked_by` counts open blockers only). Same text in this repo's `docs/agents/issue-tracker.md`.
- **J1** Jira Cloud REST v3 OpenAPI spec (`swagger-v3.v3.json`, fetched 2026-11-02): `/rest/api/3/search` deprecated ("Currently being removed"), `/rest/api/3/search/jql` params `jql` (bounded), `nextPageToken`, `reconcileIssues`. https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json
- **J2** Atlassian Support: JQL fields (Parent, Epic link retired Feb 2024, Labels, statusCategory = Done, work item link / link type). https://support.atlassian.com/jira-software-cloud/docs/jql-fields/
- **J3** Jira REST v3 spec: Edit issue ("parent field may be set by key or ID", `update.parent.set.none`, transitions not supported in edit), `hierarchyLevel` schema.
- **J4** Jira REST v3 spec: Create issue link (requires Issue Linking enabled; `inwardIssue`/`outwardIssue`/`type` required; comment goes on "from (outward)" issue). Atlassian Support JQL functions: `linkedWorkItems(key, "is blocked by")`. https://support.atlassian.com/jira-software-cloud/docs/jql-functions/
- **J5** Jira REST v3 spec: Get/Transition issue transitions; Add comment (body in ADF).
- **J6** Atlassian: Basic auth for REST APIs (email + API token; "Authentication using passwords has been deprecated"). https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/
- **J7** Atlassian CLI introduction ("compatible with … MacOS, Windows, and Linux"). https://developer.atlassian.com/cloud/acli/guides/introduction/
- **S1** Sandcastle v0.13.0 (`f5c48e0`), `docs/agents/adding-an-issue-tracker.md` ("Sandcastle is not in the loop at runtime"; `IssueTrackerEntry`).
- **S2** Sandcastle v0.13.0, `src/InitService.ts` (github-issues entry: `gh issue list --state open --label Sandcastle …`, `gh issue view <ID>`, `gh issue close <ID> --comment "Completed by Sandcastle"`).
