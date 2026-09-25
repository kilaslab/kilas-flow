---
id: FEAT-r87gtj
title: 'Credential crypto hygiene: AAD binding, key versioning, separate OAuth key, warning on a raw key'
status: todo
priority: low
labels:
    - security
    - credentials
    - crypto
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

- GCM seals with nil AAD (internal/credentials/credentials.go:243, 256), so a payload is not bound to its tenant, id or type.
- There is no key id in the ciphertext and no re-encryption tooling.
- The master key is reused as the OAuth HMAC key (cmd/kilasflow/main.go:448).
- A raw 32-character passphrase is accepted as the key with no key derivation (keysource.go:78-80).

# Acceptance Criteria
- [ ] New seals carry `tenant|id|type` as AAD and a version prefix, and old payloads still open.
- [ ] The OAuth key is derived with HKDF.
- [ ] The server warns on a raw passphrase key.
- [ ] A rotation path is documented.
