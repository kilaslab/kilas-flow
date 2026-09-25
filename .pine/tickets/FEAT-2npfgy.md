---
id: FEAT-2npfgy
title: 'Metrics endpoint: execution, queue, webhook and per-tenant series'
status: todo
priority: medium
labels:
    - saas
    - observability
    - operate
parent: EPIC-7c3ry9
created: "2026-09-25T10:08:12Z"
updated: "2026-09-25T10:08:12Z"
---

# Description

KilasFlow exposes no metrics: no Prometheus endpoint, no expvar and no OpenTelemetry instrumentation (otel appears in go.mod only as an indirect dependency). Operators can alert on logs alone, and FEAT-53pa9a is still needed for failures to show up in the log at all.

Two tickets in this epic have acceptance criteria that assume metrics: FEAT-1ge0xc ("filtered counts appear in metrics") and FEAT-rdfjh1 ("per-tenant metrics are exposed"). A SaaS operating many tenants needs queue depth, claim latency and per-tenant throughput to size the worker pool and spot a noisy tenant.

Found by the 2026-09-25 audit of this epic's tickets against the code.

# Acceptance Criteria
- [ ] An opt-in `/metrics` endpoint in Prometheus text format, on a separate listener or protected by config, never on the public API unauthenticated.
- [ ] Core series:
  - executions started, finished and failed, by trigger and status;
  - queue depth and claim latency;
  - running executions against `execution.max_concurrent`;
  - webhook requests accepted, filtered and refused;
  - node run duration by node type.
- [ ] A `tenant` label that can be switched off, or bounded to the top-N tenants, to keep cardinality under control.
- [ ] A small internal metrics interface, so engine, webhook and datastore code does not import the client library directly.
- [ ] The operate docs list every series and give an example alert.

# Implementation Plan

# Notes

FEAT-rdfjh1 depends on this. FEAT-1ge0xc does not: its filter work can ship first, and only its metrics criterion waits for this ticket.

# Related Files

go.mod (otel only as indirect)
internal/engine (terminal paths), internal/webhook/webhook.go (filtered and accepted), internal/repository/executions.go (ClaimNext)

# Attachments
