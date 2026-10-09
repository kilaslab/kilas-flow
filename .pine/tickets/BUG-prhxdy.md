---
id: BUG-prhxdy
title: Google nodes evaluate a resolved value as an expression a second time, so {{ $env.X }} in data leaks secrets
status: todo
priority: high
labels:
    - security
    - google
    - expressions
parent: EPIC-8rbys7
created: "2026-10-09T11:20:46Z"
updated: "2026-10-09T11:20:46Z"
---

# Description

The Gmail and Google Drive executors evaluate expressions twice. `resolveGoogleIR` in `nodes/google.go` first resolves the node's parameters with `expression.Resolve`, then walks the *resolved* tree with `evaluateTemplateStrings` and evaluates every string in it that contains `{{`. A value an expression produced is supposed to be data, but here it is parsed and run as a second expression.

So any text that reaches a Gmail or Drive parameter through an expression (a webhook body, an email subject read by an earlier node, a row from a spreadsheet, and, once FEAT-cf5n4j ships gmailTool, a model's tool argument) can carry `{{ $env.X }}`, `{{ $execution… }}` or `{{ $('Other node').all() }}` and have the evaluator read it. The result is sent out of the instance (as an email body, a subject, a Drive query), so an outsider who controls one upstream field can exfiltrate any allowlisted environment variable or any earlier node's output.

The walk was added (EPIC-hkypt5, 018af94f) so that imported n8n parameters nested below the top level, which the importer leaves as plain `={{ … }}` strings, still evaluate. That need is real; the bug is that it runs after resolution instead of before it.

# Steps to Reproduce

1. A Gmail node, operation `send`, with `message` set to the expression `{{ $json.body }}`.
2. Run it on an item whose `body` is the text `{{ $env.SECRET }}`, with `SECRET` in the instance's allowlisted environment.
3. Look at the mail the Gmail API receives.

The same happens with a Drive `search` whose `queryString` is `{{ $json.q }}`.

# Expected

The mail body is the literal text `{{ $env.SECRET }}`. An expression's result is never evaluated again.

# Actual

The mail body is the secret's value. `nodes/google_template_test.go` reproduces it for Gmail send (subject and body) and Drive search.

# Acceptance Criteria
- [x] A resolved value containing `{{ … }}` reaches the Gmail API (send: subject and body) as literal text; proven against an httptest stub with `$env.SECRET` set. (`TestGmailSendDoesNotEvaluateBracesInsideAResolvedValue`, nodes/google_template_test.go)
- [x] The same holds for Google Drive (search `queryString`). (`TestGoogleDriveSearchDoesNotEvaluateBracesInsideAResolvedValue`)
- [x] A plain `{{ … }}` / `={{ … }}` string the workflow author wrote in a nested parameter (the imported n8n shape) is still evaluated, exactly once. (`TestGmailStillEvaluatesANestedTemplateTheAuthorWrote`: `filters.sender: "={{ $json.from }}"` becomes `from:alice@example.com`)
- [x] No resolved parameter tree is walked for templates anywhere in the Google nodes after this change. (`evaluateTemplateStrings` is deleted; `resolveGoogleIR` is the only resolution path for Gmail and Drive and calls `expression.Resolve` once)

# Notes

Found while planning FEAT-cf5n4j (agent-tool forms): gmailTool would hand model text straight into this path, so the fix has to land before gmailTool ships. Track B (Google Sheets, FEAT-vntngh) builds on `resolveGoogleIR` and rebases onto this fix.

The same class of bug exists outside the Google nodes: the Stop and Error node (`nodes/error_workflow.go`, the message builder around line 190) resolves `errorMessage` and then calls `expression.Evaluate` on the result when it contains `{{`. Its output is an error message rather than an outbound request, but an error workflow can forward it. Not fixed here (out of scope for this ticket); worth its own ticket.

# Related Files
- nodes/google.go — resolveGoogleIR, templateStringsAsExpressions
- nodes/gmail.go
- nodes/google_drive.go
- nodes/google_template_test.go — reproduction

# Progress (2026-10-09)

- `resolveGoogleIR` no longer evaluates anything after resolution. Before the single `expression.Resolve`, a new `templateStringsAsExpressions` turns every plain string holding `{{` in the *unresolved* parameter tree into an expression marker (stripping n8n's leading `=`), so the nested n8n templates the old walk existed for are still the author's templates and still evaluate, once. Values produced by resolution are never parsed again. `evaluateTemplateStrings` is deleted.
- Verified: the two injection tests failed before the fix with the secret in the sent mail and in the Drive `q`, and pass after it; `go test ./nodes -count=1` passes.
