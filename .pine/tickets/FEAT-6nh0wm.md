---
id: FEAT-6nh0wm
title: Implement webhook response and schedule trigger vertical slices
status: todo
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
updated: "2026-08-29T15:41:20Z"
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
