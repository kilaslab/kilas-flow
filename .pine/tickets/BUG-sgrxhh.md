---
id: BUG-sgrxhh
title: 'continueErrorOutput error branch is not drawn on the canvas; Settings shows ''Continue on Fail: Disabled'''
status: todo
priority: high
labels:
    - editor
    - error-handling
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
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
- [ ] When `onError == continueErrorOutput`, the node shows Success and Error outputs, with the edge and item counts drawn (mirroring `withErrorPort` in `resolvedPorts`)
- [ ] Settings has an On Error selector (Stop workflow / Continue / Continue using error output) instead of the boolean continueOnFail
- [ ] Round-trip: an imported n8n workflow with an error branch shows it, and exports it back unchanged

# Implementation Plan

Mirror `withErrorPort` in `resolvedPorts`, labelled Success/Error, and add an `onError` options setting in place of the boolean continueOnFail.

# Notes

Related tickets: BUG-c241hm, FEAT-56nep4

Related (from the audit): FEAT-56nep4 (done; "Settings tab lacks On Error modes" still reproduces), BUG-c241hm (done, engine side works)

# Related Files

`$SP/agents/ux-debug/h-02-editor-error-output.png`, `h-01-continue-error-output.png`, `h-03-http-settings-tab.png`. internal/workflow/compiler.go:877-900 (the server adds the `error` port), web/src/lib/workflow-editor/ports.ts:110-124 (`resolvedPorts` never adds it). nodes/core.go shared settings have no onError.

# Attachments
