---
id: BUG-pder07
title: Testing an unsaved credential ignores the stored domain scope, and its one-at-a-time limit can be bypassed
status: todo
priority: medium
labels:
    - security
    - credentials
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

`TestPayload` (internal/api/handlers/credentials.go:457-489) fills the bullet placeholders from the stored secrets (`mergeStoredSecrets`), then probes with `AllowedDomains` taken from the request body, not from the stored record.

**Exploit:** send `postgres {host: attacker, password: "••••••••", credentialId}`. The stored password goes to the attacker, even when the credential is scoped to `db.corp`, and nothing is recorded.

The web UI never sends `allowedDomains` on a test (web/src/routes/(dashboard)/credentials/+page.svelte:282), so edited credentials are tested without their scope.

**Concurrency bypass:** the claim key is tenant + body `credentialId`, and the id is not validated when no placeholders are sent. Random ids therefore allow unlimited parallel probes.

# Acceptance Criteria
- [ ] With `credentialId`, the stored `AllowedDomains` apply, or their intersection with the body's.
- [ ] Placeholders are not merged when a host-like field (host, baseUrl, url) differs from the stored value.
- [ ] `credentialId` is validated before the slot is claimed, and there is a per-tenant cap on concurrent tests.
- [ ] Tests cover each case.
