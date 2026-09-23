---
id: BUG-txafja
title: Pasting n8n JSON onto the canvas bypasses the importer (placeholders, JS silently lost, '=' expressions literal)
status: todo
priority: high
labels:
    - editor
    - importer
    - clipboard
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
---

# Description

n8n users copy nodes between editors constantly. A paste should produce exactly what "Import n8n" would; today it matches types by suffix on the client.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-3). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Copying nodes in n8n and pasting them into another n8n canvas reproduces them exactly, expressions included. KilasFlow advertises the same paste (the "Pasted 3 nodes" status).

# Steps to Reproduce

1. Copy SD/n8n-snippet.json (Manual Trigger → Edit Fields (Set 3.4) → HTTP Request with url `=https://example.com/{{ $json.city }}`) to the clipboard. 2. Click the canvas and press Cmd+V. 3. Open each pasted node. 4. Repeat with an n8n Code (jsCode) node and an IF v2 node (SD/paste-code.js).

# Expected

A paste produces the same nodes and parameters as "Import n8n" would.

# Actual

Status "Pasted 3 nodes · 1 placeholder to replace · 1 connection not placed". `n8n-nodes-base.manualTrigger` becomes an **Unsupported** placeholder drawn with a dozen AI ports, so its connection is dropped. The HTTP URL stays in **fixed** mode as the literal `=https://example.com/{{ $json.city }}`. The n8n JS Code node becomes the **Go** `kilasflow.code` node with its default `return items, nil`, and the JavaScript is not shown anywhere. The IF left value is the literal `={{ $json.x }}`. The server importer handles all of these correctly: manualTrigger→kilasflow.manual (n8n.go:226), code→foreignCode (n8n.go:342), and "=" expressions (parameters.go:53).

# Acceptance Criteria
- [ ] Pasted n8n JSON goes through the server importer (or a shared translator) and yields the same nodes and parameters as Import
- [ ] A Manual Trigger pastes as a manual trigger; a JS Code node becomes foreignCode (or jsCode once the runtime exists), never a Go Code node with the source lost
- [ ] `=`-prefixed values become expressions
- [ ] Paste shows the same import diagnostics

# Implementation Plan

Send pasted n8n payloads through the server importer (a "convert fragment" endpoint that reuses internal/interop/n8n and returns canonical nodes plus diagnostics), and show its report the way import does.

# Notes

Related tickets: FEAT-0556ck, FEAT-jvembs

Related (from the audit): FEAT-jvembs (done; claimed n8n paste), FEAT-0556ck. Placeholder rendering is TPL-5.

# Related Files

SD/37-paste-n8n.png, SD/38-pasted-http.png, SD/77-paste-n8n-code-if.png. Code: web/src/lib/workflow-editor/clipboard.ts:151-200 `convertNode` copies parameters verbatim, and `matchDefinition` matches by type-name suffix (`.code` → kilasflow.code, `.manualTrigger` → nothing).

# Attachments
