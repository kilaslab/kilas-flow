---
id: BUG-2z8geh
title: 422 validation errors echo the whole request body, including credential secrets, into the response/envelope
status: todo
priority: high
labels:
    - cli
    - security
    - api
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

A failed `credential create` prints the submitted token or password back into the CLI envelope. That output lands in terminals, CI logs and agent transcripts.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n8n's public API returns `{"message":"request/body must have required property 'data'"}` without echoing values.

# Steps to Reproduce

1. `kilasflow api create-credential --body '{"name":"x","type":"httpHeaderAuth","data":{"name":"X-Api-Key","value":"s3cr3t-cli-skills"}}'`. 2. `… --body '{"name":"x","type":"httpBearerAuth","secret":{"token":"TOPSECRET-123"}}'`.

# Expected

The problem document never echoes body values for secret-bearing operations, or the CLI redacts `fields`, `token`, `password` and `value` before printing.

# Actual

Exit 2, and `error.detail.problem.errors[].value` holds the entire body, `"value":"s3cr3t-cli-skills"` / `"TOPSECRET-123"` included, twice per response. It lands in the agent's transcript and in MCP tool results, while the credentials skill's non-negotiable 1 says a secret "never appears in … chat". A field-level error (`allowedDomains` wrong type) echoes only that field. The leak is when the location is `body`.

# Acceptance Criteria
- [ ] Problem documents never echo body values for secret-bearing operations (credentials, auth, API keys)
- [ ] The CLI also redacts `fields`, `token`, `password` and `value` before printing, as defence in depth
- [ ] A test posts an invalid credential and asserts the secret does not appear in the response

# Implementation Plan

Strip `value` from huma validation errors on credential routes, or globally when location == `body`. Extend the CLI's redactor to problem `errors[].value`.

# Notes

Related (from the audit): none

# Related Files

cs/leak1.json, cs/b-cred-out.json (first attempt, in the transcript).

# Attachments
