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
- [x] The refusals of §3.2 (activate/deactivate, delete, import, /tenants, key management, embed sessions, credential mutation, datastore schema changes, pack install) are enforced by one middleware arm at the `EmbedAuth` position, each with a named reason and the CLI's `scope_denied` exit code.
      (`internal/api/middleware/scope.go`: `ScopeAuth` mounted beside `EmbedAuth` in `internal/api/server.go`, sharing one `permits` table with the embed session — the two credentials answer the same questions about the same path shapes, and the arms where they differ say which kind they are asking. `TestAScopedKeyIsRefusedTheOperationsOfSectionThreeTwo` walks every §3.2 refusal and asserts the status and the named reason; `TestAScopedKeyReachesWhatItsScopesName` walks what the scopes do buy, so a gate that refused everything would fail too. Live: a scoped key minted through `POST /api-keys` on a running server answers `kilasflow api activate-workflow` with exit 3 and `error.code: "scope_denied"` — "This agent token cannot change activation." — while the tenant's own key still activates. The embed arm's sentences are pinned byte-for-byte by `TestEmbedSentencesAreUnchangedByTheSharedTable`.)
- [x] A workflow-bound token cannot read another workflow (404, not 403).
      (`TestAWorkflowBoundKeyReadsAnotherWorkflowAsMissing`: reading, updating or running another workflow answers 404 with the same problem document a genuine miss produces, so the token cannot probe for ids it does not hold; listing another workflow's executions is refused with the binding named, because the path names no workflow and the query does. Live: a key bound to one workflow reads its own with 200 and a sibling id with `{"detail":"Workflow not found.","status":404}`.)
- [x] Audit per §3.4: `actor_kind`, `actor_label`, `actor_key_id` on `workflow_versions` and `workflow_publish_events`, exposed on the version and publish-event listings; `X-KilasFlow-Skills-Used` stored as `actor_meta`.
      (Migration `000019_workflow_actor` in both dialects, up and down, with the backfill rule stated in the file. `TestARevisionSavedByAnAPIKeyIsAttributedToTheKey`, `TestARevisionSavedByASessionIsAttributedToTheUser` and `TestAPublishIsAttributedToTheKeyThatActivated` drive real HTTP through the authenticated server: an API-key save records `actorKind: "key"` with the key's label and id, a session save records `"user"` with the address and no key id, a publish records the key that activated, and the version listing returns `actorMeta: ["kilasflow-debugging","kilasflow-expressions"]` for a call carrying `X-KilasFlow-Skills-Used`. `TestTheActorColumnsAreBackfilledFromTheLegacyLabelOn{SQLite,Postgres}` roll 000019 back over seeded rows and re-apply it, which exercises both down files. `TestARevisionRecordsTheKeyThatWroteItAndTheSkillsItReported` and the extended `TestAnUnknownAuthorIsRecordedAsAbsentRatherThanInvented` cover the repository round trip and the absence rule.)
- [x] Guarded CLI verbs work only with a tenant-wide key and `--yes`.
      (Two gates, and the second one now exists: `requireConfirmation` still refuses a missing `--yes` before the configuration chain and sends nothing at all, and `requireAuthority` — after `buildClient`, before the verb's own operation — refuses a token whose `get-me` carries a non-empty `scopes` list with exit 3 `scope_denied`, so an agent key is refused whatever the flag says and the mutation is never attempted. The fourteen verbs design §4.2 marks guarded — `workflow activate|deactivate|delete`, `credential create|update|delete`, `datastore create|rename|delete|clear|columns add|columns rename|columns drop`, `tenant delete` — each carry their operation id, a refusal sentence and a `Human` printer; `pack install` stays out because the pack surface is a local loader with no server operation to drive. `TestGuardedVerbWithoutYesSendsNothing`, `TestGuardedVerbRefusesAScopedTokenEvenWithYes` and `TestGuardedVerbWithATenantWideKeyReachesTheServer` drive every guarded verb against a stub, and the same three cases against the built binary observed exit 3 `confirmation_required` with zero requests, exit 3 `scope_denied` with only the identity read, and exit 0 with the operation's own request.)

## Implementation notes

### Audit columns (§3.4)

What landed:

- `migrations/{sqlite,postgres}/000019_workflow_actor.{up,down}.sql`: `actor_kind`, `actor_label`,
  `actor_key_id` (plus `actor_meta` on `workflow_versions`) on both audit tables. Column types were
  taken from the DDL AutoMigrate issues for the models — `text`/`varchar(16|64|255)`, `blob`/`bytea`
  for `actor_meta`, `datetime`/`timestamptz` untouched — so both drift tests pass.
- `internal/repository`: the context actor became a struct (`repository.Actor`: kind, label, key id,
  meta) with `ActorKindUser`/`ActorKindKey`; `appendVersion` and `appendPublishEvent` write it, and
  `versionSummary`/`ListPublishEvents` read it back. `CreatedBy` is still written beside `ActorLabel`
  so the dashboard's existing `createdBy` keeps working.
- `internal/api/handlers`: one helper, `audited(ctx, skillsUsed)`, turns the request's principal
  (`auth.PrincipalFrom`) into that actor — API key ⇒ `key`, session ⇒ `user` — and parses the
  `X-KilasFlow-Skills-Used` header. It is called on the five writes that create a version or a
  publish event (create, update, restore, publish, activate, deactivate) and on import, so every
  revision and every publish row is attributed. The header is declared once per mutating input
  (`createWorkflowInput`, `updateWorkflowInput`, `publishVersionInput`, `importWorkflowInput`), which
  is the only place huma can bind it; it appears in the OpenAPI document for those four operations.
- `internal/cli`: `--skills-used` (comma-separated, split and trimmed) rides on `Client.SkillsUsed`
  and is sent as the header on mutating requests only.
- Regenerated: `make generate-api generate-types generate-api-reference`, all three `-check` targets
  report fresh.

The backfill rule (stated in the migration header): the actor columns are new, so a pre-existing row
has no value in them and the migration does not invent one. Where the legacy label column
(`workflow_versions.created_by`, `workflow_publish_events.actor`) carries a value, the row is marked
`actor_kind = 'user'` with `actor_label` set from it: that column is the only identity the old schema
ever recorded, and only a signed-in person could have written one — no code path ever recorded a key
label, and `repository.WithActor` had no production caller at all. Rows with no label stay NULL, and
`actor_key_id` is never backfilled. The API reads NULL as "nobody was recorded" and omits the fields
rather than reporting a guessed actor.

Evidence:

- `KILASFLOW_TEST_POSTGRES_DSN=… go test -count=1 -p 1 ./internal/database/... ./internal/repository/... ./internal/api/...`
  → `ok` for all five packages, including `TestBaselineLeavesAutoMigrateNothingToDoOn{SQLite,Postgres}`.
- `go test -count=1 ./internal/cli/... ./internal/workflow/...` → `ok`.
- `make generate-api-check generate-types-check generate-api-reference-check` → "14 pages fresh".
