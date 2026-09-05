---
id: FEAT-1500sp
title: Expose version history, diff and restore in the editor
status: todo
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

- [ ] The workflow editor has a version panel listing every stored version newest-first, paging as it scrolls, each row showing revision, relative time, label and author where present, and a badge marking the draft and the published version.
- [ ] Selecting a version previews it on the existing canvas read-only, with no way to edit, save or run from the preview, and returning to the draft leaves the draft's unsaved state exactly as it was.
- [ ] A diff view names what changed between the selected version and the current draft — nodes added, removed, renamed, moved, parameters changed; connections added or removed; document settings changed — computed structurally from the two documents, never as a text diff of the JSON.
- [ ] Restore is confirmed before it runs, states in the confirmation that it appends a new revision rather than deleting anything, and refuses to proceed while the canvas has unsaved changes.
- [ ] Publish and unpublish are reachable from the editor, always show which version is currently serving, and surface a compile failure using the same validation-issue rendering a failed save already uses.
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

- Roadmap plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p7 — Workflow history", entry V2-p7-2.
- Editor hosts: `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` (the `{#key latestVersion.id}` remount), `web/src/lib/embed/embed-editor.svelte` (`notifyHost`, `workflow-saved`), `web/src/lib/components/workflow-editor/workflow-editor.svelte` (`readOnly`, `saveIssues`), `web/src/lib/components/workflow-editor/execution-canvas.svelte` and `web/src/routes/(dashboard)/executions/[id]/+page.svelte` (the existing precedent for rendering a historical version).
- Embed boundary: `internal/embed/embed.go` (`Scope`, `Allows`, `normalizeScopes`), `internal/api/middleware/embed.go` (`permits`), `internal/api/handlers/embed.go`, `sdk/src/server.ts` (`EmbedScope`), `sdk/src/browser.ts` (`EditorEvent`).
- Dead event type: `internal/events/events.go` (`WorkflowSaved`) against the single `Publish` call site in `internal/engine/service.go`.
- n8n 2.34.0 reference checkout, read-only and outside this repository: `packages/frontend/editor-ui/src/features/workflows/workflowHistory/` — `WorkflowHistoryList`, `WorkflowHistoryDiff`, `WorkflowHistoryVersionRestoreModal`, `WorkflowHistoryVersionUnpublishModal` and `WorkflowPublishTimelineContent` show the shape of the surface being matched, including the published / latest / default version states.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 15 — the drawer layout, the Current changes entry and the Versions / Publish Timeline tabs. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
