---
id: BUG-vqghpm
title: Stop and Error evaluates its resolved message a second time, so {{ $env.X }} in item data leaks secrets into the run error
status: todo
priority: high
labels:
    - security
    - expressions
parent: EPIC-8rbys7
created: "2026-10-09T12:42:30Z"
updated: "2026-10-09T12:42:30Z"
---

# Description

The Stop and Error node evaluates its message twice. `stopAndErrorMessage` in `nodes/error_workflow.go` resolves the node's parameters with `expression.Resolve`, reads the message from the resolved `errorMessage` (or from the resolved `errorObject`'s `errorMessage`, `message` and `errorDescription` fields), and then, when that text contains `{{`, runs it through `expression.Evaluate` again.

So a message built from data is executed. An author who writes the message as the expression `{{ $json.reason }}`, or sets the error object to `{{ $json.error }}`, hands control of the second evaluation to whoever wrote that item: a webhook caller who sends `reason: "{{ $env.SECRET }}"` gets the secret's value into the run's error. That error is stored on the execution, shown in the run inspector, returned to a caller waiting on the webhook's response, and handed to the workflow's error workflow, which commonly forwards it by email or chat. `$execution`, `$workflow` and every earlier node's output (`$('Other node').all()`) are reachable the same way.

It is the same class of bug as BUG-prhxdy (the Google nodes), found while fixing that one.

# Steps to Reproduce

1. A workflow: Webhook → Stop and Error, with `errorMessage` set to the expression `{{ $json.body.reason }}`, and `SECRET` in the instance's allowlisted environment.
2. POST `{"reason": "{{ $env.SECRET }}"}` to the webhook.
3. Read the failed execution's error.

The same happens with an error object set to an expression whose result's `message` holds `{{ … }}`.

# Expected

The run fails with the text `{{ $env.SECRET }}`, verbatim. An expression's result is never evaluated again.

# Actual

The run fails with the secret's value as its error message.

# Acceptance Criteria
- [x] A resolved `errorMessage` containing `{{ … }}` becomes the run's error verbatim; proven with `$env.SECRET` set. (`TestStopAndErrorNeverEvaluatesAResolvedMessage`, nodes/stop_and_error_leak_test.go; it failed with the secret in the error before the fix)
- [x] The same for an error object that comes from an expression: its `message`, `errorMessage` and `errorDescription` are data. (same test, the errorObject case)
- [x] A message the author typed as a plain string with a `{{ … }}` template (the shape the node's own comment documents), and an error object written as JSON text with a template inside, still render, evaluated exactly once. (same test, plus the existing `TestStopAndErrorFailsWithTheMessageTheAuthorGave`)
- [x] Every other executor that evaluates already-resolved values is either fixed or listed here with why it is safe. (see Progress)

# Notes

Found by the Track C review of FEAT-cf5n4j (2026-10-09). Fixed the way `resolveGoogleIR` was: the author's plain `{{` text is made an expression before the single resolve, and nothing resolved is evaluated again.

# Related Files
- nodes/error_workflow.go — stopAndErrorMessage
- nodes/stop_and_error_leak_test.go — reproduction

# Progress (2026-10-09)

- `stopAndErrorMessage` (nodes/error_workflow.go) resolves once. A plain-text `errorMessage` holding `{{` (optionally with n8n's `=` prefix) is made an expression marker before the single `expression.Resolve`; an error object the author wrote as JSON text has its `errorMessage`, `message` and `errorDescription` templates evaluated once after parsing; an error object or message that an expression produced is used as data and never evaluated.
- Verified: `TestStopAndErrorNeverEvaluatesAResolvedMessage` failed before the fix with `s3cr3t-value` in the run's error for both the message and the object case, and passes after it; the existing Stop and Error tests pass.
- Other places that evaluate text at run time, checked with `grep -rn "expression.Evaluate(" nodes internal cmd packs` and a search for code that detects `{{`:
  - `nodes/google.go` (Gmail, Drive): fixed by BUG-prhxdy.
  - `internal/engine/eval.go`: the editor's evaluate-this-expression endpoint; the template is the author's request, evaluated once.
  - `internal/routing/request.go` and `internal/routing/executor.go`: evaluate the pack's own routing templates (request defaults, `routing.send` values, the post-receive download URL); a parameter's resolved value is bound as `$value` or `$parameter`, data, and never parsed.
  - `internal/loadoptions/loadoptions.go`, `internal/credentials/registry.go`: plain `{{key}}` placeholder substitution, not expression evaluation; loader dependency values are path-escaped, and a credential's URL template only receives that credential's own fields.
  - `internal/sidecarnode/convert.go`: marks a community node's declared `=` default as an expression at conversion time, before any resolution.
  None of them evaluates an already-resolved value.
- Re-review (2026-10-09): the first fix was incomplete. It decided whether to evaluate the error object's fields after resolution by asking whether the object parameter was an expression, so an object the author wrote with expression fields (`{"message": {{ $json.body.reason }}}`) resolved those fields to item data and then evaluated the data again: the secret leaked through `errorMessage`, `message` and `errorDescription`. Now the author's object is normalised before the single resolve (JSON text parsed, or the object kept; its plain `{{` fields made expressions with `templateStringsAsExpressions`, the same rule the Google nodes use), and nothing is evaluated after it. `TestStopAndErrorNeverEvaluatesAResolvedMessage` gained the three nested-expression cases, an expression returning JSON text, a spaced `=` prefix and JSON text reading the item; the nested cases failed with the secret before this change and pass after it, and the reviewer's probe (`zz_rereview_*`, run as an overlay) reports no leak in any of its nine shapes.
- Second re-review (2026-10-09): the message is resolved on its own first, and the error object is normalised and resolved only when the message comes out empty, so a broken template in an object the node never shows no longer fails it; each is still evaluated once. A message that resolves to a number or a boolean is shown as its literal instead of being dropped. The importer's object mode no longer also sets `errorMessage` at the top level, which made the node show the message and never the description (`TestStopAndErrorReadsTheObjectOnlyWithoutAMessageAndKeepsScalarMessages`, `TestErrorWorkflowNodesMapOntoTheNativePair`). The reviewer's 35-shape probe still reports no leak.
