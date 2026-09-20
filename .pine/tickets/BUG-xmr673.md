---
id: BUG-xmr673
title: Datastore fleet migration runner is never started, and rows refuse any version mismatch
status: todo
priority: high
labels:
    - datastore
    - storage
    - correctness
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T05:26:52Z"
---

## Problem

The per-datastore schema migration runner exists and nothing constructs it, while the row
store refuses to serve a datastore whose `schema_version` differs from the current one. The
first bump of `CurrentSchemaVersion` therefore takes every existing datastore offline with no
mechanism to migrate it — in an embedded deployment, that is a host's customer data going
dark after a routine upgrade.

## Evidence

- `internal/datastore/fleet.go:36-40` states it in the code: nothing starts a runner, the
  composition root does not construct one, and readiness does not report the fleet's version
  spread.
- `internal/datastore/rows.go:78-87` (`checkSchemaVersion`) refuses row operations on a
  mismatch; `refuseAhead` exists in the runner.
- `VersionSpread` (`fleet.go:148`) has no HTTP consumer.
- Repo-wide, the only callers of `NewFleetRunner` are tests.

## Acceptance criteria

- [ ] The runner starts at boot after `database.Migrate`, in the composition root, and its
      failure refuses boot the way a failed schema migration does.
- [ ] `GET /api/v1/ready` (or a documented operations endpoint) reports the version spread,
      and reports not-ready while a migration is outstanding.
- [ ] A test creates a datastore at the previous schema version, starts the runner, and proves
      the datastore is migrated and serves rows afterwards — instead of refusing traffic.
- [ ] A datastore ahead of the binary is still refused, with a diagnostic naming it.

## Out of scope

Adding a second schema version. This ticket makes the existing machinery real; the version
stays at 1.
