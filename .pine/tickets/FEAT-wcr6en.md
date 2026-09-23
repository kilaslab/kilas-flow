---
id: FEAT-wcr6en
title: 'Typed credential inputs: options selects, number fields, multi-line JSON editor'
status: todo
priority: medium
labels:
    - credentials
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

JWT key type and algorithm, and Postgres SSL mode, are free text. HTTP Custom Auth JSON is a single-line password box. BUG-esb9sh proposed the fix, and it still reproduces.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Credential properties use options dropdowns (JWT Key Type and Algorithm, Postgres SSL), number inputs (port) and a multi-line JSON editor for Custom Auth.

# Steps to Reproduce

1. New credential → JWT Auth. 2. Switch to PostgreSQL. 3. Switch to HTTP Custom Auth.

# Expected

Select, number and textarea or code inputs driven by field metadata.

# Actual

JWT "Key Type" (default "passphrase") and "Algorithm" (default "HS256") are plain text boxes. Postgres "SSL mode" is free text described as "disable, require, verify-ca, or verify-full". HTTP Custom Auth's JSON is `<input type=password>`: one masked line, so you can't check the JSON you're typing. GET /credential-types has no kind, options or multiline metadata to drive better inputs.

# Acceptance Criteria
- [ ] Credential field definitions carry `kind` (string, number, options, json, multiline) and `options`
- [ ] The credential form renders selects, number inputs and a JSON editor accordingly

# Implementation Plan

Add `kind` (string|number|options|json|multiline) and `options` to credential field definitions and render them in credentials/+page.svelte.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done). Its suggested fix called for "field kind/options/placeholder metadata and … typed inputs (e.g. an SSL mode select, number for port)"; this still reproduces.

# Related Files

agents/ux-ops/55-cred-jwt-freetext.png, agents/ux-ops/56-cred-custom-json.png; GET /api/v1/credential-types.

# Attachments
