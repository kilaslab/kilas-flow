---
id: BUG-jx94q9
title: 'n8n export then import is not a fixed point: settings.executionOrder v1 comes back as a dropped issue'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T12:25:15Z"
---

# Description

A KilasFlow → n8n export writes `settings.executionOrder: "v1"` (BUG-6gkd12), but the importer then reports that key back as a **dropped** `settings` issue. So export → import is not a fixed point: a workflow round-tripped through n8n's format gains an import issue it did not have, and the skills' round-trip test has to allow exactly that one entry.

Reported by the Track H2 coder in the 2026-10-09 parallel wave.

# Steps to Reproduce

1. Export any KilasFlow workflow to n8n JSON.
2. Import that JSON.

# Expected

`executionOrder: "v1"` imports silently (it is the order KilasFlow runs in). Only `"v0"` (legacy order), or a value KilasFlow cannot honour, is reported.

# Actual

A `dropped` issue for `settings.executionOrder`.

# Acceptance Criteria
- [ ] `executionOrder: "v1"` produces no import issue; `"v0"` (or anything else) is reported as lossy with a reason saying KilasFlow runs v1 order.
- [ ] Export → import of a workflow reports no settings issue; the skills round-trip test no longer needs its allowance.
- [ ] `TestAnImportedWorkflowKeepsItsTimezone` and the round-trip fidelity tests are updated accordingly.
