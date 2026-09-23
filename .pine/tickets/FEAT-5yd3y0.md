---
id: FEAT-5yd3y0
title: API reference with request/response fields, examples and curl/TS/Go tabs; OpenAPI download; pagination guide
status: todo
priority: medium
labels:
    - docs
    - api-reference
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

The generated API pages list schema names but no fields or examples, and the OpenAPI document is only available from a running server.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-16, DOC-17). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-16: API reference pages list schema names but no fields, examples or multi-language samples, and the OpenAPI document is not downloadable from the site

*gap · medium · api-reference*

**Steps to reproduce:**

1. `reference/api/embed.md` shows `Request body: application/json — EmbedSessionBody (required)` and `201 … EmbedSessionResource` with no field list and no example. The same holds for all 81 operations. Only `events.md` and `errors.md` have field tables.
2. `grep -c curl docs/src/content/docs/reference/api/*.md` → 0. The whole site uses no `<Tabs>` component (only `index.mdx` is MDX) and has no Go samples.
3. The schemas are browsable only on a running server's `/docs` (Scalar) or `/api/openapi.json`. `docs/dist` contains no copy of the spec.

**Actual:**

A reader without a running server cannot see request or response shapes.

**Expected:**

Per-operation field tables (required, type, description), one request and response example, curl/TypeScript/Go tabs, and a downloadable `openapi.json` versioned with the site.

**Suggested fix:**

Extend `scripts/generate-api-reference.mjs` to render schemas, or adopt the `starlight-openapi` plugin (it exists; verified via Context7 `/withastro/starlight` plugin list) against a committed spec produced by `scripts/openapi-spec.mjs`.

**Evidence:**

the steps above.

**Related:**

FEAT-za118x (done)


## DOC-17: Pagination uses two conventions and no page explains them

*gap · medium · api-reference*

**Steps to reproduce:**

1. From `/api/openapi.json`: `list-workflows`, `list-credentials`, `list-schedules` and `list-api-keys` return a bare array plus an `X-Next-Cursor` header. `list-executions`, `list-datastore-rows` and `list-workflow-versions` return `{items, nextCursor}` in the body. `list-datastores` returns a body object and the header.
2. `grep -rn -i paginat docs/src/content/docs` (excluding generated pages) finds only a benchmark row and `execution-model.md:318`.

**Actual:**

An integrator writing a non-TypeScript client must discover the difference per endpoint.

**Expected:**

A "Pagination" guide that names both conventions, lists which endpoints use which, covers limits and clamps, and points to the SDK helpers (`paginateCursor`, `iterateExecutions`, `iterateDatastoreRows`).

**Suggested fix:**

Write the guide. Consider converging on one convention in `/api/v2`.

**Evidence:**

the steps above (OpenAPI dump at `$SP/agents/docs-saas/openapi.json`).

**Related:**

none


# Acceptance Criteria
- [ ] Every operation shows its request and response fields and a curl example; the top 20 operations also have TypeScript and Go
- [ ] `openapi.json` can be downloaded from the site, and its version matches the banner
- [ ] A Pagination page explains both conventions (cursor and page)
- [ ] The existing drift gate still covers the generated pages

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-za118x

# Related Files

# Attachments
