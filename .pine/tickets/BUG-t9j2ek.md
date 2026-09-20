---
id: BUG-t9j2ek
title: Inbound webhooks default to no authentication, and legacy empty-route bindings match by a non-unique path
status: doing
priority: high
labels:
    - security
    - api
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T07:42:30Z"
---

## Problem

Two independent issues on the public webhook surface: a deployment cannot require inbound
authentication, and a legacy binding can be reached by a label that is not unique across
tenants.

## Evidence

- `internal/webhook/webhook.go:565-566`: `case "", "none": return 0, nil` — a binding whose
  `authentication` parameter is unset (the default) admits any caller. `basicAuth` /
  `headerAuth` / `jwtAuth` are opt-in, authored by the workflow owner, and there is no
  deployment-level switch that refuses unauthenticated deliveries.
- `internal/repository/webhooks.go:81` (`Resolve`) matches
  `method = ? AND (route = ? OR (route = '' AND path = ?))` and `:108` (`ResolveRoute`)
  matches `route = ? OR (route = '' AND path = ?)`, both **without a tenant predicate**.
- The `path` column is deliberately not unique: migration
  `000011_webhook_path_label_index` dropped `uidx_webhook_bindings_label (tenant_id, path)`
  and replaced it with a non-unique index, so two tenants may hold the same label.
- Rows minted today always carry an opaque route (`mintWebhookRoute`), so the fallback is
  reachable only on a database migrated from the pre-route schema — but there is no backfill
  migration, and the downstream execution runs with the matched row's `TenantID`, i.e. the
  other tenant's credentials and datastores.
- `/webhook/*` is mounted outside the `/api/v1` auth gate (`internal/api/routes.go`), so
  this surface is the only unauthenticated write path besides `/resume/{token}`.

## Acceptance criteria

- [ ] A deployment setting (e.g. `webhook.require_auth`) refuses a delivery to a binding whose
      authentication mode is `none`, with a diagnostic naming the workflow and the fix, and
      the boot log states the posture.
- [ ] Empty-route bindings are backfilled with a minted route (migration, both dialects), or
      the path fallback is removed once no such row can exist — with a test proving a
      delivery cannot reach another tenant's workflow by guessing a path label.
- [ ] `Resolve`/`ResolveRoute` either take a tenant or are documented as the one deliberate
      unscoped lookup with a uniqueness argument that holds (a globally unique route).
- [ ] A test covers: same path label in two tenants, delivery addressed by label, exactly one
      tenant's workflow runs (its own).

## Out of scope

Per-tenant webhook domains or rate limiting.
