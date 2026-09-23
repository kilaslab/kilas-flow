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
updated: "2026-09-23T01:56:13Z"
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
- [ ] The tool schema comes from the trigger's `workflowInputs` (names and types), with a per-field model/fixed choice in the UI
- [ ] "not active" is used only when the lookup failed; a runtime failure returns "the sub-workflow failed: <error>"

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-6jvcs5, BUG-tcqkad

# Related Files

# Attachments
