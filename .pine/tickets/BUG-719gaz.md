---
id: BUG-719gaz
title: 'Auth-off consistency: API-key copy contradicts behaviour, /login claims auth required, 401 on /auth/me every page'
status: done
priority: low
labels:
    - auth
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-10-08T16:12:00Z"
---

# Description

With auth off, which is the default, the UI contradicts itself, and every page logs a failed request.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, ux-ops; finding ids: OPS-20, LEAD-9). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-20: With auth off, Settings says API keys "cannot be issued" but issues one anyway; /login claims auth is required; every page logs a 401

*ux · low · settings*

**n8n:** The API settings page is hidden or disabled with an explanation when the public API is off.

**Steps to reproduce:**

1. With KILASFLOW_AUTH_ENABLED unset, open /settings. 2. Click "New API key", label "[ux-ops] ci", then Create. 3. Open /login.

**Actual:**

The empty state reads "API keys need authentication — This instance has auth off, so keys cannot be issued." Yet "New API key" is enabled, POST /api/v1/api-keys returns 201, and the secret `kfa1_…` is shown and listed. /login says "This instance requires authentication. Sign in with the operator account…". Every dashboard page logs `Failed to load resource: 401 @ /api/v1/auth/me` because the SPA infers "auth off" from a 401; the API has no capability flag for it.

**Expected:**

Consistent copy: either disable key creation, or say "keys will be enforced once auth is enabled". /login redirects to the app when auth is off. Expose `authEnabled` (for example on /health or a /config endpoint) instead of probing /auth/me.

**Suggested fix:**

Add `auth: {enabled}` to GET /api/v1/health or a new /api/v1/instance, then gate the Settings and login UI on it.

**Evidence:**

agents/ux-ops/40-settings.png, agents/ux-ops/41-apikey-authoff.png, agents/ux-ops/42-apikey-after.png, agents/ux-ops/50-login-authoff.png.

**Related:**

none


## LEAD-9: Console error on every editor load when auth is off: GET /api/v1/auth/me → 401

*ux · low · editor*

**Steps to reproduce:**

Auth disabled (the default); open any /app page; check the console.

**Actual:**

`Failed to load resource: 401 (Unauthorized) @ /api/v1/auth/me`

**Expected:**

No failing request in the default configuration. Either /auth/me returns the anonymous operator principal, or the SPA reads an auth-enabled flag first.

**Suggested fix:**

Return 200 with {authenticated:false, mode:"disabled"}, or skip the call when /api/v1/auth/config says auth is off.


# Acceptance Criteria
- [x] The API exposes `authEnabled` (on /health or a new /api/v1/instance), and the SPA stops probing /auth/me
- [x] /login redirects into the app when auth is off
- [x] The Settings API-key copy matches behaviour (keys are enforced once auth is enabled)
- [x] No console errors on dashboard pages in the default configuration

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Fix

**Root cause.** The API had no way to say whether authentication was on. The dashboard layout and the Settings page called `GET /api/v1/auth/me` on every load and read the 401 as "auth off", so a default install logged a failed request on every page. Settings derived `authOff` from that 401 (or from a failed key list) and claimed keys "cannot be issued", while `POST /api-keys` happily minted them. `/login` never checked the mode at all.

**Change.**
- `internal/api/handlers/system.go`, `internal/api/routes.go`: `GET /api/v1/health` (already public in both modes) now carries `authEnabled`, set from `Config.Auth.Enabled` through `System.WithAuthEnabled`. The web client (`healthOutputBody.ts`) and the SDK types (`sdk/src/generated/models.ts`) are regenerated; `make generate-api-check`, `generate-types-check` and `generate-api-reference-check` are clean.
- New `web/src/lib/dashboard/auth-mode.ts`. `createAuthMode` reads the mode once per page load from the health probe, and `principal()` only calls `/auth/me` when auth is on. An unreadable probe answers "on" and is retried next time. `safeNext` is the shared in-app redirect guard.
- `(dashboard)/+layout.svelte` uses `authMode.principal()` instead of `getMe()`.
- `settings/+page.svelte`:
  - The identity query is `enabled` only when health says auth is on.
  - `authOff` comes from `health.authEnabled === false`.
  - The API keys card shows a note: "Authentication is off, so the API accepts calls without a key. Keys you create now are enforced once authentication is enabled."
  - The empty state says the same. New API key stays enabled, which matches the server.
  - A failing `/auth/me` with auth on now reads "Your account could not be loaded." instead of claiming auth is off.
- `login/+page.svelte`: the form renders only once the probe says auth is on. With auth off the page `goto`s `next` (or `/app/workflows`) with `replaceState`.
- Copy (en + id): `settings_account_auth_off` no longer says "looks off". `settings_keys_need_auth*` were replaced by `settings_keys_auth_off`, `settings_keys_auth_off_empty_body` and `settings_account_unavailable`.

**Tests.** First written to fail, then passing:
- Go: `TestHealthSaysWhetherAuthenticationIsOn` in `internal/api/auth_test.go` (false on a default API, true on the auth-on harness).
- vitest: `web/src/lib/dashboard/auth-mode.test.ts` checks that the probe runs once, that `/auth/me` is never called with auth off and is called with auth on, that a refused `/auth/me` gives null, that a failed probe answers on and retries, and the `safeNext` cases.
- Suites run: `pnpm check` (0 errors), `pnpm vitest run` (60 files / 739 tests), `go test ./internal/api/... ./internal/cli/...`.

**Playwright** (real binary on :18092, auth off by default):
- `/api/v1/health` returns `"authEnabled":false`.
- `/app/workflows`, `/datastores`, a data table, `/executions`, `/schedules`, `/credentials`, `/settings` and a workflow editor page logged 0 console errors, and the server log shows 0 requests to `/auth/me`.
- Settings shows the auth-off note. Creating "[ux-ops] ci" mints a `kfa1_…` key, which is listed under that note.
- `/login?next=/datastores` lands on `/datastores`, and `/login` lands on `/app/workflows`.
- Auth-on check on :18093 (`KILASFLOW_AUTH_ENABLED=true`): `/app/workflows` still redirects to `/login?next=…` and the sign-in form renders.

# Related Files

# Attachments
