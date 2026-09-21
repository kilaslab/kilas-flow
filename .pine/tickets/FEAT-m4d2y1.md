---
id: FEAT-m4d2y1
title: 'Scoped agent tokens: api_keys scopes, enforcement arm, guarded verbs and audit columns'
status: doing
priority: medium
labels:
    - agent
    - api
    - security
deps:
    - FEAT-hj8pyx
    - FEAT-bp59m4
parent: EPIC-r0yg5q
phase: p2
created: "2026-09-20T07:47:53Z"
updated: "2026-09-20T07:47:53Z"
---

## Scope

Design §3 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): an agent token is an API key with a scope list, reusing the embed scope vocabulary (`workflow:read|write|run`, `datastore:read|write`) and the default-deny path gate of `internal/api/middleware/embed.go`.

## Acceptance criteria

- [x] `api_keys` gains `scopes`, `workflow_id`, `expires_at` (migration, both dialects); NULL scopes is the legacy tenant-wide key and nothing existing changes.
      (Migration `000018_api_key_scopes` in both dialects, up and down. `TestAScopedKeyKeepsItsScopesAndALegacyKeyHasNone` proves the round trip and that a key minted without scopes reports none; `TestAMintRefusesAScopeThisBuildDoesNotKnow` proves the scope vocabulary is the embed one. The migration's column types match the models exactly — `datetime`/`varchar(255)`/`varchar(64)`/`timestamptz` — because `TestBaselineLeavesAutoMigrateNothingToDoOn{SQLite,Postgres}` refuses any drift.)
- [x] `POST /api-keys` accepts the three optional fields; a scoped key can never mint another key.
      (`create-api-key` takes `scopes`, `workflowId` and `expiresAt`; `TestCreateKeyStoresTheScopeBindingAndExpiry` proves they are stored and returned, `TestCreateKeyRefusesWhatCannotWork` the three fixable refusals — an unknown scope, a binding with no scopes, an expiry in the past — each 422. `TestAScopedKeyCannotMintAnotherKey` proves the 403 `scope_denied` refusal. The principal carries the key's scopes from the auth middleware, and an expired key is refused there like a revoked one.)
- [ ] The refusals of §3.2 (activate/deactivate, delete, import, /tenants, key management, embed sessions, credential mutation, datastore schema changes, pack install) are enforced by one middleware arm at the `EmbedAuth` position, each with a named reason and the CLI's `scope_denied` exit code.
- [ ] A workflow-bound token cannot read another workflow (404, not 403).
- [ ] Audit per §3.4: `actor_kind`, `actor_label`, `actor_key_id` on `workflow_versions` and `workflow_publish_events`, exposed on the version and publish-event listings; `X-KilasFlow-Skills-Used` stored as `actor_meta`.
- [ ] Guarded CLI verbs work only with a tenant-wide key and `--yes`.
