---
id: BUG-vbw239
title: 'make sdk-test is red: start-credential-oauth and convert-workflow-fragment have no SDK method and no exclusion'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:00:21Z"
updated: "2026-10-09T12:00:21Z"
---

# Description

`make sdk-test` is red on main. `sdk/test/operation-coverage.test.mjs` ("covers every operation the server declares") boots a real binary, reads its OpenAPI document and requires every operation id to have a `KilasFlowClient` method in `OPERATION_COVERAGE` or a justified entry in `EXCLUSIONS`. Two operations the server declares have neither: `start-credential-oauth` (POST /credentials/{id}/oauth/start) and `convert-workflow-fragment` (POST /workflows/convert). Both are listed in docs/src/content/docs/reference/api-contract.md.

Found while reviewing Track E of the 2026-10-09 parallel wave; reproduced on main 54a3ee92 and on the Track E worktree identically.

# Steps to Reproduce

1. `make sdk-test`

# Expected

155/155 pass.

# Actual

154/155: `operations with no client method and no exclusion: start-credential-oauth, convert-workflow-fragment`.

# Acceptance Criteria
- [ ] Each of the two operations either gets a client method (named in the SDK README, covered in `OPERATION_COVERAGE`, with an operations test) or a justified `EXCLUSIONS` entry whose reason is a real constraint (for example: the OAuth start is bound to the browser that starts it), and `make sdk-test` passes.
- [ ] sdk/CHANGELOG.md records any added method as Additive.
