---
id: BUG-nbymq4
title: HTTP 4xx/5xx errors drop status code, URL and response body (error and error-branch items)
status: done
priority: high
labels:
    - http
    - error-handling
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T15:43:26Z"
---

# Description

`request failed with status 500` is all a user gets. The response body is read and then discarded, and error-branch items carry no status code, so a flow cannot branch on 404 or 429.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-8). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** NodeApiError shows "The service was not able to process your request" / "The resource you are requesting could not be found" and a description with the status and the response body (`500 - {"error":"simulated",…}`), with the request details in the error pane. Items on the error output carry the error object, including the HTTP code, so a flow can branch on 404 or 429.

# Steps to Reproduce

1. Run `[ux-debug] a HTTP 500 chain` (the stub answers 500 with `{"error":"simulated","code":500,"detail":"upstream exploded"}`). 2. Run `[ux-debug] h continue on fail` and inspect the Failure items.

# Expected

The error includes the status code, method, resolved URL and a truncated response body, and error items carry `error.httpCode` or status plus the body.

# Actual

The node error is only `node "Call API": request failed with status 500` (`code: node.failed`). The body is read and then discarded, and the URL and method are not reported. Error-output items are `{"error":{"message":"node \"Call API\": request failed with status 500","node":"Call API"},"u":"status/500"}`, with no status code field and no body. With `continueRegularOutput` the input fields are also missing.

# Acceptance Criteria
- [x] The node error includes the status code, method, resolved URL (redacted) and a truncated response body
- [x] Error-output items carry `error.httpCode` (or status) plus the body, in n8n's shape
- [x] ~~continueRegularOutput keeps the input fields on the error item~~ (dropped 2026-10-08: contradicts n8n, see Decision)

# Implementation Plan

Return a structured node error `{message, httpCode, method, url, body (truncated, redacted)}` and copy it into error-output items.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/run_a.json`, `cli/run_h.json`, `a-06-exec-node.png`. nodes/http.go:334-341 (`contents` read, then `request failed with status %d` returned without it).

# Attachments

## Fix

**Root cause.** `HTTPExecutor.sendOne` read the response body (`contents`) and, for a 4xx/5xx with Never Fail off, returned a plain `fmt.Errorf("node %q: request failed with status %d")` that dropped the body, the method and the URL. The runner's `errorItem` could only write `{message, node}`, because a plain error had nothing else to give it.

**What changed.**
- `nodes/http.go`: a failed status now returns `*nodes.HTTPStatusError{Node, StatusCode, Method, URL, Body, Truncated}`. Its text reads `node "Call API": request failed with status 500 (GET http://host:port): {"error":"simulated",…}`. The URL is `safehttp.RedactURL`, so only the scheme and host are kept, the same rule a transport error follows, because the path and query are where a secret sits (httpQueryAuth writes one into the query). The body is trimmed, made valid UTF-8, cut to `httpErrorBodyLimit` (1 KiB) and marked with `…` when it was cut here or by the policy's read bound. Its `ItemErrorFields()` gives n8n's NodeApiError keys: `httpCode` as text (`"429"`, as n8n writes it), `description` (`"429 - <body>"`), plus `method`, `url`, `body` and `truncated` when cut.
- `internal/engine/runner.go`: `errorItem` reads an optional `itemErrorFields` interface (`ItemErrorFields() map[string]any`) through `errors.As` and merges those fields into the `error` object. `message` and `node` are written last, so the runner keeps ownership of both. This covers continueRegularOutput, continueErrorOutput, per-item and item-outcome paths, because all of them build error items through `errorItem`.
- `internal/engine/secret_scrub.go`: `scrubbedError.As` handles `*itemErrorFields` and returns the fields with every string value scrubbed of the node's resolved credential secrets. Without this, an upstream that echoes the key it was sent ("invalid key …") would put the secret on the error item through the body.

**Tests** (`nodes/http_error_test.go`):
- `TestHTTPRequestStatusErrorNamesTheStatusMethodURLAndBody`: a 500 error names the status, the method, the scheme+host URL and the body. The path and a query secret are withheld, and `errors.As` finds `*nodes.HTTPStatusError`.
- `TestHTTPRequestStatusErrorTruncatesALongBody`: a 64 KiB body is cut, and `Truncated` is set.
- `TestHTTPRequestErrorItemsCarryTheStatusAndTheBody`: through the engine, under both continueRegularOutput and continueErrorOutput, the error item carries `httpCode "429"`, `description`, `method`, `url`, `body` and `node`, and its message and the trace row's error carry the body. The error-output item keeps the input fields.
- `TestHTTPRequestErrorItemFieldsAreScrubbedOfTheCredential`: an httpQueryAuth secret echoed in a 401 body is withheld from every error-item field and from the trace row's error.

Verified with `go test ./nodes/... ./internal/engine/... ./internal/webhook/ ./internal/interop/n8n/` (all ok) and `go vet ./nodes/ ./internal/engine/` (clean).

## Progress

Criteria 1 and 2 are done. Criterion 3 (continueRegularOutput keeps the input fields on the error item) was **not** implemented, and it needs a product decision before anyone builds it:
- n8n's HTTP Request node under continueOnFail / continueRegularOutput pushes `{ json: { error } }` alone. It does not merge the input item's fields, so building this criterion would diverge from n8n.
- The engine pins the current behaviour deliberately. `TestContinueOnFailKeepsTheRunGoingWithErrorItems` (internal/engine/runner_test.go) asserts the input is *not* passed through on the regular output ("a downstream node has to be able to tell a tolerated failure from a success"), and `errorItem`'s doc says the same. Changing it would affect every node, not just HTTP Request.

Recommendation: drop criterion 3, or split it into its own ticket if a departure from n8n is wanted, then close this one.

## Decision (2026-10-08)

Criterion 3 is dropped, not deferred. KilasFlow V2 is n8n-first, and n8n's HTTP Request node under continueOnFail emits `{ json: { error } }` without the input's fields. The engine pins the same behaviour for every node (`TestContinueOnFailKeepsTheRunGoingWithErrorItems`), so a downstream node can tell a tolerated failure from a success. The error item does now carry everything the request knew (`httpCode`, `description`, `method`, redacted `url`, `body`), which was the debugging need behind the criterion. Merged to main with the fix commit db818f4.

