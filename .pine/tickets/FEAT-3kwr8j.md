---
id: FEAT-3kwr8j
title: an embed session has no datastore write scope at the document level
status: todo
priority: low
labels:
    - embed
    - datastore
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

An embed session carrying `workflow:write` can already save step-node and tool writes into its own confined workflow document, including Data table step nodes and Data table Tool descriptors that write rows. `ScopeDatastoreWrite` (`datastore:write`, internal/embed/embed.go ~35-41) exists, but it is only checked by the API routes that manage a datastore directly (schema, row CRUD outside a workflow document) — it has no bearing on whether a `workflow:write` session may author a data-writing node inside the document it already owns.

So today, an embed session with `workflow:write` and without `datastore:write` can still author nodes that write rows, because that authoring happens entirely within the document-scoped write path, not through a `datastore:write`-gated API route.

# Acceptance Criteria
- [ ] A decision is recorded: whether `workflow:write` alone should continue to permit authoring data-writing nodes inside the session's own document, or whether that should require `datastore:write` (or a new document-level equivalent) as well
- [ ] The embed scope model and its tests reflect that decision explicitly, rather than leaving it as an implicit side effect of `workflow:write`

# Implementation Plan

Decide whether embedded editors may author data-writing nodes (Data table step nodes, Data table Tool descriptors) without an explicit datastore scope. If not, gate node-level datastore writes inside a `workflow:write` session on `datastore:write` too; if so, document that `workflow:write` already implies it and `ScopeDatastoreWrite` remains solely for the direct datastore-management API routes.

# Notes

`datastore:write` implies `datastore:read` and nothing else today (internal/embed/embed.go ~38-40 comment): "schema changes stay outside any embed session, the way workflow import and activation already are." This ticket is about whether row-writing *nodes* should be covered by that same boundary.

# Related Files

internal/embed/embed.go ~33-41 (`ScopeWrite`, `ScopeDatastoreRead`, `ScopeDatastoreWrite` definitions and comments)
internal/api/middleware/scope.go — the datastore-management routes gated on `ScopeDatastoreWrite`
nodes/datastore.go — the step-node and tool write paths reachable purely through `workflow:write`
