---
id: BUG-t2wezf
title: Execution detail refetches ~900x/s; node data cannot be opened
status: doing
priority: critical
labels:
    - editor
    - executions
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T13:44:26Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 3 finding(s) from dims: find:ui-ndv, find:web-frontend-code.

---
### The execution detail page refetches the execution about 900 times a second, so node data cannot be opened [find:ui-ndv] (critical/bug) · area: Execution inspector / output data · confidence: high

On a finished execution the page loops forever on GET /api/v1/executions/{id}, alternating 200 and aborted requests. The constant re-rendering keeps Svelte Flow re-measuring the nodes (the wrapper is visibility:hidden with width 0), so clicking a node never opens the 'Node data' inspector. This is the only place input and output data are shown.

Evidence: Opened /executions/exec_01a0b8d3-9842-7dee-b80c-0d69ad9df283 (status succeeded). A PerformanceObserver counted 1773 requests to /api/v1/executions/ in 2 s. The playwright request log showed more than 204k alternating '[200] OK' and '[FAILED] net::ERR_ABORTED'. Clicking the 'HTTP fail' node by ref, locator and raw mouse left the panel at 'Select a node on the canvas…' (work/ui-ndv/37-exec-page.png). The previous attempt's 14-execution-node-data.png is byte-identical to 13. Cause: web/src/routes/(dashboard)/executions/[id]/+page.svelte:80-84 `$effect(() => { if (live.finished) void execution.refetch(); })` re-runs on every query-state change. Introduced in 72aa907 (SSE events).

n8n behavior: n8n's execution view loads once, and clicking a node opens its NDV with input and output data.

Impact: Every open execution page hammers the server (a DoS risk with a few tabs open), and per-node input/output inspection is broken for all executions.

Suggested fix: Refetch once when `live.finished` changes from false to true: guard with a local flag or untrack(execution) inside the effect. Skip the SSE subscription or refetch entirely when the fetched execution is already terminal. Add an e2e test that counts requests on a finished execution and clicks a node.

Files: web/src/routes/(dashboard)/executions/[id]/+page.svelte, web/src/lib/workflow-editor/event-stream.svelte.ts, web/src/lib/components/workflow-editor/execution-canvas.svelte

Existing tickets: FEAT-0j7r5s

---
### A failed background refetch of the workflow unmounts the editor and throws away unsaved edits (dashboard and embed) [find:web-frontend-code] (high/bug) · area: editor state / TanStack query · confidence: high

The workflow page checks `workflow.isError` before `currentWorkflow`. TanStack v5 sets isError when a refetch fails but keeps the data. So a window-focus refetch (staleTime 30 s) that hits a 5xx, a network blip, or an expired embed token replaces the editor with 'Workflow editor could not be loaded'. 'Try again' then remounts the editor from the server copy.

Evidence: Verified. Opened wf_01a0b8e2-fff7-7a02-98c1-64f1df146257 and dragged a node (status 'Unsaved changes'). Routed GET /api/v1/workflows/<id> to 503 with playwright, waited 30 s and dispatched visibilitychange. The editor was gone, replaced by '503 — upstream restarting / Try again' (screenshot .../work/web-frontend-code/refetch-error.png). After removing the route and clicking Try again, the status reads 'All changes saved' and the edit is lost. Code: web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:233-246; web/src/lib/embed/embed-editor.svelte:161-168 (same branch order).

n8n behavior: A failed background request in n8n shows a toast. The canvas and unsaved changes stay.

Impact: Any transient server or network error while a tab regains focus destroys the user's work. In the embed this is certain once the 15-minute token expires.

Suggested fix: Show the full-page error only when there is no data (isLoadingError, or isError && !currentWorkflow). Show refetch errors as a non-blocking banner. Disable refetchOnWindowFocus for the editor's workflow query, or never let query data replace a dirty draft.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/embed/embed-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/query-client.ts

---
### A successful focus refetch silently remounts the editor over a dirty draft, and saves have no concurrency check (last writer wins) [find:web-frontend-code] (high/bug) · area: editor state / concurrency · confidence: high

`$effect(() => { if (workflow.data) currentWorkflow = workflow.data })` plus `{#key currentWorkflow.latestVersion.id}` means any newer revision arriving through a refetch (window focus after 30 s) rebuilds the editor from the server and drops local edits without a prompt. The reverse case is also broken: PUT /workflows/{id} carries no base version, If-Match or revision (the OpenAPI lists only the path id; responses are 200 and default; no 409/412 handling exists in Go). A second tab or user saving therefore overwrites the other's revision silently.

Evidence: Verified live on wf_01a0b8e2-fff7-7a02-98c1-64f1df146257. Tab A changed the HTTP URL to https://example.com/tabA-unsaved ('Unsaved changes'). A PUT from 'tab B' through the API renamed the workflow (rev 4). After more than 30 s, a visibilitychange refetch remounted tab A: status 'All changes saved', URL back to https://example.com, title showing tab B's name, and no notice. Code: +page.svelte:71-73, :244; embed-editor.svelte:70-72, :170; the query-client.ts defaults keep refetchOnWindowFocus on. FEAT-f681vt specified 'a successful GET replaces the confirmed document and draft', which is the behaviour that causes the loss.

n8n behavior: n8n tracks versionId. A save against an outdated version is refused with a 'workflow changed by someone else' conflict and the user chooses what to do. Background activity never replaces the open canvas.

Impact: Silent loss of edits for anyone who works in two tabs, shares workflows, or has the host app or API change the workflow while an embedded editor is open.

Suggested fix: Send the base revision (If-Match or baseVersionId) on PUT and return 409 on mismatch. When refreshed data arrives and the draft is dirty, show a 'newer version available: reload / keep mine' banner instead of remounting.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/embed/embed-editor.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go

Existing tickets: FEAT-f681vt

## Acceptance criteria

- [ ] The execution detail page refetches the execution about 900 times a second, so node data cannot be opened
- [ ] A failed background refetch of the workflow unmounts the editor and throws away unsaved edits (dashboard and e
- [ ] A successful focus refetch silently remounts the editor over a dirty draft, and saves have no concurrency chec
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)