---
id: FEAT-t26rt7
title: Import and export Datastore rows as CSV
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-xeq6st
    - FEAT-agj52c
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

Nothing in this repository can read or write CSV: a search for `encoding/csv` across every `.go` file returns no hit, and no operation in `internal/api/handlers` emits anything but JSON — `sse.Register` at `internal/api/handlers/executions.go:167` is the one exception, every other handler returning its payload through a `Body` field, as `exportWorkflowOutput` does in `internal/api/handlers/interop.go`. V2-p9-7 delivers the datastore API and V2-p9-9 the grid above it, and between them a customer's first act on an empty datastore — loading the sheet they already hold — has no route.

The system columns are where the work sits. A datastore's table carries `id`, `createdAt` and `updatedAt` and reserves `dryRunState`, so an export is two documents depending on who asked: a backup, which must carry them, and a sheet a person will edit and hand back, which must not. n8n added that choice only after launch, per the roadmap — not checkable from here, since the reference checkout at `/Users/izzadev/projects/mitrachat/n8n` carries no `packages/cli/src/modules/data-table`. The consequence is visible in `design-refs/n8n-v2/23-datatable-grid-with-columns.png`: `id`, `createdAt` and `updatedAt` inline beside the user's `email` and `score`, timestamps written `2026-09-05T15:07:56.694+07:00`.

The browser half is already broken for a non-JSON response. `apiFetch` in `web/src/lib/api/http.ts` sets `Accept` to `application/json` when a caller sets none (line 68) and calls `response.json()` on every response that is not a 204 (line 96), so a `text/csv` download through a generated client throws a parse error instead of yielding bytes. The SPA has never downloaded a file at all: `web/src` contains no `Blob(` and no `createObjectURL`.

The upload half is bounded by a library default nobody chose. Huma sets `Operation.MaxBodyBytes` to `1024 * 1024` when an operation leaves it unset — `ensureMaxBodyBytes`, `huma.go:1513-1518` — and `BodyReadTimeout` to five seconds beside it, and no `huma.Register` call in `internal/api/handlers` sets either. `readBody` at `huma.go:2164-2189` refuses an over-length body with 413 by comparing `count == maxBytes` after an `io.LimitReader`, so a file of exactly one mebibyte is refused too. The ceiling on a customer's spreadsheet is inherited, not chosen.

Adoption of a datastore is a migration, not a feature: the rows already exist, in a sheet or in the n8n instance being replaced. Without CSV in both directions the datastore is somewhere to type data by hand, and a host cannot get its customers' data back out — the objection an embeddable, white-label product can least afford to leave standing.

## Acceptance criteria

- [ ] Exporting with system columns excluded yields a header row of exactly the user columns in stored `index` order, proven by a handler test with reordered columns.
- [ ] Exporting with system columns included adds `id`, `createdAt` and `updatedAt` with millisecond timestamps matching what the row store returns, proven by a handler test.
- [ ] An exported file re-imports into a fresh datastore with identical values and column types across string, number, boolean and date, proven by a round-trip test.
- [ ] An import whose header carries `id`, `createdAt`, `updatedAt` or `dryRunState` is refused with the offending name in the problem detail, proven by a table-driven test.
- [ ] An upload above the operation's configured limit is refused with 413 naming that limit, which is set explicitly rather than inherited, proven by a handler test.
- [ ] A malformed row fails the import with its one-based line number and column name, and the row count is unchanged, proven by a no-partial-write test.
- [ ] Downloading from the datastore surface saves a file rather than raising a JSON parse error, checked by hand in a browser and recorded on the ticket.
- [ ] A CSV round trip runs against both drivers through `make smoke-sqlite` and `make smoke-postgres`, run by hand with the output recorded on the ticket.

## Implementation Plan

Settle the wire format before a line of parsing is written, because it is the only part of this ticket the OpenAPI document carries and so the only one that forces regeneration in two places: `web/orval.config.ts` produces a svelte-query client, `sdk/orval.config.ts` produces types, and both read `/api/openapi.json`.

**Raw body against a base64 JSON field.** Recommend a `RawBody []byte` field tagged `contentType:"text/csv"` on import and a streamed response on export; Huma v2.39.1 supports both and neither appears in this tree today, so `setRequestBodyFromRawBody` at `huma.go:1562-1610` is the reference. Reject a JSON body carrying the file as a base64 string: it inflates the payload by a third, holds it in memory twice, and hides `text/csv` from the generated document, so every client would encode by hand. Multipart is a third option that buys only a filename field.

Put the format in `internal/datastore` beside the DDL service from V2-p9-1 and the row store from V2-p9-2 — a `csv.go` taking a reader and a writer — so the encoding is testable without HTTP and the handler stays the thin adapter `Interop.Export` already models. Page the export through V2-p9-2's keyset cursor rather than one large query, because V2-p9-5 bounds how long a datastore operation may hold the single SQLite connection.

The trap is the byte-order mark. Excel writes UTF-8 CSV with a leading U+FEFF, `encoding/csv` does not strip it, and the first header cell arrives with that character glued to `email`. Nothing errors: the import either creates a column with an invisible character in its name or fails to match the first column and shifts every value one place, and a customer finds it in their own data weeks later. Strip the mark on read and make one test feed a file that carries one.

Make the toggle a query parameter defaulting to exclusion, and refuse the four reserved names outright on import rather than ignoring them — a file carrying an `id` column is a backup being restored through the wrong endpoint, and dropping the column silently is how two rows become one. The download cannot go through `apiFetch`: a plain anchor would not carry `X-KilasFlow-Embed`, which that function attaches at lines 72-75, so a helper that fetches with the header and reads `response.blob()` is the honest answer.

**Recommend** treating an empty field as NULL for number, boolean and date columns and as the empty string for string columns, asserted in the round-trip test, because CSV cannot distinguish the two and an unstated rule quietly rewrites data on the first export and re-import. What reopens it: if V2-p9-2's `isEmpty` and `isNotEmpty` operators match NULL and the empty string identically, the distinction stops being observable and the cheaper rule — everything empty is NULL — wins.

## References

- Roadmap plan, p9 section, entry V2-p9-14: `.pine/roadmap.md`.
- `internal/api/handlers/interop.go` — `Interop.Export` and `exportWorkflowOutput`, the JSON-only output shape every operation in the tree follows.
- `internal/api/handlers/executions.go:167` — `sse.Register`, the one operation that emits something other than JSON.
- `web/src/lib/api/http.ts` — `apiFetch`, its `Accept` default at line 68, its unconditional `response.json()` at line 96, and the embed-token header at lines 72-75.
- `github.com/danielgtaylor/huma/v2@v2.39.1/huma.go` — `ensureMaxBodyBytes` and `ensureBodyReadTimeout` at 1513-1525, `readBody` at 2164-2189, and `setRequestBodyFromRawBody` at 1562-1610.
- `internal/webhook/webhook.go` — `Limits.MaxBodyBytes` and `DefaultLimits`, the only body cap the product sets for itself, with its one-mebibyte configuration default in the `Webhook` block of `internal/config/config.go`.
- `design-refs/n8n-v2/23-datatable-grid-with-columns.png` — n8n's grid with system columns beside user columns, and its timestamp format.
- `Makefile` — the `smoke-sqlite` and `smoke-postgres` targets that prove the binary against each driver.
