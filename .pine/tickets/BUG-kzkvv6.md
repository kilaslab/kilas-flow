---
id: BUG-kzkvv6
title: HTTP Request output envelope vs n8n parsed-body items; import drops options/bodies
status: todo
priority: high
labels:
    - http
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 2 finding(s) from dims: find:core-node-parity.

---
### HTTP Request output is always a {statusCode, headers, truncated, body} envelope instead of n8n's parsed-body items [find:core-node-parity] (critical/parity-gap) · area: HTTP Request executor · confidence: high

KilasFlow's HTTP node wraps every response in an envelope. By default n8n emits the parsed response body as the item, splits a top-level array into separate items, and puts text in `data`. As a result, every downstream `$json.<field>` after an imported HTTP node resolves to nothing.

Evidence: Case http_resp_array (GET returning [{id:1},{id:2},{id:3}]): n8n gives 3 items {id:1},{id:2},{id:3}. KilasFlow gives 1 item {body:[...],headers:{Content-Length,...},statusCode:200,truncated:false}. Case http_resp_text_auto: n8n {data:"hello plain text"}, KilasFlow {body:"hello plain text",...}. Case http_resp_204: n8n {}, KilasFlow the envelope. Case http_resp_full (fullResponse): n8n lower-cases header names (content-type), KilasFlow keeps canonical case (Content-Type) and has no statusMessage. Code: nodes/http.go:289-300 (sendOne always builds the envelope).

n8n behavior: The default response is the parsed JSON object as the item, a top-level array becomes one item per element, text goes to `data`, and an empty body gives {}. The {body, headers, statusCode, statusMessage} shape appears only with options.response.response.fullResponse=true.

Impact: Affects nearly all 199 HTTP Request nodes in 54 of the 100 templates wherever a later node reads the response.

Suggested fix: Make the default output n8n-compatible and emit the envelope only when fullResponse is set, with header names lower-cased. Keep the current envelope as an explicit KilasFlow-native option if it is wanted.

Files: /Users/izzadev/projects/k-flow/nodes/http.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go

Existing tickets: FEAT-pn3dtq

---
### HTTP Request import silently drops options, JSON query/headers, raw and multipart bodies [find:core-node-parity] (high/bug) · area: importer / HTTP Request · confidence: high

httpToKilas never reads `options`. Timeout, neverError, responseFormat, fullResponse, redirect, pagination, batching and allowUnauthorizedCerts are all lost without an issue. It also ignores specifyQuery/specifyHeaders=json, and for raw and multipart bodies it sends an empty body.

Evidence: - http_timeout (timeout 500 ms, stub sleeps 3 s): n8n errors; KilasFlow succeeds after 3 s.
- http_err_404_never (response.neverError): n8n returns a success item; KilasFlow fails 'request failed with status 404', although kilasflow.httpRequest has a neverError option.
- http_pagination: n8n 3 pages / 3 items; KilasFlow 1.
- http_redirect_nofollow: n8n returns the 302; KilasFlow follows it.
- http_query_json and http_headers_json: KilasFlow sends no query string and no X-Json header (stub echo).
- http_post_raw (body '=raw {{ $json.q }}', rawContentType text/plain): n8n sends 'raw hello'; KilasFlow sends an empty body with content-length 0.
- http_post_multipart: n8n sends multipart form-data; KilasFlow sends an empty text/plain body.
Code: internal/interop/n8n/parameters.go:669-720.

n8n behavior: All of these options and body types are honoured.

Impact: Options appear on 30+ HTTP nodes in the templates (response 21, redirect 5, batching 4, timeout 3, allowUnauthorizedCerts 4). Raw bodies: 9 nodes in 5 templates. Multipart: 13 nodes in 7 templates.

Suggested fix: Map timeout to requestTimeoutSeconds, and carry neverError, responseFormat, fullResponse, redirect, jsonQuery/jsonHeaders, the raw body text and rawContentType. Report pagination, batching, multipart and binary bodies as lossy or blocking until they are supported.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/http.go

Existing tickets: FEAT-pn3dtq, FEAT-nbqye0

## Acceptance criteria

- [ ] HTTP Request output is always a {statusCode, headers, truncated, body} envelope instead of n8n's parsed-body i
- [ ] HTTP Request import silently drops options, JSON query/headers, raw and multipart bodies
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)