---
id: BUG-w1jp70
title: The dashed + under an empty attachment slot opens an empty picker, and a node picked there is wired the wrong way
status: todo
priority: medium
labels:
    - editor
    - canvas
    - node-picker
parent: EPIC-8rbys7
created: "2026-10-09T13:43:12Z"
updated: "2026-10-09T13:43:12Z"
---

# Description

The dashed "+" under an empty attachment slot (an AI Agent's Chat Model, Memory or Tools slot, a Vector Store Tool's Chat Model or Vector Store slot) is meant to be the shortest path to filling the slot: pick a node, and it arrives already wired. It does neither.

1. **The picker offers nothing.** `openPickerFrom(nodeID, port, kind)` in `web/src/lib/components/workflow-editor/workflow-editor.svelte` sets `pendingSource` and `pendingKind`; the picker is given `connecting={Boolean(pendingSource)}` and `providesKind={pendingKind}`, `node-picker.svelte` turns those into `catalogEntries(definitions, { acceptsMain: connecting, providesKind })`, and `web/src/lib/workflow-editor/catalog.ts` then requires BOTH a main input (`acceptsMain`) AND an output of the slot's kind (`providesKind`). A sub-node (chat model, memory, tool, embeddings) has no main input, so no node satisfies both and the list is empty. The unit test fixture hid it: its Chat Model is spread from a step definition, so it carries a main input a real one does not.
2. **A picked node is wired the wrong way.** `addNode` (same file) builds the connection from the pending node's port as the SOURCE and looks for an INPUT of the slot's kind on the new node (`ports.inputs.find((port) => port.kind === (pendingKind ?? 'main'))`). For an attachment slot the new node is the provider: its OUTPUT of that kind must feed the consumer's slot, so the lookup finds nothing and the node is added unwired.
3. The new sub-node is also placed one column to the right of the consumer, where the main-output "+" puts a next step, instead of under it where the slot is.

# Steps to Reproduce

1. Open a workflow with a Manual Trigger and an AI Agent (no chat model wired).
2. Click the dashed "+" under the Agent's "Chat Model" slot.
3. Look at the picker.

# Expected

The picker lists the nodes that provide a language model (Chat Model, OpenAI Chat Model, ...). Choosing one adds it below the Agent with its `model` output wired to the Agent's `model` slot, and the "+" under that slot disappears. The same holds for the Memory and Tools slots, and for a Vector Store Tool's slots.

# Actual

The picker is empty ("no results"). If a node were added some other way from that flow it would not be connected to the slot.

# Acceptance Criteria
- [ ] `catalogEntries` applies `acceptsMain` only when no `providesKind` is set, so a slot's picker lists the nodes that provide that kind (vitest on the filter with a realistic sub-node that has no main input, and with `acceptsMain` alone still excluding sub-nodes)
- [ ] A node picked for an attachment slot is connected from its own output of the slot's kind to the consumer's slot, through the same validator a dragged wire uses; a main-output "+" still wires main to main (vitest on the extracted connection builder: both directions, refused when the new node has no output of that kind)
- [ ] The picked sub-node is placed under the consumer, clear of other nodes, rather than to its right (vitest on the position helper)
- [ ] An e2e spec clicks the "+" under an Agent's Chat Model slot, picks a chat model from the list, and finds it wired to the slot with the "+" gone

# Implementation Plan

Fix the filter in `catalog.ts`; extract the picked-node wiring out of `addNode` into a pure function beside `connectionFromCanvas` in `ports.ts` (source/target chosen by whether the pending slot is an attachment) so it can be unit tested; add a `positionBelow` helper next to `positionAfter` in `document.ts`; add the e2e.

# Notes

Found in review of BUG-pc284d (2026-10-09). Related: the picker filters on a node type's static ports, so a vector store is never offered as an `ai_tool` / `ai_vectorStore` provider from a slot "+" (separate feature ticket, depends on this one). Dropping a wire from an attachment OUTPUT handle onto empty canvas has the mirror problem (it asks for a main input) and is not part of this ticket.

# Related Files
- web/src/lib/components/workflow-editor/workflow-editor.svelte — openPickerFrom, addNode
- web/src/lib/components/workflow-editor/node-picker.svelte — the catalogue filter
- web/src/lib/workflow-editor/catalog.ts — catalogEntries
- web/src/lib/workflow-editor/ports.ts — connectionFromCanvas
- web/src/lib/workflow-editor/document.ts — positionAfter
