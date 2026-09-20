---
id: FEAT-jvembs
title: 'Canvas authoring parity: undo/redo, copy/paste, rename, shortcuts, picker, minimap, tidy, edges'
status: done
priority: medium
labels:
    - editor
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:07Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 19 finding(s) from dims: find:ui-canvas, find:web-frontend-code.

---
### No undo/redo: a mistaken delete, move or tidy can only be reverted by throwing away all unsaved work [find:ui-canvas] (high/parity-gap) · area: canvas editing · confidence: high

The editor keeps no history stack, so Mod+Z and Mod+Shift+Z do nothing.

Evidence: grep for undo/redo in web/src/lib/workflow-editor and components/workflow-editor finds nothing. Repro (a6.js): select a node, press Delete → node and edges removed (6→5 nodes, 4→3 edges); Cmd/Ctrl+Z → unchanged. t1 rerun: drag Set, then Cmd/Ctrl+Z → the box stays at the dragged position (x743,y614 vs x690,y509 originally). Tidy (F26) is also irreversible.

n8n behavior: Ctrl/Cmd+Z and Ctrl/Cmd+Shift+Z (or Ctrl+Y) undo and redo add, delete, move, connect, rename and parameter edits.

Impact: Destructive actions (Delete key, Tidy) have no safety net.

Suggested fix: Keep a bounded snapshot stack around replaceDraft (drafts are already immutable clones). Bind Mod+Z / Mod+Shift+Z on the canvas region, add toolbar undo/redo buttons, and merge drag moves into a single step.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### No copy, paste or duplicate for nodes, and pasting n8n workflow JSON onto the canvas does nothing [find:ui-canvas] (high/parity-gap) · area: canvas clipboard · confidence: high

There is no copy, paste or duplicate handler. Pasting nodes+connections JSON copied from n8n (the usual way people share snippets) is ignored, so importing always means leaving the editor and creating a new workflow.

Evidence: There is no paste/copy/clipboard handler in the editor (grep). t1 rerun: select 'Set fields', Cmd/Ctrl+C then Cmd/Ctrl+V → still 6 nodes; Cmd/Ctrl+D → nothing. t15: dispatching a paste ClipboardEvent with template 1954's nodes+connections JSON (paste.json.txt) on the canvas → nodes before 6, after 6.

n8n behavior: Ctrl+C copies the selected nodes as workflow JSON. Ctrl+V pastes into the current canvas with ids regenerated and names de-duplicated, including JSON copied from n8n.io or forums. Ctrl+D duplicates.

Impact: Blocks a core n8n habit (reusing snippets across workflows) and makes KilasFlow↔n8n snippet round-tripping impossible.

Suggested fix: Copy the selection as n8n-shaped JSON. Add a paste handler that detects KilasFlow or n8n JSON and runs n8n JSON through the importer (a dry-run import returning a document fragment plus issues). Offset positions, regenerate ids and make names unique. Bind Mod+D to duplicate.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go

---
### Nodes can't be renamed, and new nodes get duplicate names [find:ui-canvas] (high/parity-gap) · area: node naming · confidence: high

createWorkflowNode names every new node after definition.displayName with no uniqueness suffix. The inspector shows the name as a static heading, and F2 or double-click do nothing. Two 'Set' nodes can coexist, and the server accepts that, although expressions address nodes by name.

Evidence: web/src/lib/workflow-editor/document.ts createWorkflowNode: name = definition.displayName. properties-panel.svelte header: <h2>{node.name}</h2>. t1 rerun: after pressing F2 the active element is still the node div, and no editor opens. The server accepted '[ui-canvas] dup names' (wf_01a0af2d-56ad-7943-9db7-2d73e0a40eca) with two nodes named 'Set'.

n8n behavior: Names are unique ('Set1'). Rename via F2, the NDV title or the context menu. Expression references to the node are rewritten on rename.

Impact: Makes $('Name') references ambiguous, and imported node names can never be changed.

Suggested fix: Make names unique on add, paste and duplicate. Add an editable title in the inspector and F2 inline rename. Propagate renames into expressions. Have the server reject duplicate names.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/properties-panel.svelte

---
### Only Delete/Backspace are wired: no Mod+S, select-all, zoom/fit keys or open-picker key, and the picker ignores arrow keys and Enter [find:ui-canvas] (medium/parity-gap) · area: keyboard shortcuts · confidence: high

Apart from Svelte Flow's deleteKey, no canvas keyboard shortcut exists. The node picker handles only Escape and Tab, so 'type, then Enter' adds nothing.

Evidence: t1 rerun on the fixture: Mod+S leaves 'Unsaved changes'; Mod+A still selects only 'Set fields'; '1' and 'Shift+1' don't change the viewport (translate(282px,473px) scale(1)); d, p and F2 are ignored. a13.js: typing 'telegram' in the picker and pressing Enter → picker still open, node count unchanged; ArrowDown leaves focus in the search box. Code: workflow-editor.svelte:560 deleteKey only; node-picker.svelte:75 handleKeydown handles Escape/Tab only.

n8n behavior: Mod+S save, Mod+A select all, 1 fit view, 0 reset zoom, +/- zoom, Tab or N open the node creator, Enter opens the NDV, F2 rename, D deactivate, P pin, Shift+Alt+T tidy, and arrow keys + Enter inside the node creator.

Impact: Slow editing for n8n users, and saving has no keyboard path.

Suggested fix: Add a canvas-level keymap that is ignored while focus is in inputs, roving focus with Enter in the picker, and a '?' help overlay listing the shortcuts.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/node-picker.svelte

---
### AI Agent slots have no '+': adding a model, memory or tool means the generic picker plus a manual drag to a 10px handle, and the slot labels overlap [find:ui-canvas] (medium/parity-gap) · area: AI sub-node wiring · confidence: high

'+' buttons exist only for main outputs. To attach a Chat Model the user adds it from the generic picker (it lands on the grid, sometimes on top of other nodes) and drags from its top handle to the agent's bottom slot. The agent's four slot labels overlap at the 68px tile width, and an empty required Chat Model slot has no warning.

Evidence: canvas-node.svelte renders the add-step button only inside `{#each mainOutputs}`; attachmentInputs get just a Handle and a label. 13-ai-connected.png shows labels merging into 'Chat MoMemoryToolsOutput Parser' and the new model tile landing on the first agent's labels. t5: dragging model→Memory slot is refused (correct), model→Chat Model connects, and a duplicate drag is refused.

n8n behavior: Each agent slot has a '+' that opens the node creator filtered to that connection type and wires the result. A required empty slot is marked (red asterisk; design-refs n8n-v2 02).

Impact: Building the most common AI pattern (agent + model + memory + tools) is slow and error-prone.

Suggested fix: Add a '+' per attachment input that calls openPickerFrom with the slot kind. Filter the picker by the matching output kind, place the new node under the slot, auto-connect it, stagger or abbreviate the labels, and mark required empty slots.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/node-picker.svelte

---
### Node picker lists each registered version as a separate entry and offers nodes that can never run (Unsupported ×4, foreign Code) [find:ui-canvas] (medium/ux) · area: node picker · confidence: high

The picker shows one entry per registered type@version, with no version collapsing and no hidden flag. It also offers the import-only placeholders.

Evidence: t4.out sections: 'Database: MySQL, MySQL, PostgreSQL, PostgreSQL'; 'Messaging: Telegram, WAHA, WAHA'; 'Triggers: … WAHA Trigger, WAHA Trigger'; 'Imported: Unsupported node ×4'; 'Core: … Code, Code (JavaScript or Python)'. The foreignCode description says 'written in a language this server does not run… cannot be activated'. Code: node-picker.svelte iterates every Definition; the footer reads '56 of 56 nodes'.

n8n behavior: One entry per node type (the latest version). Older versions stay only for existing workflows, and nothing un-runnable is offered.

Impact: Confusing duplicates, and users can add nodes that block activation.

Suggested fix: Group definitions by type and offer the highest version. Add a catalog flag (for example hidden/importOnly) for kilasflow.unsupported and kilasflow.foreignCode.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/node-picker.svelte, /Users/izzadev/projects/k-flow/nodes/unsupported.go

---
### New and imported nodes are placed without regard to the viewport or existing nodes [find:ui-canvas] (medium/ux) · area: node placement · confidence: high

'Add step' with no source node uses a fixed 4-column grid from (60,60) based only on the node count. On large workflows the new node lands far outside the visible area or overlaps existing nodes, and the view doesn't move to it. Imported n8n coordinates are used 1:1 even though KilasFlow's wide tiles are wider than n8n's.

Evidence: document.ts nextNodePosition(index). Template 2465 (28 nodes): 'Add step' → Set lands at (60,1012), the bottom-left corner under the zoom controls at 0.32 zoom (t16.out, 24-2465-add-step.png). On the Scratch canvas the model tile overlaps the first agent's labels (13-ai-connected.png). Imported overlaps are visible in 04-zoom-placeholders.png.

n8n behavior: A node added from the header goes to the right of the last or selected node (auto-connected) or at the viewport centre, and the canvas pans to it.

Impact: Users lose track of the node they just added, especially on imported templates.

Suggested fix: Place new nodes at the viewport centre (screenToFlowPosition) or after the selected node using positionAfter's collision check. Auto-connect from the selected node, then setCenter on it. Rescale imported positions to the KilasFlow grid.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### No disable/enable, pin data or node notes, and imported disabled nodes become live [find:ui-canvas] (medium/parity-gap) · area: node-level actions · confidence: high

The Node model has no disabled, notes or pinData fields. The editor can't deactivate or pin a node, and the importer drops n8n's disabled flag, so disabled nodes, triggers included, will run.

Evidence: web/src/lib/api/generated/models/node.ts: id, name, type, typeVersion, position, parameters, settings, credentials only. Keys 'd' and 'p' do nothing (t1), and the node toolbar has only Delete. Import baseline issue: "n8n's disabled flag has no KilasFlow equivalent, so this node will run". There are 13 disabled non-sticky nodes in templates 1073, 2557, 2605, 2786, 2982 and 3066, including a Webhook, a Schedule Trigger and a Chat Trigger. pinData is dropped in 7 templates and node notes on 94 nodes. BUG-xmcm8x (done) explicitly deferred 'disabled' as a future feature, and I found no open ticket for it.

n8n behavior: D toggles deactivate (the node is greyed and passes data through). P pins the output data used by test runs. Notes can be shown on the canvas.

Impact: Imported workflows can trigger or run steps their author had switched off, and test-driven editing with pinned data isn't possible.

Suggested fix: Add `disabled` to the node model with runner pass-through, canvas styling and a toolbar toggle. Carry notes. Add an editor-only pinData section of the document used by manual runs.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

Existing tickets: BUG-xmcm8x

---
### Execute requires a save first and runs webhook-style triggers with empty input, so they 'succeed' with no data; there is no way to supply test input [find:ui-canvas] (medium/parity-gap) · area: manual execution semantics · confidence: high

The run button is disabled while the canvas is dirty, and it runs the saved revision with no input, although the API accepts `input`. A Webhook-triggered workflow runs immediately with an empty item and reports success.

Evidence: workflow-editor.svelte run() returns if dirty (mobile label 'Save to execute'). +page.svelte runWorkflow(id) sends no body. Probe: '[ui-canvas] Webhook exec' (Webhook→Set), POST /run {} → exec_01a0b8d5-eded-74f5-a941-ba8756db0a3b succeeded, and the webhook node output is [{json:{}}]. The UI has no listening state or test URL.

n8n behavior: Execute runs the current, unsaved canvas. For Webhook, Form or Chat triggers it waits for a test event on the test URL, or opens the chat or form panel.

Impact: Testing webhook and chat flows from the editor gives meaningless green runs.

Suggested fix: Allow running the draft (send the document with the run, or auto-save a scratch revision). For webhook-like triggers, show a 'waiting for test event' state with the test URL, or an input-JSON dialog wired to `input`.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

---
### Workflows can't be renamed, and workflow settings (such as timezone) have no UI [find:ui-canvas] (medium/parity-gap) · area: workflow metadata · confidence: high

The editor breadcrumb shows the name as static text, and the list page sets a name only at creation. document.settings is honoured by the engine (timezone) but can't be seen or edited anywhere.

Evidence: +page.svelte breadcrumb snippet: <p …>{currentWorkflow?.name}</p>. workflows/+page.svelte only uses the name in the create dialog. The compiler and scheduler read settings.timezone (internal/workflow/compiler.go:738, internal/scheduler/extract.go:17), but no editor code reads or writes draft.settings.

n8n behavior: Click the title to rename inline. A Workflow Settings modal covers timezone, error workflow, execution-saving options and timeout.

Impact: Imported workflows keep their n8n names, and there is no UI control over settings that affect scheduling.

Suggested fix: Add an inline-editable title that saves the name, and a workflow settings dialog for the settings the engine honours.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte

---
### Large workflows can't be seen whole: fit is clamped at minZoom 0.3, there is no minimap, and the inspector hides right-edge nodes [find:ui-canvas] (medium/ux) · area: zoom / fit / minimap · confidence: high

minZoom={0.3} with fitView leaves big imported workflows partly off-screen with illegible labels, and there is no minimap for orientation. Opening the 20rem inspector covers the right part of the canvas without panning.

Evidence: workflow-editor.svelte:560 minZoom={0.3}, no <MiniMap>. Template 2431 (wf_01a0af16-3780-7c3c-b652-6b384aaaf13f, 63 nodes, 6340px wide) opens at scale(0.3) with 19/63 nodes off-screen (a15.js, a15-2431-fit.png); 12px labels render at about 3.6px. 2465 opens at 0.32. a10.js: after selecting a right-edge node, elementFromPoint at its centre returns the inspector, so the node can't be dragged.

n8n behavior: Zoom-to-fit shows the whole workflow on open, a minimap appears while panning, and the NDV is an overlay, so the canvas keeps its size.

Impact: Hard to navigate the medium and large templates that make up much of the top 100.

Suggested fix: Lower minZoom (for example 0.1) at least for fit, add <MiniMap> (for example shown while panning), and pan to keep the selected node visible when the inspector opens.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/editor-controls.svelte

Existing tickets: FEAT-ezeap5

---
### Tidy up wrecks imported layouts: sticky notes stacked in a column, AI sub-nodes laid out as main-flow steps, wide tiles overlapping [find:ui-canvas] (medium/bug) · area: auto-layout · confidence: high

tidyDocument feeds every node to dagre as a 72×96 box and every connection as a left-to-right edge. That includes sticky notes, AI sub-nodes and attachment wires. The toolbar Tidy button also doesn't re-fit the view, unlike the Controls one.

Evidence: web/src/lib/workflow-editor/layout.ts tidyDocument/layoutGraph. Template 2465 after Tidy (25-2465-tidy.png, t17 → scale 0.323): 10 sticky notes in one vertical column; Embeddings, Data Loader, Text Splitter and Product Catalogue in a column; 'OpenAI Chat Model1' overlapping 'Embeddings OpenAI'; placeholder labels overlapping. workflow-editor.svelte tidyUp() has no fitView, while editor-controls.svelte tidyUp() calls fitView.

n8n behavior: Tidy up keeps sticky notes with the nodes they cover, lays sub-nodes under their parent agent, and fits the view afterwards.

Impact: Tidy makes imported templates less readable, and without undo (F10) the damage is permanent unless the draft is discarded.

Suggested fix: Leave sticky notes out of the layout and move them with the nodes they enclose. Lay AI sub-nodes as a top-to-bottom cluster under their root. Use real node widths. Share the fit-after-tidy between both buttons.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/layout.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/editor-controls.svelte

Existing tickets: FEAT-c71wy8

---
### Moving a multi-selection collapses it to one node, so a following Delete removes only that node [find:ui-canvas] (low/bug) · area: multi-select · confidence: high

After a group drag, syncCanvas → replaceDraft rebuilds the nodes with selected = (id === selectedNodeID). onSelectionChange keeps only selectedNodes[0], so only one node stays selected.

Evidence: Repro (a12.js) on the fixture: Cmd-click Manual and Set fields (both selected) → drag → selection shows 'Manual' only → Delete removes only Manual. Deleting a multi-selection without dragging removes both (works). Shift-drag marquee selection works (a7.js). Code: workflow-editor.svelte:243/270 and onSelectionChange.

n8n behavior: The selection survives moves, and group operations apply to every selected node.

Impact: Surprising partial deletes and lost selections during layout work.

Suggested fix: Keep a Set of selected node ids instead of a single selectedNodeID when projecting Flow nodes.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### Connections have no in-canvas actions: a wire dropped on empty canvas does nothing, edges have no hover insert or delete, and there is no context menu [find:ui-canvas] (low/parity-gap) · area: edges / context menu · confidence: high

There is no onconnectend handler, no custom edge with buttons, and no right-click menu on the canvas, nodes or edges.

Evidence: a19.js: dragging from 'Set fields output main' to empty space → picker not opened; hovering an edge exposes 0 buttons. Deleting a wire needs select + Delete or the toolbar 'Delete' button (t5 confirms that path works). Code: workflow-editor.svelte:560 SvelteFlow props; edges use the default smoothstep/bezier types from document.ts.

n8n behavior: Releasing a dragged wire on empty canvas opens the node creator and connects the new node. Hovering a connection shows '+' (insert a node in between) and a trash icon. Right-click opens a context menu with open, execute, rename, deactivate, pin, copy, duplicate, tidy, select all and add sticky.

Impact: Slower graph editing, and a missing muscle-memory path for n8n users.

Suggested fix: onconnectend → openPickerFrom(source handle) at the drop point. A custom edge with EdgeLabelRenderer buttons that splice a node into the connection. A context menu exposing the node actions from the rename, copy and disable findings.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts

---
### Node picker structure: a modal that hides the canvas, a flat list mixing sub-nodes with steps, and no category drill-down or per-node actions [find:ui-canvas] (low/parity-gap) · area: node picker information architecture · confidence: medium

The picker is a centred aria-modal dialog over a blurred backdrop, listing every definition grouped only by category name. 'Add step' offers chat models, embeddings, memory and tools alongside ordinary steps. Search also matches category text, so 'form' returns the Transform category.

Evidence: node-picker.svelte markup (a13-picker.png). a13.js searches: 'form' → Aggregate, Date & Time, Remove Duplicates, Sort, Split Out, Summarize (category match); 'chat trigger', 'gmail', 'slack', 'google sheets' → no match. The AI section in t4.out mixes AI Agent and Basic LLM Chain with sub-nodes.

n8n behavior: A right-side 'What happens next?' panel keeps the canvas visible. The root view lists categories with descriptions (AI / Action in an app / Data transformation / Flow / Core / Human review / Add another trigger), drilling down to per-node Triggers/Actions derived from resource/operation options (design-refs n8n-v2 09, 12). Sub-nodes are added only through agent slots.

Impact: Harder discovery. Users can add sub-nodes into the main flow, where they can't run.

Suggested fix: A side-panel picker with a category landing view and drill-down. Derive action entries from resource/operation options. Leave sub-nodes out of the main-flow context. Rank name matches above category matches.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/node-picker.svelte

---
### Sticky notes render as generic 68 px icon tiles: content, width, height and colour are ignored [find:web-frontend-code] (high/parity-gap) · area: canvas · confidence: high

kilasflow.stickyNote declares content ('Markdown shown on the canvas'), width, height and color, and FEAT-t5q318 added those parameters 'so the canvas can render it'. The canvas still renders every node with the same CanvasNode tile and has no branch for stickies. The note text is visible only as a flattened single-line input in the inspector (see the newline finding). Stickies are also not placed behind nodes and are not resizable.

Evidence: web/src/lib/components/workflow-editor/canvas-node.svelte (no type-specific rendering); document.ts:84-92 sets no width, height or zIndex. Screenshots .../work/web-frontend-code/sticky.png and tidy2777.png show stickies as yellow file-icon tiles labelled 'Sticky Note3'. The 3135 import has 43 sticky tiles among 100 nodes.

n8n behavior: n8n draws sticky notes as resizable coloured rectangles behind the nodes, with rendered markdown and in-place editing.

Impact: 91 of 100 templates contain 640 sticky notes, which carry the setup instructions users need to finish an imported workflow. They are unreadable on the canvas and clutter it with extra tiles.

Suggested fix: Add a dedicated sticky node component: sized by width and height, zIndex below nodes, a safe markdown renderer (no raw HTML, sanitised links), double-click to edit in a textarea, and resize handles that write width and height back.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts

Existing tickets: FEAT-t5q318

---
### Imported placeholders are unreadable: six generic AI handles and their labels collide with the 'Unsupported' badge, wide tiles overlap, and placeholder triggers get an input port [find:ui-canvas] (medium/ui) · area: unsupported placeholder rendering · confidence: high

kilasflow.unsupported@1 declares main plus inModel/inMemory/inTool inputs and main plus outModel/outMemory/outTool outputs, whatever the original n8n node was. The canvas draws all of them, with labels under the tile that sit on top of the 'Unsupported' pill and the raw-type subtitle. Placeholder triggers accept inputs and model wires.

Evidence: 04-zoom-placeholders.png and 18-1954-run.png: text renders as 'inModelUnMemorydinTool'; 'Embeddings OpenAI1' and 'Default Data Loader' tiles overlap (n8n x 760 vs 900); the 'When chat message received' and 'WhatsApp Trigger' placeholders show a main input and AI slots; the imported switch labels its ports '0'/'1' instead of rule names; the subtitle shows '@n8n/n8n-nodes-langc…'. Definition: node-types.json kilasflow.unsupported@1/2/4/8.

n8n behavior: A missing or uninstalled node keeps its original shape and ports and is shown in a 'not installed' state.

Impact: Most AI templates contain placeholders (chatTrigger ×29, openAi, googleSheets, embeddings…), and they are what users have to find and replace first.

Suggested fix: Have the importer record the original port shape on the capsule (connection kinds and counts actually used, whether it is a trigger) and draw only those. Show attachment labels on hover only. Size tiles to the n8n grid or rescale imported positions.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte, /Users/izzadev/projects/k-flow/nodes/unsupported.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

Existing tickets: FEAT-t5q318

---
### White-label leak: a 'Svelte Flow' attribution link appears on every canvas [find:ui-canvas] (low/ui) · area: branding / embed · confidence: high

The Svelte Flow attribution isn't hidden, so the embeddable white-label editor shows a third-party brand link to end customers.

Evidence: A bottom-right 'Svelte Flow' link appears in a1-switch.png, 15-after-execute.png and 26-mobile.png. workflow-editor.svelte:560 <SvelteFlow …> has no proOptions={{ hideAttribution: true }}; execution-canvas.svelte is the same.

n8n behavior: Not applicable.

Impact: Visible to every embed host's customers.

Suggested fix: Set proOptions hideAttribution on both SvelteFlow instances (consider sponsoring xyflow) and credit the library under About/licences.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/execution-canvas.svelte

---
### At phone width the workflow name disappears, Activate and status move off-screen, and Execute appears twice [find:ui-canvas] (low/ui) · area: responsive editor header · confidence: high

At 390px the toolbar overflows horizontally (scrollWidth 484). The breadcrumb name shrinks to zero width. Activate and the save status can only be reached by scrolling, and both the toolbar Execute and the floating 'Execute workflow' button are shown.

Evidence: t18 at 390×844: header scrollWidth 484 > clientWidth 390 (26-mobile.png). Breadcrumb <p class="min-w-0 max-w-32 flex-1 truncate"> collapses; the toolbar Execute has no md:hidden counterpart, while the floating button is md:hidden.

n8n behavior: Not directly comparable (n8n isn't built for phones), but the product aims at a 16px-gutter mobile layout.

Impact: Mobile and embed-in-narrow-iframe users can't see which workflow they are editing or whether it is active.

Suggested fix: On narrow screens use a two-row header (name row, then actions) with an overflow menu for secondary actions, and hide the toolbar Execute while the floating one is visible.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

## Acceptance criteria

- [ ] No undo/redo: a mistaken delete, move or tidy can only be reverted by throwing away all unsaved work
- [ ] No copy, paste or duplicate for nodes, and pasting n8n workflow JSON onto the canvas does nothing
- [ ] Nodes can't be renamed, and new nodes get duplicate names
- [ ] Only Delete/Backspace are wired: no Mod+S, select-all, zoom/fit keys or open-picker key, and the picker ignore
- [ ] AI Agent slots have no '+': adding a model, memory or tool means the generic picker plus a manual drag to a 10
- [ ] Node picker lists each registered version as a separate entry and offers nodes that can never run (Unsupported
- [ ] New and imported nodes are placed without regard to the viewport or existing nodes
- [ ] No disable/enable, pin data or node notes, and imported disabled nodes become live
- [ ] Execute requires a save first and runs webhook-style triggers with empty input, so they 'succeed' with no data
- [ ] Workflows can't be renamed, and workflow settings (such as timezone) have no UI
- [ ] Large workflows can't be seen whole: fit is clamped at minZoom 0.3, there is no minimap, and the inspector hid
- [ ] Tidy up wrecks imported layouts: sticky notes stacked in a column, AI sub-nodes laid out as main-flow steps, w
- [ ] Moving a multi-selection collapses it to one node, so a following Delete removes only that node
- [ ] Connections have no in-canvas actions: a wire dropped on empty canvas does nothing, edges have no hover insert
- [ ] Node picker structure: a modal that hides the canvas, a flat list mixing sub-nodes with steps, and no category
- [ ] Sticky notes render as generic 68 px icon tiles: content, width, height and colour are ignored
- [ ] Imported placeholders are unreadable: six generic AI handles and their labels collide with the 'Unsupported' b
- [ ] White-label leak: a 'Svelte Flow' attribution link appears on every canvas
- [ ] At phone width the workflow name disappears, Activate and status move off-screen, and Execute appears twice
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---

## Progress (FrontendCore3, 2026-09-20)

Status: `testing`. Landed in one commit; scoped proof below.

### Done
- **Undo/redo** (`workflow-editor.svelte` + `workflow-editor/history.ts`): bounded snapshot stack (64 steps) over the immutable draft, coalescing per field within 700 ms so a typed word is one step; Mod+Z / Mod+Shift+Z / Mod+Y, toolbar buttons, `git`-free redo cleared on the next edit. Delete, Tidy, drag (one step per drag via `onnodedragstop`), connect, rename, paste and property edits all record.
- **Copy / paste / duplicate** (`workflow-editor/clipboard.ts`): Mod+C copies the selection as workflow JSON (`kind: kilasflow.node-fragment`), Mod+V accepts both that fragment and an n8n payload (`{nodes, connections}` keyed by node name, positions as `[x, y]`), Mod+D duplicates. Paste regenerates ids, de-duplicates names, offsets onto the viewport centre, and reports placeholders/dropped wires in a status strip. Node types resolve against the catalogue by exact type then by last segment; an unknown type becomes the same `kilasflow.unsupported` capsule an import produces, at the arity its own edges need.
- **Rename**: F2, node toolbar, double-click on a tile, and an editable title in the inspector. Names are made unique on add/paste/duplicate (`Set` → `Set1` → `Set2`) and `renameNode` rewrites `$('Old')`, `$items("Old")` and `$node['Old']` references in every node's parameters/settings, skipping the imported `original` capsule.
- **Shortcuts** (`workflow-editor/shortcuts.ts` + `?` help overlay rendered from the same table): Mod+S save, Mod+A select all, Mod+C/V/D, F2 rename, Enter opens the inspector, Tab/N node picker, 1 fit, 0 reset zoom, +/− zoom, Shift+Mod+T tidy, ? help. Suppressed in text fields, while an overlay is open, and when focus is outside this editor.
- **Node picker** (`workflow-editor/catalog.ts`): one entry per type at its latest version, import-only types and deployment-unavailable nodes hidden, ranking name > type/alias > description > category, arrow/Home/End + Enter with `aria-activedescendant`, and a `providesKind` filter so an agent slot offers only what fits it.
- **AI slots** (`canvas-node.svelte`): a `+` per empty attachment input that opens the picker filtered to that port's kind and drops the tile under the slot already wired; required-and-empty slots are marked; labels truncate and stagger two rows deep instead of colliding. Ports now come from FrontendCore2's `resolvedPorts`, so Switch/Merge branch counts follow the node's parameters.
- **Sticky notes**: rendered as sized, coloured rectangles behind the graph with a safe markdown subset (`workflow-editor/sticky.ts` — runs of text, never markup), zIndex below steps, drag-resizable writing `width`/`height` back.
- **Minimap / zoom**: minimap above 12 nodes on wide screens, `minZoom` 0.1, Tidy now uses measured tile sizes and fits the view.
- **Tidy** (`layout.ts`): sticker notes excluded and translated with the nodes they covered, attachment tiles placed beneath their consumer instead of as upstream rank columns, main-flow edges only through dagre, real measured sizes, and a final separation pass so no two tiles overlap.
- **Edges** (`canvas-edge.svelte`): hover `+` splices a new step into a connection (source → new → old target), hover trash deletes it; releasing a dragged wire on empty canvas opens the picker at the drop point.
- **Multi-selection**: every selected id is kept (a group drag no longer collapses to one node), and select-all is bound.

### Scoped proof
- `cd web && npx vitest run src/lib/workflow-editor` → 29 files, 361 tests, all passing.
- `npx svelte-check --tsconfig ./tsconfig.json` → 0 errors in the files this ticket touches (3 remaining errors are in `credentials.test.ts` (pre-existing), `embed/embed-editor.svelte:277` and `routes/(dashboard)/app/workflows/[id]/+page.svelte:420`, all owned elsewhere).
- New tests added: `history.test.ts`, `shortcuts.test.ts`, `catalog.test.ts`, `clipboard.test.ts`, `sticky.test.ts`, plus layout/document cases (attachment columns, overlap-freedom, sticky travel, wide tiles, unique names, reference rewrite, fragment paste).

### Not done (recorded, not silently dropped)
- Disable/pin/notes (BUG-xmcm8x's deferred model work), execute-without-save/test input, workflow rename + settings dialog, and the side-panel node-creator restructure need files outside this slice (`internal/**`, `routes/**`) — left for their owners.
- Svelte Flow's attribution link is still shown. Hiding it needs a Pro subscription per xyflow's terms; recorded as a licensing decision for Main rather than changed silently.
- Visual verification in a browser was not possible from this slice (no backend/stub of my own); proof is the unit suites plus the type check, with Main's end-to-end gate covering the rendered surface.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (4):
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
  - `af18807e` — FEAT-jvembs: wire-drop picker only on empty canvas; authoring pipeline cross-module test — frontend
  - `8f207508` — FEAT-jvembs: canvas authoring parity — undo/redo, clipboard, rename, shortcuts, picker, sticky, minimap, tidy, edge actions
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 +++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  524 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  630 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 ++++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  639 +++++
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  617 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  752 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 +++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 76458 insertions(+), 4725 deletions(-)
```
