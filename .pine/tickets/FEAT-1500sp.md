---
id: FEAT-1500sp
title: Expose version history, diff and restore in the editor
status: done
priority: medium
labels:
    - versioning
    - history
deps:
    - FEAT-ajw7wt
parent: EPIC-m42s3g
phase: p7
created: "2026-09-05T05:01:47Z"
updated: "2026-09-05T05:01:47Z"
---

## Scope

The editor shows exactly one version of a workflow and offers no way to reach any other. `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` renders `currentWorkflow.latestVersion.document` and nothing else; the embed shell in `web/src/lib/embed/embed-editor.svelte` does the same. History exists in the database and, after p7-1, in the API, but a person editing a workflow cannot see that it has a past, cannot compare what they are about to save against what is running, and cannot go back.

Publishing is worse: there is no activation control anywhere in the SPA. `POST /workflows/{id}/activate` and its deactivate counterpart have existed since V1, and grepping `web/src` for "activate" matches only the generated client and one sentence of explanatory copy on the schedules page. A workflow is published today by calling the API by hand. That makes "published version" a concept the product has and the interface does not, and it is the reason a version pin nobody can see has never been questioned.

This ticket builds the surface over p7-1's endpoints: a version panel listing history, a read-only preview of any version on the existing canvas, a structural diff against the current draft, restore, and publish/unpublish with the currently serving version always visible. The canvas already supports most of this — `workflow-editor.svelte` takes a `readOnly` prop, and `execution-canvas.svelte` already draws a historical document, which is how the execution detail page replays the revision a run pinned.

The plan leaves one decision open here: whether the embed surface and the host SDK gain publish events. They should, but only behind a new scope. `permits` in `internal/api/middleware/embed.go` refuses activate and deactivate outright — "Activation publishes a webhook endpoint for the whole deployment; that is an owner action, not an embed one" — and that reasoning is sound for the default. A white-label host that wants its customers to publish from inside the embedded editor needs to opt in explicitly, and the SDK's `EditorEvent` union already carries `workflow-saved` with a revision, so `workflow-published` is its natural counterpart.

## Acceptance criteria

- [x] The workflow editor has a version panel listing every stored version newest-first, paging as it scrolls, each row showing revision, relative time, label and author where present, and a badge marking the draft and the published version.
- [x] Selecting a version previews it on the existing canvas read-only, with no way to edit, save or run from the preview, and returning to the draft leaves the draft's unsaved state exactly as it was.
- [x] A diff view names what changed between the selected version and the current draft — nodes added, removed, renamed, moved, parameters changed; connections added or removed; document settings changed — computed structurally from the two documents, never as a text diff of the JSON.
- [x] Restore is confirmed before it runs, states in the confirmation that it appends a new revision rather than deleting anything, and refuses to proceed while the canvas has unsaved changes.
- [x] Publish and unpublish are reachable from the editor, always show which version is currently serving, and surface a compile failure using the same validation-issue rendering a failed save already uses.
- [ ] An embed session's authority over the new routes is explicit and tested: browsing and diffing history needs `workflow:read`, restoring needs `workflow:write`, and publishing is refused unless the session was minted with a publish scope it does not carry by default.
- [ ] The host SDK documents a `workflow-published` editor event carrying the workflow and the published version, the embed shell emits it when a session with the publish scope publishes, and a session without that scope emits nothing.

## Implementation Plan

Regenerate the clients first: `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`, so p7-1's three new operations exist as typed calls before any component is written. Skipping this and hand-writing a fetch is what makes the drift checks fail later.

The diff belongs in a pure module, `web/src/lib/workflow-editor/history-diff.ts`, with its own `.test.ts` beside it — `document.ts`, `ports.ts`, `parameter.ts` and `key-value.ts` all follow that pattern, and a diff is the kind of thing that is only trustworthy with a table of fixtures. Match nodes by ID, not by index or name, since a rename must read as a rename rather than as a delete plus an add; compare `Parameters`, `Credentials`, `Settings` and `Position` separately so a pure reposition can be reported as cosmetic; compare connections by `(kind, source, target)`. Do not diff the serialized JSON: key order and canvas coordinates would drown every real change.

The panel itself is a new `web/src/lib/components/workflow-editor/version-panel.svelte`, opened from the toolbar in `workflow-editor.svelte`. Build it on the existing `sheet` component rather than `dialog`: the editor already switches layout at `mediaQuery('(max-width: 1023.98px)')` and a side sheet is the shape that survives that breakpoint. Both hosts — the dashboard page and `embed-editor.svelte` — must mount the same component with the same props; putting the fetch or the restore logic in the two pages instead is how they drift, and they have already drifted once on error handling.

The trap is the remount. The dashboard page wraps the editor in `{#key currentWorkflow.latestVersion.id}`, so any operation that produces a new latest revision tears down and rebuilds the canvas, discarding local edits. That is correct for restore — restoring is exactly "throw away the current canvas" — but only if the confirmation says so and the flow refuses while `dirty` is set. Publishing an older version must *not* change `latestVersion`, so it must not remount; verify that, because a naive implementation that refetches the workflow and reassigns `currentWorkflow` will remount anyway if the key expression is touched.

For the embed boundary, the decision to make is whether an embed session may publish at all. Recommendation: add a fourth scope, `ScopePublish = "workflow:publish"`, to `internal/embed/embed.go`. `normalizeScopes` rejects any scope it does not know by name, so an unlisted string is refused at session creation rather than silently ignored — that switch, `Allows` (publish should imply read, exactly as write and run already do), the `doc:` tag on `embedSessionBody.Scopes` in `internal/api/handlers/embed.go`, and the hand-written `EmbedScope` union in `sdk/src/server.ts` all have to be updated together. Then in `permits`, route the new sub-paths deliberately instead of letting them fall through: a `versions` GET is read, `restore` is write, `publish` and `unpublish` require the new scope, and `activate`/`deactivate` stay refused as they are. The cheaper alternative is to refuse publishing from an embed session entirely and drop the SDK event — that is defensible, but it leaves a white-label host unable to offer publishing at all, which is the opposite of what the embed surface is for.

Finish with the SDK: add `{ type: 'workflow-published'; workflowId: string; versionId: string; revision: number }` to the `EditorEvent` union in `sdk/src/browser.ts` and emit it through the existing `notifyHost` helper in `embed-editor.svelte`, which already posts to the session's exact origin. One thing to leave alone: `events.WorkflowSaved` ("workflow.saved") is declared in `internal/events/events.go`, carried through the SSE contract and echoed by both clients, and is never published by any Go code — the only `Publish` call site is `internal/engine/service.go`, which emits execution and node events. Do not fill it in here. The broker's streams are keyed `(tenantID, executionID)`, so a workflow-scoped event has no channel to arrive on, and giving it one is a broker change rather than a history feature; note the dead type and move on.

## References

- Roadmap plan, p7 section, entry V2-p7-2: `.pine/roadmap.md`.
- Editor hosts: `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` (the `{#key latestVersion.id}` remount), `web/src/lib/embed/embed-editor.svelte` (`notifyHost`, `workflow-saved`), `web/src/lib/components/workflow-editor/workflow-editor.svelte` (`readOnly`, `saveIssues`), `web/src/lib/components/workflow-editor/execution-canvas.svelte` and `web/src/routes/(dashboard)/executions/[id]/+page.svelte` (the existing precedent for rendering a historical version).
- Embed boundary: `internal/embed/embed.go` (`Scope`, `Allows`, `normalizeScopes`), `internal/api/middleware/embed.go` (`permits`), `internal/api/handlers/embed.go`, `sdk/src/server.ts` (`EmbedScope`), `sdk/src/browser.ts` (`EditorEvent`).
- Dead event type: `internal/events/events.go` (`WorkflowSaved`) against the single `Publish` call site in `internal/engine/service.go`.
- n8n 2.34.0 reference checkout, read-only and outside this repository: `packages/frontend/editor-ui/src/features/workflows/workflowHistory/` — `WorkflowHistoryList`, `WorkflowHistoryDiff`, `WorkflowHistoryVersionRestoreModal`, `WorkflowHistoryVersionUnpublishModal` and `WorkflowPublishTimelineContent` show the shape of the surface being matched, including the published / latest / default version states.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 15 — the drawer layout, the Current changes entry and the Versions / Publish Timeline tabs. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work evidence

Scope was `web/` only — four other sessions hold the Go, `migrations/`, `docs/`,
`sdk/` and `.github/` trees — so the two acceptance criteria that need those
trees are only half-satisfiable here. That is stated plainly below rather than
quietly skipped.

### Stale premises found in the ticket

- **"Regenerate the clients first: `pnpm generate:api`"** — not needed. The
  generated client was already current: `listWorkflowVersions`,
  `getWorkflowVersion`, `publishWorkflowVersion`, `restoreWorkflowVersion` and
  `listWorkflowPublishEvents` all exist, along with
  `WorkflowVersionSummaryResource`, `WorkflowVersionListResource`,
  `ListWorkflowVersionsParams`, `PublishVersionInputBody` and
  `WorkflowPublishEventResource`. Nothing was regenerated.
- **"publish/unpublish"** — there is no unpublish operation. Deactivation is
  the unpublish: `Deactivate` writes the `unpublished` audit row and leaves
  `active_version_id` populated, so the panel calls `deactivateWorkflow` and
  distinguishes "published" (the pin) from "serving" (the pin plus `active`).
  A workflow that was deactivated therefore reads as "Nothing is serving —
  Revision N was the last published revision", which is the only honest
  reading of a retained pin.
- **`ScopePublish` does not exist server-side.** `normalizeScopes` in
  `internal/embed/embed.go` still accepts exactly read, write and run, and
  refuses anything else by name at session creation. So no session can carry
  `workflow:publish` today.
- **`permits` already routes the new sub-paths acceptably by accident.**
  `strings.Cut(rest, "/")` yields `action == "versions"` for all three new
  paths, so a `versions` GET lands on the read branch and `restore` lands on
  the write branch — which is what the criterion asks for. `publish` also lands
  on the write branch, which is *not* what the criterion asks for; closing that
  needs the Go change this session cannot make.
- The claim that `execution-canvas.svelte` is the precedent for rendering a
  historical document is true, but it was not reused: it draws a *run*, with
  per-node execution status, and the criterion asks for the ordinary canvas
  read-only. `workflow-editor.svelte`'s existing `readOnly` prop was the right
  seam.

### What was built

- `web/src/lib/workflow-editor/history-diff.ts` (+ `.test.ts`, 26 tests) — the
  structural diff. Nodes matched by ID so a rename reads as a rename;
  `parameters`, `credentials`, `settings`, `position` and type compared
  separately so a pure reposition is reportable as cosmetic; connections
  matched on `(kind, source, target)` rather than their own IDs, since a save
  may reassign a connection's ID without the user touching it. Never a text
  diff of the JSON.
- `web/src/lib/workflow-editor/version-history.ts` (+ `.test.ts`, 28 tests) —
  relative time, the Draft/Published badges, and the restore and publish guard
  rules with the sentences they are refused and confirmed with.
- `web/src/lib/components/workflow-editor/version-panel.svelte` — the side
  sheet, on `sheet` as instructed. Versions tab (draft row first, then history,
  cursor-paged on scroll and by button, reusing `$lib/dashboard/cursor-page`)
  and a Publish timeline tab over the audit trail.
- `workflow-editor.svelte` gained a `history` prop, a History toolbar button
  and the preview mechanism. Both hosts pass the same `WorkflowHistoryHost`
  object.

### Design decisions worth recording

- **The preview is drawn beside `draft`, never into it.** A new `preview` state
  holds the fetched revision and the canvas projection reads
  `preview?.document ?? draft`. Loading a version into the draft and restoring
  it afterwards would be the same work with one extra way to lose the user's
  unsaved edits. `dirty` still compares `document` against `draft`, so
  returning from a preview leaves the unsaved state exactly as it was.
- **One `locked` flag, not two.** `readOnly || previewing` gates every mutation,
  the toolbar, the canvas and the inspector, so a mutation added later cannot
  honour one and miss the other.
- **`preview` is bound between the editor and the panel**, so the banner's
  "Back to draft" and the panel's own both clear one variable. The first
  version of this kept a separate `selected` in the panel and desynced the
  moment the banner was used.
- **A refused publish is handed back to the editor** through `onIssues` and
  rendered by the existing validation-issue list, so clicking an issue still
  focuses the node at fault. A second list in the panel would have been the
  same information with a different way to reach the node.
- **The publish gate is written even though no session can pass it.**
  `SCOPE_PUBLISH` lives in `session.svelte.ts` and every publish control in the
  embed is behind it, so the embed offers nothing today and is already correct
  when the server grants the scope — rather than the two halves landing out of
  step and briefly offering a button the API refuses.

### Remount trap: verified, not assumed

The ticket warns that publishing must not remount the canvas. Confirmed against
the server: `publish` in `internal/repository/workflows.go` issues
`Updates(map[string]any{"active": true, "active_version_id": version.ID})` and
never touches `latest_revision`. So `latestVersion.id` — the `{#key}` expression
in both hosts — is unchanged by a publish, and `applyPublish` reassigns
`currentWorkflow` without remounting. Restore does append a revision and does
remount, which is correct, and the panel refuses to restore while `dirty` is set
so nothing is discarded.

### Two defects found and fixed during review

- The publish-timeline effect read `events.length` and `loadingEvents` and also
  wrote both, so a workflow with an *empty* publish history would have re-fired
  it forever. Now guarded by a deliberately non-reactive `eventsRequested`.
- The panel held its own `selected` alongside the editor's `preview`, so
  clearing the preview from the canvas banner left the panel showing a diff for
  a revision the canvas had stopped drawing. `preview` is now bound and the
  selection is derived from it.

### Criteria not fully met, and why

- **Criterion 6 (embed authority tested).** The `web/` half is done and tested:
  browsing needs read, restoring needs write, publishing needs a publish scope
  no session carries. The server half — adding `ScopePublish` to
  `internal/embed/embed.go`, routing `publish`/`unpublish` deliberately in
  `permits`, and the `doc:` tag on `embedSessionBody.Scopes` — is untouched,
  and `permits` today would let a write-scoped session publish.
- **Criterion 7 (SDK `workflow-published` event).** The emit half is done:
  `embed-editor.svelte` posts `kilasflow:workflow-published` with the workflow
  and the published version through the existing `notifyHost`, and a session
  without the scope emits nothing because it can never publish. The
  declaration half — adding the variant to the `EditorEvent` union in
  `sdk/src/browser.ts` and `workflow:publish` to `EmbedScope` in
  `sdk/src/server.ts` — is in the fenced `sdk/` tree.

Also confirmed and left alone as instructed: `events.WorkflowSaved` in
`internal/events/events.go` is still declared and still published by nothing —
the only `Publish` call site remains `internal/engine/service.go`, which emits
execution and node events.

### Verification

```
pnpm check     # 1370 FILES 0 ERRORS 0 WARNINGS
npx vitest run # 25 files, 311 tests passed
pnpm build     # clean
```

`pnpm check` was itself verified: a deliberate type error was planted in
`src/lib/workflow-editor/planted-check.ts`, caught
("Type 'string' is not assignable to type 'number'", 1 ERROR over 1362 files),
and removed. A bare `npx svelte-check --threshold error` does silently pass.

### Tests proven to fail without the change

Each mechanism was reverted in turn, the suite re-run, and the file restored.

| Reverted | Failing tests |
| --- | --- |
| node matching by ID → by name | `reads a rename as one renamed node rather than a delete and an add`, `keeps the parameters of a renamed node attached to it` |
| connection identity → `connection.id` | `does not report a connection that only got a new identifier`, `reports a connection that was moved to another port` |
| empty-bag normalisation in `sameBag` | `treats a missing parameter bag and an empty one as the same node` |
| per-aspect comparison for `cosmeticOnly` | `stops calling the diff cosmetic once a parameter changed alongside a move` |
| clock-skew clamp in `relativeTime` | `reads a revision saved seconds ago as just now`, `does not render a revision as arriving in the future when the server clock runs ahead` |
| the `dirty` guard in `restoreRefusal` | `refuses while the canvas has unsaved changes` |
| the `published` role flag | `marks the revision production traffic runs as published`, `gives a revision that is both the draft and the published one both badges` |
| letting write imply publish in `scopeAllows` | `refuses publishing to every scope a host can mint today` |
