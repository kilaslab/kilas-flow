---
id: FEAT-70j6dn
title: Execute step, partial execution, and pinned data (imported pinData is dropped today)
status: todo
priority: high
labels:
    - editor
    - debugging
    - pinned-data
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

Pinned data and Execute step are how n8n users build and debug without re-hitting real services. Imported `pinData` is dropped, the run API has no destination node, and Execute is disabled until the draft is saved.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-3). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** "Execute step" on a node runs it and the nodes before it, reusing earlier nodes' last run data, or pinned data where it exists. Pinned output (P key, or the pin icon; editable) persists with the workflow and is used on later manual runs instead of calling the real service. The unsaved canvas can be executed.

# Steps to Reproduce

1. Import `wf/a.json` with `pinData: {"Call API":[{"json":{"ok":true,"pinned":"yes"}}]}` (workflow `[ux-debug] p pinned import`). 2. Run it. 3. Look for a pin or Execute step control on the canvas node toolbar or in the node panel. 4. Check `POST /api/v1/workflows/{id}/run`.

# Expected

Pin, edit and unpin output per node, stored in the document and honoured by manual runs. Execute step and "execute previous nodes" backed by a partial-run API that reuses the last run's outputs. Execute works on the unsaved draft.

# Actual

The import report says `{"severity":"dropped","field":"pinData","reason":"pinned test data is an n8n editor feature with no KilasFlow equivalent; it was not carried, so the nodes that had it pinned will run for real"}`, and the run calls the real endpoint and fails with a 500. The node toolbar has only rename and delete, and there are no P or D shortcuts (shortcuts.ts). The run body accepts only `input`, `triggerNodeId` and `workflowVersionId`, so there is no destination node, no start node and no pin data. The Execute button is `disabled={dirty || running || previewing}`, so every tweak must be saved as a new revision before it can be tested.

# Acceptance Criteria
- [ ] `pinData` is part of the canonical document, the importer carries it, and the exporter writes it back
- [ ] Manual runs substitute pinned outputs; the node toolbar has pin/unpin/edit (P shortcut)
- [ ] The run API accepts `destinationNodeId` and reuses the last run's data for Execute step and "execute previous nodes"
- [ ] Execute works on the unsaved draft (runs the in-memory document without creating a revision)

# Implementation Plan

Add `pinData` to the canonical document and the importer, and have the runner substitute pinned outputs in manual mode. Add `destinationNodeId`/`startNodeId` plus `runData` reuse to the run API, and put Execute step and pin buttons on the node toolbar.

# Notes

Related tickets: FEAT-56nep4, FEAT-nbqye0

Related (from the audit): FEAT-56nep4 (done, item unchecked), FEAT-nbqye0

# Related Files

`$SP/agents/ux-debug/cli/run_p.json`, `a-04-node-click.png` (toolbar). OpenAPI RunWorkflowInputBody (`$SP/agents/ux-debug/openapi.json`). web/src/lib/workflow-editor/shortcuts.ts:84-110. workflow-editor.svelte:1036 and :1262.

# Attachments
