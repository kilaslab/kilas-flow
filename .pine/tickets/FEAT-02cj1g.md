---
id: FEAT-02cj1g
title: 'Expression editor: live result preview, node/field completions, validation of unknown nodes and syntax'
status: todo
priority: medium
labels:
    - editor
    - expressions
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

The preview and completion code exists in property-field.svelte, but nothing wires it, so it is unreachable. `workflow validate` accepts `$('Nope')` and broken syntax.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-10, UXD-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXD-10: The expression editor has no result preview and no node or field completions, and neither the editor nor validate flags unknown nodes or syntax errors

*gap · medium · ndv*

**n8n:** Every expression field shows a live "Result" for the current item and flags `[ERROR: Referenced node doesn't exist]` or a syntax error as you type. Autocomplete lists `$('Node')` names and the `$json.` fields from the last run, and the node gets a parameter-issue warning.

**Steps to reproduce:**

1. Open `[ux-debug] a HTTP 500 chain` → Call API → URL (expression mode). 2. Type `{{ $('P`, then `{{ $json.`, then `{{ $('Nope').item.json.x }}`, then `{{ $json.path + }}`. 3. `kilasflow workflow validate --file wf/c_doc2.json`, which has `$('Nope')` and `{{ $json.nothere + }}`.

**Actual:**

`$js` suggests `$json`, but `$('P` and `$json.` produce no suggestion list. `$('Nope')…` and `$json.path +` show only the static hint `Resolved per item on the server, for example {{ $json.id }}.`, and only an unknown root (`$jsn`) is flagged. validate returns `{"valid":true,"diagnostics":[]}` for both. No result preview ever appears: property-field has `upstreamNodeNames`, `upstreamFieldPaths` and `resolvedValues` props, but no host passes them, so the completion and preview code is unreachable. In Set assignments the value box is too narrow to read (`{{ $('Nope').item.jsor`).

**Expected:**

Pass upstream node names and last-run field paths to property-field, evaluate the template against the last run (the eval endpoint exists) for the preview, and have validate report unknown `$('…')` names and parse errors with the node and parameter.

**Suggested fix:**

Wire the three props from properties-panel using the document graph and the latest execution, and parse expressions in the compiler or validator.

**Evidence:**

`$SP/agents/ux-debug/a-07-expr-completion-node.png`, `a-08-expr-json-completion.png`, `a-09-expr-unknown-node-no-warning.png`, `c-01-missing-node-panel.png`, `c2-01-syntax-error-panel.png`. web/src/lib/components/workflow-editor/property-field.svelte:86-109, :291-292, :341, :596-602. `grep -rn "resolvedValues\|upstreamFieldPaths\|upstreamNodeNames" web/src --include=*.svelte` finds only property-field.svelte.

**Related:**

FEAT-56nep4 (done; commit 3e6a9b9a "expression completions+preview", but the preview and node/field completions are not wired)


## UXD-12: Expression error text uses internal parameter paths and gives the same message for a missing node and one that did not run

*ux · low · debugging*

**n8n:** `Referenced node doesn't exist` is a different error from `Node 'X' hasn't been executed`, and the error names the parameter by its label.

**Steps to reproduce:**

1. Run `[ux-debug] c bad expressions` (`$('Nope')` where Nope does not exist). 2. `kilasflow debug eval --execution exec_01a0cbc3-4524-7d0f-b745-c133a93e94f7 --node no "{{ $('Yes').first().json }}"` (Yes exists but was on the untaken branch).

**Actual:**

Both give `$('…') names a node that has not produced output in this run`. The node error reads `node "Missing node": parameter "assignments": "assignments": index 0: "value": …`, naming internal keys and an index instead of the field `x`. The execution error also repeats the node id and name: `execute node "call-api": node "Call API": …`.

**Expected:**

`No node named "Nope" in this workflow` as distinct from `"Yes" did not run in this execution`, the field named (`Fields to Set → x`), and the node named once.

**Suggested fix:**

Check the document's node names before the run lookup, and map parameter paths to labels and assignment names.

**Evidence:**

`$SP/agents/ux-debug/cli/run_c.json`, CLI output for debug eval in this report.

**Related:**

none


# Acceptance Criteria
- [ ] property-field receives `upstreamNodeNames`, `upstreamFieldPaths` and `resolvedValues` from the graph and the latest execution
- [ ] Each expression field shows a live result for the current item (via the existing eval endpoint), or an inline error
- [ ] `$('` completes node names, and `$json.` completes last-run field paths
- [ ] `workflow validate` and the canvas flag unknown `$('…')` names and parse errors, with the node and parameter
- [ ] Error text distinguishes "no node named X" from "X did not run", names parameters by label, and names the node once

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-56nep4

# Related Files

# Attachments
