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
- [ ] A resolved `errorMessage` containing `{{ … }}` becomes the run's error verbatim; proven with `$env.SECRET` set.
- [ ] The same for an error object that comes from an expression: its `message`, `errorMessage` and `errorDescription` are data.
- [ ] A message the author typed as a plain string with a `{{ … }}` template (the shape the node's own comment documents), and an error object written as JSON text with a template inside, still render, evaluated exactly once.
- [ ] Every other executor that evaluates already-resolved values is either fixed or listed here with why it is safe.

# Notes

Found by the Track C review of FEAT-cf5n4j (2026-10-09). Fixed the way `resolveGoogleIR` was: the author's plain `{{` text is made an expression before the single resolve, and nothing resolved is evaluated again.

# Related Files
- nodes/error_workflow.go — stopAndErrorMessage
- nodes/stop_and_error_leak_test.go — reproduction
