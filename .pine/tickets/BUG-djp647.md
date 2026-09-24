---
id: BUG-djp647
title: 'Code node: a file returned inline as base64 binary is refused at run time, where n8n stores it'
status: testing
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T14:07:17Z"
updated: "2026-09-24T14:07:17Z"
---

# Description

Found by `make js-diff` over the Code-node compatibility corpus (FEAT-afkx3k): one body among the 500 most-viewed n8n templates builds a file in code and returns it inline, and KilasFlow refuses the result.

**What n8n does** (read from n8n's Code node result validation outside this repository; behaviour only): a returned item's `binary` only has to be an object. An entry of the form `{ data: <base64 text>, mimeType, fileName }` is accepted as the file's contents and flows on to the next node as binary data. This was the usual way to make a file in a Code or Function node before `this.helpers.prepareBinaryData` existed, so older templates use it.

**What KilasFlow does:** `jsrun` accepts a returned binary entry only when it references a file the node was given or stored with `prepareBinaryData` (EPIC-tjnr1z amendment 10), so the inline entry is refused with `item 0 has a binary "data" that is not a file reference`.

This may be the intended boundary: amendment 10 kept file bytes out of the returned JSON. If so, it should be a refusal the importer and validation can name, not a run-time surprise. If not, the runtime can store inline base64 the way `prepareBinaryData` does.

# Minimal reproduction

All-items mode, one input item:

```js
items[0].binary = { data: { data: Buffer.from('<p>hi</p>').toString('base64'), mimeType: 'text/html', fileName: 'page.html' } }
return items
```

- n8n: one item whose binary `data` is a 9-byte text/html file named page.html
- KilasFlow: `item 0 has a binary "data" that is not a file reference; binary entries can only pass on files the node was given or stored with prepareBinaryData`

# Acceptance Criteria

- [x] Decide: store inline base64 binary returned by code as a file (bounded by `MaxFileBytes`), or keep refusing it.
- [x] Either way the answer is the same at import, validate and run: stored and passed on, or refused through `jsrun.Refusal` in one sentence that names `prepareBinaryData` as the alternative.

# Notes

## n8n's behaviour, settled from its source (2026-09-24)

Read in the official image `docker.n8n.io/n8nio/n8n:2.33.7` (the Code node's result validation and n8n-core's binary data service), outside this repository. Behaviour only, in my own words.

- The Code node checks only that a returned item's `binary` is an object; it does not look inside an entry.
- Wherever n8n later needs a file's bytes, an entry with an `id` is read from binary storage by that id; an entry without one is read by base64-decoding its `data` field with Node's `Buffer` decoder, which is lenient (it skips characters outside the alphabet, takes either alphabet, needs no padding). `mimeType` and `fileName` pass on as the entry carries them, unchecked. So an inline entry works downstream as a file.

## Decision (controller ruling, 2026-09-24)

**Stored, not refused.** Inline base64 is stored through the same server-side path as `prepareBinaryData`: the worker never writes a file; the bytes come back in the result JSON (bounded by the output cap), and the process that prepared the job decodes and stores them after the run succeeded. Amendment 10's "a returned id must come from this node's input" still holds for references. The answer is the same at import, validate and run because nothing refuses it anywhere: the analyser and importer never looked inside a returned binary.

How it works (`internal/jsrun/inline.go`, `items.go`, `run.go`, `helpers.go`):

- A returned binary entry with a non-empty string `id` is a reference, whatever else it holds (as in n8n, where the id wins). One without an id whose `data` is a string is a file given inline. Anything else is refused with `item N has a binary "x" that is neither a file reference nor a file given inline; …` naming both ways to make one.
- Worker side, after each call (`checkFiles`): the base64 is decoded to validate it, the size is held to `MaxFileBytes` (a named `ErrFileLimit`), `fileName` and `mimeType`, when given and not null, must be text, and `mimeType` must parse as a media type. Failures are `ErrInvalidReturn` at the item that returned it, so a per-item run that continues on failure fails only that item. Each inline file is **charged as one host call** against `MaxHostCalls` (the prepareBinaryData call it stands in for), which is what stops a result of thousands of tiny files from filling the store.
- Server side (`Job.Finish`, which now takes the execution's `ctx`): every entry is decoded and validated again and the count per result is held to `MaxHostCalls` (a worker is trusted only as far as its code could go; a breach is an engine fault). Only once the whole result has decoded are the files stored, through `ledger.writeFile`, the helper path `prepareBinaryData` now shares, so the nil-helpers, cancelled-run and size checks and the ledger record are the same. A storage failure fails the run with no items; a run that failed stores nothing.
- **Decision: base64 is validated, not decoded Node's way.** Line-wrapped, URL-safe and unpadded base64 is read; anything else is refused rather than decoded into different bytes (Node's decoder would skip, say, a `data:` URL prefix's `:;,` and store garbage). The brief asked for validation; this keeps the leniency real inputs need.
- **Decision: no `mimeType` stays unset** and the server works one out from the name, the bytes or `text/plain`, exactly as for `prepareBinaryData` (n8n would leave it undefined).

## Progress (2026-09-24)

- Tests: `TestAFileReturnedInlineIsStoredLikeAPreparedOne`, `TestAnInlineFileThatCannotBeStoredIsRefused`, `TestInlineFilesCountAgainstTheHostCallLimit` (jsrun); `TestAFileReturnedInlineIsStoredByTheServer` and the `badinline`/`manyinline` lying-worker cases in `TestTheServerTrustsAWorkerOnlyAsFarAsItsCodeCouldGo` (jsworker); `TestACodeNodeStoresAFileReturnedInline` (nodes, a real binary store, the ticket's repro: a 9-byte text/html page.html).
- Corpus: 2314/4 moved from `invalid-return` to `error: this.helpers.prepareBinaryData (not wired in the instrument)`: the body is now accepted and fails only where the instrument has no file storage. The scoreboard's classifier names that case (it used to match only thrown errors). Baseline regenerated.
- Docs: n8n-migration.md ("Binary data is metadata"), safety-boundaries.md and `internal/jsworker/doc.go` (the server re-checks and stores inline files), `internal/jsrun/doc.go`.

## Fix round 1 (2026-09-24)

- **An id wins whenever it is truthy, as in n8n.** `fileOf` used to take an entry with a non-string id (say `id: 5`) and string `data` down the inline route; it now reads any truthy JSON id (not null, false, 0 or "") as a reference, which then names no known file and is refused. A falsy id (`id: 0`, `id: ""`) with string data is still a file given inline, as n8n reads it.
- **One budget.** The server held inline files to `MaxHostCalls` per result, apart from the helper calls, so a lying worker could have stored about twice the budget. `Finish` now holds the inline files plus the helper calls to one budget: `MaxHostCalls` for the run, or `MaxHostCalls` × items in per-item mode (the server cannot tell which item made a call, so it holds the whole run to the budget of all its items; the worker still charges each item its own). The helper-call count is the server's own: `Executed.HelperCalls` (`json:"-"`, so it never crosses from a worker) is set by the runtime in process and by the pool from the calls it answered. Test: the `callsinline` lying worker (2 helper calls and 1 inline file under a budget of 2).
- Follow-up ticketed: BUG-qe71kf (files stored for a run that then fails stay unreferenced until the execution's storage goes).
