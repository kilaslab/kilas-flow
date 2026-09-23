---
id: FEAT-hxztwz
title: "Pack-trigger signature verification: sha512-only, secret stored in the workflow document; add sha256/Standard Webhooks, credential-held secret"
status: todo
priority: high
labels:
    - saas
    - packs
    - webhooks
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

Pack triggers verify only `sha512` over the raw body (`internal/nodepack/trigger.go:60-71,178-179`).
- The secret is a node parameter (`secretParameter`), so it is stored in the workflow document. That contradicts "A workflow document never contains a secret" (`docs/src/content/docs/concepts/credentials.md:8`).
- Common schemes cannot be verified: Stripe or Standard Webhooks style HMAC-SHA256 over `timestamp.body`, `v1=` prefixes, and two signatures during a secret rotation.

# Acceptance Criteria
- [ ] The `hmac` block accepts: `algorithm: sha256 | sha512`; `encoding: hex | base64`; `prefix`; `signingString`, a template over the body, the timestamp and header values; `timestampHeader` with `toleranceSeconds`; multiple comma-separated signatures
- [ ] The secret can come from a credential field.
- [ ] There is a `standardWebhooks` preset.
- [ ] A missing, bad or stale signature is refused before any execution exists.
- [ ] Docs and tests are updated.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
