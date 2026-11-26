---
id: FEAT-s3sfx5
title: Actor identity in embed sessions
status: testing
priority: medium
labels:
    - saas
    - embedding
    - audit
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-26T07:35:00Z"
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
