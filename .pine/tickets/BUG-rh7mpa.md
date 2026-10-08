---
id: BUG-rh7mpa
title: Structured Output Parser ignores minimum/maximum/additionalProperties and refuses nullable types
status: done
priority: medium
labels:
    - ai
    - output-parser
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-10-08T15:41:07Z"
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
- [x] Numeric ranges, `additionalProperties: false`, enums and required are enforced, and violations trigger the retry
- [x] Array-typed `type` (nullable), `anyOf` and `oneOf` are accepted and enforced

# Implementation Plan

Use a real JSON Schema validator (e.g. santhosh-tekuri/jsonschema) for both the shape check and value validation, and report all violations in the repair turn.

# Notes

Related (from the audit): none

# Related Files

`case-9-normal-1.execution.json`, `case-9-nullable-validate.response.json`. Code `internal/ai/outputschema.go:129-137,261-300`.

# Attachments

## Fix (2026-10-08)

`internal/ai/outputschema.go` now enforces the schema instead of checking only type/properties/required/items/enum. The in-house validator was extended rather than adding a new module (the implementation plan's santhosh-tekuri/jsonschema): it keeps the existing path-named messages, adds no dependency, and covers every keyword the audit found ignored.

- **Value validation** (`ValidateValue` → `schemaViolations`): `type` as one name or a list (`["string","null"]`), OpenAPI `nullable: true`, `enum`, `const`, `minimum`/`maximum`, `exclusiveMinimum`/`exclusiveMaximum` (number and draft-4 boolean forms), `multipleOf`, `minLength`/`maxLength` (code points), `pattern`, `minItems`/`maxItems`/`uniqueItems`, `required`, `properties`, `patternProperties`, `additionalProperties` (false or a schema), `minProperties`/`maxProperties`, `allOf`/`anyOf`/`oneOf`/`not`. `enum` no longer short-circuits the other keywords.
- **Every violation is reported** in one error joined by `; `, so the repair turn lists them all (e.g. `output: property "mood" is not allowed…; output.score: 8 is greater than the maximum 5; output.sentiment: value "ecstatic" is not one of the allowed options [...]`). A failed `anyOf`/`oneOf` states why each alternative failed.
- **Save-time shape check** (`checkSchemaShape`) accepts type lists and refuses, naming the path, a malformed keyword the validator reads: unknown type names, non-array `anyOf`/`oneOf`/`allOf`, non-object members, `additionalProperties` that is neither boolean nor object, non-numeric bounds, negative lengths, non-array `enum`, and a `pattern` that does not compile. Patterns use Go RE2 syntax, so a lookaround pattern is refused at save rather than silently unenforced. `$ref`/`format` are still ignored.

Tests: `internal/ai/outputschema_test.go` — the audit's review schema (score 8 / 0, extra property, enum, missing required, fractional integer, all-violations-at-once), string/array/number bounds, nullable + `anyOf` + `oneOf` acceptance and enforcement, malformed-keyword refusals, and an agent run where a score of 8 is retried and the repair turn names `output.score` and `maximum`.
