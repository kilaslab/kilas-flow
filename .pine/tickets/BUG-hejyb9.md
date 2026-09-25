---
id: BUG-hejyb9
title: 'Code node: httpRequest''s error for a non-2xx status does not read as n8n''s'
status: done
priority: low
labels:
    - code-node
    - javascript
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T10:11:00Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. When `this.helpers.httpRequest` gets a 404, its error message is "The request failed with status 404 Not Found". n8n's `httpRequest` (axios underneath) says "Request failed with status code 404", and scripts that match on the message behave differently.

# Acceptance Criteria
- [x] The error thrown for a non-2xx status reads as n8n's does (verify n8n's actual message and error properties black-box or from its source as a behaviour reference, never ported).
- [x] The error's other properties scripts commonly read (status/statusCode, response body) match what n8n exposes, or the difference is documented.

# Notes

## n8n's behaviour (read from its source on 2026-09-25 as a behaviour reference, never ported)

- Where the error comes from. In n8n the Code node's JavaScript always runs in
  the task runner, and `this.helpers.httpRequest` there is not a local
  function: the runner forwards the call to the main process, which runs the
  real helper and sends the outcome back over the broker's WebSocket. The
  real helper hands the request to axios and does not wrap what axios
  throws: there is no NodeApiError for a Code node's helper call. For a status
  axios's default check refuses (anything outside 2xx, unless
  `ignoreHttpStatusErrors` widened it), axios rejects with its own error
  class.
- The message is axios's: "Request failed with status code" and the number,
  with no status text. Its `name` is "AxiosError", and its `code` is
  "ERR_BAD_REQUEST" for a 4xx status and "ERR_BAD_RESPONSE" for anything else
  (a 5xx, or a 3xx when redirects are off).
- What the code receives is not that error but its JSON. The main process
  serialises the rejection to send it, which calls axios's `toJSON`, and the
  runner rejects the code's promise with the parsed object as it arrived. So
  the code catches a plain object, not an `Error` (`err instanceof Error` is
  false), holding `message`, `name`, `stack` (the main process's frames, never
  the code's), `code`, `status` (the number) and `config` (axios's request
  settings, serialised). There is **no `response`**: axios's `toJSON` leaves
  it out, so `err.response` is `undefined` and the response body is not
  reachable from the error. Nor is `statusCode`, `httpCode` or `description`
  there.
- Left uncaught, the runner reports it with the message above and, taken from
  the stack's first line, "AxiosError" as the description; no line number,
  since the stack holds no frame of the code.

## Plan and decisions

1. RED: the helper test for a non-2xx status expects the message, `name`,
   `code` and `status` above and `response` undefined; a new test pins
   `code` by status class and how an uncaught one reads.
2. GREEN: `internal/jsrun/js/modules/helpers.js` builds that error. The
   server half (`HTTPRequest` over the worker protocol) already returns every
   status and is unchanged: the 2xx check is the JavaScript's.
3. Decisions:
   - **It stays an `Error`.** n8n's is a plain object only as an accident of
     crossing its process boundary. An `Error` gives an uncaught one a proper
     report here ("AxiosError: Request failed with status code 404"), where a
     plain object would read "[object Object]". The cost is that
     `err instanceof Error` is true where n8n says false; documented.
   - **`response` is dropped**, matching n8n. A script written against n8n
     that tests `err.response` takes the n8n branch here too. The body of a
     refused status is reachable as n8n documents it: `ignoreHttpStatusErrors:
     true` with `returnFullResponse: true`. Documented.
   - **`config` is not given.** It is axios's internal request settings (its
     adapter list, agents, validator), nothing a script reads, and it would
     carry the request's headers. Documented as a difference.
4. Docs (Helpers section + Differences from n8n) and CHANGELOG (Fixed).
   Corpus: httpRequest is not wired in the corpus instrument, so no outcome
   can move.

## Progress (2026-09-25)

- `helpers.js` rejects a refused status with an `Error` named `AxiosError`,
  message "Request failed with status code N", `code` ERR_BAD_REQUEST (4xx)
  or ERR_BAD_RESPONSE (any other), `status` N, and no `response`. The body is
  no longer decoded for a refused status, since nothing reads it.
- Tests: `TestHTTPRequestAnswersInN8nsShapes` updated;
  `TestAStatusTheRequestDoesNotAcceptRejectsAsN8nsHelperDoes` added (404,
  401 under an `except` list, 500, 302 with redirects off, and an uncaught
  503 failing the node as "AxiosError: Request failed with status code 503").
- Differences documented in the Code (JavaScript) guide: `instanceof Error`
  is true here, false in n8n; no `config`. CHANGELOG Fixed entry added.
- Corpus: `make js-corpus && make js-corpus-check` passes with the baseline
  unchanged (httpRequest is not wired in the instrument).
- Surface: `TestTheScriptSurfaceIsTheReviewedOne` passes unchanged.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `4ee3d1a9` (last commit at or before ticket created 2026-09-25)
- Commits (1):
  - `9507d29c` — BUG-hejyb9: httpRequest rejects a status outside 2xx with n8n's AxiosError, its message, code and status, and no response
- Files changed (the ticket's own commits, 57f6779..worktree-agent-aad405ea2277eae60):

```
 .pine/tickets/BUG-hejyb9.md                     | 78 +++++++++++++++++++++++++++++++++++++++++++--
 CHANGELOG.md                                    |  5 +++
 docs/src/content/docs/guides/code-javascript.md | 15 +++++++--
 internal/jsrun/helpers_test.go                  | 42 ++++++++++++++++++++++--
 internal/jsrun/js/modules/helpers.js            | 23 +++++++++----
 5 files changed, 150 insertions(+), 13 deletions(-)
```
