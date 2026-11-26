---
id: BUG-ykxfbe
title: 'PostgreSQL: a key label or email longer than 64 characters makes every save and publish fail (created_by / publish actor are varchar(64))'
status: todo
priority: medium
labels:
    - persistence
    - postgres
created: "2026-11-26T07:41:56Z"
updated: "2026-11-26T07:41:56Z"
---

# Description

`workflow_versions.created_by` and `workflow_publish_events.actor` are `varchar(64)` on PostgreSQL (migrations/postgres/000002_workflow_history.up.sql; `size:64` in internal/repository/models.go). `requestActor` (internal/api/handlers/actor.go) writes the raw actor label into them through `appendVersion` / `appendPublishEvent` (internal/repository/workflow_history.go). That label is an API key's label (allowed up to 255 characters by internal/api/handlers/auth.go and admin.go) or a user's email (users.email is size 255).

Failure scenario (PostgreSQL only; SQLite ignores varchar length): a key whose label is longer than 64 characters gets `value too long for type character varying(64)` and a 500 on every save, publish, restore, activate or deactivate it performs. Found by the FEAT-s3sfx5 review (2026-11-26) by reading the code; not reproduced against a live PostgreSQL yet. The embed actor path added by FEAT-s3sfx5 caps display names at 64, so it is not affected.

# Acceptance Criteria
- [ ] A regression test on PostgreSQL (eachDriver with KILASFLOW_TEST_POSTGRES_DSN) saves a revision and publishes with a 255-character key label and a 255-character user email.
- [ ] Either widen both columns to varchar(255) in a new migration for both dialects (model tags updated so the AutoMigrate drift test stays green), or truncate only the legacy copy rune-safely; the full label stays in `actor_label`.

# Notes

Related: FEAT-s3sfx5 (embed actor identity).
