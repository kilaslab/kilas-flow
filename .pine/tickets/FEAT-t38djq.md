---
id: FEAT-t38djq
title: Data-utility nodes (Rename Keys, Compare Datasets, Redis) and a 'deliberately unsupported' reason table
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

The server-local nodes (Execute Command, Read/Write Files, Local File Trigger) are probably deliberately absent from a multi-tenant engine, but the diagnostic does not say so.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-22). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** - Read/Write Files appears in 12 templates, Execute Command in 7, Local File Trigger in 3, FTP in 4 and SSH in 3. These server-local nodes appear in 3.9% of templates overall, mostly the most-viewed.
- Redis appears in 9 templates, n8n API node in 7, Compare Datasets in 5, MongoDB in 4 and Execution Data in 3. These data-utility nodes appear in 3.0%.
- n8n Pulse ranks Redis #36 and Execute Command #45.

# Steps to Reproduce

Import Manual → X for each node, using real instances: executeCommand 2334, readWriteFile 2339, localFileTrigger 2335, redis 19669 (`operation`, `key`, `value`, `ttl`), compareDatasets 1964, executionData 2772, plus a docs-derived renameKeys node.

# Expected

Either a tenant-scoped implementation, or a placeholder that states the node is intentionally unavailable and names the alternative. Redis, Rename Keys and Compare Datasets are ordinary gaps.

# Actual

- Every one is a generic blocking placeholder.
- For the server-local nodes, the omission is probably deliberate: shell and disk access are unsafe in a multi-tenant embedded engine. But the diagnostic does not say so or point to Drive, HTTP or the Datastore instead.
- Rename Keys and Compare Datasets are pure transforms that fit alongside Set and Merge.

# Acceptance Criteria
- [ ] Rename Keys and Compare Datasets (4 outputs) import and run
- [ ] Redis, behind a `redis` credential with the same host guard as SQL
- [ ] Placeholders for deliberately unsupported nodes say why, and point to the alternative (Drive, HTTP, Datastore)

# Implementation Plan

Add a "deliberately unsupported" reason table to `placeholderFor`. Implement Rename Keys (a Set subset), Compare Datasets (a Merge variant with 4 outputs) and Redis (behind a `redis` credential with the same host guard as SQL).

# Notes

Related (from the audit): none

# Related Files

`results-extra.json` (Execute Command, Read-Write Files from Disk, Local File Trigger, Rename Keys, Compare Datasets, Execution Data), `probes/Redis.json`

# Attachments
