---
id: BUG-pwckhd
title: HTTP Request body expressions sent upstream as literal expression-wrapper JSON
status: testing
priority: critical
labels:
    - http
    - expression
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T01:00:52Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 2 finding(s) from dims: find:core-node-parity, find:expression-parity.

---
### HTTP Request body fields that use expressions are sent as literal {"mode":"expression",...} JSON [find:core-node-parity] (critical/bug) · area: importer / HTTP Request · confidence: high

When an n8n HTTP Request sends a body built from fields (bodyParameters, JSON or form-urlencoded), the importer turns the already-converted expression wrappers into one fixed body string. The {{ }} expressions are never evaluated, so the external API receives the wrapper objects instead of the item's values, and the node still reports success.

Evidence: Cases work/core-node-parity/cases/http_post_keypair_json.json and http_post_form.json (HTTP 4.2, sendBody, bodyParameters [{id:"={{ $json.id }}"},{q:"={{ $json.q }}"}]), both run against the stub on :8094. n8n sent {"id":1,"q":"hello"} and id=1&q=hello. KilasFlow sent {"id":{"mode":"expression","value":"{{ $json.id }}"},"q":{"mode":"expression","value":"{{ $json.q }}"}}, and the form version sent id=%7B%22mode%22%3A%22expression%22... Cause: internal/interop/n8n/parameters.go:687-700 (httpToKilas) calls json.Marshal(namedValues(...)) into a static body string. The expression-parity dimension found the same bug independently (its F2).

n8n behavior: Each body field is evaluated per item and the body is built from the resolved values.

Impact: 12 JSON field-bodies with expressions across 10 of the 100 templates, plus form-urlencoded ones. Wrong data is written to third-party systems with no error.

Suggested fix: Keep bodyParameters as a key/value parameter that is evaluated per item, the way queryParameters and headers already are. Alternatively, build a JSON body template that keeps the {{ }} expressions. Add an import+run regression test.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/http.go

Existing tickets: FEAT-pn3dtq

---
### HTTP Request bodyParameters expressions are sent upstream as literal {"mode":"expression"} JSON [find:expression-parity] (critical/bug) · area: importer / HTTP Request node · confidence: high

When importing an HTTP Request with sendBody plus bodyParameters (JSON or form-urlencoded), the importer json.Marshals the name/value map after the values have already become expression markers. The body is stored as a fixed string, so expressions are never evaluated, and the internal marker is sent to the third-party API. Exporting back to n8n keeps the corruption and reports it as lossless.

Evidence: internal/interop/n8n/parameters.go:690-701: `encoded, _ := json.Marshal(fields); parameters["body"] = string(encoded)`, where fields come from namedValues -> fromN8NValue. Live (t_http.py): bodyParameters t=`={{ $json.x }}`. The KF upstream received rawBody `{"lit":"plain","t":{"mode":"expression","value":"{{ $json.x }}"}}`, while n8n sent `{"t":"hello world","lit":"plain"}`. Query and header parameters resolve correctly. GET /workflows/{id}/export returns `"jsonBody": "{\"t\":{\"mode\":\"expression\",...}}"` with no '=' prefix and `lossy: []`. The import raised no issue.

n8n behavior: Each bodyParameters value is evaluated per item, and the body is encoded as JSON or form data from the resolved values.

Impact: 12 of 13 bodyParameters HTTP nodes in the corpus carry expressions, across 10 templates (2035, 2417, 2557, 2605, 2783, 2878, 3121, 3442, 4110, 5035). The run looks successful while sending wrong payloads (and internal representation) to external APIs.

Suggested fix: Keep bodyParameters as a structured map of (possibly expression) values and let the HTTP executor resolve and encode them at run time, as it already does for query and headers. On export, write them back as bodyParameters. Add an importer test that asserts the resolved body.

Files: internal/interop/n8n/parameters.go, nodes/http.go

## Acceptance criteria

- [ ] HTTP Request body fields that use expressions are sent as literal {"mode":"expression",...} JSON
- [ ] HTTP Request bodyParameters expressions are sent upstream as literal {"mode":"expression"} JSON
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (WebhookParity 2026-09-20)

- Runtime half landed: `kilasflow.httpRequest` now carries a structured `bodyFields` key/value parameter (`nodes/http.go`). The values are resolved per item by `expression.Resolve` (nested maps already recurse), then the executor encodes them — JSON via `json.Marshal` (so a lone expression keeps its native type, matching n8n's `{"id":1,"q":"hello"}`) and form via `url.Values`. An absent/empty `bodyFields` falls back to the existing `body` field, so nothing already saved changes.
- Also added `rawContentType` (default `text/plain; charset=utf-8`) so a raw body keeps n8n's `rawContentType` instead of a hard-coded text/plain.
- Scoped proof (isolated worktree at baseline a91157f + only my files, because siblings were mid-edit): `go test ./nodes/ -run TestHTTPRequest -count=1` → ok. New test `TestHTTPRequestSendsResolvedBodyFieldsRatherThanExpressionWrappers` fails pre-fix (`body = ""`, the wrapper map was never encoded) and passes post-fix, asserting `{"id":1,"lit":"plain","q":"hello"}` and `id=1&lit=plain&q=hello` on the wire.
- Remaining (ImporterTail's file): `httpToKilas` must map `bodyParameters` to `bodyFields` instead of `json.Marshal`-ing into `body`, and `httpToN8N` must write `bodyParameters` back. Contract sent via hub (field names + shapes). Runtime side is complete and independently tested.
