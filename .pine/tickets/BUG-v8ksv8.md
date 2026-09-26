---
id: BUG-v8ksv8
title: Unknown /api/* paths return 200 with the SPA HTML instead of a JSON 404
status: done
priority: medium
labels:
    - api
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-26T16:43:55Z"
---

# Description

A client, the CLI's `api` verb or an agent sees success, and then fails trying to parse HTML.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Unknown /rest and /api/v1 routes return 404 JSON.

# Steps to Reproduce

1. `curl -i http://127.0.0.1:18080/api/v1/nonexistent`. 2. `curl -i http://127.0.0.1:18080/api/v1/schedules/sched_x` (GET on a single schedule isn't implemented).

# Expected

`404 application/problem+json` for anything under /api/.

# Actual

Both return `200 text/html; charset=utf-8` with the SvelteKit index.html, and so does /api/v2/foo. A client, the CLI's `api` verb or an agent sees success and then fails parsing HTML.

# Acceptance Criteria
- [x] Anything under `/api/` that doesn't match a route returns `404 application/problem+json`
- [x] A test covers /api/v1/nonexistent and /api/v2/foo

# Implementation Plan

Exclude the /api/ prefix from the SPA fallback handler and return a problem+json 404.

# Notes

**2026-09-26: fixed.**
- `internal/web/embed.go`: the SPA handler is the router's `/*` catch-all, registered after every API route, so a request under `/api` that reaches it matches no route. It now answers `404 application/problem+json` (`title`, `status`, `detail`, the shape the hand-written problems elsewhere use) for `/api`, `/api/` and everything below, for every method, ahead of the method check. Before, a POST got a text `405` with `Allow: GET, HEAD`, which pointed a client at a page that does not exist. The security headers are still applied, and the path is matched after cleaning, as the asset lookup does.
- A method the API does not serve on a path it does serve (a POST to a GET-only route) also falls to the catch-all in chi, so it now answers the same 404 rather than a 405; the detail says no route matches "this method and path". A real 405 with an `Allow` list would need the router's own method table.
- `internal/web/embed_test.go` covers `/api/v1/nonexistent` and `/api/v2/foo` (GET, HEAD, POST, DELETE), `/api`, `/api/` and `//api/...`, and pins that `/workflows`, `/apiary`, `/apis/v1` and the embed routes are still the SPA and a missing `.js` is still a plain 404.
- `TestTheSPACatchAllAnswersAnUnknownAPIPath` in `internal/cli` pinned the old 200 text/html and is now `TestAnUnknownAPIPathAnswersAProblemNotThePage`. The CLI keeps refusing an unknown operation id before it sends anything: a wrong path and a missing resource are now both a 404. The comments, `docs/.../reference/cli.md` and `scripts/smoke-cli.sh` that described the 200 HTML are updated.

Related (from the audit): none

# Related Files

curl output in this report; the SPA fallback in internal/web.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-26. Rewritten on 2026-09-26 to list only this ticket's own commit (`git log --grep BUG-v8ksv8`) and the files exactly that commit changed. The chore(pine) commit that records this closing changes only this ticket file.

- Commits (1):
  - `ee185c8a` — BUG-v8ksv8: an unknown /api path answers a 404 problem document instead of the editor's page
- Files changed by that commit:

```
 .pine/tickets/BUG-v8ksv8.md            | 10 +++-
 CHANGELOG.md                           |  4 ++
 docs/src/content/docs/reference/cli.md | 15 +++---
 internal/cli/command_test.go           |  4 +-
 internal/cli/openapi.go                | 16 +++---
 internal/cli/openapi_contract_test.go  | 33 ++++++------
 internal/cli/verbs_api_test.go         | 12 +++--
 internal/cli/verbs_datastore.go        |  5 +-
 internal/cli/verbs_workflow.go         |  4 +-
 internal/web/embed.go                  | 43 +++++++++++++--
 internal/web/embed_test.go             | 95 ++++++++++++++++++++++++++++++++++
 scripts/smoke-cli.sh                   | 10 ++--
 12 files changed, 199 insertions(+), 52 deletions(-)
```
