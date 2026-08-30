# Fix Request: {{.ID}} — {{.Title}}

## Issue
Status: {{.Status}} · Priority: {{.Priority}}{{if .Labels}} · Labels: {{join .Labels ", "}}{{end}}

{{.Body}}

## Related Files
{{range .RelatedFiles}}- {{.}}
{{else}}(none listed)
{{end}}
{{if .Learnings}}## Relevant Learnings
{{range .Learnings}}- **{{.ID}}** ({{.Scope}}{{if .Tags}}, tags: {{join .Tags ", "}}{{end}}): {{.Body}}
{{end}}
{{end}}## Acceptance Criteria
- The behavior described under "Expected" occurs when following "Steps".
- No regressions in the related files above.

## When done
- Edit `.pine/tickets/{{.ID}}.md` and set `status: testing` (then `done` once verified).
- Summarize your changes in a `# Fix Notes` section in the ticket.
- If you discovered a non-obvious project gotcha, run `pine learn "..."` before ending.
