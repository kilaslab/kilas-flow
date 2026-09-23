---
id: FEAT-zjrw76
title: 'JS Code runtime P3: Luxon, IntlLite, require allowlist, crypto, Buffer and web-API shims'
status: doing
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-yxhgeh
parent: EPIC-tjnr1z
phase: p3
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T05:34:44Z"
---

# Description

The libraries and shims that real Code nodes use: Buffer is in 3.6% of them, toLocale*/Intl in about 3%, Luxon in 1.7%.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 3* section. Read it before starting.

# Acceptance Criteria
- [ ] Luxon loads from a precompiled Program, with `DateTime` defaulting to the workflow timezone; every spike Luxon probe plus the DST cases match the committed Node goldens
- [ ] `Intl.DateTimeFormat` (en-US) and `Intl.NumberFormat` (any CLDR locale via x/text); a non-English date locale is a named error, never English output
- [ ] `require()` has an allowlist (crypto, luxon, lodash, util subset); `fs` and other modules are refused with the `unsupportedScript` sentence
- [ ] The crypto subset (hash, hmac, random*, pbkdf2/scrypt, AES CBC/CTR/GCM), `Buffer`, `TextEncoder`/`TextDecoder`, `atob`/`btoa`, `URL`, `structuredClone`, `Object.groupBy` and timers pass their golden tests. CI never runs Node

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 3*.

# Notes

# Related Files

# Attachments
