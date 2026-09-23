---
id: BUG-namghh
title: 'Low-contrast text: root links 1.48:1, datastore type labels and ''Failed'' badge under WCAG AA'
status: todo
priority: low
labels:
    - accessibility
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Most pages pass the contrast check. A few tokens don't.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-19). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a

# Steps to Reproduce

1. Run agents/ux-ops/contrast.js (WCAG ratio for every visible text node) on /, /app/workflows, /credentials, /datastores/<id>, /settings, /executions and /schedules in dark theme, and again with data-theme=light.

# Expected

≥4.5:1 for body text.

# Actual

The "/" links (API reference, OpenAPI JSON/YAML) score 1.48:1 in dark and 1.22:1 in light, because they use `text-(--color-accent)`, and --accent is the hover-surface token oklch(0.31 0.055 170). Datastore header "· string/number/boolean/datetime" scores 4.43:1 (dark) and 3.27:1 (light) at 12 px. The executions "Failed" badge scores 4.36:1 at 12 px. Every other page passes, and focus rings are visible on every tab stop.

# Acceptance Criteria
- [ ] All body text reaches at least 4.5:1 in both dark and light themes (the audit's contrast.js scan is clean)

# Implementation Plan

Use text-primary for links, drop the /70 opacity on the datastore type label, and darken the destructive badge text a step.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/contrast.json, agents/ux-ops/01-root.png; web/src/routes/+page.svelte:154-156; web/src/app.css:42,108.

# Attachments
