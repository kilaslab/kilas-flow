---
id: FEAT-jrrqs2
title: Pin a node's output data (n8n's P key)
status: done
priority: medium
created: "2026-10-08T16:20:41Z"
updated: "2026-10-08T16:20:41Z"
---

# Description

n8n's `P` pins the selected node's last output so later runs reuse it instead of executing the node. KilasFlow has no pinned-data model: the editor document has no `pinData`, the engine never reads one, and the n8n importer drops it. BUG-3k12ky bound the rest of n8n's canvas keys (Ctrl/Cmd+Enter, D, Shift+S, Cmd+X, Shift+Alt+T, double-click) and left `P` unbound, because a key that pins nothing would be worse than no key.

# Acceptance Criteria
- [x] A node's output can be pinned from the editor and a run uses the pinned items instead of executing the node
- [x] `P` toggles the pin on the selected nodes and the help overlay lists it
- [x] An n8n import keeps `pinData`

# Related Files
- web/src/lib/workflow-editor/shortcuts.ts — add the `P` binding once pinning exists
- internal/interop/n8n/ — keep `pinData` on import

# Progress

Implemented 2026-10-25: pinData in the workflow document, engine replays pinned items without executing (trace marks pinned), n8n import keeps pinData (export carries it), editor P toggles pin + help overlay lists it. Binary attachments not carried on import (BinaryRef is a runtime key) — recorded in MAPPING_LIMITS. Gates: go test engine/workflow/execution/interop ok, web vitest 1202 ok, api/types/reference generate-checks fresh.
