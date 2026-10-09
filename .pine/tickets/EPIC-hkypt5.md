---
id: EPIC-hkypt5
title: 'n8n RAG templates: Drive, Gmail, PGVector'
status: done
priority: high
labels:
    - n8n
    - rag
    - v2
parent: EPIC-m42s3g
created: "2026-09-21T12:03:36Z"
updated: "2026-10-09T11:01:23Z"
---

# n8n RAG templates: Drive, Gmail, PGVector

Ship import-and-run for RAG graphs like n8n templates 16706, 3647, 4799, 5908.

First slice (this epic's children except FEAT-gcq50s): cluster RAG, extractFromFile, customer PGVector, Google OAuth popup, Drive search/download/move, leased poll, Gmail Trigger/Get, hand-authored fixtures. Low-parity remainder stays on FEAT-gcq50s.

Licence: reimplement wire format only. Do not copy n8n source or commit n8n.io template JSON.

Parent context: EPIC-m42s3g (V2 n8n compatibility).

## Closed (2026-10-09)

Every child of the first slice is done (8 of 8). The low-parity remainder, FEAT-gcq50s, was moved to the parity backlog EPIC-8rbys7, so it no longer holds this epic open. The retrieve side of template 16706 (retrieve-mode store and Vector Store Tool) is tracked by FEAT-zm3wh2 and BUG-9pmv8y.
