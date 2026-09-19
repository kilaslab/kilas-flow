---
id: FEAT-x5km1z
title: 'Auth-enabled dashboard unusable: no sign-in page, no 401 handling, empty Settings'
status: todo
priority: high
labels:
    - auth
    - ui
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:ui-ops-surfaces, find:ui-shell-lists, find:unfinished-work, find:web-frontend-code.

---
### Auth-enabled deployments have no sign-in page: the dashboard renders and then every call fails with 401 [find:ui-ops-surfaces] (critical/unfinished) · area: auth / dashboard shell · confidence: high

With KILASFLOW_AUTH_ENABLED=true the SPA has no login route, no redirect on 401, no sign-out and no current-user display. Every page renders its shell and then shows "401 — This request needs an API key or a signed-in session".

Evidence: Private instance on :18099 with auth on, fresh browser. /app/workflows renders the full shell (Import n8n, New workflow) and its lists fail. /executions shows "Executions could not be loaded — 401 …" (17-auth-on-executions.png). /login returns "404 Not Found". Grepping web/src for auth/login, authLogin, logout and auth/me finds no caller outside the generated client. The only way in is POST /api/v1/auth/login with curl to get the kilasflow_session cookie. FEAT-ddzk2k explicitly deferred "the SPA login route with a 401 redirect", and no follow-up ticket exists.

n8n behavior: An unauthenticated visit redirects to /signin, and the user menu offers sign-out.

Impact: Nobody can use the editor on a production (auth-on) instance through the UI.

Suggested fix: Add a /signin route that uses POST /api/v1/auth/login. Make lib/api/http.ts send any 401 to /signin?next=…. Add a user menu (auth/me, logout) to the (dashboard) layout.

Files: web/src/routes/(dashboard)/+layout.svelte, web/src/lib/api/http.ts, web/src/routes (new signin route)

Existing tickets: FEAT-ddzk2k

---
### Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; Settings is an empty placeholder [find:unfinished-work] (high/unfinished) · area: web / auth · confidence: high

The server implements sessions, API keys and tenancy behind auth.enabled, and the docs tell operators to enable it. The SPA never calls any auth endpoint and has no login route. Once identity is on, every dashboard API call gets 401 and the user has no way to sign in.

Evidence: The API has POST /api/v1/auth/login|logout, GET /api/v1/auth/me and /api/v1/api-keys, and config.example.yaml:130-160 describes 'SessionTTL is how long a dashboard login lasts'. A grep of web/src (excluding generated/) finds no reference to auth/login, auth/me or api-keys. web/src/lib/api/http.ts has no 401 branch. There is no login route. web/src/routes/(dashboard)/settings/+page.svelte is 11 lines: 'No settings are available in this shell yet.' FEAT-ddzk2k Work evidence (line 114): 'The SPA login route and the SDK's apiKey convenience are not in this change'. No follow-up ticket exists; the same note says account lockout 'is worth its own ticket', and none exists. docs/operate/security.md tells operators to turn identity on. Not tested live: no private port was available to start an auth-enabled instance.

n8n behavior: Sign-in page, user management, and personal API keys under Settings.

Impact: The recommended production posture (auth on) locks operators out of the editor. API keys can only be managed with curl.

Suggested fix: Add a /login route, redirect to it on 401, add sign-out, and add Settings pages for API keys (create/revoke, show once) and the account. Open a ticket for login rate-limiting and lockout.

Files: web/src/lib/api/http.ts, web/src/routes/(dashboard)/settings/+page.svelte, web/src/routes/+layout.svelte, internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### The SPA has no sign-in route and no 401 handling, so the dashboard is unusable when auth is enabled [find:web-frontend-code] (high/unfinished) · area: auth UI · confidence: high

When auth is on, the server gates /api/v1 and returns 401 'This request needs an API key or a signed-in session.' It also provides /auth/login, /auth/logout, /auth/me and /stream-tickets. The SPA never calls any of these: no login route, no global 401 redirect, no logout or user menu. EventSource does not use stream tickets. Every page would just show '401 — ...' errors.

Evidence: internal/api/middleware/auth.go:67-105 (gate). The generated operations login, logout, getMe and createStreamTicket have 0 call sites outside /generated/. web/src/routes contains only (dashboard), approve and embed. web/src/lib/api/http.ts treats 401 as a generic ApiError. FEAT-ddzk2k (done) line 114: 'The SPA login route and the SDK's apiKey convenience are **not** in this change', and no follow-up ticket exists. Static evidence only; not checked on a live auth instance.

n8n behavior: n8n shows a sign-in page and redirects there on 401, and has a user menu with sign out.

Impact: Operators cannot turn on authentication and still use the built-in dashboard, which blocks any multi-user or production deployment of the editor.

Suggested fix: Add a /login route that posts email and password to /auth/login. Add a 401 interceptor in apiFetch or a QueryCache onError that redirects with returnTo. Add a user menu with logout. Mint a stream ticket per EventSource connect.

Files: /Users/izzadev/projects/k-flow/web/src/lib/api/http.ts, /Users/izzadev/projects/k-flow/web/src/lib/query-client.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/event-stream.svelte.ts, /Users/izzadev/projects/k-flow/internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### Settings is an empty placeholder, while API keys and 'who am I' have a complete API but no UI [find:ui-shell-lists] (medium/unfinished) · area: settings / IA · confidence: high

/settings shows only 'No settings are available in this shell yet.' The server provides GET/POST/DELETE /api/v1/api-keys (create returns a token shown once) and GET /auth/me, but nothing in web/src calls them. Operators have to use curl to create or revoke API keys. There is also no instance information (version, public URL, timezone) and no profile section. This may overlap ui-ops-surfaces, whose plan includes settings and API keys.

Evidence: routes/(dashboard)/settings/+page.svelte is 11 lines. Searching web/src outside api/generated for createApiKey, listApiKeys or getMe finds nothing. openapi has list-api-keys, create-api-key and revoke-api-key.

Impact: Embedding customers need API keys for server-to-server calls and cannot get them in the product.

Suggested fix: Add Settings sections: API keys (list with prefix, label and last used; create with a copy-once dialog; revoke with confirm), Profile (from /auth/me, with sign out), and About (version from /health, public URL, timezone).

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/settings/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/auth.go

---
### Settings page is an empty placeholder: no UI for API keys, account or instance info [find:ui-ops-surfaces] (high/unfinished) · area: settings · confidence: high

The API key endpoints exist (GET/POST /api-keys, DELETE /api-keys/{id}), but Settings only says "No settings are available in this shell yet." There is also no profile, sign-out or version page.

Evidence: routes/(dashboard)/settings/+page.svelte is 11 lines of placeholder text (18-settings.png). GET /api/v1/api-keys returns 200 {"items":[]} on :8090, and on :18099 with a session. There is no caller in web/src outside the generated client.

n8n behavior: Settings has an n8n API page (create a key with label/expiry, copy once, delete), plus Personal, Users and other sections.

Impact: On auth-on deployments, getting an API key for the SDK, a host backend or CI needs curl plus a session cookie. Keys cannot be listed, audited or revoked in the product.

Suggested fix: Add Settings sections: API keys (list with prefix/created/last used; create with name/expiry and a show-once secret; revoke with confirmation), Account (auth/me, sign out), and About (version from /health) with read-only instance config (timezone, retention, embed origins).

Files: web/src/routes/(dashboard)/settings/+page.svelte

Existing tickets: FEAT-ddzk2k

---
### Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; Settings is an empty placeholder [find:unfinished-work] (high/unfinished) · area: web / auth · confidence: high

The server implements sessions, API keys and tenancy behind auth.enabled, and the docs tell operators to enable it. The SPA never calls any auth endpoint and has no login route. Once identity is on, every dashboard API call gets 401 and the user has no way to sign in.

Evidence: The API has POST /api/v1/auth/login|logout, GET /api/v1/auth/me and /api/v1/api-keys, and config.example.yaml:130-160 describes 'SessionTTL is how long a dashboard login lasts'. A grep of web/src (excluding generated/) finds no reference to auth/login, auth/me or api-keys. web/src/lib/api/http.ts has no 401 branch. There is no login route. web/src/routes/(dashboard)/settings/+page.svelte is 11 lines: 'No settings are available in this shell yet.' FEAT-ddzk2k Work evidence (line 114): 'The SPA login route and the SDK's apiKey convenience are not in this change'. No follow-up ticket exists; the same note says account lockout 'is worth its own ticket', and none exists. docs/operate/security.md tells operators to turn identity on. Not tested live: no private port was available to start an auth-enabled instance.

n8n behavior: Sign-in page, user management, and personal API keys under Settings.

Impact: The recommended production posture (auth on) locks operators out of the editor. API keys can only be managed with curl.

Suggested fix: Add a /login route, redirect to it on 401, add sign-out, and add Settings pages for API keys (create/revoke, show once) and the account. Open a ticket for login rate-limiting and lockout.

Files: web/src/lib/api/http.ts, web/src/routes/(dashboard)/settings/+page.svelte, web/src/routes/+layout.svelte, internal/api/middleware/auth.go

Existing tickets: FEAT-ddzk2k

---
### API features with no UI: delete workflow, cancel execution, API keys, credential test; Settings is a placeholder [find:web-frontend-code] (medium/unfinished) · area: dashboard · confidence: high

The OpenAPI exposes DELETE /workflows/{id}, POST /executions/{id}/cancel, /api-keys CRUD and credential test endpoints. The generated clients deleteWorkflow, cancelExecution, listApiKeys, createApiKey, revokeApiKey and testCredential have 0 call sites in web/src. The Settings page reads 'No settings are available in this shell yet'. The workflow list has no delete, duplicate, rename, search or sort, over an unpaginated list (636 rows on the shared instance).

Evidence: grep of generated operation names outside /generated/ gives 0 for each. routes/(dashboard)/settings/+page.svelte (placeholder). routes/(dashboard)/app/workflows/+page.svelte (list with only an Activate toggle). routes/(dashboard)/executions/[id]/+page.svelte has no Stop control for running or waiting executions.

n8n behavior: n8n offers workflow delete/archive and duplicate, a Stop execution button, API key management in Settings, and credential Test.

Impact: Users cannot remove workflows, stop runaway or waiting executions, or issue API keys without calling the API by hand.

Suggested fix: Wire the existing endpoints: row menu (Delete / Duplicate / Rename), Stop button on running executions, Settings > API keys, and credential Test.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/executions/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/settings/+page.svelte

## Acceptance criteria

- [ ] Auth-enabled deployments have no sign-in page: the dashboard renders and then every call fails with 401
- [ ] Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; S
- [ ] The SPA has no sign-in route and no 401 handling, so the dashboard is unusable when auth is enabled
- [ ] Settings is an empty placeholder, while API keys and 'who am I' have a complete API but no UI
- [ ] Settings page is an empty placeholder: no UI for API keys, account or instance info
- [ ] Enabling authentication makes the dashboard unusable: no login page, no 401 handling, no API-key or user UI; S
- [ ] API features with no UI: delete workflow, cancel execution, API keys, credential test; Settings is a placehold
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)