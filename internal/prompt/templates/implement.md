# Task

Implement Ticket {{.Ticket.ID}}: {{.Ticket.Title}}

Work only on this Ticket. {{if .Spec}}Its full text, its comments and the Spec it belongs to are below, so you don't need to fetch them.{{else}}Its full text and its comments are below, so you don't need to fetch them.{{end}}

<ticket>
{{.Ticket.Body}}
</ticket>

{{if .Comments -}}
<ticket-comments>
{{range .Comments -}}
<comment author={{printf "%q" .Author}} date={{printf "%q" .Date}}>
{{.Body}}
</comment>
{{end -}}
</ticket-comments>

{{end -}}
{{with .Spec -}}
# The Spec

Your Ticket is part of Spec {{.ID}}: {{.Title}}. The Spec describes the whole feature. Use it to understand why your Ticket exists and how it fits in, but implement only what your Ticket asks for. The rest of the Spec is other Tickets' work.

<spec>
{{.Body}}
</spec>

## Tickets in this Spec

{{range .Tickets -}}
- {{.ID}} {{.Title}} ({{.Mark}})
{{end}}
Closed Tickets are finished: don't redo them. In progress and open Tickets are someone else's work: don't do them, even if they look easy to include.

{{end -}}
# Recent commits

{{if and .Recent.OnSpecBranch .Spec -}}
The commits on the Spec branch since `{{.Recent.BaseBranch}}`, newest first:
{{- else if .Recent.OnSpecBranch -}}
The commits made for this Ticket since `{{.Recent.BaseBranch}}`, newest first:
{{- else if .Spec -}}
Nothing has been committed for this Spec yet. The last commits of `{{.Recent.BaseBranch}}`, newest first:
{{- else -}}
The last commits of `{{.Recent.BaseBranch}}`, newest first:
{{- end}}

<recent-commits>
{{range $i, $c := .Recent.Commits -}}
{{if $i}}
{{end -}}
commit {{$c.Short}}
{{$c.Text}}
{{end -}}
</recent-commits>

{{with .Failed -}}
# The Factory's checks failed

This is a retry. Your earlier commits are on the branch, but after them the Feedback command `{{.Name}}` (`{{.Line}}`) failed ({{.Err}}). Its last lines:

{{.Fence}}
{{.Tail}}
{{.Fence}}

Fix the cause, then commit.

{{end -}}
{{if .Services -}}
# Services

These Services are ready now. Each one runs for this Sandbox alone, with fresh data, unless it's marked shared. A shared one serves every Sandbox of the Run, so other agents use its data at the same time: don't wipe it or assume it's empty.

{{range .Services -}}
- {{.Name}}{{if .Shared}} (shared){{end}}: {{range $i, $a := .Addresses}}{{if $i}}, {{end}}`{{$a}}`{{end}}
{{end}}
Each one's host and first port are also in `FLIESSWERK_SERVICE_<NAME>_HOST` and `FLIESSWERK_SERVICE_<NAME>_PORT`.

{{end -}}
# Explore

Explore the repository until you understand the code this Ticket touches. Pay extra
attention to the test files around it.

# Implement

Use test-driven development. If the Ticket or the Spec names seams to test at,
test there.

1. RED: write one test for the next piece of behaviour and watch it fail.
2. GREEN: write the least code that makes it pass.
3. Repeat until the Ticket is done.
4. REFACTOR the code.

{{if .Commands -}}
# Feedback commands

Run these before every commit and make them pass:

{{range .Commands -}}
- {{.Name}}: `{{.Line}}`
{{end}}
{{end -}}
# Commit

Commit when the Ticket is done. The commit message must:

1. Include the Ticket completed + Spec reference if provided
2. Key decisions made
3. Files changed
4. Blockers or notes for the next Iteration

Keep it concise.

# Do not close the Ticket

The Factory closes it after your work has been merged.

# Rules

- You are on the branch `{{.Branch}}`, in a worktree made for this Ticket. Commit your work here. Don't push, switch branches, rebase or rewrite existing commits.
- Never change these Protected paths: {{range $i, $p := .Protected}}{{if $i}}, {{end}}`{{$p}}`{{end}}. Work that changes them is rejected, and the Ticket goes back to a person.
- Do not leave commented-out code or TODO comments in committed code.

# Done

When you are finished, report "done" with a short summary of what you changed.

Report "cannot_do", with the reason, only if:

1. the Ticket is ambiguous, or context is missing
2. it needs a change to a Protected path
3. it needs something the Sandbox can't reach: an outside system or API, a credential, or another team's change
4. tests fail and you can't fix them, including failures that were already there
5. the Ticket is already done or no longer makes sense

Anything else means try harder: nobody will answer questions.
