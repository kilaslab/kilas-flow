---
id: FEAT-ds4e0m
title: 'live e2e: datastore CRUD + external postgres interaction'
status: done
priority: medium
parent: EPIC-87t47t
created: "2026-09-20T03:25:28Z"
updated: "2026-09-20T03:52:31Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Implemented

`e2e/tests/live-backend-datastore.spec.ts`:

- REST CRUD: create 201 + Location, rename, add/rename/drop column, insert 201,
  GET row, filtered `PUT /rows` `{matched:1}`, upsert `inserted:true` then
  `false`, filtered `DELETE /rows` `{deleted:1}`, empty-filter delete 422 with
  nothing removed, `POST /clear` keeping the schema, keyset row paging without
  overlap, CSV import → export → re-import byte-identical, delete 204 → 404.
- Node CRUD: `datastoreWriteReadDocument` (manual → insert defineBelow → filtered
  get) and `datastoreAutoMapDocument` (manual → Set → insert autoMapInputData);
  the filtered read returns exactly the inserted row's id from the redacted trace;
  the REST filtered read serves the same row; the unmapped field never lands.
- External PostgreSQL (gated on `pgDsn()`): `POST /credentials` with
  `pgCredentialFields(dsn)`, then manual → `kilasflow.postgres`
  (`operation:'query'`, `statement:'SELECT 1 AS ok'`) → succeeded, output carries
  `ok:1`. The DB endpoint is admitted by name through
  `outbound.allowed_private_endpoints` (the harness never sets
  `allow_private_networks`).

## Findings

No defects. The datastore trace redacts cells by design (`{datastore:{ids,rows}}`),
which is why both CRUD proofs assert identifiers, never cell text.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- _(no file changes detected since ticket creation)_
