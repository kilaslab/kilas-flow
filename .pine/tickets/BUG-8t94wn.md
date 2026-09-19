---
id: BUG-8t94wn
title: 'Wait/sub-workflow mapping: expression amounts, v1/v2 ports, workflowInputs, SplitInBatches'
status: todo
priority: high
labels:
    - nodes
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:core-node-parity, find:engine-runtime, find:importer-fidelity, find:webhook-trigger-parity.

---
### SplitInBatches v1/v2 imported with v3 port semantics: loop body is wired to 'done' and the run fails [find:core-node-parity] (medium/bug) · area: importer / SplitInBatches · confidence: high

Every splitInBatches version is mapped onto the v3 loop (output 0 = done, output 1 = loop) with no typeVersion handling. In v1/v2 the only output (index 0) is the batch, so the loop body ends up attached to 'done'. The noItemsLeft context idiom is not reported either.

Evidence: cases/v3_sib_v1.json uses the classic v1 loop: SIB batchSize 2 → Set → IF v1 {{$node["SIB"].context["noItemsLeft"]}} → Done, with the false branch back to SIB. n8n runs SIB 3 times, all 5 items get marked, and Done runs. KilasFlow emits the batch on the unconnected loop port and fails 'compiled workflow graph has no schedulable node'. The import raises no issue. Code: internal/interop/n8n/parameters.go:1941-1960 and n8n.go:356.

n8n behavior: v1/v2 have a single batch output. The loop ends when an IF reads context.noItemsLeft.

Impact: splitInBatches@1 appears in 2 of the 100 templates and is common in older community workflows.

Suggested fix: For typeVersion < 3, remap output 0 to the loop port and synthesise the done edge, or raise a blocking issue. Translate the noItemsLeft IF idiom.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

Existing tickets: FEAT-vvwpjw, FEAT-sar60r

---
### Execute Sub-workflow: workflowInputs mapping ('Define using fields below') is ignored, so the sub receives the caller's raw items [find:core-node-parity] (medium/parity-gap) · area: importer + sub-workflow trigger · confidence: high

executeWorkflowToKilas never reads workflowInputs.value, and the 'fields' input source does not project items onto the declared inputs. The sub-workflow gets the parent's items unchanged: mapped fields are missing and undeclared fields leak through. The import also always raises a *blocking* workflowId issue, even when the ID resolves to an active KilasFlow workflow and activation succeeds.

Evidence: cases/v2_sub_mapping.json: the parent maps {id:{{$json.id}}, label:'L-{{$json.name}}'}, and the sub trigger 1.1 declares [id:number, label]. n8n gives {id:1,label:'L-a',fromSub:'1-'}; KilasFlow gives {id:1,name:'a',fromSub:'1-a'}. These cases match: passthrough once, each, waitForSubWorkflow=false and v1 string IDs (v2_sub_once/each/nowait/v1_string_id). Code: internal/interop/n8n/parameters.go:941-1030 and nodes/subworkflow.go.

n8n behavior: Evaluates the mapping per item in the caller and passes only the declared, typed fields to the sub-workflow.

Impact: executeWorkflowTrigger 1.1 appears in 10 of the 100 templates, and modern n8n sub-workflows default to typed inputs.

Suggested fix: Import workflowInputs.value as a per-item mapping and have the trigger project and convert to the declared fields. Downgrade the workflowId issue when the ID resolves locally.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/subworkflow.go

Existing tickets: FEAT-az620p (AC 29)

---
### Wait amount set by an expression, or omitted (n8n default), imports as 'no wait' [find:core-node-parity] (medium/bug) · area: importer / Wait · confidence: high

waitToKilas copies `amount` without fromN8NValue, so an expression amount stays a raw "={{...}}" string and becomes 0. A missing amount (n8n omits its default) also becomes 0.

Evidence: - wait_expr_amount (amount '={{ $json.w }}', w=1): n8n waits 1.03 s; KilasFlow finishes in 0.03 s.
- wait_default_amount (Wait 1.1 with only unit=seconds): n8n waits 5.03 s; KilasFlow 0.03 s.
Code: internal/interop/n8n/parameters.go:1313-1315; numberValue() in nodes/wait.go:180.

n8n behavior: Evaluates the expression and applies the per-version default amount.

Impact: Rate-limit pauses silently disappear, which can trigger 429s from third-party APIs.

Suggested fix: Pass amount through fromN8NValue, apply n8n's default amount when absent, and raise an issue when the amount cannot be read.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/wait.go

Existing tickets: FEAT-q81bq4

---
### Split In Batches v1 is wired as v3: its single batch output lands on the loop's `done` port [find:importer-fidelity] (medium/bug) · area: importer/splitInBatches · confidence: high

In v1/v2 the single output emits each batch (the loop body), and termination is an If on context["noItemsLeft"]. v3 has done=0 and loop=1. Every version is mapped to kilasflow.loop [done, loop] positionally, so a v1 body edge is attached to `done`, and no issue is raised.

Evidence: 1073 'SplitInBatches' v1: output0 -> uProc becomes done -> uProc. 1862 'Split In Batches' v1: output0 -> HTTP Request, Merge becomes done -> both. All 19 v3 loops map correctly.

n8n behavior: v1 runs the body once per batch.

Impact: 2/100 templates; the loop body runs once at the end instead of per batch.

Suggested fix: For versions below 3, remap output 0 to `loop` and synthesise `done`, or block with a message. Translate or report the noItemsLeft idiom.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-sar60r (done)

---
### Sub-workflow inputs are lost on import: a trigger without inputSource becomes passthrough, and the caller's workflowInputs mapping is dropped [find:webhook-trigger-parity] (high/bug) · area: importer / executeWorkflowTrigger + executeWorkflow · confidence: high

n8n's ExecuteWorkflowTrigger defaults inputSource to 'workflowInputs' for v1.1 and later, so exports omit it. KF imports that as passthrough and discards the declared fields. The Execute Workflow caller's workflowInputs mapping (v1.2) is dropped without an issue, and KF's caller node has no inputs mapping at all.

Evidence: n8n 2.33.7 ExecuteWorkflowTrigger definition (container) shows inputSource defaulting to WORKFLOW_INPUTS for version 1.1 and later. KF import of trigger v1.1 {workflowInputs:{values:[requestId,jobType,data]}} gives {inputSource:'passthrough'}. Caller v1.2 {workflowInputs:{mappingMode:'defineBelow', value:{jobType:'report', requestId:'={{ $json.id }}', data:'={{ $json }}'}}} gives {itemsPerCall, mode, workflowId}; the only issue raised is about workflowId. KF's 'fields' mode is documentation-only and never shapes the item. Code: internal/interop/n8n/parameters.go:1013-1029 and 942-989; nodes/subworkflow.go:35-70 and 110-125.

n8n behavior: The sub-workflow receives exactly the mapped workflowInputs fields.

Impact: Trigger side: 7/100 templates (2878, 3770, 3050, 3135, 3790, 2085, 3514). Caller mapping: 2878 (3 calls; its sub-workflow routes on jobType, which the caller no longer sends, so no branch runs) and 3770. Every modern n8n sub-workflow pair is affected.

Suggested fix: Treat a missing inputSource on v1.1+ as workflowInputs. Add a workflowInputs mapping (key/value with expressions) to kilasflow.executeWorkflow and import it. Make 'fields' mode shape items like n8n: declared keys only, missing keys set to null, optional type conversion.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/subworkflow.go

Existing tickets: FEAT-az620p

---
### SplitInBatches v1/v2 imports silently wire the batch output to the loop's `done` port, and the run dies with 'compiled workflow graph has no schedulable node' [find:engine-runtime] (medium/bug) · area: importer SIB mapping + runner error text · confidence: high

Every splitInBatches version maps to kilasflow.loop, whose output 0 is `done` (n8n.go:356). The single batch output of v1/v2 therefore lands on `done`, and no diagnostic is emitted.

Evidence: Private :8101, script <W>/t_sib_v1.py: a classic v1 loop (SIB v1 -> Process -> IF noItemsLeft -> false back to SIB / true -> Finished). The import reports no issues and stores the connection `l.done -> Process`. Running it over [1,2,3] ends in status failed, `compiled workflow graph has no schedulable node`.

n8n behavior: v1/v2 have a single output carrying each batch. The loop is closed by an IF on context.noItemsLeft.

Impact: 2/100 top templates, plus many pre-2023 customer workflows, fail with a confusing error.

Suggested fix: Map typeVersion < 3 output 0 onto the `loop` port and treat the IF exit as done, or flag the node as blocking. Make the scheduler error name the nodes that cannot be scheduled.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go, internal/engine/runner.go

Existing tickets: FEAT-vvwpjw

---
### Wait v1.1 imports with v1 defaults: a missing unit becomes 'hours' (n8n: seconds) and a missing amount becomes 0 (n8n: 5) [find:webhook-trigger-parity] (high/bug) · area: importer / wait · confidence: high

n8n's Wait defaults differ by version: v1 is amount 1 / unit hours, v1.1 is amount 5 / unit seconds. The importer always fills unit 'hours' and copies amount as-is. So 'wait 3 seconds' becomes 3 hours and fails the 1 h cap, and a missing amount means no wait at all.

Evidence: The n8n 2.33.7 Wait node definition inside the container shows v1.1 defaults amount 5 and unit 'seconds'. KF import of Wait v1.1 {amount:3} named 'Wait 3 seconds' gives {amount:3, unit:'hours'}; a manual run fails with 'a wait of 3h0m0s is longer than this server's limit of 1h0m0s'. An empty {} or {unit:'minutes'} leaves amount unset, which numberValue reads as 0, so there is no pause. No import issue in either case. Code: internal/interop/n8n/parameters.go:1314-1316; nodes/wait.go:176-183.

n8n behavior: Wait v1.1 with {amount:3} pauses 3 seconds; with {} it pauses 5 seconds.

Impact: 14/100 templates: 2320, 2340, 2466, 2557, 2567, 2605, 2896, 2982, 3121, 3442, 4352, 4846, 5035, 5171. Short rate-limit pauses become 1-60 hour suspensions or hard failures, or disappear.

Suggested fix: Use version-aware defaults (1.1: amount 5, unit seconds; 1: amount 1, unit hours) and write them explicitly into the imported parameters.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/wait.go

## Acceptance criteria

- [ ] SplitInBatches v1/v2 imported with v3 port semantics: loop body is wired to 'done' and the run fails
- [ ] Execute Sub-workflow: workflowInputs mapping ('Define using fields below') is ignored, so the sub receives the
- [ ] Wait amount set by an expression, or omitted (n8n default), imports as 'no wait'
- [ ] Split In Batches v1 is wired as v3: its single batch output lands on the loop's `done` port
- [ ] Sub-workflow inputs are lost on import: a trigger without inputSource becomes passthrough, and the caller's wo
- [ ] SplitInBatches v1/v2 imports silently wire the batch output to the loop's `done` port, and the run dies with '
- [ ] Wait v1.1 imports with v1 defaults: a missing unit becomes 'hours' (n8n: seconds) and a missing amount becomes
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)