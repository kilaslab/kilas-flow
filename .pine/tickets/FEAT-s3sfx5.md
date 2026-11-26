---
id: FEAT-s3sfx5
title: Actor identity in embed sessions
status: done
priority: medium
labels:
    - saas
    - embedding
    - audit
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-26T07:52:25Z"
---

# Description

The token names the tenant, workflow, scopes and origin, but not the user (`internal/embed/embed.go:148-171`). Version history, publish events and manual runs therefore cannot say which host user acted.

# Acceptance Criteria
- [x] Minting accepts `actor: { id, displayName }`, which is signed into the token.
- [x] The actor is recorded on versions, publish events and manually queued executions, returned by the API, and shown in history.

# Implementation Plan

## Implementation notes

- `embed.Actor{ID, DisplayName}` is validated in `Actor.Validate()` (id 1-128 of `[A-Za-z0-9._:@|+/=-]`; display name optional, max 64 runes because it is copied into the varchar(64) `created_by`/`actor` columns, letters/marks/digits/spaces/simple punctuation, no leading or trailing space) and signed into the token as `Session.Actor` (json `act`, pointer). Old tokens still verify with a nil actor; tokenVersion stays `kfe1`.
- `requestActor()` checks the embed session first and maps it to the new kind `embed` (Label = displayName, new `Actor.ID` = host id; never reuses `actor_key_id`). An unnamed session records `embed` with no label.
- Migration 000029 (both dialects): `workflow_versions.actor_id` (nullable), `workflow_publish_events.actor_id`, and `executions.actor_kind/actor_label/actor_key_id/actor_id` (NOT NULL DEFAULT ''). No backfill.
- `queueManual` fills the execution actor from `repository.ActorFrom(ctx)`; Run and Retry pass `audited(ctx, "")`. QueueTriggered, sub-workflow queueing and the idempotency fingerprint are untouched.
- API: actor enums are `user,key,embed`; `actorId` on version and publish-event resources; four actor fields on execution detail and summary. Web: `actorName()` renders "{name} (embedded)" / "Embedded editor" in the version list, the publish timeline and the execution detail page. SDK `EmbedSessionRequest.actor`. Docs: embedding guide section "Naming the user who acts", changelogs, regenerated clients and references.
- Review follow-ups: embed.Actor carries OpenAPI minLength/maxLength/pattern tags (Validate stays the authority); tests cover a run pinned with workflowVersionId, a retry made with the embed token, a triggered run (no actor even under an actor-tagged context), and schema vs issuer refusals at mint. The UI never displays actorId (an embed actor without a display name shows "Embedded editor"); the guide says actorId is returned to history/execution readers and readable in the token, so hosts should use an opaque id, and that createdBy/actor carry no kind. Rows with actor_kind 'embed' survive a 000029 rollback (noted in the down migration and CHANGELOG).
- Verified: gofmt, go vet ./internal/..., CGO_ENABLED=0 go test of embed, repository, database, api/..., guardrails, tenantpurge, workflow, execution (all ok); make generate-api-check generate-types-check generate-api-reference-check generate-sdk-reference-check generate-docs-facts-check (fresh); web version-history test + pnpm check (0 errors); sdk pnpm test (167 passed) + pnpm check.

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, but the plumbing is half there. Versions and publish events already carry actor columns (internal/repository/models.go:252-265, :297-305) filled by `audited()` (internal/api/handlers/workflows.go:958-976); API enums are `user,key` (workflows.go:178, :199).
- Work = an `embed` actor kind + mapping the session actor in `audited()`. Only executions lack actor fields (schema change needed there).

# Related Files

# Attachments

## Work Evidence

Recorded from commits by `pine close --evidence --commit` on 2026-11-26.

- Commits (9):
  - `f05d6950` — FEAT-s3sfx5: record review follow-ups in the ticket notes
  - `c2405275` — FEAT-s3sfx5: never display the host's actor id; document its exposure
  - `c754b36d` — FEAT-s3sfx5: publish actor limits in the schema; cover pinned runs, retries and triggers
  - `7d889e7c` — chore(pine): FEAT-s3sfx5 to testing with implementation notes
  - `ebdd927b` — FEAT-s3sfx5: name who queued a run on the execution detail page
  - `053d5739` — FEAT-s3sfx5: document naming the acting user in embed sessions
  - `c253e3b4` — FEAT-s3sfx5: show the embed actor in version history; SDK forwards actor
  - `b6d11337` — FEAT-s3sfx5: test embed actor signing, recording, and the HTTP flow
  - `04e63f37` — FEAT-s3sfx5: sign a host-asserted actor into embed sessions and record it
- Files changed:

```
f05d6950 FEAT-s3sfx5: record review follow-ups in the ticket notes

 .pine/tickets/FEAT-s3sfx5.md | 1 +
 1 file changed, 1 insertion(+)
c2405275 FEAT-s3sfx5: never display the host's actor id; document its exposure

 CHANGELOG.md                                          |  8 ++++++--
 docs/src/content/docs/guides/embedding.md             | 17 ++++++++++++-----
 migrations/postgres/000029_embed_actor.down.sql       |  4 +++-
 migrations/sqlite/000029_embed_actor.down.sql         |  4 +++-
 sdk/src/server.ts                                     |  5 ++++-
 web/src/lib/workflow-editor/version-history.test.ts   | 19 +++++++++++--------
 web/src/lib/workflow-editor/version-history.ts        | 17 +++++++++--------
 .../routes/(dashboard)/executions/[id]/+page.svelte   |  2 +-
 8 files changed, 49 insertions(+), 27 deletions(-)
c754b36d FEAT-s3sfx5: publish actor limits in the schema; cover pinned runs, retries and triggers

 docs/public/openapi.json                     |  6 ++-
 docs/src/content/docs/reference/api/embed.md |  2 +-
 internal/api/embed_actor_test.go             | 74 ++++++++++++++++++++++++++--
 internal/embed/embed.go                      |  8 ++-
 internal/repository/embed_actor_test.go      | 28 +++++++++++
 sdk/src/generated/models.ts                  | 12 ++++-
 web/src/lib/api/generated/models/actor.ts    | 12 ++++-
 7 files changed, 130 insertions(+), 12 deletions(-)
7d889e7c chore(pine): FEAT-s3sfx5 to testing with implementation notes

 .pine/tickets/FEAT-s3sfx5.md | 17 +++++++++++++----
 1 file changed, 13 insertions(+), 4 deletions(-)
ebdd927b FEAT-s3sfx5: name who queued a run on the execution detail page

 web/src/routes/(dashboard)/executions/[id]/+page.svelte | 5 ++++-
 1 file changed, 4 insertions(+), 1 deletion(-)
053d5739 FEAT-s3sfx5: document naming the acting user in embed sessions

 CHANGELOG.md                                      |  9 +++
 docs/public/openapi.json                          | 85 +++++++++++++++++++++--
 docs/src/content/docs/guides/embedding.md         | 31 ++++++++-
 docs/src/content/docs/reference/api/embed.md      |  3 +
 docs/src/content/docs/reference/api/executions.md | 20 ++++++
 docs/src/content/docs/reference/api/workflows.md  |  1 +
 sdk/CHANGELOG.md                                  |  7 ++
 7 files changed, 149 insertions(+), 7 deletions(-)
c253e3b4 FEAT-s3sfx5: show the embed actor in version history; SDK forwards actor

 sdk/src/generated/models.ts                        | 67 ++++++++++++++++++++--
 sdk/src/server.ts                                  | 18 ++++++
 sdk/test/server.test.ts                            | 18 ++++++
 web/messages/en/versions.json                      |  2 +
 web/messages/id/versions.json                      |  2 +
 web/src/lib/api/generated/models/actor.ts          | 14 +++++
 .../lib/api/generated/models/embedSessionBody.ts   |  3 +
 .../lib/api/generated/models/executionResource.ts  |  9 +++
 .../generated/models/executionResourceActorKind.ts | 19 ++++++
 .../lib/api/generated/models/executionSummary.ts   |  9 +++
 .../generated/models/executionSummaryActorKind.ts  | 19 ++++++
 web/src/lib/api/generated/models/index.ts          |  3 +
 .../models/workflowPublishEventResource.ts         |  6 +-
 .../workflowPublishEventResourceActorKind.ts       |  3 +-
 .../models/workflowVersionSummaryResource.ts       |  6 +-
 .../workflowVersionSummaryResourceActorKind.ts     |  3 +-
 .../workflow-editor/version-panel.svelte           |  4 +-
 .../lib/workflow-editor/version-history.test.ts    | 40 +++++++++++++
 web/src/lib/workflow-editor/version-history.ts     | 25 +++++++-
 19 files changed, 254 insertions(+), 16 deletions(-)
b6d11337 FEAT-s3sfx5: test embed actor signing, recording, and the HTTP flow

 internal/api/embed_actor_test.go        | 177 ++++++++++++++++++++++++++++++++
 internal/repository/embed_actor_test.go | 111 ++++++++++++++++++++
 2 files changed, 288 insertions(+)
04e63f37 FEAT-s3sfx5: sign a host-asserted actor into embed sessions and record it

 internal/api/handlers/actor.go                  |  26 ++++--
 internal/api/handlers/embed.go                  |   2 +
 internal/api/handlers/execution_retry.go        |   5 +-
 internal/api/handlers/executions.go             |   4 +
 internal/api/handlers/workflows.go              |  30 +++++--
 internal/embed/embed.go                         |  59 +++++++++++++
 internal/embed/embed_actor_test.go              | 105 ++++++++++++++++++++++++
 internal/embed/embed_test.go                    |   3 +
 internal/execution/records.go                   |   8 ++
 internal/repository/executions.go               |  11 +++
 internal/repository/models.go                   |  10 +++
 internal/repository/workflow_history.go         |  26 ++++--
 internal/workflow/lifecycle.go                  |   6 +-
 migrations/postgres/000029_embed_actor.down.sql |  17 ++++
 migrations/postgres/000029_embed_actor.up.sql   |  29 +++++++
 migrations/sqlite/000029_embed_actor.down.sql   |  17 ++++
 migrations/sqlite/000029_embed_actor.up.sql     |  29 +++++++
 17 files changed, 362 insertions(+), 25 deletions(-)
```
