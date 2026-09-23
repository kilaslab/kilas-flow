---
id: BUG-719gaz
title: 'Auth-off consistency: API-key copy contradicts behaviour, /login claims auth required, 401 on /auth/me every page'
status: todo
priority: low
labels:
    - auth
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
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
- [ ] The API exposes `authEnabled` (on /health or a new /api/v1/instance), and the SPA stops probing /auth/me
- [ ] /login redirects into the app when auth is off
- [ ] The Settings API-key copy matches behaviour (keys are enforced once auth is enabled)
- [ ] No console errors on dashboard pages in the default configuration

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
