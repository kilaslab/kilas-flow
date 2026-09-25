---
id: BUG-5dn8hr
title: Database TLS defaults do not verify the server certificate
status: todo
priority: low
labels:
    - credentials
    - databases
    - tls
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

- Postgres defaults to `sslmode=require`, which does not verify the certificate (internal/sqlnode/sqlnode.go:613-621).
- MySQL TLS is optional and accepts `skip-verify` (internal/credentials/builtin.go:159).

# Acceptance Criteria
- [ ] Decide and document the default (`verify-full`, or a UI warning).
- [ ] A test pins the choice.
