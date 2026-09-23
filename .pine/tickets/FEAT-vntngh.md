---
id: FEAT-vntngh
title: Google Sheets node, trigger and tool (the most-used n8n app node)
status: todo
priority: critical
labels:
    - n8n
    - parity
    - node-catalog
    - google
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

Google Sheets appears in 33.5% of the 998 sampled n8n templates (45.7% of the newest). It is the #1 app node, and after Code it unlocks the most templates. KilasFlow has no Sheets node, and because HTTP Request cannot attach an OAuth2 credential, there is no workaround either.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n-nodes-base.googleSheets` appears in 334/998 templates (33.5%; 45.7% of the newest), #6 overall and #1 among app nodes. The Sheets family (node, Trigger, Tool) together appears in 35.0% of templates (48.1% of the newest). It is #1 on n8n.io/integrations and #7 on n8n Pulse.

# Steps to Reproduce

1. Import Manual → Google Sheets v4.7. I used a real instance from template 19200 (`operation: appendOrUpdate`, `documentId`/`sheetName` locators, `columns` resourceMapper).
2. Run it.
3. Repeat with `googleSheetsTrigger` and `googleSheetsTool`.

# Expected

Sheets read / append / appendOrUpdate / update / delete / clear, plus the rowAdded/rowUpdated trigger and the tool variant, import and run.

# Actual

- Import issue: blocking "KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.googleSheets\". It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced." The parameters survive only inside the `original` capsule.
- The run fails with 422: "this node was imported from n8n-nodes-base.googleSheets, which KilasFlow does not support."
- There is also no workaround. `GET /api/v1/credential-types` has no Sheets or generic OAuth2 type, and the HTTP Request node cannot attach an OAuth2 credential (see NG-19).

# Acceptance Criteria
- [ ] `n8n-nodes-base.googleSheets` imports onto a native `kilasflow.googleSheets` that implements read/append/appendOrUpdate/update/delete/clear, using n8n's operation and value strings and the `columns` resourceMapper
- [ ] `googleSheetsTrigger` (rowAdded/rowUpdated) runs on the leased poll framework, and `googleSheetsTool` is usable by the AI Agent
- [ ] Uses the existing Google OAuth2 credential flow from Drive/Gmail
- [ ] Template 19200 imports with no blocking issue and runs its Sheets step against a stub or real sheet

# Implementation Plan

Build on the Google OAuth2 (FEAT-mc6s65) and leased poll trigger (FEAT-wcmk3r) that Drive and Gmail already use. Ship `kilasflow.googleSheets` with n8n's operation and value strings, the resourceMapper `columns`, a Sheets trigger and a tool variant, and add the three importer mappings.

# Notes

Related tickets: FEAT-gcq50s, FEAT-mc6s65, FEAT-wcmk3r

Related (from the audit): FEAT-gcq50s explicitly marks Google Sheets "out of scope". No open ticket covers it.

# Related Files

- `probes/Google_Sheets.json`
- `results-extra.json` (Google Sheets Trigger)
- `results-ai.json` (Google Sheets Tool)
- `greedy-unlock.json`: Sheets is the #2 unlock (+85 templates).
- `internal/interop/n8n/n8n.go:803-816`

# Attachments
