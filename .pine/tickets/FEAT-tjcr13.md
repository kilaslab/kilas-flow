---
id: FEAT-tjcr13
title: 'Human validation errors across dashboard: field labels not keys, no ''422 —'', inline + cleared on edit, 404 back action'
status: todo
priority: medium
labels:
    - ux
    - errors
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Users see messages like `422 — credential field "name" is required` (the header name, not the Name field), regexes, and stale alerts.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-14). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Inline, field-level messages in plain language ("Column name must start with a letter…").

# Steps to Reproduce

1. New credential → HTTP Header Auth, fill Name only, click Save. 2. Datastore → Add column "full name", then "email" twice. 3. New schedule → cron "every day at 9". 4. Open /app/workflows/wf_doesnotexist.

# Expected

Labels instead of keys, no status codes, rules shown before submit, errors attached to the offending field and cleared on edit, and 404s with a "Back to workflows" action.

# Actual

(1) "422 — credential field "name" is required". "name" is the Header name key, so users who did fill the Name field are confused. The alert then stays after the fields are fixed and a later Test runs. (2) `422 — datastore: invalid column name "full name", want 1-63 bytes matching ^[a-zA-Z][a-zA-Z0-9_]*$`, and for an exact duplicate `column name "email" collides with "email" differing only in case`. (3) `422 — invalid cron expression: expected 5 to 6 fields, found 4: [every day at 9]`, while the hint says "Five fields". (4) "Workflow editor could not be loaded / 404 — workflow not found / Try again", with no link back, and retrying can't help.

# Acceptance Criteria
- [ ] problem+json `errors[].location` maps to form fields on the client, and the server names field labels
- [ ] The "NNN —" prefix is stripped, and errors clear when the field is edited
- [ ] Rules are shown before submit (column name rules, cron hint consistent with the parser)
- [ ] 404 pages offer a "Back to …" action instead of "Try again"

# Implementation Plan

Map problem+json `errors[].location` to fields on the client, and have the server name the field label. Strip the "NNN —" prefix in message(), clear formError on input, and give 404s a back action.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/11-cred-header-empty-save.png, agents/ux-ops/23-col-err-fullname.png, agents/ux-ops/23-col-err-email.png, agents/ux-ops/37-schedule-errors.png, agents/ux-ops/53-workflow-404.png; web/src/lib/api/http.ts `message()`.

# Attachments
