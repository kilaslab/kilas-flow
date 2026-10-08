---
id: BUG-rs0xq1
title: Published workflow JSON Schema allows 4 connection kinds; the engine accepts 13 (RAG/parser docs fail validation)
status: done
priority: medium
labels:
    - api
    - schemas
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-10-08T15:51:09Z"
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
- [x] The schema's connection `kind` enum matches `internal/workflow` ConnectionKind
- [x] A drift test (like the other generate-*-check gates) fails when they diverge

# Implementation Plan

Generate the enum from the Go constants, or add a drift test like the other generate-*-check gates.

# Notes

# Related Files

- schemas/workflow-v1.schema.json
- internal/workflow/document.go (ConnectionKinds)
- internal/workflow/document_test.go

# Attachments

## Fix (2026-10-08)

`schemas/workflow-v1.schema.json` now lists all thirteen connection kinds in `workflow.ConnectionKinds()` order (main, ai_agent, ai_chain, ai_document, ai_embedding, ai_languageModel, ai_memory, ai_outputParser, ai_retriever, ai_reranker, ai_textSplitter, ai_tool, ai_vectorStore). `TestCanonicalJSONSchemaConnectionKindsMatchTheEngine` (internal/workflow/document_test.go) reads the published file's `$defs.connection.properties.kind.enum` and fails unless it equals `ConnectionKinds()` element for element. Since it runs under `go test ./...` there is no separate Makefile target. The schema stays hand-written rather than generated: nothing embeds or generates from it (no SDK type, OpenAPI enum or docs page carries the list), so there was nothing to regenerate. TDD: the test failed against the four-kind enum and passes after the fix.
