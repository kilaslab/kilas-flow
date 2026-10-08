---
id: BUG-sgrxhh
title: 'continueErrorOutput error branch is not drawn on the canvas; Settings shows ''Continue on Fail: Disabled'''
status: done
priority: high
labels:
    - editor
    - error-handling
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T15:58:00Z"
---

# Description

The engine routes failed items down the error output, but the editor neither draws that output nor lets the user create or remove it, and the Settings tab says errors stop the node.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-6). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A node set to On Error → "Continue (using error output)" shows two outputs, Success and Error, with the error branch drawn and editable. The Settings tab shows the On Error mode.

# Steps to Reproduce

1. Open `[ux-debug] h continue on fail` (the n8n import has `onError: continueErrorOutput` on `Call API`, whose error output goes to `Failure`). 2. Look at the canvas. 3. Select Call API → Settings. 4. Run it and open the execution.

# Expected

The dynamic `error` output port drawn when `settings.onError == continueErrorOutput`, with its edge and item count, and an On Error selector (Stop / Continue / Continue using error output) in Settings.

# Actual

The document holds `{nodeId: call-api, port: error} → failure`, and at run time `Failure` gets the 2 failed items. Both the editor canvas and the replay draw `Failure` with no incoming edge and only one output handle on `Call API`, so the snapshot has no "Call API error to Failure" edge. The Settings tab lists `Continue on Fail: Disabled`, `Retry on Fail`, `Timeout` and `Always Output Data`, with no On Error control, so the panel says errors stop the node while the workflow actually routes them. A user cannot see, create or remove the error branch in the UI.

# Acceptance Criteria
- [x] When `onError == continueErrorOutput`, the node shows Success and Error outputs, with the edge and item counts drawn (mirroring `withErrorPort` in `resolvedPorts`)
- [x] Settings has an On Error selector (Stop workflow / Continue / Continue using error output) instead of the boolean continueOnFail
- [x] Round-trip: an imported n8n workflow with an error branch shows it, and exports it back unchanged

## Verification (2026-10-08)

Fix commit: `dc29ff1`. This verification also made a small follow-up fix to the execution replay, described in the first criterion.

- **Success and Error outputs, with the edge and item counts drawn (met after the follow-up fix).** In the editor, `withErrorPort` (`web/src/lib/workflow-editor/ports.ts:130`) sits inside `resolvedPorts` (`:140`). Tests: `ports.test.ts:415`, `:425` and `:431`. `edgeItemCounts` counts through `resolvedPorts`. The imported Call API (`onError: continueErrorOutput`) shows "Success" and "Error" outputs, with the error edge drawn to Failure (`09-error-branch-run.png`).
  - **Gap found and fixed:** the execution replay node (`execution-canvas-node.svelte`) built its handles from the bare definition. The replay therefore drew Call API with one output and dropped the "Call API error to Failure" edge, even though Failure ran (`10-error-branch-execution.png`). This is the ticket's "the replay draws Failure with no incoming edge".
  - The replay node now uses `resolvedPorts` and `portLabel`, like the editor. After rebuilding, the replay shows the Success and Error handles plus the edges "Call API error to Failure main, 1 item" and "Call API main to Ok main, 0 items" (`12-error-branch-execution-fixed.png`).
  - Regression test added: `execution.test.ts` "counts the error branch of a node that continues using its error output".
- **On Error selector instead of the continueOnFail boolean (met).** `nodes/core.go:333` defines the `onError` options Stop Workflow, Continue (regular output) and Continue (using error output). In the app, the Settings tab shows "On Error: Continue (using error output)" and no Continue on Fail control (`07-callapi-settings-onerror.png`).
- **Round trip (met).** Import wires the n8n `main[1]` branch to the `error` port, and export writes `onError` back. `TestOnErrorContinueRegularOutputBecomesContinueOnFail` (`internal/interop/n8n/importer_tail_test.go:697`) was extended here to assert that the exported Call node keeps `onError: continueErrorOutput` and that `Connections["Call"]["main"][1]` targets the same node again. In the app, the imported error branch is visible on the canvas and in the replay.

Not blocking, for a follow-up: a legacy document that carries only `continueOnFail: true` has no `onError`. That covers an n8n 0.x import and a KilasFlow save from before this change. The selector then shows its default, "Stop Workflow", while the runner (`internal/engine/runner.go` `onErrorMode`) continues on the regular output. Suggested fix: the importer writes `onError: continueRegularOutput` when it reads the boolean, and the panel shows `continueOnFail` as Continue (regular output) when `onError` is absent.

Self-test setup: `make build-web` + `go build ./cmd/kilasflow` from this branch, served on 127.0.0.1:18095 with an isolated SQLite file (`KILASFLOW_DATABASE_DSN=/tmp/kf-verify/data/kilasflow.db`), driven with playwright-cli. Probe workflows were imported through `POST /api/v1/workflows/import`. Screenshots are in `/tmp/kf-verify/` and are not committed.

# Implementation Plan

Mirror `withErrorPort` in `resolvedPorts`, labelled Success/Error, and add an `onError` options setting in place of the boolean continueOnFail.

# Notes

Related tickets: BUG-c241hm, FEAT-56nep4

Related (from the audit): FEAT-56nep4 (done; "Settings tab lacks On Error modes" still reproduces), BUG-c241hm (done, engine side works)

# Related Files

`$SP/agents/ux-debug/h-02-editor-error-output.png`, `h-01-continue-error-output.png`, `h-03-http-settings-tab.png`. internal/workflow/compiler.go:877-900 (the server adds the `error` port), web/src/lib/workflow-editor/ports.ts:110-124 (`resolvedPorts` never adds it). nodes/core.go shared settings have no onError.

# Attachments
