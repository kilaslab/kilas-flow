---
id: FEAT-q80xe3
title: 'The node picker offers a vector store for the roles its modes play: Tools slot as a tool, Vector Store slot as a retrieve store'
status: todo
priority: low
labels:
    - editor
    - canvas
    - node-picker
    - rag
deps:
    - BUG-w1jp70
parent: EPIC-8rbys7
created: "2026-10-09T13:43:17Z"
updated: "2026-10-09T13:43:17Z"
---

# Description

The node picker decides what to offer for a slot from each node type's STATIC ports (`catalogEntries` in `web/src/lib/workflow-editor/catalog.ts` reads `definition.inputs` / `definition.outputs`). A vector store's ports depend on its mode, so as a type it only ever advertises main in and main out. From a slot "+" a vector store is therefore never offered:

- under an AI Agent's Tools slot (`ai_tool`), although a store in Retrieve as Tool mode is exactly a tool;
- under a Vector Store Tool's Vector Store slot (`ai_vectorStore`), although a store in Retrieve mode is exactly what that slot takes.

The author has to add the store from the general picker, switch its Mode, and drag the wire by hand. n8n's picker offers these stores in every role they play.

# Steps to Reproduce

1. Open a workflow with an AI Agent and a Vector Store Tool.
2. Click the dashed "+" under the Vector Store Tool's Vector Store slot (once the empty-picker bug is fixed), or under the Agent's Tools slot.
3. Look for "Postgres PGVector Store" or "Vector Store".

# Expected

The picker lists a vector store for each role it can play in that slot, labelled by the role ("Postgres PGVector Store (retrieve)", "... (as tool)"), and choosing one adds the store already in that mode and already wired.

# Actual

Neither store is listed for either slot.

# Acceptance Criteria
- [ ] The catalogue can offer a node type once per mode that gives it a different port shape, each entry carrying the parameter values that select that mode; or the node-types API serves the resolved ports for a configuration and the picker reads them (decide in the plan, and say why)
- [ ] From the Vector Store Tool's Vector Store slot, both stores are offered in Retrieve mode; from an Agent's Tools slot, both are offered in Retrieve as Tool mode; a store is not offered in a mode that does not fit the slot
- [ ] The node is created with the mode already set, wired to the slot through the slot "+" flow, and its other ports (embedding) are drawn as the mode resolves them
- [ ] The general picker still lists each store once

# Implementation Plan

Depends on the slot "+" fix (the picker must list sub-nodes at all). Two designs to weigh: (a) mode-variant catalogue entries built from each definition's `mode` options plus a per-type table of what ports a mode gives, which mirrors Go and needs the same golden protection `ports.ts` has (`nodes/testdata/ports_for.golden.json` already holds the resolved ports per mode, so the variants can be derived from it in a test); (b) a resolved-ports field on the node-types API, generated from `PortsFor` for each enumerated option, which cannot drift from Go but needs an API change and the generated clients.

# Notes

Found in review of BUG-pc284d (2026-10-09). Related: FEAT-zm3wh2 (retrieve mode and the Vector Store Tool), BUG-pc284d (the canvas resolves ports per mode).

# Related Files
- web/src/lib/workflow-editor/catalog.ts — catalogEntries
- web/src/lib/components/workflow-editor/node-picker.svelte
- nodes/rag.go — vectorStorePortsFor
- nodes/testdata/ports_for.golden.json

# Attachments
