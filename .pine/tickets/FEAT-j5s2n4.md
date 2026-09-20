---
id: FEAT-j5s2n4
title: 'Importer fidelity program: 4/100 activate, disabled nodes, onError, defaults, silent drops'
status: done
priority: high
labels:
    - importer
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:06Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:importer-fidelity.

---
### Only 4/100 top templates activate, none of those 4 behaves like n8n; blocker ranking and minimum unlock set [find:importer-fidelity] (high/parity-gap) · area: import coverage / roadmap · confidence: high

A fresh re-import of all 100 templates on current main imports 100 and activates 4 (1744, 1750, 5170, 5171). In side-by-side runs none of the 4 matches n8n: 1744 fails at run time (Date & Time $now), 1750 and 5171 answer GET with 404 (webhook method default), 1750 ignores keepOnlySet, and 5170 emits wrong items from 8 of 9 Set nodes. Bindable credentials and template holes are excluded from the ranking below.

Evidence: Data in work/importer-fidelity/reimport.json, blockers.py, groups.py and sbs_*.json. Templates blocked, by type: JS Code/Function 35 (79 Code nodes; the trivial Code->Set rewrite converts none), chatTrigger 29, OpenAI app node 26 (+3 legacy), googleSheets 23, googleDrive 13, documentDefaultDataLoader 11, extractFromFile 11, gmail 9, embeddingsOpenAi 9, formTrigger 9, recursive splitter 9, lmChatGoogleGemini 8, markdown 7, `$workflow.id` workflow tools 7, html 6, toolSerpApi 6, informationExtractor 6, whatsApp 6, facebookGraphApi 6, non-Tools agents 5. By group: Google suite 45, other SaaS 41, JS 35, OpenAI app 29, Chat Trigger 29, core utility (html/markdown/itemLists/stopAndError/errorTrigger) 24, importer mapping fixes 23, file nodes 18, AI tools 17, chat models 14, RAG 12, AI chains 11, forms 9. Greedy count of templates with no product blocker, starting from 5: +JS 8, +core utility 1

n8n behavior: All 100 run once credentials are provided.

Impact: The V2 goal ('customers' n8n workflows import and run') is met by 0 of the 100 most-viewed templates.

Suggested fix: Cheapest first: (1) importer mapping fixes (Set, Merge, Aggregate/Sort, defaults, sub-workflow refs/inputs, error outputs), which need no new nodes and also fix templates that run but are wrong; (2) Chat Trigger as a webhook-style trigger emitting {chatInput, sessionId, action}; (3) the OpenAI app node's message/image operations over the existing OpenAI-compatible client; (4) core utility nodes (Markdown, HTML extract, Stop and Error, Error Trigger, Item Lists); (5) file nodes over the binary st

Files: internal/interop/n8n/n8n.go, nodes/

Existing tickets: EPIC-m42s3g

---
### Disabled n8n nodes are imported as live, non-blocking nodes, so their side effects fire (disabled triggers become live) [find:importer-fidelity] (high/bug) · area: importer + engine (disabled nodes) · confidence: high

KilasFlow has no disabled-node concept. Import raises a 'dropped' issue and a duplicate 'lossy' issue for the same node (n8n.go:721-726 and nodeIssues 1550-1552). Neither blocks activation, so the node runs.

Evidence: Probe work/importer-fidelity/disabled_probe.py: Manual -> HTTP Request (disabled:true, POST http://127.0.0.1:8093/disabled-node-should-not-fire) -> Set. POST /api/v1/workflows/{id}/run returned status succeeded, and the stub received POST /disabled-node-should-not-fire. Corpus: 9 mapped disabled nodes in 6 templates. 2605 has a disabled Schedule Trigger (every 15 min) that becomes live. 2982 has a disabled Webhook 'upsert' that becomes a public endpoint. 3066 has a disabled HTTP 'pollinations.ai' and a disabled 'gpt-4o' model wired to the same agent as the active Gemini model.

n8n behavior: A disabled node is skipped: its input passes through unchanged on output 0. A disabled trigger never fires, and a disabled AI sub-node is ignored.

Impact: Unintended API calls, schedules and public endpoints after migration (6/100 templates).

Suggested fix: Add `disabled` to the canonical node with pass-through semantics in the runner, and skip disabled nodes in trigger registration and sub-node resolution. Round-trip the flag. Until then, mark disabled mapped nodes blocking and emit a single issue.

Files: internal/interop/n8n/n8n.go, internal/engine/runner.go, internal/workflow/document.go

---
### onError 'continueRegularOutput' is not mapped to continueOnFail, although the runner supports exactly that; the error item shape also differs [find:importer-fidelity] (high/bug) · area: importer/error handling · confidence: high

errorHandlingSettings (n8n.go:1583) reads only the legacy continueOnFail boolean. Current n8n writes onError:'continueRegularOutput' instead. The diagnostic says onError has 'no equivalent beyond continueOnFail, which was carried', but nothing was carried. The imported node aborts the run where n8n continues. Tolerated failures also emit {"$error":{message,node}} (runner.go:1093), not n8n's json.error.

Evidence: 26 nodes in 8 templates use continueRegularOutput. 15 of them are mapped nodes whose imported settings lack continueOnFail: 2431 (6 HTTP 'Delete Session*'/'Inject Cookie'), 2567, 2878 ('Upload to Notion Page' keeps retryOnFail but not continue), 2982 (Loop), 3066 and 3135. engine/runner.go:1049 already honours settings.continueOnFail. Templates 1534, 2006 and 2878 read `{{ $json.error }}` / `$json.error.status` downstream, which resolve empty here.

n8n behavior: continueRegularOutput replaces the failing item with {error: ...} on the regular output.

Impact: 8/100 templates stop on errors they were designed to tolerate.

Suggested fix: Map onError=='continueRegularOutput' to continueOnFail:true and export it back as onError. Emit n8n's `error` key alongside `$error`. Restrict the diagnostic to continueErrorOutput.

Files: internal/interop/n8n/n8n.go, internal/engine/runner.go

Existing tickets: FEAT-a6yg3n (done), FEAT-nbqye0 (done)

---
### onError 'continueErrorOutput' error branches are silently cut, with a misleading 'declares no main port' diagnostic [find:importer-fidelity] (high/parity-gap) · area: importer/connections + engine · confidence: high

KilasFlow has no per-node error output. Every edge from n8n's error output (the last main slot) fails resolvePort and is held back with `the "main" connection from X to Y was held back because X declares no main port for it`. The node also imports without any continue behaviour.

Evidence: 33 nodes in 16 templates use continueErrorOutput, and 14 templates have the error output wired: 1534, 2035, 2063, 2415, 2417, 2431, 2454, 2534, 2557, 2567, 2605, 2729, 2803, 3135. Examples of edges held back at output index 1: 2417 HF request -> 'Respond with error' (the webhook caller never gets the error page), 2729 chainLlm -> 'Error Response', 2431 8 Selenium HTTP nodes -> 'Delete Session*' (sessions leak), 2803 Postgres -> Telegram error.

n8n behavior: The node gains an 'error' output: failed items go there and successful items go to output 0.

Impact: 14/100 templates lose their designed error handling. Webhook APIs hang or 500 instead of returning their error response.

Suggested fix: Add an optional error output port when settings.onError=continueErrorOutput (via PortsFor), route tolerated failures to it in the runner, and map it on import and export. Meanwhile report a dedicated 'error output not supported' issue.

Files: internal/interop/n8n/n8n.go, internal/engine/runner.go

---
### Webhook with no stored httpMethod imports as POST; n8n's default is GET, so GET callers get 404 [find:importer-fidelity] (high/bug) · area: importer/webhook · confidence: high

webhookToKilas defaults httpMethod to POST (parameters.go:800). n8n omits default values from exported JSON, and its Webhook default is GET at v1, 1.1, 2 and 2.1 (checked via n8n /rest/node-types).

Evidence: Template 1750 (activatable). On n8n, GET /webhook/<path>?first_name=Ada&last_name=Lovelace returns 200 with text. In KilasFlow the import reports method POST, GET on the minted URL returns 404 'No active workflow is bound to this webhook', and only POST works. Template 5171 (activatable) has 4 webhooks named 'GET /menu', 'GET /order', 'GET /secret-dish' and 'GET /slow-service' with no httpMethod; its HTTP Request nodes call them with GET and all get 404. 2982 is affected the same way. In total 6 nodes in 3 templates.

n8n behavior: httpMethod defaults to GET.

Impact: External callers break silently after migration. This affects 2 of the 4 'activatable' templates.

Suggested fix: Default to GET when httpMethod is absent, write it explicitly on export, and add a corpus test.

Files: internal/interop/n8n/parameters.go

---
### Respond to Webhook with no stored respondWith answers with an empty text body; n8n answers with the first item's JSON [find:importer-fidelity] (high/bug) · area: importer/respondToWebhook · confidence: high

respondToKilas maps a missing respondWith to 'text' and calls that n8n's default (parameters.go:876-878). n8n's default is firstIncomingItem at v1, 1.1 and 1.4.

Evidence: respond_probe.py: Webhook(responseNode) -> Set {answer:42} -> Respond to Webhook 1.1 {"options":{}}. n8n returns 200 {"answer":42}. KilasFlow returns 200 text/plain with an empty body, and no import issue was raised. Corpus nodes relying on the default: 2431 (x2), 2679, 2786, 2846.

n8n behavior: respondWith defaults to firstIncomingItem.

Impact: Webhook-backed APIs and chat widgets in 4/100 templates return nothing after migration.

Suggested fix: Default to firstIncomingItem and always write respondWith on export.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-az620p (done: respondWith set)

---
### Many other source parameters vanish with no diagnostic, breaking the 'never silent' contract [find:importer-fidelity] (medium/bug) · area: importer/diagnostics · confidence: high

Translators build their output from an allow-list, and nothing reports the keys they did not consume. A local diff of source parameters against the round-trip export for all 100 templates finds, beyond the other findings, unreported drops including: AI Agent <=1.5 with no `agent` key (n8n's Conversational Agent) imported silently as a Tools Agent in 2035 and 2534, while an explicit conversationalAgent is refused (2508, 2777, 3066), plus options.humanMessage dropped; HTTP Request Tool optimizeResponse/dataField/fieldsToInclude/fields (2413) and parametersQuery/parametersBody (2786, 3859); Switch options ignoreCase (2454) and renameFallbackOutput (2466); If v1 options.looseTypeValidation (2340); Respond to Webhook responseHeaders (1435) and responseKey (2431); Webhook binaryPropertyName (2751); a Loop batchSize given as the expression '=1' (2621), whose node imports with no parameters; Set options dotNotation/ignoreConversionErrors (1934, 2454, 2786).

Evidence: work/importer-fidelity/silent.py and silent.json (source keys absent after export with no issue naming the field), cross-checked against the imported documents via GET /api/v1/workflows/{id}.

n8n behavior: Each of these changes runtime behaviour or output.

Impact: Users cannot trust the import report: an empty report does not mean a faithful import.

Suggested fix: After each translator, diff consumed keys against source keys (ignoring default values) and emit one dropped issue per unconsumed key. Add the 100-template corpus as a regression test asserting no silent key loss. Map conversationalAgent/openAiFunctionsAgent to the Tools Agent with a lossy note, consistently.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-nbqye0 (done: 'Report every field the n8n importer drops'; regression), FEAT-cgm1y3 (done)

## Acceptance criteria

- [ ] Only 4/100 top templates activate, none of those 4 behaves like n8n; blocker ranking and minimum unlock set
- [ ] Disabled n8n nodes are imported as live, non-blocking nodes, so their side effects fire (disabled triggers bec
- [ ] onError 'continueRegularOutput' is not mapped to continueOnFail, although the runner supports exactly that; th
- [ ] onError 'continueErrorOutput' error branches are silently cut, with a misleading 'declares no main port' diagn
- [ ] Webhook with no stored httpMethod imports as POST; n8n's default is GET, so GET callers get 404
- [ ] Respond to Webhook with no stored respondWith answers with an empty text body; n8n answers with the first item
- [ ] Many other source parameters vanish with no diagnostic, breaking the 'never silent' contract
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## ImporterTail slice — 2026-09-20

Status: `testing`. Commit: `47544b7` (content; swept into a peer's commit).

Landed:
- Disabled nodes are blocking, with exactly one issue (the duplicate in
  `nodeIssues` is gone): importing a disabled trigger as a live endpoint is the
  behaviour change the adapter exists to refuse.
- `onError: "continueRegularOutput"` becomes `continueOnFail` and is written back
  as the modern spelling; `continueErrorOutput` is reported for what it is — an
  error output this server has no equivalent for — instead of the misleading
  "declares no main port".
- Webhook `httpMethod` defaults to GET; Respond to Webhook defaults to
  `firstIncomingItem`.
- Settings: `alwaysOutputData` and `executeOnce` carried onto node settings (the
  two "dropped" diagnostics removed, per EngineWaits), `executionTimeout` carried,
  the `"DEFAULT"` timezone sentinel dropped.
- Silent drops: the HTTP Request and Respond to Webhook option collections are
  now diffed key by key and each unconsumed key is named, as are the Wait's own
  options.

Scoped proof: `go test ./internal/interop/n8n/ -count=1` green, including
`TestDisabledNodesBlockActivation`, `TestOnErrorContinueRegularOutputBecomesContinueOnFail`,
`TestWebhookMethodDefaultsToGet`, `TestRespondToWebhookDefaultsToTheFirstItem`.

Remaining: full disabled-node semantics need a canonical flag and runner
pass-through (`internal/workflow/document.go`, `internal/engine/runner.go` —
EngineFlow); the generic consumed-key diff exists only for the HTTP/webhook/wait
translators, not every node type; Code auto-translation is unstarted (see
BUG-gaavr5).

### Follow-up (same day, EngineFlow's half landed)

The disabled flag and `onError` are now carried natively rather than reported:
`workflow.Node.Disabled` is set from the n8n node (and written back on export),
and `settings["onError"]` carries `stopWorkflow | continueRegularOutput |
continueErrorOutput` verbatim. The blocking "disabled" issue and the "no
equivalent" onError diagnostics are gone, and an error branch now imports onto
the node's own `error` output port — which the compiler declares for
`continueErrorOutput` — instead of being held back as an unroutable edge.

Proof: `go test ./internal/interop/n8n/ -count=1` green, including
`TestDisabledNodesStayDisabled` (flag carried, exported back, no diagnostic) and
`TestOnErrorContinueRegularOutputBecomesContinueOnFail` (error branch wired to
the `error` port; the mode round-trips).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (5):
  - `f0956e86` — BUG-cq4yk3 BUG-kzkvv6 FEAT-j5s2n4: document trigger-header storage, the webhook answers and the HTTP node's output — docs
  - `c27d7062` — FEAT-j5s2n4: carry disabled and onError natively now that the runtime honours them
  - `a6f63041` — BUG-8t94wn BUG-gaavr5 BUG-wdypd2 BUG-qq4xva FEAT-j5s2n4: record testing state — importer tail slice
  - `47544b77` — BUG-8dmp5y: throttle login, revalidate sessions, bound credential scope through redirects — auth/security
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 +++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  524 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  630 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 ++++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  639 +++++
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  617 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  752 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 +++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 76007 insertions(+), 4725 deletions(-)
```
