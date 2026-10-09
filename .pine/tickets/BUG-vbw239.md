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
- [x] Each of the two operations either gets a client method (named in the SDK README, covered in `OPERATION_COVERAGE`, with an operations test) or a justified `EXCLUSIONS` entry whose reason is a real constraint (for example: the OAuth start is bound to the browser that starts it), and `make sdk-test` passes. Evidence: `convert-workflow-fragment` -> `convertWorkflowFragment` (README Interop row, `OPERATION_COVERAGE`, operations test "converts pasted n8n nodes without saving anything"); `start-credential-oauth` -> `EXCLUSIONS` entry stating the HttpOnly-cookie / Origin / Host browser binding. `make sdk-test`: 156/156 (was 154/155), `make sdk-check` and `make sdk-build` clean.
- [x] sdk/CHANGELOG.md records any added method as Additive. Evidence: Unreleased **Additive** line for `convertWorkflowFragment`.

## Plan (2026-10-09)

Decision per operation, from reading the handlers rather than assuming:

- `convert-workflow-fragment` (`internal/api/handlers/interop.go`, `ConvertFragment`): a pure translator. It runs `n8n.ImportFragment` against the tenant's catalogue, saves nothing, needs only `workflow:read`, and returns nodes, connections and the import report as JSON. A host backend can use it (translate n8n JSON a customer pastes into the host's own UI, then decide whether to save it), exactly as `importWorkflow` and `validateWorkflowDocument` are used. So it gets a client method, `convertWorkflowFragment`, next to `importWorkflow`.
- `start-credential-oauth` (`internal/api/handlers/oauth.go`, `StartOAuth`): the sign-in is bound to the browser that starts it. The response sets an HttpOnly nonce cookie scoped to the callback path, the state carries the nonce hash, and the callback refuses ("this sign-in was not started in this browser") unless that cookie comes back with it. The state also records the starting request's `Origin` as the `postMessage` target and the redirect URI is derived from that request's `Host`. The SDK client is a backend component: its `Transport` reads only the JSON body, so the cookie would land in the host's server process (and be dropped) while the end user's browser, opening the returned `authorizeUrl`, reaches the callback without it. An embed session is refused the whole `/credentials/*` mutation prefix (`internal/api/middleware/scope.go`), so the iframe cannot call it either, and the SDK's browser entry deliberately has no API client. The only caller is the first-party dashboard (`web/src/routes/(dashboard)/credentials/+page.svelte`). So it gets an `EXCLUSIONS` entry stating that constraint.

Changes:
1. `sdk/src/server.ts`: `convertWorkflowFragment(input: ConvertFragmentInputBody, signal?)` -> `POST /workflows/convert`, returning `ConvertedFragmentResource` (types already generated).
2. `sdk/test/operation-coverage.test.mjs`: `'convert-workflow-fragment': 'convertWorkflowFragment'` in `OPERATION_COVERAGE`; `'start-credential-oauth'` in `EXCLUSIONS` with the reason above.
3. `sdk/test/operations.test.ts`: an interop test that the method posts the envelope untouched to `/api/v1/workflows/convert` and resolves the typed result.
4. `sdk/README.md`: name `convertWorkflowFragment` in the Interop row, total 80 -> 81, and a short paragraph saying the Google sign-in start has no method and why.
5. `sdk/CHANGELOG.md`: an **Additive** line under Unreleased.

Proof: the new operations test fails first (no method), then `make sdk-test` goes from 154/155 to green (the new total), plus `make sdk-check` and `make sdk-build`-level typecheck.

## Progress (2026-10-09)

- Baseline reproduced first: `make sdk-test` 154/155 with `operations with no client method and no exclusion: start-credential-oauth, convert-workflow-fragment`.
- `convert-workflow-fragment` got a method. `KilasFlowClient.convertWorkflowFragment(input: ConvertFragmentInputBody, signal?)` posts to `/workflows/convert` and returns the generated `ConvertedFragmentResource`; it sits beside `importWorkflow` in `sdk/src/server.ts`. The operations test was written first and failed with "convertWorkflowFragment is not a function" before the method existed.
- `start-credential-oauth` got no method, verified rather than assumed in `internal/api/handlers/oauth.go`: the response sets an HttpOnly, SameSite=Lax nonce cookie scoped to the callback path, the signed state carries the nonce hash, the callback refuses with "this sign-in was not started in this browser" without that cookie, the state records the starting request's `Origin` as the `postMessage` target, and the redirect URI derives from that request's `Host`. The SDK `Transport` reads only the JSON body, so a host backend would hold the cookie in its own process while the customer's browser reached the callback without it. An embed session is refused every non-GET `/credentials/*` request (`internal/api/middleware/scope.go`), the SDK browser entry has no API client by design, and the only caller in the repo is the dashboard's credentials page. The `EXCLUSIONS` entry says this, and `sdk/README.md` carries a paragraph for readers.
- README total moved 80 -> 81 ("all 81 under `/api/v1` that a backend can drive"); the README test ties that number to `OPERATION_COVERAGE`, and the declared surface is now 81 covered + 1 excluded = 82.
- No deviations from the plan. Not regenerated: nothing under `sdk/src/generated` (the types for both operations already existed).
