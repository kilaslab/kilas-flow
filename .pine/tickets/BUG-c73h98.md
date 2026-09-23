---
id: BUG-c73h98
title: the embedded frame gets 403 on GET /credential-types on every load
status: todo
priority: low
labels:
    - embed
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review. Pre-existing; found while testing the cross-origin save/run fix (BUG-b3p8va).

`GET /api/v1/credential-types` requires `subject.Kind() == kindKey` before it even looks at scopes (internal/api/middleware/scope.go ~149-156): `if subject.Kind() != kindKey { return http.StatusForbidden, subject.Denial("cannot use this endpoint") }`. An embed session's subject is never `kindKey`, so this check refuses it unconditionally, regardless of which scopes the embed session carries. `TestEmbedSentencesAreUnchangedByTheSharedTable` already pins this as expected behavior (`GET /credential-types` → "This embed session cannot use this endpoint.").

The editor iframe requests this endpoint to render credential-typed fields, so every embed session load gets a 403 on it.

# Steps to Reproduce

1. Open the editor inside an embed session (any scope combination).
2. Observe the request the editor makes to `GET /api/v1/credential-types`.

# Expected

Either the embed scope permits the credential-types read the editor needs, or the editor does not request it while running in an embed.

# Actual

`GET /api/v1/credential-types` 403s for every embed session on every load, because the route requires `kindKey` before any scope check runs.

# Acceptance Criteria
- [ ] The embed scope that a workflow-editing session already carries (e.g. `workflow:read`) permits `GET /credential-types`, or the editor stops requesting it when running in an embed
- [ ] `TestEmbedSentencesAreUnchangedByTheSharedTable`'s credential-types case is updated to match the decided behavior
- [ ] A manual or automated check confirms the editor iframe no longer surfaces a 403 for this request in an embed session

# Related Files

internal/api/middleware/scope.go ~149-160 (the `/credential-types` case, gated on `subject.Kind() == kindKey`)
internal/api/middleware/embed_sentences_test.go ~21 (pins the current 403 sentence for this route)
