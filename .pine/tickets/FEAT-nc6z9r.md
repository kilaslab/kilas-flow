---
id: FEAT-nc6z9r
title: Add the Datastore management surface to the SDK
status: todo
priority: high
labels:
    - sdk
    - api
    - datastore
deps:
    - FEAT-xeq6st
    - FEAT-1c70nt
    - FEAT-yx0qt6
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:51:07Z"
updated: "2026-09-05T11:51:07Z"
---

## Scope

`FEAT-1c70nt` (V2-p9-15) carries an acceptance criterion reading "`KilasFlowClient` exposes typed datastore methods", and that criterion is scoped by the ticket it sits in: V2-p9-15 is about the **embed vocabulary** — a `DatastoreID` beside `WorkflowID`, a `permits` entry, `datastore:read` and `datastore:write` scopes, and a browser entry point that fails cleanly when handed a datastore-only session. The datastore methods it adds are the ones needed to prove that path works.

That is not the surface a host application needs, and the difference is the reason this ticket exists rather than widening that one. An embed session is bound to one datastore, lives at most thirty minutes, is pinned to one exact browser origin, and reaches nothing the `permits()` allowlist does not name. A SaaS provisioning datastores for its customers is doing the opposite in every dimension: creating and destroying datastores, editing column schemas, bulk-writing rows from its own backend with a long-lived tenant API key. That is `FEAT-xeq6st`'s three resource groups — datastores, columns, rows — and none of it is reachable through an embed token by design.

This ticket depends on `FEAT-1c70nt` rather than racing it, so the two never write the same method twice: V2-p9-15 establishes the types and the embed-scoped subset, and this ticket completes the management surface on top of them.

Three pieces of the management API need real client work rather than one-line wrappers.

**The filter shape is a structure, not a string.** `FEAT-nrfg6e` (V2-p9-2) defines n8n's shape — `{type: and|or, filters: [{columnName, condition, value}]}` over `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, `isEmpty`, `isNotEmpty`. Handing a host a raw object literal for this is handing it a runtime error, because an unrecognised operator is a server-side error by deliberate design — the operator slot is a predicate-bypass hole if it is ever a passthrough.

**Row delete requires a filter, and the client is where that must first be enforced.** `FEAT-xeq6st` specifies that an absent, empty or unrecognised filter on `DELETE /datastores/{id}/rows` is a 422 and never means "all rows". A typed client that permits the call to be written without a filter has moved a full-table wipe from impossible to one forgotten argument away, and has done so in the layer the host actually writes.

**Paging needs the cursor helper the executions surface never got.** `listExecutions` returns `{items, nextCursor}` and leaves the loop to the caller. `FEAT-nrfg6e` reuses that keyset pattern for rows and extracts the cursor helpers into a shared Go helper first, "because a second copy is how two cursor formats drift apart" — the same argument applies on the client, where a datastore of any size makes hand-rolled paging the common case rather than the exception.

## Acceptance criteria

- [ ] All three resource groups are reachable through the client — datastore create, list, get, update and delete; column add, rename, retype and delete; row insert, get, update, upsert and delete — matching whatever `FEAT-xeq6st` registers.
- [ ] The filter is built through a typed constructor whose operator set is closed, so an unrecognised operator is a compile-time error in TypeScript rather than a runtime 4xx.
- [ ] A row delete cannot be expressed without a filter: the signature requires one, and a test proves that an empty filter object is rejected client-side rather than sent.
- [ ] An async iterator pages a datastore's rows to exhaustion using `nextCursor`, and it is used for executions too rather than written twice.
- [ ] Row values are typed against the datastore's declared column types — string, number, boolean, date — including SQLite's boolean and date normalisation, so a host does not have to know which driver is underneath.
- [ ] The dry-run behaviour on update, upsert and delete is exposed and its paired before/after result is typed, rather than surfacing as an untyped extra field.
- [ ] The methods this ticket adds do not duplicate or contradict those added by `FEAT-1c70nt`; the embed-scoped subset and the management surface are one coherent API, verified by reading both.
- [ ] It is documented that a workflow SQL node can never read a datastore and the Datastore node and this API are the only paths, since `FEAT-a94c8y` makes that a guard rule rather than a convention.

## Implementation Plan

Do not start before `FEAT-xeq6st` has registered its operations. The coverage test from V2-p10-5 will list the datastore operations as uncovered the moment they exist, which is the signal to begin and also the check that this ticket finished.

The filter builder is the piece worth designing rather than transcribing. A discriminated union over the operator, with the value type varying by operator, gets most of the safety for little code: `isEmpty` and `isNotEmpty` take no value at all, and a builder that requires one for them is a builder that will be worked around. Keep the wire shape exactly as `FEAT-nrfg6e` defines it — the builder produces that object, it does not invent a friendlier one, because the server is the authority and a client-side dialect would need translating in both directions.

For paging, write one generic cursor iterator and use it for both rows and executions. The Go side of this ticket family extracts a shared cursor helper for exactly this reason; doing the same on the client costs nothing now and prevents the identical drift.

Column typing is the one place to be careful about promising too much. A datastore's columns are known at runtime, not at compile time, so full static typing of row values is only available to a host that declares its schema in TypeScript. Recommend offering a generic parameter for hosts that want it and a permissive default for hosts that do not, and say plainly which is which — an API that looks statically typed but is not is worse than one that is honestly dynamic.

One boundary to state rather than discover: `FEAT-cjpbe6` (V2-p9-12) settles that writing a datastore row must not copy row contents into the execution trace, and that redaction rewrites by key match at several layers. A client that echoes row contents into its own error messages reintroduces exactly the leak that ticket closes, so keep row values out of `KilasFlowError` messages and let the caller decide what to log.

## References

- Roadmap plan, p10 section, entry V2-p10-7: `.pine/roadmap.md`.
- `.pine/tickets/FEAT-xeq6st.md` — V2-p9-7, the three resource groups and the required-filter rule on row delete.
- `.pine/tickets/FEAT-1c70nt.md` — V2-p9-15, whose fifth acceptance criterion this ticket completes rather than repeats.
- `.pine/tickets/FEAT-nrfg6e.md` — V2-p9-2, the filter shape, the operator set, the keyset cursor and the dry-run result.
- `.pine/tickets/FEAT-cjpbe6.md` — V2-p9-12, the redaction and trace rules a client must not undo.
- `.pine/tickets/FEAT-a94c8y.md` — V2-p6-5, which makes "a SQL node can never read a datastore" a guard rule.
- `sdk/src/server.ts` — `listExecutions` and `ExecutionListResource`, the existing `{items, nextCursor}` shape the iterator generalises.
- `sdk/src/http.ts` — `Transport.request` and `KilasFlowError`, which the new methods build on.
