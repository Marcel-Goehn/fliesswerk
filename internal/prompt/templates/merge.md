# Task

Resolve the merge of Ticket {{.Ticket.ID}}: {{.Ticket.Title}}

This Ticket's work is being merged into `{{.SpecBranch}}`, and other Tickets changed the same files after it started. The merge is in progress in this worktree and has stopped on its conflicts. The Ticket's {{if .Spec}}full text, its comments and the Spec it belongs to are below, so you don't need to fetch them.{{else}}full text and its comments are below, so you don't need to fetch them.{{end}}

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

The Ticket is part of Spec {{.ID}}: {{.Title}}. The Spec describes the whole feature. Use it to understand what each side of the conflict is for.

<spec>
{{.Body}}
</spec>

## Tickets in this Spec

{{range .Tickets -}}
- {{.ID}} {{.Title}} ({{.Mark}})
{{end}}
Resolve only this conflict. The other Tickets' work is not yours to add or change.

{{end -}}
# Conflicting files

Their paths start at the top of the repository.

{{range .Files -}}
- `{{.}}`
{{end}}
{{if .Merged -}}
# Tickets merged since this Ticket started

These Tickets' commits touched the conflicting files. They are the other side of the conflict.

{{range .Merged -}}
## {{.ID}} {{.Title}}

<ticket>
{{.Body}}
</ticket>

{{range .Commits -}}
- {{.Short}} {{.Subject}}
{{end}}
{{end -}}
{{end -}}
{{if .Services -}}
# Services

These Services are ready now. Each one runs for this Sandbox alone, with fresh data, unless it's marked shared. A shared one serves every Sandbox of the Run, so other agents use its data at the same time: don't wipe it or assume it's empty.

{{range .Services -}}
- {{.Name}}{{if .Shared}} (shared){{end}}: {{range $i, $a := .Addresses}}{{if $i}}, {{end}}`{{$a}}`{{end}}
{{end}}
Each one's host and first port are also in `FLIESSWERK_SERVICE_<NAME>_HOST` and `FLIESSWERK_SERVICE_<NAME>_PORT`.

{{end -}}
# Resolve

For each conflicting file, read both sides and the Tickets they came from. Keep the
intent of both Tickets: the result must do what each of them asked for.

{{if .Commands -}}
# Feedback commands

After resolving, run these and make them pass:

{{range .Commands -}}
- {{.Name}}: `{{.Line}}`
{{end}}
{{end -}}
# Commit

Stage the resolved files and finish the merge with `git commit --no-edit`.

# Do not close the Ticket

The Factory closes it after the merge has been verified and pushed.

# Rules

- You are in a worktree made for this merge, at the tip of `{{.SpecBranch}}`, on no branch. Commit the merge here. Don't push, switch branches, rebase, rewrite existing commits or abort the merge.
- Never change these Protected paths: {{range $i, $p := .Protected}}{{if $i}}, {{end}}`{{$p}}`{{end}}. Work that changes them is rejected, and the Ticket goes back to a person.
- Do not leave commented-out code or TODO comments in committed code.

# Done

When the merge is committed and nothing is left uncommitted, report "done" with a short summary of how you resolved it.

Report "cannot_do", with the reason, only if the two sides can't both be kept, or the merged code fails and you can't fix it. The Ticket is then implemented afresh on top of the other Tickets' work.
