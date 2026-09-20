---
id: FEAT-j5s2n4
title: 'Importer fidelity program: 4/100 activate, disabled nodes, onError, defaults, silent drops'
status: testing
priority: high
labels:
    - importer
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:47:48Z"
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
