---
id: FEAT-1ge0xc
title: Filter conditions on triggers
status: done
priority: medium
labels:
    - saas
    - triggers
deps:
    - FEAT-mccadj
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T08:51:26Z"
---

# Description

Workflows on high-volume events, such as every inbound message, need filters by channel, DM or group, and keywords. Today the filter must run inside the workflow, so every event creates an execution.

# Acceptance Criteria
- [x] Webhook, pack and host-event triggers accept a `filter` (conditions property) evaluated on the trigger item before queueing.
- [x] A non-match answers `filtered` and creates no execution.
- [x] Filtered counts appear in metrics.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Partly done (infrastructure). A pre-queue filter hook exists: `webhook.Kind.Accept` (internal/webhook/shape.go:215-225); a reject answers `200 {"filtered":true,"reason"}` with no execution (webhook.go:198-209). Only Telegram uses it (nodes/telegram.go:391). Plug a user `filter` into this hook, likely backed by internal/conditions.
- The "filtered counts in metrics" AC assumes a metrics subsystem that does not exist (no prometheus/expvar/otel); filtering only logs at Info today. That criterion waits on FEAT-2npfgy; the filter itself does not (no dep set).

# Related Files

# Attachments

## Progress

2026-11-27: landed on `feat/epic-sweep-2026-11-27`.

- The parameter key is `triggerFilter` (label "Only run when"), not `filter`: pack triggers declare their own parameters and a service's own "filter" setting would collide. The key is reserved on pack triggers (a pack declaring it fails to load).
- `internal/triggerfilter`: resolves the filter's expressions against the trigger item (`$json`) and evaluates it with `internal/conditions` (the IF/Filter/Switch evaluator). The rich-shape reader moved to `conditions.Parse`; `nodes.richFilter` delegates to it. A filter that cannot be evaluated filters the event out, with the error as the reason.
- Webhook: checked after the trigger kind's own `Accept`, on the shaped item. A miss answers `200 {"filtered": true, "reason"}`, queues nothing and counts `metrics.WebhookFiltered` (the metrics subsystem exists now, so this AC no longer waits on FEAT-2npfgy).
- Host events: the filter is stored on `host_event_subscriptions.trigger_filter` at activation (migration 000032, both dialects) and checked per target in `hostevents.Service.Publish`. A workflow with several listening triggers runs from the first that admits the event; when none does it is reported once under `filtered` with `reason: "filter"` and a `detail`. A filtered workflow is not claimed for the event id.
- Docs: concepts/webhooks.md, "Trigger filters" plus the host-events paragraph.
- Tests: internal/triggerfilter, internal/webhook (filtered answer, no execution, metric), internal/hostevents (per-target filter, multi-trigger fallthrough, extractor), internal/repository (filter round trip on both drivers), internal/nodepack (property present, reserved key).

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (3):
  - `8d0b2f50` — FEAT-1ge0xc: filter conditions on webhook, host-event and pack triggers
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1646 files changed, 275634 insertions(+), 7906 deletions(-)
```
