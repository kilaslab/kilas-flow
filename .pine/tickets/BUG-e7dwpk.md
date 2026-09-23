---
id: BUG-e7dwpk
title: Datastore names can collide (incl. case-only); 'By Name' silently acts on the wrong table
status: todo
priority: high
labels:
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

A Data table node set to By Name `leads` read the 46 rows of `Leads`. With Update, Delete or Clear, the write would hit the wrong table.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-3). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Data table names are unique within a project, so a name identifies exactly one table.

# Steps to Reproduce

1. Create datastore "[ux-ops] Leads" and import 45 rows. 2. Create "[ux-ops] leads", which is accepted, and a second "[ux-ops] <img src=x onerror=alert(1)> 📊", which is also accepted. 3. Build Manual → Data table (Row: Get) with Data table = By Name "[ux-ops] leads" and run it (wf_01a0cbdf-16e0-…).

# Expected

Refuse duplicate names (case-insensitive, since lookup is case-insensitive) on create and rename, or make By-Name fail when the name is ambiguous.

# Actual

No uniqueness check exists, not even for exact duplicates. The run succeeded and returned the 46 rows of "[ux-ops] Leads", not the empty "[ux-ops] leads". With Update, Delete or Clear, that write would hit the wrong table. The "From list" picker also shows identical labels for exact duplicates.

# Acceptance Criteria
- [ ] A unique index on (tenant_id, lower(name)), with a friendly 409 in the create and rename dialogs
- [ ] By-Name lookup errors when more than one table matches, and never picks the first
- [ ] The "From list" picker disambiguates any existing duplicates

# Implementation Plan

Add a unique index on (tenant_id, lower(name)) with a friendly 409 in the create and rename dialogs. In datastoreID, error when more than one table matches.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/21-datastore-dup.png; execution exec_01a0cbdf-16fb-7d7d-ab54-8f4130fe629e; nodes/datastore.go:783-802 (the first `strings.EqualFold` match wins).

# Attachments
