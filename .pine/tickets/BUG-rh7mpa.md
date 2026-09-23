---
id: BUG-rh7mpa
title: Structured Output Parser ignores minimum/maximum/additionalProperties and refuses nullable types
status: todo
priority: medium
labels:
    - ai
    - output-parser
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T01:56:13Z"
---

# Description

A score of 8 passed a schema with max 5, so the retry never triggered.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-8). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Structured Output Parser converts the JSON Schema to Zod, which enforces `minimum`/`maximum`, string length and pattern, `additionalProperties`, and union and nullable types, and retries or auto-fixes on violation.

# Steps to Reproduce

1. Agent + outputParser, schema `{sentiment enum[positive,negative,neutral], score integer minimum 1 maximum 5, topics string[]}`, `additionalProperties:false`, all required.
2. Run on a product review twice.
3. Save a schema with `"nickname":{"type":["string","null"]}`.

# Expected

Out-of-range and extra properties fail validation and trigger the retry. Array-typed `type` (nullable) and `anyOf`/`oneOf` are accepted and enforced.

# Actual

Run 2 returned `{"score": 8, "sentiment": "positive", …}` as success, so `maximum: 5` was not enforced and no retry happened. The nullable schema is refused at save: `jsonSchema.properties.nickname.type must be a string`. `anyOf` is accepted but ignored. The validator only checks type, properties, required, items and enum (`internal/ai/outputschema.go:260`: "ignores the rest").

# Acceptance Criteria
- [ ] Numeric ranges, `additionalProperties: false`, enums and required are enforced, and violations trigger the retry
- [ ] Array-typed `type` (nullable), `anyOf` and `oneOf` are accepted and enforced

# Implementation Plan

Use a real JSON Schema validator (e.g. santhosh-tekuri/jsonschema) for both the shape check and value validation, and report all violations in the repair turn.

# Notes

Related (from the audit): none

# Related Files

`case-9-normal-1.execution.json`, `case-9-nullable-validate.response.json`. Code `internal/ai/outputschema.go:129-137,261-300`.

# Attachments
