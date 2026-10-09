---
id: FEAT-qae4sh
title: Credential audit trail and delete-in-use protection
status: doing
priority: low
labels:
    - credentials
    - audit
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

Credential create, update, delete and test, and OAuth connect, record no actor, although workflow writes do (internal/api/handlers/workflows.go:958). Deleting a credential in use is not checked; it fails closed, with webhooks answering 500 and runs erroring.

Also latent, not wired today: the external secret bindings. `Binding.TokenEnv` may name any server environment variable, and the tenant chooses `Address` (internal/credentials/external.go:423-431, vault.go:123). Before a binding API is exposed to tenants, allow only a fixed prefix of variable names. `RefreshingCredentialStore` would also write resolved `ext://` plaintext back into the sealed payload (internal/repository/refresh.go:82-85).

# Acceptance Criteria
- [x] Credential writes, tests and connects record an actor. — `credential_events` (migration 000026); api `TestCredentialWritesAreAttributedToTheKey`, `TestCredentialWritesAreAttributedToTheUser`, `TestOAuthConnectIsAttributedToTheBrowserThatStartedIt`; repository `TestCredentialEventsRecordTheActor`; database `TestTheCredentialEventsTableComesAndGoesWithItsMigrationOn{SQLite,Postgres}`.
- [x] Deleting a credential referenced by a workflow warns or refuses. — refuses: DELETE answers 409 with one error per workflow unless `?force=true`, and the dashboard dialog lists the workflows first: `TestDeletingAnInUseCredentialIsRefusedUnlessForced`, `TestCredentialUsersFindsDraftAndActiveReferences`, web `credential-usage.test.ts`; live UI check (see Progress).
- [x] The external-binding guard rails are in place before any tenant-facing binding API. — TokenEnv must be `KILASFLOW_EXTERNAL_SECRET_TOKEN_[A-Z0-9_]+`, checked in `Binding.Validate` and again in `managerToken` (`TestBindingRefusesATokenVariableOutsideThePrefix`, `TestManagerTokenRefusesAStoredVariableOutsideThePrefix`); refresh and Connect write back only Google's token response (`TestRefreshKeepsAnExternalReferenceSealed`, `TestOAuthCallbackKeepsAnExternalClientSecretSealed`).

## Progress (2026-10-09)

What changed (commits abf5073e, 3d79ff89, f8253594, 4076c941 on the Track D branch):

- **Binding guard rails**: `credentials.BindingTokenEnvPrefix`; config exempts the prefix from the unknown-variable warning (`externalSecretTokenPrefix`, kept equal by `TestExternalSecretTokenVariablesAreNotReportedAsUnknown`). Test fixtures that named arbitrary variables now use the prefix.
- **ext:// write-back**: `RefreshingCredentialStore.Resolve` and the OAuth callback pass `Fields: token` to `Update`, which merges it over the stored payload, so a stored reference stays a reference; the caller still receives the merged, resolved map.
- **Audit trail**: table `credential_events` (tenant, credential id/name/type, action, actor kind/label/key id, detail, created_at; index on tenant, credential, created_at), in `Models()` so the drift test checks it, covered by the tenant purge (`definitions` step) and tenant-deletion.md. `RecordCredentialEvent` and `CredentialEvents` on `CredentialRepository`. Handlers record `created`, `updated`, `deleted` (detail `forced; used by N workflow(s)` when forced), `tested` (from both test endpoints when a stored credential is named; detail ok/failed/untestable), `connect-started` (detail = the state's nonce hash) and `connected` (actor copied from the connect-started row with the same nonce hash, so no identity travels in the state URL). `requestActor(ctx)` (handlers/actor.go) is shared with `audited()`. A failed audit insert is logged and the request still succeeds; the write and its audit row are not atomic. Token refresh records nothing.
- **Delete in use**: `CredentialUsers` (one join over each workflow's latest revision and active version, `bytes.Contains` prefilter, then decode), `usedBy` on GET for unscoped callers only, 409 on DELETE unless `force=true`, a small change to the credentials page's existing dialog (credential-usage.ts helper; messages in en and id).
- Docs: concepts/credentials.md (audit trail, "A credential in use"), operate/security.md, operate/configuration.md (the prefix exemption), skills/kilasflow-credentials/SKILL.md (the delete answer), sdk/CHANGELOG.md (breaking: 409 from `deleteCredential`, signature unchanged; additive: `usedBy`).

Live check (2026-10-09, SQLite, new binary on a copy of a pre-change database): the credentials page's delete dialog for a credential attached by workflow "Partner sync" listed it with its node ("Nodes: Call partner") and offered "Delete anyway"; confirming deleted it (GET 404) and `credential_events` held `deleted … forced; used by 1 workflow` after `updated` and `tested` rows.

Deviations from the plan:
- The migration is `000026_credential_events` (Track E owns 000025), per the orchestrator.
- `CredentialEvents` (a read, newest first) joined `RecordCredentialEvent` on the interface: the callback needs it to find who started the Connect, and tests read the trail through it. There is still no HTTP read endpoint (no new contract row).
- The PostgreSQL migration tests' reset list (`postBaselineTables` in internal/database/migrate_test.go) had to name the new table, or every PostgreSQL migration test fails with "relation already exists" on a reused server.

Left / follow-ups:
- A read endpoint for the trail (adds an api-contract row and `contractRowCount`).
- An SDK `force` option on `deleteCredential` (today: send `?force=true` through the transport).
- Residual binding risk, before any tenant-facing binding API: one tenant can name a prefixed variable meant for another tenant, and the tenant chooses `Address`. Give each tenant its own token namespace or have the operator own the binding-to-variable mapping.
