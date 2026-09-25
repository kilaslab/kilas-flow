---
id: FEAT-qae4sh
title: Credential audit trail and delete-in-use protection
status: todo
priority: low
labels:
    - credentials
    - audit
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

Credential create, update, delete and test, and OAuth connect, record no actor, although workflow writes do (internal/api/handlers/workflows.go:958). Deleting a credential in use is not checked; it fails closed, with webhooks answering 500 and runs erroring.

Also latent, not wired today: the external secret bindings. `Binding.TokenEnv` may name any server environment variable, and the tenant chooses `Address` (internal/credentials/external.go:423-431, vault.go:123). Before a binding API is exposed to tenants, allow only a fixed prefix of variable names. `RefreshingCredentialStore` would also write resolved `ext://` plaintext back into the sealed payload (internal/repository/refresh.go:82-85).

# Acceptance Criteria
- [ ] Credential writes, tests and connects record an actor.
- [ ] Deleting a credential referenced by a workflow warns or refuses.
- [ ] The external-binding guard rails are in place before any tenant-facing binding API.
