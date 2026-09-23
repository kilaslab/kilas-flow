---
id: BUG-nbymq4
title: HTTP 4xx/5xx errors drop status code, URL and response body (error and error-branch items)
status: todo
priority: high
labels:
    - http
    - error-handling
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
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
- [ ] The node error includes the status code, method, resolved URL (redacted) and a truncated response body
- [ ] Error-output items carry `error.httpCode` (or status) plus the body, in n8n's shape
- [ ] continueRegularOutput keeps the input fields on the error item

# Implementation Plan

Return a structured node error `{message, httpCode, method, url, body (truncated, redacted)}` and copy it into error-output items.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/run_a.json`, `cli/run_h.json`, `a-06-exec-node.png`. nodes/http.go:334-341 (`contents` read, then `request failed with status %d` returned without it).

# Attachments
