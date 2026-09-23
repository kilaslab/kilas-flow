---
id: BUG-rs0xq1
title: Published workflow JSON Schema allows 4 connection kinds; the engine accepts 13 (RAG/parser docs fail validation)
status: todo
priority: medium
labels:
    - api
    - schemas
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Anyone validating a document against `schemas/workflow-v1.schema.json` rejects every workflow with an output parser or RAG wiring.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead; finding ids: LEAD-7). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Steps to Reproduce

Validate any RAG or output-parser workflow document (ai_outputParser, ai_embedding, ai_document, ai_textSplitter, ai_vectorStore, ...) against schemas/workflow-v1.schema.json.

# Expected

The schema enum matches internal/workflow/document.go ConnectionKind (13 kinds, see document_test.go:1191).

# Actual

`"kind": {"enum": ["main","ai_languageModel","ai_memory","ai_tool"]}`, so schema validation rejects valid documents.

# Acceptance Criteria
- [ ] The schema's connection `kind` enum matches `internal/workflow` ConnectionKind
- [ ] A drift test (like the other generate-*-check gates) fails when they diverge

# Implementation Plan

Generate the enum from the Go constants, or add a drift test like the other generate-*-check gates.

# Notes

# Related Files

_not recorded_

# Attachments
