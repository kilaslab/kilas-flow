---
id: FEAT-r87gtj
title: 'Credential crypto hygiene: AAD binding, key versioning, separate OAuth key, warning on a raw key'
status: doing
priority: low
labels:
    - security
    - credentials
    - crypto
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

- GCM seals with nil AAD (internal/credentials/credentials.go:243, 256), so a payload is not bound to its tenant, id or type.
- There is no key id in the ciphertext and no re-encryption tooling.
- The master key is reused as the OAuth HMAC key (cmd/kilasflow/main.go:448).
- A raw 32-character passphrase is accepted as the key with no key derivation (keysource.go:78-80).

# Acceptance Criteria
- [x] New seals carry `tenant|id|type` as AAD and a version prefix, and old payloads still open. — internal/credentials/cipher_test.go (`TestALegacyPayloadStillOpens` with a golden v0 vector captured from the pre-change `Encrypt`, `TestNewSealsCarryTheVersionPrefixAndKeyID`, `TestASealOpensOnlyUnderItsOwnTenantIDAndType`, `TestTheHeaderIsAuthenticated`, `TestTheHeaderIsPartOfTheAdditionalData`, `TestAADRefusesAPartHoldingNUL`); repository `TestAPayloadSwappedIntoAnotherRowDoesNotOpen`, `TestLifecycleStateIsBoundToItsRoute`; live: a database written by the pre-change binary opened and resealed (see Progress).
- [x] The OAuth key is derived with HKDF. — `credentials.DeriveKey(master, "kilasflow/oauth-state/v1")`; `TestDeriveKeySeparatesPurposes`, cmd/kilasflow `TestOAuthSigningKeyIsDerivedNotTheMasterKey`.
- [x] The server warns on a raw passphrase key. — `credentials.ParseKey` reports the format; one boot warning per raw key (master, secondary, embed, auth signing keys), naming the variable, never the key: `TestParseKeyReportsARawString`, `TestBootWarnsOnceAboutARawStringKey`.
- [x] A rotation path is documented. — `security.secondary_encryption_key_env` + `kilasflow -reseal-credentials` (`TestResealUpgradesLegacyAndRotatedRows`, `TestResealSkipsARowChangedMeanwhile`, `TestResealReportsUnopenableRows`, `TestASecondaryKeyOpensWhatTheOldKeySealed`, `TestResealCredentialsReportsAndFailsOnAnUnopenableRow`); docs/src/content/docs/operate/key-rotation.md (single process, several replicas, one-time reseal after upgrade).

## Progress (2026-10-09)

What changed (commits db21257c, 9deecd8d, bf044ad7 on the Track D branch):

- **Sealed format v1** (internal/credentials/cipher.go, moved out of credentials.go): `"kfs\x01" || kid(4) || nonce(12) || ct||tag`. kid = HKDF-SHA256(master, info `kilasflow/credential-key-id/v1`, 4 bytes); AES key = HKDF-SHA256(master, `kilasflow/credential-seal/v1`), so the master key never seals. AAD = header(8) || purpose || NUL || parts…; credentials use `CredentialAAD(tenant, id, type)` (purpose `kilasflow/credential/v1`), webhook lifecycle state uses `LifecycleAAD(tenant, route)`. A part holding NUL is refused. `Encrypt`/`Decrypt` were removed (so no unbound call site can survive) and replaced by `Seal`/`Open`/`OpenForReseal`.
- **Legacy v0 stays readable**: Open tries v1 when the magic and kid match, then falls back to v0 (raw key, nil AAD) under every key in the keyring, so a v0 nonce that happens to start with the magic still opens. Golden vector: key `0123456789abcdef0123456789abcdef`, plaintext `{"password":"hunter2"}`, captured from the code at 2f3fa05b before any change.
- **Keyring**: `NewCipher(primary, secondary...)`; only the primary seals. Config `security.secondary_encryption_key_env` (default `KILASFLOW_ENCRYPTION_KEY_SECONDARY`), read from the environment even when the primary comes from a manager; a malformed value refuses boot.
- **`kilasflow -reseal-credentials`** (run() right after `database.Migrate`, then exit): keyset batches across tenants, compare-and-swap `UPDATE … WHERE id=? AND tenant_id=? AND payload=?` via `UpdateColumn` (updated_at untouched), for credentials and for `webhook_routes.lifecycle_state`; logs resealed/current/skipped/unopenable and exits non-zero naming every unopenable row.
- **OAuth key**: `loadCredentialKeys`/`credentialKeys` in main.go derive it with HKDF from the primary only.
- **Raw-key warning**: `ParseKey`, `KeyFromEnvironmentFormat`, `KeyFromManagerFormat` (old entry points keep their signatures); `masterKey()` now returns the format.
- Docs: concepts/credentials.md (Encryption), operate/security.md, operate/configuration.md, operate/upgrades.md, new operate/key-rotation.md, start/install.md (one sentence that said there was no re-encryption pass), regenerated configuration-reference.md and config.example.yaml.

Deviations from the plan:
- `NeedsReseal(sealed)` became `OpenForReseal(sealed, aad) (fields, stale, err)`: the reseal pass opens every row anyway, and deciding "stale" from a successful open is exact, where a header check is wrong for a v0 nonce that starts with the magic and the primary kid.
- The warning message is the codebase's constant-message-plus-attribute style (`msg="key holds a 32-character string used as raw key bytes, not a generated key: generate one with openssl rand -base64 32 and rotate to it" variable=…`), not the variable spliced into the message.

Live check (2026-10-09, SQLite): a database written by the pre-change binary (built at 2f3fa05b) held two v0 payloads. The new binary applied migration 000026, opened both (a payload test resolved the stored bearer token; a PUT on the header credential answered 200 and re-sealed it as v1). On a second copy, `kilasflow -reseal-credentials` logged `resealed=2 current=0 unopenable=0`, then `resealed=0 current=2`; both raw payloads then began `6B667301` (`kfs\x01`) with `updated_at` unchanged. A rotation on a third copy: the new key alone reported `unopenable=2` and exited 1 naming both rows; new primary + old secondary resealed 2; the new key alone then reported `current=2`.

PostgreSQL gate (2026-10-09): a private container `docker run -d --name trackd-pg-gate -e POSTGRES_USER=kf -e POSTGRES_PASSWORD=kfpass -e POSTGRES_DB=kftest -p 127.0.0.1:55439:5432 pgvector/pgvector:pg17`, then `KILASFLOW_TEST_POSTGRES_DSN='postgres://kf:kfpass@127.0.0.1:55439/kftest?sslmode=disable' go test -p 1 ./internal/database/... ./internal/repository/... ./internal/tenantpurge/...` — all ok after `credential_events` was added to `postBaselineTables` (commit 4076c941). The PostgreSQL halves ran, not skipped (`go test -v` showed `--- PASS: …/postgres` for TestResealUpgradesLegacyAndRotatedRows, TestResealSkipsARowChangedMeanwhile — the compare-and-swap `UPDATE … WHERE payload = $n` on a bytea parameter — TestAPayloadSwappedIntoAnotherRowDoesNotOpen, TestLifecycleStateIsBoundToItsRoute, TestCredentialEventsRecordTheActor, TestCredentialUsersFindsDraftAndActiveReferences, TestActivationRefusesAnUnknownCredential, plus TestBaselineLeavesAutoMigrateNothingToDoOnPostgres and TestTheCredentialEventsTableComesAndGoesWithItsMigrationOnPostgres). Re-run after the review fixes and the merge of main (workflow_tags) on a fresh container (port 55440): the same three packages ok, and TestFindCredentialEventMatchesActionAndDetailUnderTheTenant/postgres passed too.

Review fixes (2026-10-09): `security.legacy_seals` (`open` default, `refuse`; anything else refuses the boot) and `Cipher.RefusingLegacySeals` — `TestRefusingLegacySealsNeverOpensAV0Payload`, `TestLegacySealsDefaultToOpenAndAcceptOnlyOpenOrRefuse`, `TestLegacySealsSettingReachesTheBootKeyring`; the reseal failure message names `security.secondary_encryption_key_env` when it is empty (`TestResealFailureNamesWhereTheOldKeyGoesWhenNoSecondaryIsConfigured`); .env.example and the root CHANGELOG updated.

Residual risk (documented in key-rotation.md, security.md, upgrades.md and concepts/credentials.md): a legacy v0 payload carries no AAD, so while the keyring opens legacy payloads (the default, `security.legacy_seals: open`) a v0 payload — including a copy taken before `-reseal-credentials` upgraded the stored rows, from a dump, a backup or a replica — can be moved between rows by someone with database write access and open there. The reseal upgrades the stored rows only. Setting `security.legacy_seals: refuse` once a reseal reports none left, or rotating to a new key after the upgrade reseal, retires those copies; key-rotation.md recommends the rotation where old dumps have left the operator's control. Binaries older than this release cannot open v1 seals, so replicas must be upgraded together and a downgrade means restoring the backup. A Connect popup open across the upgrade or a rotation fails once.
