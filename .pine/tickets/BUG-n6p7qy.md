---
id: BUG-n6p7qy
title: 'Workflow Tool: sub-workflow''s typed inputs not offered to the model; every failure reported as ''not active'''
status: todo
priority: medium
labels:
    - ai
    - ai-tools
    - subworkflow
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-10-08T15:59:05Z"
---

# Description

The model is told the tool is unavailable whenever the sub-workflow fails, so it gives up instead of correcting its arguments.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-6, AI-7). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## AI-6: Workflow Tool does not offer the sub-workflow's declared typed inputs to the model

*gap · medium · ai-tools*

**n8n:** With the Database source, the sub-workflow's Workflow Input Schema is pulled into Workflow Inputs, and each field can be set to "let the model define" (`$fromAI`), so the tool schema lists typed parameters.

**Steps to reproduce:**

1. Sub-workflow: Execute Workflow Trigger `inputSource: fields`, inputs `tempC:number` and `roundTo:number` → Set `fahrenheit = $json.tempC*9/5+32`. Activate it.
2. Parent: Agent + workflowTool `celsius_to_fahrenheit` pointing at it, with no `$fromAI` in `workflowInputs`.
3. Ask "Convert 37 degrees Celsius to Fahrenheit using the celsius_to_fahrenheit tool."

**Actual:**

The model is offered `{input: object}`, so it guesses `{"input":{"temperature":37}}` in run 1 and `{"input":{"degrees":37}}` in run 2. The sub-workflow fails (`the JSON body is not a valid object: invalid character 'N'`, NaN from the missing `tempC`), and the agent tells the user the tool is unavailable. With a hand-written `{{ { "celsius": $fromAI(...) } }}` in `workflowInputs` it works (case 4 fromAI, 2/2). The trigger's declared inputs are also not enforced: unknown keys pass through and missing ones stay unset.

**Expected:**

The tool schema is derived from the trigger's `workflowInputs` (names and types), with a per-field model/fixed choice in the UI.

**Suggested fix:**

When `workflowId` is fixed, load the target's Execute Workflow Trigger inputs at build time and emit one typed property per input. Offer a "defined by model" toggle per field in the NDV.

**Evidence:**

`case-4c-t300-{0,1}.execution.json`, `case-4-fromAI-*.execution.json`. Code `nodes/ai.go:2583-2601` (the schema comes only from `$fromAI` calls, otherwise `{input: object}`).

**Related:**

BUG-tcqkad (done) fixed the importer side of Workflow Tool v2 mappings, not native schema derivation.


## AI-7: Every sub-workflow failure reaches the model as "not active, or it no longer exists … tell the user the tool is unavailable"

*bug · medium · ai-tools*

**n8n:** The sub-workflow's actual error message goes back to the agent as the tool's error, so the model can retry with corrected input.

**Steps to reproduce:**

Case AI-6, step 3, with the sub-workflow active and existing.

**Actual:**

`tool failed: node "AI Agent": the sub-workflow "wf_01a0cbcf-…" this tool calls could not be run — it is not active, or it no longer exists. This is a configuration problem in this workflow, not a missing record: tell the user the tool is unavailable … (sub-workflow "…" failed: execute node "set": node "Compute": the JSON body is not a valid object…)`. The model obeys the instruction and gives up ("I'm sorry … the conversion tool is currently unavailable") instead of retrying with the right field. The execution status is succeeded.

**Expected:**

The "not active" wording is used only when the lookup actually failed. A runtime failure is returned as "the sub-workflow failed: <error>" so the model can self-correct.

**Suggested fix:**

Distinguish `repository not found / not active` errors from execution errors (`errors.Is`) and word each one separately.

**Evidence:**

`case-4c-t300-0.execution.json`, `case-4c-t300-1.execution.json`. Code `nodes/ai.go:2626-2636` (every `InvokeWorkflow` error is wrapped the same way).

**Related:**

BUG-6jvcs5 (done) introduced this wording for the inactive-target case only.


# Acceptance Criteria
- [ ] The tool schema comes from the trigger's `workflowInputs` (names and types), with a per-field model/fixed choice in the UI (schema derivation done; the per-field choice exists only as "a key written in Workflow inputs is fixed", there is no NDV toggle yet, see Progress)
- [x] "not active" is used only when the lookup failed; a runtime failure returns "the sub-workflow failed: <error>"

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-6jvcs5, BUG-tcqkad

# Related Files

# Attachments

## Fix

**Root cause.**
- AI-6: `workflowTool.Definition` built its schema only from `$fromAI` calls, and fell back to `{input: object}` otherwise. Nothing on the runtime side could read another workflow's trigger without running it, so the tool never knew what its target declared.
- AI-7: `workflowTool.Invoke` wrapped every `InvokeWorkflow` error in the "not active … tell the user the tool is unavailable" text. The engine returned a child's run failure as a plain `fmt.Errorf("sub-workflow %q failed: %w")`, so a caller could not tell it from a lookup failure.

**Change.**
- `internal/engine/runner.go`: new `SubworkflowError{WorkflowID, Err}`. Its text is the same `sub-workflow "…" failed: …` as before, so traces and the Execute Sub-workflow node read the same. New optional `WorkflowDescriber` interface: `SubworkflowTrigger(ctx, parent, workflowID) (workflow.Node, bool, error)`.
- `internal/engine/service.go`: `invokeWorkflow` returns `*SubworkflowError` when the child ran and failed. `Service.SubworkflowTrigger` reads the target's trigger, using the same tenant clause and active revision a call would use, so an inactive target reports `repository.ErrNotFound`. `ExecutionStore` gains `ActiveDocument`.
- `internal/repository/executions.go`: the active-revision lookup moved out of `StartChild` into `activeCallableVersion`. `StartChild` and the new `ActiveDocument` both use it.
- `nodes/ai.go`:
  - `workflowToolFrom` (now given the agent's ctx) reads the target's declared inputs once per agent run via `declaredWorkflowInputs`. This happens only when `workflowId` is fixed in the node, not when it is an expression. A target that cannot be read declares nothing, and the call itself reports why.
  - `Definition` offers each declared input as a typed property (string/number/boolean/object; array with `items: {}`; any untyped), marked required, merged with any `$fromAI` properties (a `$fromAI` key of the same name wins).
  - The per-field model/fixed choice: a key written in Workflow inputs is fixed and not offered. Workflow inputs written as one expression fix every field. With no declared inputs and no `$fromAI`, the tool still offers `{input: object}`.
  - The Workflow inputs description says this.
  - `workflowToolFailure` words a failure by cause:
    - `*engine.SubworkflowError` → `node "…": the sub-workflow "…" failed: <child error>`
    - `repository.ErrNotFound` → the existing "not active" text
    - anything else (cycle, depth) → passed on as it is.
- Not changed: the trigger still does not enforce its declared fields (its own doc says they are "never a filter"), and the criteria do not ask for it.

**Tests.**
- `internal/engine/subworkflow_test.go` `TestACallerCanReadATargetsInputsAndTellALookupFailureFromAFailedRun`. Through a real service and SQLite, it checks that:
  - the runtime's invoker describes a target's trigger
  - an inactive target is a not-found, both when described and when called
  - a child that fails is a `*SubworkflowError` carrying the child's error, with the old wording.
- `nodes/ai_tools_test.go` `TestWorkflowToolOffersTheSubworkflowsDeclaredInputs`. It checks that:
  - `tempC`/`roundTo` are offered as required numbers, `unit` (fixed in Workflow inputs) is not offered, and there is no `input` object
  - a passthrough target keeps `{input: object}`
  - the sub-workflow receives `{tempC: 37, roundTo: 1, unit: "F"}`.
- `nodes/ai_tools_test.go` `TestWorkflowToolWordsAFailedCallByItsCause`. A failed run reaches the model as `the sub-workflow "wf_child" failed: execute node "set": …` with no "not active"/"unavailable" in it. A not-found still says "not active".
- `go test ./internal/ai/... ./nodes/... ./internal/engine/... ./internal/repository/...` pass, and `go vet ./...` is clean.

## Progress

Criterion 2 is done. For criterion 1, the backend half is done: the schema comes from the trigger's declared names and types. A per-field model/fixed choice also exists, but only through the Workflow inputs JSON: a key written there is fixed, and a key left out is the model's.

Left: the NDV toggle per field ("defined by model" / fixed) that the finding suggests. The editor already has a resource-mapper control (`web/src/lib/workflow-editor/resource-mapper.ts`, `node.PropertyResourceMapper`), and n8n's Workflow Tool v2 stores `workflowInputs` in that shape (`{mappingMode, value, schema}`). Building the toggle means three things:
1. Turn `workflowInputs` into a resource mapper whose schema loader lists the selected workflow's trigger fields.
2. Add a per-column "let the model define" switch to that control, stored as a `$fromAI` value the way n8n does.
3. Accept both the old plain-JSON shape and the mapper shape in `modelDefinedInputs` / `workflowToolInput`.

That is frontend and storage-shape work, outside this backend fix.
