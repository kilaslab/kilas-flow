---
id: EPIC-brpz48
title: 'Credential security hardening: the 2026-09-25 audit and live test of the common credential types'
status: todo
priority: high
labels:
    - security
    - credentials
created: "2026-09-25T12:47:14Z"
updated: "2026-09-25T12:47:14Z"
---

# Description

A code audit and a live test of the credentials module (2026-09-25), covering the common credential types in use: httpBasicAuth, httpHeaderAuth, httpBearerAuth, httpQueryAuth, httpCustomAuth, jwtAuth, postgres, mysql, sqlite, telegramApi, wahaApi, openAiApi, openRouterApi, and the Google Drive and Gmail OAuth2 types.

**What holds**, checked against the code and confirmed live against a running binary:
- Secret fields are sealed with AES-256-GCM, a fresh nonce per seal. The live database file holds no plaintext.
- The API and the UI never return a secret. Every response shows the bullet mask, and the edit form's secret input starts empty.
- Writing the mask back keeps the stored secret.
- Tenant isolation holds at every repository query.
- Expressions and Code nodes cannot read a secret:
  - `$env` is prefix-limited;
  - `$credentials` exists only in routing templates;
  - `this.getCredentials` and `httpRequestWithAuthentication` are refused statically.
- n8n export carries no credential.
- Postgres and MySQL targets go through the egress policy.

**What does not hold** is in the children, in priority order.

# Children (priority order)

H2 → H1 → M1 → M2/M3 → M4 → SQLite (M5 plus the hang) → L1 → L2 → the low ones.
