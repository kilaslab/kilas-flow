---
id: FEAT-0f87fn
title: Store binary data behind the item contract
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:04:07Z"
updated: "2026-09-05T05:04:07Z"
---

## Scope

KilasFlow's item contract already has a binary half, and nothing implements it. `workflow.BinaryRef` in `internal/workflow/document.go` is `{ID, FileName, MediaType, Size}` under a comment saying outright that payload storage is out of scope for the milestone, and `Item.Binary map[string]BinaryRef` sits beside `Item.JSON`. The entire lifecycle of that field today is two clone functions — `cloneItem` in `nodes/executors.go` and its twin in `internal/engine/runner.go` — copying a map that nothing ever fills. Nothing writes a ref, nothing reads a payload, and there is no store for one to live in.

The Code node is worse than empty: it actively discards. `nodes/code.go` hands the sandbox `runcode.Item{JSON: item.JSON}` and rebuilds the results as `workflow.Item{JSON: json}`, so any binary attached upstream is gone the moment an item passes through a Code node, with no error and no diagnostic.

Two features in this phase force the issue, and neither can be finished without it. WhatsApp media is the point of a WAHA integration — an image arrives, a workflow does something with it, a reply goes back — and the Telegram trigger's Download Images/Files option exists to pull a photo or document off an update. A workflow that can only carry JSON can carry a URL to a file, which means the file is fetched twice, is subject to whatever expiry the provider sets, and never survives into the execution record as evidence.

## Acceptance criteria

- [ ] A binary store writes a payload and returns a reference, reads it back by reference, and refuses a read from a different tenant, proven by test.
- [ ] Payloads are bounded by configuration and a write that would exceed the bound fails cleanly rather than filling the disk.
- [ ] A binary payload never enters a workflow document, an execution record, an API response body or a log line; only the reference does.
- [ ] Stored payloads are removed when their execution is removed, so a store cannot outgrow the executions it belongs to.
- [ ] The Code node carries incoming binary references through to its output instead of dropping them, and a test that passes an item with a binary through a Code node asserts the reference survives.
- [ ] The HTTP Request node stores a non-text response as a binary reference under the existing response-size bound, instead of forcing it through JSON or text decoding.
- [ ] WAHA's send-image and send-file operations and Telegram's Send Photo, Send Document and Get File all read and write through the store rather than holding payloads in item JSON.
- [ ] The editor shows an item's binary references as name, type and size, and never attempts to render the payload inline.

## Implementation Plan

Add `internal/binary` with a `Store` interface — put, get, delete by execution — and one filesystem-backed implementation rooted at a configured directory, keyed by tenant, execution and reference id. Do not put payloads in the internal database. Multi-megabyte WhatsApp media in SQLite rows would bloat the file this product ships as its default, and under the PostgreSQL tier it would land in a table inside a customer's own shared database, which is exactly the posture that phase is trying to keep narrow. A filesystem root is also the honest shape for the object-storage backend a hosted deployment will eventually want behind the same interface.

Thread the store through `engine.Request`, alongside the credential resolver and for the same reason: an executor must not reach into storage on its own, so the runtime stays the single place where tenant scoping is enforced. Then fix the two clone functions to copy references, fix the Code node's two conversion points, and only then touch the nodes that produce binaries.

Name the configuration section with one word. `internal/config`'s environment override maps the first underscore in a key to the section separator — the `OutboundHTTP` struct carries that warning in its own doc comment, which is why the section is `outbound` and not `outbound_http` — so a section called `binary_store` could never be set by an environment variable. Call it `binary`.

The trap is retention. A binary store with no deletion path is a disk-full incident with a delay fuse, and execution pruning does not exist yet anywhere in this codebase. Do not wait for it: give the store its own delete-by-execution call and wire it to whatever removes an execution today, so that when retention arrives it has one function to call rather than a directory tree to reverse-engineer.

The second trap is redaction. `internal/execution/redact.go` runs on every executions write and walks item JSON; a reference is metadata and must stay readable, so keep file names and media types out of the sensitive-key path, and keep payloads out of the record entirely rather than relying on redaction to hide them.

## References

- Roadmap plan, p3 section, entry V2-p3-8: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `internal/workflow/document.go` — `BinaryRef` and `Item`, with the comment declaring payload storage out of scope.
- `nodes/code.go` — `runcode.Item{JSON: item.JSON}` on the way in and `workflow.Item{JSON: json}` on the way out, the two points where binary is dropped.
- `nodes/executors.go` and `internal/engine/runner.go` — the two `cloneItem` implementations that copy `Item.Binary`.
- `internal/config/config.go` — `OutboundHTTP`'s doc comment on why a section name must be one word.
- `internal/safehttp/safehttp.go` — `Policy.ReadBody` and `MaxResponseBytes`, the bound a binary download has to respect.
