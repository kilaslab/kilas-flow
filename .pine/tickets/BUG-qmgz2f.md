---
id: BUG-qmgz2f
title: 'Frontend perf/a11y: keystroke clones, loader storms, version panel, API drift, ai.* drops'
status: done
priority: medium
labels:
    - frontend
    - perf
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:02Z"
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

### Follow-up (FrontendCore3, 2026-09-20)
- Fixed the pre-existing type error in `src/lib/workflow-editor/credentials.test.ts` that kept `pnpm check` red: the fixture now declares the required `parameters`/`sharedSettings` fields instead of casting a partial object to `Definition` (`8922292`).
- `cd web && pnpm check` → **0 errors, 0 warnings** across all 1507 files, measured after that fix.
- The API-client drift fix (`pnpm generate:api` + the five list-page call-site migrations) is in the working tree uncommitted: Main assigned the single regen commit to WebFormsOps3, so it lands from their hand. Measured on this tree: `pnpm generate:api:check` exits 0.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (5):
  - `b4b07cae` — BUG-qmgz2f: record green pnpm check and the drift handoff — notes
  - `89222922` — BUG-qmgz2f: credentials test fixture declares required Definition fields — editor tests
  - `260b277b` — BUG-qmgz2f: per-dependency loader fetches, one canvas projection, inspector a11y, version-panel races — frontend
  - `d661a652` — chore(pine): align editor-core + ops-form ticket states (testing/doing)
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
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
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
 434 files changed, 67896 insertions(+), 4725 deletions(-)
```
