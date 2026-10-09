---
id: BUG-n6p7qy
title: 'Workflow Tool: sub-workflow''s typed inputs not offered to the model; every failure reported as ''not active'''
status: doing
priority: medium
labels:
    - ai
    - ai-tools
    - subworkflow
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-10-09T11:03:29Z"
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
- [x] The tool schema comes from the trigger's `workflowInputs` (names and types), with a per-field model/fixed choice in the UI. Evidence: `TestWorkflowToolOffersTheSubworkflowsDeclaredInputs` (schema from the trigger), `TestWorkflowInputsLoaderListsTheTargetsDeclaredFields` (the NDV's column list), `TestWorkflowToolReadsAMapperShapedWorkflowInputs` (the mapper shape), the vitest `isModelDefined` / `setColumnSource` suites and the SSR `a resource mapper that lets the model fill a column` suite (the switch), and the e2e `workflow-tool-inputs.spec.ts` (switch, save, run, n8n round trip, run headless against the built binary).
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

## Progress (2026-10-09)

Criterion 1 is done: `workflowInputs` is a resource mapper whose columns are the picked workflow's trigger fields, each column carries a model/fixed switch in the node panel, and the runtime, importer and exporter read both the legacy plain object and the mapper.

**What a stored value means (Go and TypeScript agree).**
- Absent or nil: every declared input is the model's.
- A plain object (the shape before this change): a key that is present is fixed.
- One whole expression: fixes every field, as before.
- A mapper, `{mappingMode, value, matchingColumns, schema}`: `value` holds the columns.
  - A column that is exactly one `$fromAI` call is the model's. The editor writes `{{ $fromAI('<id>', '', '<type>') }}`; an imported `={{ /*n8n-auto-generated-fromAI-override*/ $fromAI(...) }}` is the same after its marker comment is stripped.
  - Any other present value, `null` included, is fixed and sent as written.
  - A column with no entry is the model's, so a freshly picked workflow works at once.
  - `schema` is a display copy.
- The runtime never forwards the mapper's own keys to the sub-workflow.

**Changes.**
- `internal/loadoptions/workflows.go`: schema loader `WorkflowInputs`. It reads the target's *active* revision, says why when the target is inactive or takes anything, and confines an embed session to its own workflow. Registered in `cmd/kilasflow/main.go` as `workflows.inputs`. `nodes.DeclaredWorkflowInputs` is the one parser, shared with the model's tool schema; `nodes.SubworkflowTriggerOf` finds the trigger.
- `internal/property/mapper.go`: new `ModelDefinable` flag, opt-in. Generated web client and SDK models regenerated.
- `nodes/ai.go`: `workflowToolNode` declares the mapper. New exported `WorkflowToolInputs` reads all three shapes. `modelDefinedInputs` and `workflowToolInput` use it. `Definition` refines a `$fromAI(..., 'json')` column to the declared object or array, and orders `required` by declared order (it came out in map-walk order before).
- `internal/ai/fromai.go`: `WholeFromAICall`, the one rule for "this column is the model's".
- `internal/api/handlers/nodes.go`: `load-schema` answers an expression dependency with a reason, as `load-options` does. A By ID locator whose value is an expression (an imported self-referencing tool) counts as an expression in both; it used to reach the loader as the marker's JSON.
- Importer: `workflowToolInputsToKilas` keeps the n8n mapper whole instead of flattening it. The Execute Workflow node still gets a flat field map. Exporter: `workflowInputsToN8N` writes n8n's marker in front of a whole `$fromAI` column and exports a schema column with no entry as the model's own. A whole-expression value has no n8n counterpart and is reported as dropped instead of being written as a column named "mode".
- Web: `resource-mapper.ts` helpers (`isModelDefined`, `setColumnSource`, `flipColumnSource`, `modelDefinedValue`, `fromAIType`, `columnText`, `editColumn`, `isWholeExpression`); `property-field.svelte` mapper block (pill switch copying the Fixed/Expr pill's token classes, without its `focus-visible:outline-*` classes at the orchestrator's request); six strings in both catalogs.
- Skill: one sentence in `PROPERTY_KINDS.md`.

**Deviations from the plan.**
- New tests live in new files (`nodes/workflow_tool_inputs_test.go`, `internal/api/node_types_schema_expression_test.go`, `internal/ai/fromai_whole_test.go`), not in `ai_tools_test.go` / `node_types_test.go`, to stay clear of Tracks C and F. The importer code lives in `internal/interop/n8n/workflow_tool_inputs.go` for the same reason (Track B edits `parameters.go`).
- `readMapping`'s legacy-plain-object reading and the number-as-text input apply only to a mapper with `modelDefinable`, so a mapper that did not opt in (Track B's) reads and renders as before. The `editColumn` fix (an expression column edits as its template) applies to every mapper, because the old behaviour wiped the marker on the first keystroke.
- The whole-expression notice sits under the existing expression textarea, which already renders for any kind holding an expression, so the expression stays editable. The plan's version put the notice in the mapper block, which is unreachable for that value.
- For a mapper with `modelDefinable` the client no longer keeps its column list in the module-level `schemaCache`: the endpoint is `no-store`, and a target's fields change whenever its author edits and activates it in the same page session.
- Added `e2e/tests/workflow-tool-inputs.spec.ts`, which the plan did not list: it drives the switch, a real agent run against a scripted model, and the n8n round trip.

**Unverified: how n8n stores an untouched column.** The repo has no real Workflow Tool v2 export with a populated schema (only fixtures with an empty `value` and `schema`), so I could not confirm that n8n stores a column the author never touched as absent or null. The importer follows the plan: an untouched, non-removed schema column is stored as an explicit `null`, which keeps it out of the model's schema as it was in n8n. What it does not know is whether n8n passes such a column to the sub-workflow as `null` or leaves it out; here it arrives as `null`. Check this against a real n8n 2.x export before relying on it for sub-workflows that tell the two apart.

**Left / follow-ups.**
- A tool whose columns are all fixed (and a target that declares none) still offers the model the open `{input: object}`; it could offer an empty object instead. Not changed: it predates this ticket.
- A fixed column for an object or array field is stored as the text the author typed. To send an object, write it as an expression.
- Adding a new arbitrary static key to a passthrough target's tool is no longer possible in the form (existing keys stay visible and removable).
- Embed sessions can read only their own workflow's inputs; a confinement-aware version using `Confinement.AllowsWorkflow` is a follow-up.
