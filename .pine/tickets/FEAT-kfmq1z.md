---
id: FEAT-kfmq1z
title: RSS Read and RSS Feed Trigger
status: todo
priority: low
labels:
    - n8n
    - parity
    - node-catalog
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

RSS appears in 2% of templates (unlock #18).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-21). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** RSS Read appears in 15 templates and RSS Feed Trigger in 3, which is 2.0% of templates. n8n Pulse ranks RSS Read #35. RSS is unlock #18.

# Steps to Reproduce

Import Manual → RSS Read (template 19408: `url`, `options`). Import RSS Feed Trigger (template 4506: `feedUrl`, `pollTimes`) → No Op.

# Expected

RSS/Atom read and a feed trigger on the leased poll framework, with GUID watermarking, import and run.

# Actual

Both are blocking placeholders.

# Acceptance Criteria
- [ ] RSS/Atom read
- [ ] A feed trigger on the leased poll framework, with a GUID watermark

# Implementation Plan

Use a small pure-Go feed parser, with the trigger on FEAT-wcmk3r's poll framework.

# Notes

Related tickets: FEAT-nqpvf6, FEAT-wcmk3r

Related (from the audit): FEAT-nqpvf6 (done; names RSS only as a would-be poll consumer)

# Related Files

`results-extra.json` (RSS Read, RSS Feed Trigger)

# Attachments
