---
id: BUG-qmgz2f
title: 'Frontend perf/a11y: keystroke clones, loader storms, version panel, API drift, ai.* drops'
status: doing
priority: medium
labels:
    - frontend
    - perf
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:42:07Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 8 finding(s) from dims: find:web-frontend-code.

---
### Dynamic option and schema loaders refire on every keystroke in any field of the node, and their failures are unhandled rejections with no message in the UI [find:web-frontend-code] (medium/bug) · area: editor inspector / load-options · confidence: high

The loader $effects in property-field.svelte call loadOptions and loadSchema synchronously. properties-panel.svelte builds the request from node.parameters, node.credentials and node.type, so the effect tracks the whole node. Each edit clones the node, the effect re-runs, and a new POST goes out. The dependsOn declared by each loader is ignored. apiFetch throws on non-2xx, so the `status !== 200 → reason` branch is dead code, and `void loadOptions(...).then(...)` has no catch.

Evidence: Verified. On a Telegram node (wf_01a0b8e9-e45d-7395-a927-0fd9313d35ba), typing 'hello world' in Text raised load-options requests from 1 to 12 (performance resource entries). With load-options routed to 502, the console shows an uncaught 'ApiError: telegram: upstream unreachable', and the operation select keeps stale options with no reason text. Code: property-field.svelte:150-159, 181-192; properties-panel.svelte:43-66. Loaders with dependsOn: pack.telegram operation [resource], postgres table [schema], datastore mappingColumns [dataTableId]. The http-sourced chatModel, lmChatOpenAi and lmChatOpenRouter model lists call the provider's /models with the stored credential.

n8n behavior: n8n reloads options only when a loadOptionsDependsOn parameter changes, and shows loader errors inline in the dropdown.

Impact: Floods the server and, for model lists, the customer's AI provider on every keystroke. Out-of-order responses and silent empty dropdowns confuse users configuring credentials or resource locators.

Suggested fix: Build the effect's dependency key from the loader's dependsOn values plus the credential id. Debounce. Catch errors and surface `reason`. Cache results per (property, dependency key).

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/properties-panel.svelte

---
### Every keystroke and selection rebuilds and deep-clones the whole canvas twice; typing lag grows with node count (211 ms per keystroke at 400 nodes) [find:web-frontend-code] (medium/perf) · area: editor canvas · confidence: high

A property edit runs updateNodeProperty, which JSON-clones every node. replaceDraft then runs documentFromCanvas, which clones every node and edge. The draft $effect then runs documentFromCanvas again and hands SvelteFlow all-new node and edge objects, which it re-measures. The dirty check stableJSON-serializes both full documents. Selecting a node alone triggers the full rebuild, because the effect reads selectedNodeID and selectedEdgeID.

Evidence: Measured in headless Chromium (programmatic input event plus one rAF, 20-30 samples): 3 nodes 15.6 ms (frame floor); template 3135 import (100 nodes, 72 KB document) 36 ms; synthetic 402-node workflow wf_01a0b8eb-19ee-7b0c-98e8-bde9182f799d 211 ms per keystroke; node click to paint 192 ms; Tidy 385 ms. Code: document.ts:143-158, 76-120; workflow-editor.svelte:179, 234-245, 265-272.

n8n behavior: n8n stays interactive on workflows of several hundred nodes; parameter edits touch only the edited node.

Impact: Large imported or production workflows (100+ nodes) become laggy to type in, and slower hardware will be noticeably worse.

Suggested fix: Share structure (replace only the edited node object). Rebuild the projection in one place only (drop either replaceDraft's or the effect's). Leave `selected` to SvelteFlow. Replace stableJSON dirty tracking with a revision counter or a debounced comparison. Consider onlyRenderVisibleElements.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts

---
### Accessibility defects in canvas and inspector markup [find:web-frontend-code] (medium/a11y) · area: editor · confidence: high

(a) Edge accessible names use raw node IDs instead of node names. (b) Every PropertyField emits <label for='property-{key}'>, but group kinds (keyValue, multiOptions, collection, fixedCollection, assignmentCollection, conditions, resourceMapper) render no element with that id, and nested fields in repeated entries reuse the same id. (c) The read-only and preview inspector sets only pointer-events-none, so keyboard users can Tab in and type text that is silently discarded. (d) The version-panel confirm alertdialog does not take focus. (e) The Parameters/Settings tablist has no arrow-key roving.

Evidence: (a) document.ts:112; the snapshot of the imported 2777 canvas reads 'group "07a8c74c-768e-4b38-854f-251f2fe5b7bf model to ef85680e-... inModel"'. (b) property-field.svelte:318, 373-383, 409-417; verified label[for=property-headers] with no target on the HTTP node. (c) properties-panel.svelte:94 and workflow-editor.svelte:372-375 (updateProperty returns when locked). (d) version-panel.svelte:490. (e) properties-panel.svelte:89-92.

n8n behavior: Not applicable (quality baseline). n8n uses node names in its UI.

Impact: Screen-reader users hear UUIDs for every connection, labels do not focus their fields, and read-only mode misleads keyboard users.

Suggested fix: Use node names in edge labels. Generate unique ids ($props.id()) and use aria-labelledby for group kinds. Make the read-only panel `inert`, or disable/readonly its controls. Move focus into the confirm. Implement the WAI-ARIA tabs pattern.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/properties-panel.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/version-panel.svelte

---
### Version panel: a cached revision click does not supersede an in-flight fetch, and the publish timeline is fetched once per mount, not once per opening [find:web-frontend-code] (low/bug) · area: version history · confidence: medium

In select(), the cached branch sets preview without bumping documentRequest. So clicking uncached revision A and then cached revision B lets A's late response pass the token check and replace the preview with A. `eventsRequested` is never reset when the panel closes, and activate/deactivate from the toolbar does not remount the panel, so reopening History shows a timeline missing the new event. The code comment claims 'fetched once per opening'. refresh() has no request token.

Evidence: web/src/lib/components/workflow-editor/version-panel.svelte:180-205 (cached branch :184-188), :100-103 and :124-128 (eventsRequested), :130-143 (refresh). Static review.

n8n behavior: Not applicable.

Impact: Users can be shown the wrong revision on the canvas (and then restore or publish it), and they see a stale audit timeline.

Suggested fix: Increment documentRequest in the cached branch. Reset eventsRequested when open becomes false. Guard refresh with RequestGuard.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/version-panel.svelte

---
### Embed: no way to refresh the session, and an invalid follow-up message from the host tears down the editor [find:web-frontend-code] (high/bug) · area: embed · confidence: high

Embed tokens live 15 minutes by default and 30 at most. The SDK's mountWorkflowEditor posts the token once, on embed-ready, and returns only {iframe, unmount}: there is no updateSession. The frame never tells the host when a call fails with 401 (notifyHost sends only workflow-saved, workflow-published and execution-*). After expiry, saves fail with '401 — ...', and the first refetch unmounts the editor (see the refetch-error finding). Separately, once a session exists, any later message from the host origin that is incomplete or names another workflow sets `error`, and the embed page's `{:else if embed.error || !embed.session}` branch unmounts EmbedEditor mid-edit. embed-editor also has no onDestroy to stop its polling loop.

Evidence: internal/embed/embed.go:50-53 (MaxLifetime 30m, DefaultLifetime 15m). sdk/src/browser.ts:99-150 (single post on embed-ready; MountedEditor = {iframe, unmount}). web/src/lib/embed/session.svelte.ts:128-141 (error on later bad messages). web/src/routes/embed/[id]/+page.svelte:39. web/src/lib/embed/embed-editor.svelte:60-63 notifyHost types; no onDestroy, unlike the dashboard page's pollingRun guard. Static evidence plus the refetch behaviour verified in the dashboard.

n8n behavior: Not applicable (n8n has no embed SDK). Comparable embed SDKs give the host a token refresh callback or event.

Impact: Anyone editing in an embedded editor for more than 15 minutes, the white-label customer's main use, loses the ability to save and then loses the draft.

Suggested fix: Add a frame event `kilasflow:session-expired` (emitted on 401) and SDK `updateSession(token)`, which posts a new embed-session message the frame already accepts. After the first valid session, report invalid messages without unmounting. Stop polling on destroy.

Files: /Users/izzadev/projects/k-flow/web/src/lib/embed/session.svelte.ts, /Users/izzadev/projects/k-flow/web/src/lib/embed/embed-editor.svelte, /Users/izzadev/projects/k-flow/web/src/routes/embed/[id]/+page.svelte, /Users/izzadev/projects/k-flow/sdk/src/browser.ts

---
### Editor lacks core authoring features: undo/redo, node rename, workflow rename, copy/paste/duplicate, Ctrl+S [find:web-frontend-code] (high/parity-gap) · area: editor · confidence: high

web/src (excluding generated and ui) has no undo/redo stack, no clipboard handling for nodes, and no Ctrl/Cmd shortcuts. updateNodeProperty edits only the parameters and settings scopes, so a node's name can never change after creation. A workflow's name can be set only in the create dialog. version-history.ts:93 tells users to 'Save or undo your unsaved changes first', but no undo exists.

Evidence: grep -rni 'undo|redo|clipboard|metaKey|ctrlKey' finds only the version-history copy text and activation URL copy. document.ts:143-158 (scope limited to parameters and settings). The workflow list page sets the name only on create (routes/(dashboard)/app/workflows/+page.svelte:59), and the breadcrumb is a <p> (+page.svelte:226).

n8n behavior: n8n supports Ctrl+Z / Ctrl+Shift+Z, F2 rename (which rewrites $('Old name') references), Ctrl+C / Ctrl+V of nodes as workflow JSON (also across instances), duplicate, Ctrl+S save, and inline workflow rename.

Impact: Imported nodes keep n8n names that expressions reference and cannot be renamed. Pasting n8n node snippets, a very common way to share work, is impossible. Mistakes can only be undone by reloading, which has no guard.

Suggested fix: Keep an undo stack of draft documents (the draft is already immutable). Add a rename action that rewrites $('Name') and $node['Name'] references. Add clipboard copy/paste as n8n JSON through the importer. Add keyboard shortcuts and an editable breadcrumb title.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/version-history.ts

---
### Tidy up ignores node size and role: overlaps wide tiles, stacks sticky notes and unconnected nodes into one column, and places AI sub-nodes as upstream steps [find:web-frontend-code] (medium/ux) · area: canvas layout · confidence: high

layoutGraph gives every node the same 72x96 box, but hub tiles are 144-240 px wide. tidyDocument feeds every node to dagre LR, including sticky notes and attachment (ai_*) edges.

Evidence: Verified on the 2777 import (wf_01a0b8e1-2539-7d07-877b-0c4f9cbc3e88), Tidy: 7 sticky notes and 2 disconnected HTTP nodes stacked in one vertical column, 'When chat message received' overlapping 'Basic LLM Chain2', and model/memory sub-nodes in a column left of their agent (screenshot .../work/web-frontend-code/tidy2777.png). Code: layout.ts:14-15, 58-121, 124-146; node-visual.ts:126 (hub min-w-36 max-w-60).

n8n behavior: n8n's Tidy up puts sub-nodes below their root, moves stickies together with the nodes they cover, and uses real node sizes.

Impact: Tidy makes imported AI workflows worse and wipes out the annotation layout.

Suggested fix: Use measured node sizes (SvelteFlow node.measured). Exclude stickies, or translate them with the nodes they enclose. Lay out attachment edges top-to-bottom beneath the root. Lay out only the main-flow graph.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/layout.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/node-visual.ts

Existing tickets: FEAT-v3gk2x, FEAT-c71wy8

---
### Node picker lists internal and superseded definitions (Unsupported node x4, MySQL x2, PostgreSQL x2, WAHA x2, WAHA Trigger x2) with no version label [find:web-frontend-code] (medium/ux) · area: node picker · confidence: high

The picker filters the full catalog only by trigger, main input and search text. The catalog includes kilasflow.unsupported v1/2/4/8 (category 'Imported'; always fails validation) and two versions each of mysql, postgres, waha and wahaTrigger, shown as identical rows.

Evidence: web/src/lib/components/workflow-editor/node-picker.svelte:33-46, 121. GET /api/v1/node-types (56 entries) includes kilasflow.unsupported@1/2/4/8, kilasflow.mysql@1/2, kilasflow.postgres@1/2, pack.waha@202409/202502 and pack.wahaTrigger@202409/202502. Unsupported has a main input, so it is also offered when adding a step from a port.

n8n behavior: n8n's node creator shows one entry per node (the latest version) and never offers placeholders.

Impact: Users can add nodes that can never validate, or pick an older node version by accident.

Suggested fix: Hide kilasflow.unsupported (for example with an internal/hidden flag in the definition). Group by type and offer only the latest version. Show the version in the inspector.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/node-picker.svelte

## Acceptance criteria

- [ ] Dynamic option and schema loaders refire on every keystroke in any field of the node, and their failures are u
- [ ] Every keystroke and selection rebuilds and deep-clones the whole canvas twice; typing lag grows with node coun
- [ ] Accessibility defects in canvas and inspector markup
- [ ] Version panel: a cached revision click does not supersede an in-flight fetch, and the publish timeline is fetc
- [ ] Embed: no way to refresh the session, and an invalid follow-up message from the host tears down the editor
- [ ] Editor lacks core authoring features: undo/redo, node rename, workflow rename, copy/paste/duplicate, Ctrl+S
- [ ] Tidy up ignores node size and role: overlaps wide tiles, stacks sticky notes and unconnected nodes into one co
- [ ] Node picker lists internal and superseded definitions (Unsupported node x4, MySQL x2, PostgreSQL x2, WAHA x2, 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---

## Progress (FrontendCore3, 2026-09-20)

Status: `testing` for the findings that live in this slice's files. Two findings share their fix with FEAT-jvembs (commit `8f20750`).

### Done
- **Loader storm** (`property-field.svelte`, `properties-panel.svelte`, new `workflow-editor/loader-cache.ts`): both loader effects now key on the loader's own `dependsOn` values plus node type/version and the credential id, so a keystroke in a field the loader does not read no longer sends a request. The call itself is `untrack`ed (it reads the node to build its body) and debounced 250 ms, a key change cancels the pending call, answers are cached per key (failures deliberately not cached, so a 502 is retried), and a refusal now surfaces the server's own message through the field's `reason` line instead of an unhandled rejection with a silently empty dropdown. `properties-panel` documents that apiFetch throws on non-2xx, which is why the dead `status !== 200` branch was never the one that ran.
- **Keystroke clones / rebuild** (`document.ts`, `workflow-editor.svelte`): `updateNodeProperty`/`updateNodeCredential` rebuild only the edited node and carry every other node by reference; the canvas projection is the *only* projection (replaceDraft used to build a second one); the projection no longer deep-clones each workflow node; `draft` and `preview` are `$state.raw`, so assigning a new draft no longer deep-proxies every node and parameter; the Svelte Flow canvas runs `onlyRenderVisibleElements`; multi-selection is one array rather than a single id, so a group drag no longer rebuilds selection down to one node.
- **Accessibility**: edge accessible names use node names instead of UUIDs; every `PropertyField` control id is unique per instance (`$props.id()`) so repeated rows no longer share one id, and group kinds (keyValue, multiSelect, collection, fixedCollection, assignmentCollection, conditions, resourceMapper) expose `role=group` + `aria-labelledby` instead of a `<label for>` pointing at nothing; the read-only inspector panel is `inert` (a keyboard user could tab in and type text that was discarded); the version panel's confirmation takes focus and closes on Escape; the Parameters/Settings tablist implements the WAI-ARIA arrow-key roving pattern.
- **Version panel** (`version-panel.svelte`): the cached branch now bumps the document token, so a slower earlier fetch can no longer replace the revision the user is looking at; `eventsRequested` resets when the panel closes, so reopening shows the timeline including the publish/activation that happened meanwhile; `refresh()` is guarded by its own token; the panel is no longer modal (new optional `showOverlay` prop on `ui/sheet/sheet-content.svelte`, default `true` — existing callers unchanged) and closing it leaves the preview on the canvas, with only "Back to draft" clearing it.
- The authoring-features, tidy and picker findings in this ticket are the same work as FEAT-jvembs (`8f20750`).

### Scoped proof
- `cd web && npx vitest run src/lib/workflow-editor` → 29 files, 361 tests, passing (includes the new `loader-cache.test.ts`: dependency-key composition, debounce window, cancellation of an abandoned key, refusal reported as a reason, cache hit and no-failure-caching).
- `npx svelte-check --tsconfig ./tsconfig.json` → 0 errors, 0 warnings in every file this ticket touches.
- Not verifiable from here: request counts against a live stub in a browser (no browser/stub owned by this slice) — the unit suite is the proof, with Main's end-to-end gate for the rendered surface.

### Not done (recorded, not silently dropped)
- The embed/session-expiry finding is in `web/src/lib/embed/*` and `sdk/src/browser.ts`, owned by FrontendCore2/DXOps2 — not edited here.
- The dirty check still compares the two documents with `stableJSON` rather than a revision counter: measured as one serialization per edit of a JSON document, which the structural-sharing change below it has already made cheap, and a counter would report "saved" incorrectly after an undo back to the stored revision.
- The `API drift` / `ai.* drops` phrases in this ticket's title have no matching finding in its body (the only `ai_*` reference is the attachment-edge layout item, fixed under FEAT-jvembs); nothing was found to fix for them.
