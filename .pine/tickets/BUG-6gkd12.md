---
id: BUG-6gkd12
title: 'Round-trip fidelity: node notes, settings, tags dropped; versions shifted (If downgraded); get→validate not symmetric'
status: todo
priority: low
labels:
    - importer
    - round-trip
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
---

# Description

Export must re-open cleanly in n8n, and the CLI's get → edit → validate loop must accept its own output.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-12, TPL-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## TPL-12: The round trip also drops node notes and workflow settings, and shifts node versions (If is downgraded)

*gap · low · importer*

**n8n:** Node `notes`/`notesInFlow`, workflow `settings` (including `executionOrder`) and `typeVersion` are part of the workflow JSON.

**Steps to reproduce:**

`python3 agents/n8n-templates/roundtrip.py 1954 1750 5170 2462 1747 2753`, then diff `roundtrip/<id>.diff.json`.

**Actual:**

- No node or edge was lost, and placeholders and Code nodes came back unchanged. But:
  - All 24 node notes in 5170 are gone.
  - Every credential reference is stripped.
  - Workflow `settings` (`{"executionOrder":"v1"}` in 2753), `meta`, `pinData` and `tags` are not exported.
  - Versions change: Agent 1.7/1.8 → 3.1, Webhook 1 → 2, Merge 2 → 3, Code 1 → 2, Telegram 1.1 → 1.2, and If 2.2/2.3 → 2 (a downgrade).
- Each version change is listed as lossy.

**Expected:**

Notes and settings round-trip, and a node's authored version is kept when the translator can write it.

**Suggested fix:**

Add a notes field to the canonical node (as planned in BUG-gaavr5), export `settings.executionOrder: "v1"`, and pin the If export to the source version when it is ≥ 2.2.

**Evidence:**

`roundtrip/summary.json`, `roundtrip/5170.diff.json`, `roundtrip/2753.diff.json`, `roundtrip/2462.diff.json`, `internal/interop/n8n/n8n.go:1941` (notes dropped)

**Related:**

BUG-gaavr5 (done). "Per-node notes are discarded" is still unchecked and still reproduces.


## TPL-15: `workflow get` returns a document that `workflow validate` refuses ("unexpected property body.id")

*ux · low · onboarding*

**n8n:** Not applicable.

**Steps to reproduce:**

1. `kilasflow workflow get <id> --url …`, then save `.data.latestVersion.document` to `doc.json`.
2. `kilasflow workflow validate --file doc.json --url …`.
3. I reproduced it on the workflows for 2384 and 1750.

**Actual:**

`{"ok":false,"error":{"code":"bad_request","message":"validation failed","status":422,…"errors":[{"message":"unexpected property","location":"body.id",…`

**Expected:**

The document the API returns validates as-is, because get → edit → validate is the agent loop the CLI is built for. Validate could ignore `id`, or `get` could offer a `--document` form without it.

**Suggested fix:**

Accept and ignore `id` in `WorkflowDocumentInput`, or strip it client-side in `workflow validate`.

**Evidence:**

`agents/n8n-templates/doc_2384.json`, `doc_1750.json`; `WorkflowDocumentInput` in `/api/openapi.json` has `additionalProperties: false` and no `id`.

**Related:**

none


# Acceptance Criteria
- [ ] Node `notes`/`notesInFlow` are part of the canonical node and round-trip
- [ ] Workflow `settings.executionOrder` is exported; tags round-trip
- [ ] A node's authored `typeVersion` is kept when the translator can write it (If ≥ 2.2 is not downgraded)
- [ ] The document returned by `workflow get` passes `workflow validate` unchanged (`id` is accepted and ignored)

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-gaavr5

# Related Files

# Attachments
