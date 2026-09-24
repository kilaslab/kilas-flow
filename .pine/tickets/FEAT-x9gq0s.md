---
id: FEAT-x9gq0s
title: 'JS Code runtime P5: this.helpers (httpRequest, binary) and $getWorkflowStaticData'
status: testing
priority: medium
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p5
created: "2026-09-23T01:33:22Z"
updated: "2026-09-24T12:00:00Z"
---

# Description

Host helpers that go through the tenant's egress policy, plus a workflow static-data store.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 5* section. Read it before starting.

# Acceptance Criteria
- [x] `this.helpers.httpRequest` returns a Promise, goes through `internal/safehttp`, and counts against `MaxHostCalls`
- [x] `getBinaryDataBuffer` and `prepareBinaryData` are tenant-scoped
- [x] `$getWorkflowStaticData('global'|'node')` uses a new tenant-scoped table; it is saved only after a successful non-manual run, capped at 256 KiB
- [x] The editor output panel has a Console tab (live for manual runs, persisted in execution detail)

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 5*.

# Notes

## Plan (2026-09-24, backend half — Task 1 of the EPIC-tjnr1z remaining-work plan)

The editor Console tab is a separate task (Task 2); this is the backend.

1. **Analyser** (`internal/jsrun/analyze.go`): drop the blanket refusal of
   `this.helpers` and `$getWorkflowStaticData`; refuse a *named*
   `this.helpers.X` other than `httpRequest`, `getBinaryDataBuffer` and
   `prepareBinaryData`.
2. **Runtime** (`internal/jsrun`): one asynchronous host call, `Host.Call`,
   that every helper goes through. The VM hands each call to a goroutine and
   settles its promise through the job loop, so the VM keeps running timers
   and other promises, and `Promise.all` has several calls in flight. The
   clock pauses while the VM is idle waiting *only* on host calls. Every call
   counts against `MaxHostCalls`. A JavaScript module (`helpers.js`) turns
   n8n's options into a plain request and the answer back into n8n's shapes.
   `$getWorkflowStaticData` is a synchronous call (as `$('Node')` is); what
   the code handed out is written back as JSON after a successful run, under
   a 256 KiB cap. Files a run stores are recorded beside the job, so the
   decoder accepts them and nothing else.
3. **Worker protocol** (`internal/jsworker`): every call carries an id; a
   reader goroutine in the worker routes replies, so a reply can arrive while
   the job loop runs; the server answers asynchronous calls on goroutines and
   pauses the job's kill deadline while any is in flight. Out-of-turn,
   oversized or unknown frames retire the worker.
4. **Engine**: `engine.StaticData`, a per-execution handle loaded lazily
   through a `StaticDataStore`, saved by the service only after a successful
   execution whose trigger is not `manual`, and only when it changed.
5. **Repository**: migration 000024 (`workflow_static_data`, sqlite and
   postgres), a GORM store, deleted with its workflow and by the tenant purge.
6. **Nodes**: the Code (JavaScript) executor's server half: HTTP through
   `internal/safehttp` with the deployment's egress policy, files through
   `request.Binaries`, static data through the execution's handle.


## Console tab (editor)

Frontend-only slice of this ticket (Task 2 of the EPIC-tjnr1z remaining-work
plan). The backend half (httpRequest/binary helpers, static data) is being done
in parallel on another branch and is untouched here.

**Where "the editor output panel" actually is.** The workflow editor itself
(`workflow-editor.svelte`) has no per-node output surface today — Execute only
links to `/executions/[id]` (`m.editor_view_execution()`). That execution
detail page's node aside (`routes/(dashboard)/executions/[id]/+page.svelte`)
already streams live events for an in-progress run and shows the persisted
trace once fetched — it *is* the output panel a manual run from the editor
lands on, and it already had an inline (non-tabbed) Console section from P2.
So this ticket turns that aside into a small tabbed panel (mirroring
`properties-panel.svelte`'s WAI-ARIA tablist pattern: "Node data" / "Console"),
shown only for `kilasflow.jsCode` nodes, rather than inventing a second output
surface inside the canvas editor route.

**Plan:**
1. Extract the console parsing (`nodeConsole` → `parseConsole`) and tone
   helper out of the page into `lib/workflow-editor/execution.ts`, which
   already holds the page's other pure node-run helpers.
2. Add `liveConsole(nodeId, events)` to `event-stream.svelte.ts`: folds every
   `code.console` SSE event for a node into one `{ lines, truncated }`, the
   same shape the persisted `NodeRun.Console` parses to.
3. Extract the `<ol>` rendering into `components/workflow-editor/node-console.svelte`
   (empty state, per-level tone, truncation note, `max-h-72 overflow-auto`),
   so both the live and the persisted path render through one component.
4. The page prefers the persisted console (`selectedRun.console`, present once
   `execution.refetch()` lands after the run ends) and falls back to
   `liveConsole` while the node's run row isn't in the fetched trace yet —
   which is the case for the whole duration of an in-progress run, since the
   trace is only refetched once the *execution* (not the node) finishes.
5. Tab only appears for `kilasflow.jsCode` nodes; the "node unreached" message
   is shown on the Console tab too while the node is `skipped`.

**Known limitation, not fixed here:** a retried node's live console
accumulates lines across every attempt (the SSE `ExecutionEvent` carries no
attempt number), while the persisted view only ever shows the latest attempt
per `latestNodeRuns`. So a retried Code node's console can visibly shrink the
moment the execution finishes and the persisted trace takes over. Pre-existing
shape of the problem (the same trace-only-updates-on-refetch gap applies to
Input/Output too); flagging it rather than fixing it, since the brief scopes
this ticket to the Console tab only.

## Progress (2026-09-24): the backend half is done

Everything in the plan above is in, on branch `epic/p5-backend`. The Console
tab criterion is the editor half (Task 2), done on its own branch.

### n8n's behaviour, read from its source as a reference (owner decision 2026-09-24), in our own words

- **httpRequest.** The options are n8n's `IHttpRequestOptions`. A GET never
  sends a body, and a HEAD or OPTIONS with an empty one sends none; a
  non-empty plain object is sent as JSON, or as a form when the code set the
  form content type; a string or bytes as they are; anything else not at all.
  `json: true` adds `Accept: application/json` when no Accept header was set.
  The body comes back as text parsed as JSON when it parses (the HTTP
  library's default), bytes for `encoding: 'arraybuffer'`, text for `text`. A
  status outside 2xx rejects unless `ignoreHttpStatusErrors` is `true`, or an
  `{ except: [...] }` list the status is not in. `returnFullResponse` gives
  `{ body, headers, statusCode, statusMessage }`. `qs` goes through the HTTP
  library's serializer (arrays as `key[]=`) unless `arrayFormat` names another.
  Options n8n does not know are ignored.
- **Static data.** n8n keeps `staticData` on the workflow as one document,
  `global` plus `node:<name>` per node. Its after-execution hook saves it
  when the execution was not `manual` and a node changed it, whatever the
  execution's status (failed and waiting runs included), and sub-workflow
  runs (mode `integrated`) save theirs too. `$getWorkflowStaticData` takes
  `'global'` or `'node'` and throws for anything else.
- **Files.** `getBinaryDataBuffer(itemIndex, property)` reads the input
  item's file (a file object can be passed instead of the property name).
  `prepareBinaryData` takes the base of the path it is given as the file
  name, and a type from the name, else from the bytes' signature, else
  `text/plain`.

### Decisions

1. **Save only after success**, as the brief says, although n8n saves after a
   failed production run too; the docs say so. A run that suspends on a Wait
   node saves nothing either (it is not a success yet), and its resumed half
   loads the stored data afresh. A sub-workflow's own run saves, as n8n's
   does. A node run that fails keeps nothing of what it changed.
2. **All three helpers count against `MaxHostCalls`**, not only httpRequest:
   waiting on the server is free of the clock, so an unbounded loop of file
   reads would otherwise hold a worker until the execution's timeout.
3. **Caps.** A file or request body moves at most 32 MiB in one call
   (`jsrun.MaxFileBytes`, below the 64 MiB Buffer cap). A response is capped
   at the policy's `max_response_bytes` or 32 MiB, whichever is less. Each is
   a named error that stops the code; a refused target, a network failure or
   a missing file rejects the promise, which the code may catch.
4. **Options.** Refused by name: `proxy`, `skipSslCertificateValidation`,
   `abortSignal`, `agentOptions`, `allowedDomains`, and encodings other than
   `arraybuffer`, `json` and `text`. Supported beyond the brief's list because
   ignoring them would change the request and honouring them is small:
   `baseURL`, `auth` (the code's own basic auth, never a stored credential),
   `disableFollowRedirect`, `maxRedirects` (never past the policy's) and
   `arrayFormat`. A file object passed to getBinaryDataBuffer is refused.
5. **The clock** pauses while the VM is idle with only helper calls pending
   (a timer armed beside them keeps it running); the pool's kill deadline is
   held while any helper call is in flight. The execution's context and the
   helper's timeout bound the wait.
6. **Protocol.** Every call carries an ID. A reader goroutine in the worker
   routes replies; a call is bound to its job and is never sent after the job
   ends, and a late reply for the job that just ended is dropped. The server
   answers helper calls on goroutines, writes nothing for a job once it is
   over, and retires a worker that makes more helper calls than its limit,
   names an unknown helper, or sends more than one call may carry.
7. **`this.helpers`** is a Proxy: the three helpers run; any other name the
   code reaches refuses in the one sentence (the analyser refuses a named one
   before the code runs). The old `bindAsync` test seam is gone; the helpers
   are the seam it was kept for.

# Related Files

# Attachments
