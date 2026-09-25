---
id: FEAT-g33qf6
title: Per-tenant rate limiting for webhook ingress, the run API and host events
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - security
parent: EPIC-7c3ry9
created: "2026-09-25T10:08:12Z"
updated: "2026-09-25T10:08:12Z"
---

# Description

Only sign-in is throttled: `LoginLimiter` (internal/api/middleware/loginlimit.go) is an in-memory token bucket per key, held in one process. Nothing limits inbound webhooks, the run API or (once FEAT-mccadj lands) host events per tenant, so one tenant can flood the shared queue. FEAT-rdfjh1's `webhookRatePerMinute` assumes a limiter that does not exist.

An in-memory bucket is also wrong once several server processes sit behind a load balancer, because each process allows the full rate.

Found by the 2026-09-25 audit of this epic's tickets against the code.

# Acceptance Criteria
- [ ] A reusable per-(tenant, bucket) rate limiter, generalised from `LoginLimiter`, with a pluggable store: in-memory for a single process and database-backed (Postgres) for multi-process deployments.
- [ ] Applied to webhook ingress, `POST /workflows/{id}/run` and `POST /api/v1/events`, with deployment defaults in config and per-tenant overrides read from tenant settings.
- [ ] A refused request answers `429` with `Retry-After` and a named problem code before any execution or delivery claim exists.
- [ ] Limiter state is bounded, like `maxTrackedLoginKeys`.
- [ ] Tests cover the refusal, `Retry-After`, the override and two limiter instances sharing the database store.

# Implementation Plan

# Notes

FEAT-rdfjh1 depends on this. Per-tenant overrides come from the tenant settings ticket, but a deployment-wide default can ship first.

# Related Files

internal/api/middleware/loginlimit.go (existing token bucket to generalise)
internal/api/middleware/auth.go (request pipeline)
internal/webhook/webhook.go (ingress)

# Attachments
