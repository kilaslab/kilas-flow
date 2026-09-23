---
id: BUG-2z8geh
title: 422 validation errors echo the whole request body, including credential secrets, into the response/envelope
status: done
priority: high
labels:
    - cli
    - security
    - api
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T05:02:38Z"
---

# Description

A failed `credential create` prints the submitted token or password back into the CLI envelope. That output lands in terminals, CI logs and agent transcripts.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n8n's public API returns `{"message":"request/body must have required property 'data'"}` without echoing values.

# Steps to Reproduce

1. `kilasflow api create-credential --body '{"name":"x","type":"httpHeaderAuth","data":{"name":"X-Api-Key","value":"s3cr3t-cli-skills"}}'`. 2. `… --body '{"name":"x","type":"httpBearerAuth","secret":{"token":"TOPSECRET-123"}}'`.

# Expected

The problem document never echoes body values for secret-bearing operations, or the CLI redacts `fields`, `token`, `password` and `value` before printing.

# Actual

Exit 2, and `error.detail.problem.errors[].value` holds the entire body, `"value":"s3cr3t-cli-skills"` / `"TOPSECRET-123"` included, twice per response. It lands in the agent's transcript and in MCP tool results, while the credentials skill's non-negotiable 1 says a secret "never appears in … chat". A field-level error (`allowedDomains` wrong type) echoes only that field. The leak is when the location is `body`.

# Acceptance Criteria
- [x] Problem documents never echo body values for secret-bearing operations (credentials, auth, API keys)
- [x] The CLI also redacts `fields`, `token`, `password` and `value` before printing, as defence in depth
- [x] A test posts an invalid credential and asserts the secret does not appear in the response

# Implementation Plan

Strip `value` from huma validation errors on credential routes, or globally when location == `body`. Extend the CLI's redactor to problem `errors[].value`.

# Notes

Related (from the audit): none

# Related Files

cs/leak1.json, cs/b-cred-out.json (first attempt, in the transcript).

# Attachments

## Progress

API side. `internal/api/problems.go` wraps `huma.NewErrorWithContext` once per
process (`installProblemRedaction`, a `sync.Once`, called first thing in
`NewServer`), which is the constructor huma uses for a validation or parse
failure. Each detail's value is dropped when its location is `body` (the raw
bytes of a body that does not parse), when it is an object or a list (huma's
missing- and unexpected-property errors carry the whole surrounding object),
or always on an operation registered with `Metadata["sensitiveBody"] = true`
(`handlers.SensitiveBodyKey`): create-credential, update-credential,
test-credential-payload, login, create-tenant-user, set-tenant-user-password,
create-api-key and create-tenant-api-key. Messages and locations are kept.
Problems a handler builds itself (compile issues, idempotency, the datastore
conflict) never pass through that constructor and keep their values.

Tests in `internal/api/problems_test.go`:
`TestAnInvalidCredentialIsNeverEchoedIntoTheProblem` posts the audit's two
bodies and a malformed-JSON body and asserts neither secret is anywhere in
the response; `TestASecretBearingOperationEchoesNoValueAtAll` sends a scalar
secret to each marked operation; `TestAProblemKeepsAFieldsValueButNeverTheWholeBody`
pins that an unmarked operation still names the refused field's own value.
All three failed with the leak before the fix and pass after; the full
`internal/api/...` suites pass, and `make generate-api-reference-check`
reports no drift (Metadata is not part of the document).

CLI side (defence in depth against a server from before the fix, or a proxy).
`redactProblem` in `internal/cli/client.go` now always decodes the problem
(with `UseNumber`, so surviving values are carried exactly). An `errors[]`
value at the location exactly `body` is dropped whatever its type: that is
where huma put both the whole object and the raw body string. Any other
`errors[]` value is kept but key-redacted at any depth with the credential
keys plus `fields` and `value`, and the rest of the document with the
credential keys plus `fields`. The client's own token is still redacted
wherever it appears. Neither server-built problem an agent is told to read
uses location `body`: compile issues are `body` + a non-empty JSON pointer
(every `ValidationError` carries a `Path`), and idempotency uses
`header.Idempotency-Key`. So their `{code,nodeId,connectionId}` and `{code}`
values come through whole.

Tests: `TestAPINeverCarriesAnEchoedSecretInTheProblem`
(`internal/cli/verbs_api_test.go`) drives `api create-credential` and reads
the printed envelope. A whole credential and a raw body at `body` lose their
value, a field-level object loses `value`/`Token`/`fields`/`PASSWORD`/`api_key`
but keeps its other keys, and a compile issue and an idempotency code stay
intact. `TestMCPToolResultNeverCarriesAnEchoedSecret`
(`internal/cli/mcp_test.go`) asserts the same through an MCP tool result.
The leak cases failed before the change and pass after.
`TestProblemDocumentsAreRedactedBeforeTheyAreCarried` still passes. The CLI
reference's envelope section no longer says the problem is carried verbatim.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (3):
  - `4caa13fc` — BUG-2z8geh: the CLI never carries a body echoed into a problem, nor a secret-named field inside one
  - `fcd92daa` — BUG-2z8geh: a refused request's problem never echoes the body back, nor any value on an operation that carries a secret
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 +++
 .pine/tickets/BUG-15st2k.md                        | 162 +++++++
 .pine/tickets/BUG-2eryxn.md                        | 346 +++++++++++++++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        | 102 +++++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 +++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 +++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  51 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 ++++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 +++
 .pine/tickets/BUG-nbymq4.md                        |  52 +++
 .pine/tickets/BUG-ngt25j.md                        |  52 +++
 .pine/tickets/BUG-nn74ph.md                        |  51 +++
 .pine/tickets/BUG-nzy3pa.md                        | 186 ++++++++
 .pine/tickets/BUG-p334yw.md                        |  53 +++
 .pine/tickets/BUG-p3j233.md                        |  53 +++
 .pine/tickets/BUG-phv0r9.md                        |  56 +++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 +++
 .pine/tickets/BUG-q6b75c.md                        |  52 +++
 .pine/tickets/BUG-r1m83f.md                        | 347 +++++++++++++++
 .pine/tickets/BUG-rbask0.md                        | 140 ++++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 +++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 +++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 ++++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 +++
 .pine/tickets/BUG-xam6t8.md                        | 179 ++++++++
 .pine/tickets/BUG-y38bss.md                        | 342 +++++++++++++++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 +++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 +++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 +++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 +++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 +++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 +++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 +++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 +++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 +++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 +++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 +++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 +++
 .pine/tickets/FEAT-5fx926.md                       |  57 +++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 +++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 +++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 +++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 +++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 +++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 ++++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 +++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 ++++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 +++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 +++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 +++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 +++
 .pine/tickets/FEAT-m7aw75.md                       |  54 +++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 +++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 +++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 ++++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 +++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 +++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 +++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 +++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 +++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 +++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 +++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 +++
 .pine/tickets/FEAT-tjcr13.md                       |  52 +++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 ++++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 +++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 +++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 +++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 +++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++--------------
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/reference/cli.md             |  48 ++-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/admin.go                     |   9 +-
 internal/api/handlers/auth.go                      |   4 +-
 internal/api/handlers/credentials.go               |   5 +-
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/api/handlers/problem.go                   |  16 +
 internal/api/problems.go                           |  97 +++++
 internal/api/problems_test.go                      | 171 ++++++++
 internal/api/server.go                             |   1 +
 internal/cli/cli.go                                |  22 +
 internal/cli/client.go                             |  93 +++-
 internal/cli/guard_test.go                         | 144 ++++++-
 internal/cli/mcp.go                                |  44 +-
 internal/cli/mcp_test.go                           | 317 ++++++++++++--
 internal/cli/openapi.go                            |  38 +-
 internal/cli/openapi_contract_test.go              |   8 +-
 internal/cli/verbs_api.go                          |  59 ++-
 internal/cli/verbs_api_test.go                     | 232 ++++++++++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 internal/engine/approval.go                        |   2 +-
 internal/mcp/server.go                             |  32 +-
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 ++
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 ++
 .../api/generated/models/aIModelStartedEvent.ts    |  24 ++
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 ++
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 ++
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 ++
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 ++
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++++---
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 +++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 +++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 +++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 +++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 270 files changed, 16980 insertions(+), 634 deletions(-)
```
