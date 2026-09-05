---
id: FEAT-6nh0wm
title: Implement webhook response and schedule trigger vertical slices
status: done
priority: high
labels:
    - webhook
    - scheduler
    - trigger
deps:
    - FEAT-pn3dtq
parent: EPIC-c7gbdp
phase: p2
created: "2026-08-29T15:41:20Z"
updated: "2026-09-05T02:05:00Z"
---

## Scope

Route activated workflows from inbound webhooks and schedules into the existing engine. Implement Respond to Webhook and a single-instance scheduler without bypassing workflow lifecycle, credential, validation, or execution-record rules.

## Acceptance criteria

- An active workflow can bind a stable webhook ID/path to a configured trigger node; inactive/unknown workflows do not execute and return a documented response.
- Webhook requests enforce payload and request timeout limits, authenticate through the approved V1 modes, redact inbound credentials before persistence, and create a normal execution record.
- Respond to Webhook can deterministically produce the workflow HTTP response; error/timeout/no-response behaviour is explicitly documented and tested.
- Scheduler persists cron schedule, target workflow, active status, last run, and next run; an active schedule starts the same execution path once per due time in the SQLite single-instance model.
- End-to-end coverage proves Webhook → HTTP → Respond to Webhook, including unauthorized and timeout/error cases; scheduler tests use a controllable clock or equivalent deterministic mechanism.

## References

- PRD: §§24 Triggers/Core, 35–37, 49, 53, 57; Milestone 2.
- Design reference: `21-webhook-auth-options.png`, `24-webhook-configured-basic-auth.png`, `25-canvas-secured-rest-endpoint.png`, `30-logs-details-tab.png`.

## Relevant documentation

- Use `find-docs` before selecting/configuring a scheduler or auth library; record the official documentation and the clock/test strategy.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `playwright-cli` for browser verification of webhook configuration UI if implemented in the same ticket.

## Implementation Plan

- `webhook_bindings` maps `(method, path)` to an active workflow's trigger node. Bindings are written inside the same transaction that activates a workflow and dropped inside the ones that deactivate or delete it, so there is never a window where a workflow is active but unroutable, or routable but inactive. The unique index on `(method, path)` makes an ambiguous route an activation failure rather than a coin flip at request time.
- Webhook and schedule runs go through `QueueTriggered`, which pins the *active* revision and re-checks activation inside the transaction. A trigger therefore cannot bypass lifecycle, validation, credential, or execution-record rules, and cannot silently start running unsaved editor work.
- The scheduler uses `robfig/cron/v3` restricted to five UTC fields, with a `Clock` seam so tests advance time instead of sleeping. `ClaimDue` reads, checks, and advances under one row lock, and computes the next run from the *due* time so a late tick cannot make a schedule drift.

## Work Evidence

- Live end-to-end, against a running binary: `POST /webhook/orders` returns 404 before activation, 401 unauthenticated, and after `Basic ada:hunter2` returns `201` with header `X-Kilas: ok` and body `{"order":"A-1","received":"yes"}` — Webhook → Set → Respond to Webhook, with the response body built from `{{ $json.body.id }}` (trigger payload) and `{{ $json.received }}` (Set output).
- A defect found and fixed during that verification: Respond to Webhook read its parameters raw and returned the unevaluated expression marker. It now resolves per item through the same shared expression context every other executor uses.
- Response modes are all covered: immediate returns the configured status with the execution ID; `responseNode` returns the node's status, headers, and body; a graph configured for `responseNode` that never reaches one answers 500 naming the missing node rather than a misleading 200; and a run that does not finish answers 504 bounded by the response timeout.
- Authentication: basic and header modes verified with constant-time comparison; a webhook configured to authenticate but missing its credential fails closed with 500, because failing open would silently publish an unprotected endpoint.
- Inbound redaction: a request carrying `Authorization`, `Cookie`, and a `token` body field produced an execution record containing none of them.
- Limits: an oversized body is rejected with 413 before anything is queued.
- Scheduler: `TestTickFiresADueScheduleExactlyOncePerDueTime` proves one execution per due time using a controllable clock, and `TestTickAdvancesFromTheDueTimeSoASlowTickDoesNotDrift` proves a 40-minute-late tick still schedules the top of the next hour. Live, an every-minute schedule produced exactly one succeeded execution with `lastRunAt 02:01:00` and `nextRunAt 02:02:00`.
- A schedule whose workflow is deactivated is itself deactivated rather than retried forever; an invalid cron expression is rejected with 422.
- A parameter that declares a default is no longer also "must be present": the default is the server's answer for an absent value, so a hand-authored or imported document is not rejected over a field the server already knows how to fill in.
- `go test ./... -race`, `go vet ./...`, `pnpm test` (45 passing), `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
