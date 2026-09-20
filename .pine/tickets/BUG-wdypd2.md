---
id: BUG-wdypd2
title: 'Shipped-but-unreachable: $env, Wait defaults, importer hacks, Telegram/GOWA, durable waits'
status: testing
priority: high
labels:
    - importer
    - engine
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:47:48Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 10 finding(s) from dims: find:unfinished-work.

---
### Importer maps n8n Wait v1.1 with the default unit to hours instead of seconds and drops the default amount of 5 [find:unfinished-work] (high/bug) · area: importer / Wait · confidence: high

waitToKilas fills in unit='hours' for every typeVersion. For v1.1, n8n's defaults are amount=5 and unit=seconds. So an n8n 'Wait 3 seconds' node that relies on the default unit becomes a 3-hour wait. A node with no amount becomes a zero wait. The import reports no issue for either.

Evidence: internal/interop/n8n/parameters.go:1313-1315: `parameters["unit"] = defaultString(..., "hours")`, and amount is copied as-is (nil when n8n relied on the default). Live n8n 2.33.7 /types/nodes.json for Wait: v1 amount=1, unit=hours; v1.1 amount=5, unit=seconds. Live KilasFlow: I imported wf_01a0b8e6... with Wait v1.1 {amount:3} and Wait v1.1 {unit:'minutes'}. The result was kilasflow.wait {amount:3, unit:'hours'} and {amount:null, unit:'minutes'}, and unsupported[] was empty. Running it (exec_01a0b8e6-8153-796a-8131-b086cdaf9fab) failed with 'a wait of 3h0m0s is longer than this server's limit of 1h0m0s'. A null amount becomes 0, so there is no wait at all.

n8n behavior: Wait v1.1 defaults to amount 5 and unit seconds; v1 defaults to 1 hour.

Impact: Five of the 100 templates (7 nodes, all v1.1) are silently changed: 5171 'Wait 3 seconds' {amount:3}, 4846 'Wait 60 sec.' {amount:60}, 2567 {amount:2}, 4352 {amount:10}, and 2557 {amount:30}x2 plus one {unit:minutes} with no amount. Waits of an hour or less run 3600x longer (2 s becomes 2 h). Longer ones fail at run time.

Suggested fix: Pick defaults by typeVersion (v1: 1 hour; v1.1 and later: 5 seconds) and fill in amount when absent. Add import and export round-trip tests for both versions.

Files: internal/interop/n8n/parameters.go, nodes/wait.go

Existing tickets: FEAT-q81bq4

---
### The generic importer contains a customer-specific hack: JS Code nodes matching a few keywords are replaced by a hard-coded 8-field Set, reported only as lossy [find:unfinished-work] (high/bug) · area: importer / Code node · confidence: high

trivialcode.go was written for one customer workflow (AGL 'Normalize Message'). Any JavaScript Code node whose source contains certain words and 'return [{ json:' is replaced by fixed assignments, whatever the JS actually computes. The import reports a single lossy note, so the workflow activates and produces different data.

Evidence: internal/interop/n8n/trivialcode.go:12-80. The rule matches when the source contains '.body' and the words payload, chat_id, event, device_id, message_id, sender and is_group, and none of the banned tokens. It then emits message_id={{$json.body.payload.id}}, sender={{...payload.from}}, sender_name, body, and is_group=chat_id.endsWith("@g.us"). Live: wf_01a0b8f3-5e72-7733-adb2-7f6e77ba2257 has a Code node returning message_id=b.payload.message_id, sender=b.payload.sender, is_group=b.payload.is_group==='yes' and text=b.payload.text.toUpperCase(). It imported as kilasflow.set with the hard-coded 8 fields: wrong sources, `text` dropped, `sender_name`/`body` invented. The only issue raised was lossy ('only field-normalised the incoming item'). The import dialog promises 'Unsupported nodes arrive as visible placeholders, never silent remaps' (web/src/routes/(dashboard)/app/workflows/import-dia

n8n behavior: n/a (it is the source JS that runs).

Impact: Any customer WhatsApp-style normaliser with overlapping field names is silently rewritten into different logic and still activates. This also undermines trust in lossy severity.

Suggested fix: Remove this from the generic importer, or put it behind an explicit opt-in rewrite profile. Alternatively, parse the returned object literal and rewrite only when every property maps 1:1; otherwise keep the blocking Code placeholder.

Files: internal/interop/n8n/trivialcode.go, internal/interop/n8n/n8n.go

Existing tickets: FEAT-9pe65j, FEAT-c32499, EPIC-3844bz

---
### GOWA importer turns unmapped operations into a different live action (send image or revoke becomes POST /send/message), reported only as lossy [find:unfinished-work] (high/bug) · area: importer / GOWA (packs/gowa) · confidence: high

There is no GOWA pack. The importer maps the GOWA community node onto kilasflow.httpRequest. For resource/operation pairs it does not know, it falls back to POST /send/message (under send/message) or GET /app/devices, and raises only a lossy issue. An unbound GOWA credential is also downgraded to lossy. The workflow therefore activates and sends WhatsApp text messages where the original sent media or revoked a message.

Evidence: internal/interop/n8n/gowa.go:63-106 (gowaRoute defaults) and gowa.go:40-57 (lossy severity); n8n.go (the commit 75d0441 hunk) downgrades unbound GOWA credentials to lossy. Live: wf_01a0b8e1-c267-7e38-9486-3f82c6800ebf. A node with resource=send, operation=image, caption 'Invoice' became POST http://127.0.0.1:3000/send/message with jsonBody {message:'Invoice', phone:...}. A node with resource=message, operation=revoke became POST /send/message with {message:{{$json.message}}}. unsupported[] held 2 lossy entries, and activate returned active:true. packs/gowa has only README/GAPS.md, and GOWA is absent from /api/v1/node-types. EPIC-3844bz, FEAT-c32499 and FEAT-vzp0kb were closed with empty descriptions and the placeholder '- [ ] Define acceptance criteria'.

n8n behavior: The GOWA community node calls the endpoint for the selected operation (media send, revoke, etc.).

Impact: Imported GOWA automations can message real contacts with wrong content (for example a caption instead of an image, or 'revoke' becoming a new message). This contradicts the importer's 'never silent remaps' promise.

Suggested fix: Import unknown GOWA operations as the blocking kilasflow.unsupported placeholder. Keep the credential issue blocking when the node had auth. Longer term, generate packs/gowa from GOWA's OpenAPI the same way as WAHA (cmd/nodepackgen).

Files: internal/interop/n8n/gowa.go, internal/interop/n8n/n8n.go, packs/gowa/GAPS.md

Existing tickets: EPIC-3844bz, FEAT-c32499, FEAT-vzp0kb

---
### Telegram import copies parameters verbatim: sendAndWait and additionalFields (parse_mode etc.) pass through without diagnostics and fail or are ignored at run time [find:unfinished-work] (high/parity-gap) · area: importer / pack.telegram · confidence: high

packToKilas copies n8n Telegram parameters verbatim and reports nothing. pack.telegram has 23 operations, while n8n 2.33.7 has 27. The pack reads top-level parseMode/caption/disableNotification/replyToMessageId, whereas n8n stores these under additionalFields.*. n8n's replyMarkup is an enum with separate keyboard collections; the pack treats it as raw JSON. The result: formatting is silently lost, and sendAndWait activates and then fails at run time.

Evidence: internal/interop/n8n/parameters.go:1894-1903 (packToKilas); packs/telegram/pack.json has no additionalFields key. Live: wf_01a0b8e0-e27a-7261-8b83-f36b881e244a (Webhook -> telegram sendAndWait -> telegram sendMessage with additionalFields.parse_mode=HTML). The import reported only credentials and meta issues. After binding a credential, activate returned 200. The run failed with 'no request is declared for resource "message" operation "sendAndWait"'. From n8n /types/nodes.json: the pack lacks message.sendAndWait, sendMessageDraft, sendRichMessage and sendRichMessageDraft. additionalFields keys include parse_mode, caption, disable_notification, reply_to_message_id and message_thread_id. Template tally: parse_mode in 15 of 36 send nodes; sendAndWait in template 2982. This regresses FEAT-6vfn3s criterion 41 ('a parameter n8n supports that KilasFlow does not carry produces a named import dia

n8n behavior: Telegram node with 27 operations, including send-and-wait approval; per-send options live in additionalFields; replyMarkup selects a keyboard type with its own collection.

Impact: 18 of the 100 templates use Telegram sendMessage. HTML or Markdown formatting is silently dropped, so messages show raw tags. Approval flows fail after activation. Inline keyboards in customer bots would send reply_markup:'inlineKeyboard'.

Suggested fix: In the importer, check resource/operation against the pack definition and emit a blocking issue when unknown. Map additionalFields.*, binaryData and the replyMarkup/inlineKeyboard collections onto pack keys, and report appendAttribution as dropped. Have the validator reject unknown operation values at save and activate. Add sendAndWait once approval waits are wired (see the durable-wait finding).

Files: internal/interop/n8n/parameters.go, packs/telegram/pack.json, internal/routing/request.go

Existing tickets: FEAT-6vfn3s

---
### Durable wait machinery (resume URL, approval page, webhook mode) shipped but no node can reach it; Wait still refuses webhook/form resume and keeps a 1-hour cap [find:unfinished-work] (high/unfinished) · area: engine / Wait & approval · confidence: high

The engine, the /resume/{token} route and the /approve/[token] page support webhook and approval waits, but no node ever raises a SuspendError in those modes. kilasflow.wait offers only timeInterval and specificTime and rejects webhook/form resume as 'this server does not do yet'. It also still enforces MaxWaitDuration = 1h, although waits are now parked in storage and validateSuspend allows 7 days. No node offers sendAndWait or approval.

Evidence: internal/engine/approval.go:63-70 defines WaitModeWebhook and WaitModeApproval. internal/api/routes.go:59-63 serves /resume, and web/src/routes/approve/[token] exists. The only SuspendError in any node is at nodes/wait.go:174, for interval/until. nodes/wait.go:74-75 and :118-121 refuse webhook/form, and :43 sets MaxWaitDuration=time.Hour. The comment justifying the cap says the wait 'occupies a worker slot', which is no longer true: a live 2-minute wait goes to status 'waiting'. The importer marks webhook/form waits blocking (parameters.go:1318-1329). FEAT-rj17xj (done) ends with: 'REMAINING ... no executor returns SuspendError yet ... Webhook/form wait modes ... nodes-owned future work'. No follow-up ticket exists. engine.WaitRegistry (approval.go:193-310) is used only by tests.

n8n behavior: Wait resumes after an interval, at a time, on a webhook call ($execution.resumeUrl) or on a form submission, for any duration (offloaded to the DB). Many nodes have Send and Wait for Response.

Impact: Human-in-the-loop flows are impossible: Wait on webhook/form, and Gmail/Telegram sendAndWait (4 nodes in 3 of the 100 templates). 'Wait until tomorrow' style waits over 1 hour are refused (e.g. template 2557). The half-wired approval page is dead weight.

Suggested fix: Add resume=webhook (plus approval and form) to kilasflow.wait, raising SuspendError{Mode: webhook/approval}. Raise the cap to validateSuspend's 7-day limit. Add sendAndWait to pack.telegram using approval mode. Delete WaitRegistry. Open a ticket for this remaining work.

Files: nodes/wait.go, internal/engine/approval.go, internal/engine/wait_service.go, internal/api/handlers/resume.go

Existing tickets: FEAT-rj17xj, FEAT-q81bq4

---
### A timer Wait can be cut short, and its output replaced, by anyone holding the resume URL [find:unfinished-work] (medium/security) · area: engine / wait resume · confidence: high

Every waiting execution, including plain interval/until waits, gets a resumeUrl and an approvalUrl. POST /resume/<token> on a timer wait resumes it immediately and uses the caller's JSON body as the Wait node's output, discarding the upstream items.

Evidence: Live: wf_01a0b8e7-74ea-789f-86e9-9edcfea728f2 is Manual -> Wait 2 minutes. Execution exec_01a0b8e7-74fb-71cc-ada7-9a06e096f85e went to status 'waiting', with resumeUrl http://127.0.0.1:8090/resume/<token> and approvalUrl /approve/<token> in GET /executions/{id}. GET /resume/<token> returned mode 'interval'. POST /resume/<token> with body [[{"json":{"injected":true}}]] returned 200 queued. The execution then succeeded at once instead of after 2 minutes, and node b's output was [[{"json":{"injected":true}}]]. Code: internal/api/handlers/resume.go:147-177 sends every non-approval mode to ResumeCall with the request body as output, with no mode check. internal/engine/wait_service.go:576-585 (WaitingLinks) publishes links for every wait. /resume is outside /api/v1 (routes.go:59-63).

n8n behavior: Time-interval and specific-time waits have no resume URL; only On Webhook Call waits resume through $execution.resumeUrl.

Impact: Leaked or logged resume links (they are also available as $execution.resumeUrl) let an outsider skip rate-limit or polling waits and inject arbitrary items into downstream nodes.

Suggested fix: Issue and accept resume tokens only for webhook and approval modes. Refuse ResumeCall for interval/until with 409. Return approvalUrl only for approval waits.

Files: internal/api/handlers/resume.go, internal/engine/wait_service.go

Existing tickets: FEAT-rj17xj

---
### A write-scoped embed session can publish a version (changing what production runs); FEAT-1500sp's publish-scope criteria were never implemented [find:unfinished-work] (medium/security) · area: embed / workflow history · confidence: medium

The embed middleware refuses activation as an owner-only action. However, POST /workflows/{id}/versions/{v}/publish (and /restore) falls through to the default case and needs only workflow:write. An embedded end-user editor can therefore push a new version live on an active workflow.

Evidence: internal/api/middleware/embed.go:113-133 special-cases only run, activate/deactivate, GET and DELETE; everything else requires ScopeWrite. internal/embed/embed.go:32-40 defines no publish scope. web/src/lib/embed/embed-editor.svelte:102 emits 'workflow-published' for any session. FEAT-1500sp (done) has criteria 34-35 unchecked: 'publishing is refused unless the session was minted with a publish scope it does not carry by default' and the matching event rule. Not tested live: the shared server has no embed origin allowlist, so it refuses to mint sessions (422).

n8n behavior: Publishing/activating is a separate permission (workflow:publish/activate) from editing.

Impact: Host applications that give customers an embedded editor cannot stop them from changing the version live traffic runs, although activation is deliberately owner-only.

Suggested fix: Add a workflow:publish scope that is not granted by default. Require it in the middleware for versions/*/publish. Hide the editor's Publish button and suppress the event without it.

Files: internal/api/middleware/embed.go, internal/embed/embed.go, web/src/lib/embed/embed-editor.svelte

Existing tickets: FEAT-1500sp

---
### External-secret references in credentials (ext://binding/key) are documented but cannot work: resolver never attached, bindings cannot be created [find:unfinished-work] (medium/unfinished) · area: credentials / external secrets · confidence: high

Only 'fetch the master key from Vault' is wired. The per-field ext:// reference resolver is never attached to the credential store, and secret bindings have repository functions but no caller, API or UI. A credential holding ext:// is accepted and sealed, then fails on every use.

Evidence: GORMCredentialStore.SetExternalResolver (internal/repository/credentials.go:285) and Create/Get/List/Update/DeleteSecretBinding have no callers; OpenAPI has no binding routes. cmd/kilasflow/main.go:908-920 wires only KeyFromManager. Live: POST /api/v1/credentials with type httpHeaderAuth and value 'ext://vault/prod-token' returned 200 (cred_01a0b8ef-c690-71ec-b7d2-bd6a31ba4d99). POST /credentials/{id}/test returned 422 'holds an external reference but no secret manager is configured', and this cannot change even with secrets.manager_addr set. Meanwhile docs/operate/security.md:62-75 says credential fields 'may hold an ext://<binding>/<key> reference ... against a manager binding that belongs to the calling tenant'. FEAT-knpfqf is done with all 7 criteria unchecked.

n8n behavior: External Secrets ({{ $secrets.<provider>.<key> }}) configured under Settings.

Impact: Operators following the security docs store references that break every workflow using the credential.

Suggested fix: When a manager is configured, construct credentials.NewResolver in main and attach it. Add tenant-scoped binding CRUD (API + Settings UI). Otherwise reject ext:// on write and remove the doc claim.

Files: internal/repository/credentials.go, internal/credentials/external.go, cmd/kilasflow/main.go, docs/src/content/docs/operate/security.md

Existing tickets: FEAT-knpfqf

---
### JavaScript sidecar for community nodes (FEAT-7cg0cd, done) is an unwired library, while the docs describe enabling it [find:unfinished-work] (medium/unfinished) · area: community nodes / sidecar · confidence: high

The sidecar package (protocol, per-tenant pool, limits) is never imported by the binary. No config key selects a script, nothing reads a package.json n8n manifest, and nothing registers a sidecar-sourced node. The outbound-HTTP proxy is explicitly unimplemented.

Evidence: No non-test code imports github.com/kilaslabs/kilas-flow/sidecar. node.SourceSidecar is referenced only in internal/node/registry.go, and no Go code mentions n8nNodesApiVersion. config.example.yaml has no sidecar section. The live catalogue has 51 builtin and 5 pack entries, none from a sidecar. docs/concepts/node-registry.md:225 admits 'nothing registers one', but docs/guides/community-nodes.md:50-84 talks about 'without a sidecar configured' and 'Enabling the sidecar'. sidecar/doc.go says the first host call fails with host-call-denied. All 7 FEAT-7cg0cd criteria are unchecked.

n8n behavior: Community nodes install from npm through Settings and run in-process.

Impact: There is no execution path for programmatic community nodes (11 third-party node instances in the 100 templates, plus customers' own packages), and the guide over-claims.

Suggested fix: Either wire it up (config for the node binary and script, a manifest loader calling RegisterFrom(SourceSidecar), and a sidecar executor with the HTTP proxy), or reopen the ticket and mark the guide section as planned.

Files: sidecar/sidecar.go, sidecar/doc.go, internal/node/registry.go, docs/src/content/docs/guides/community-nodes.md

Existing tickets: FEAT-7cg0cd

---
### Deployment-level white-label config (branding.name/logo/favicon/powered_by) is dead; the dashboard hard-codes 'KilasFlow' [find:unfinished-work] (medium/unfinished) · area: white-label / branding · confidence: high

Config and docs define branding.name, logo, favicon and powered_by 'shown in the dashboard', but nothing outside internal/config reads them and no endpoint hands them to the SPA. Only per-embed-session branding works.

Evidence: internal/config/config.go:420-434, config.example.yaml:243-255 and docs/operate/configuration-reference.md:526+. A grep finds no reader of cfg.Branding (embed.Branding is a different per-session struct). 'KilasFlow' is a literal string in 10 web route files, including +layout.svelte, (dashboard)/+layout.svelte, the workflow pages, approve/[token]/+page.svelte and export-dialog.svelte.

Impact: The standalone dashboard and the public /approve/<token> page shown to a host's end users cannot be white-labelled, which contradicts the product's positioning.

Suggested fix: Expose branding through a public endpoint (or inject it into index.html) and use it for titles, logo, favicon and the powered-by mark. Otherwise remove the keys.

Files: internal/config/config.go, config.example.yaml, web/src/routes/+layout.svelte, web/src/routes/(dashboard)/+layout.svelte

## Acceptance criteria

- [ ] Importer maps n8n Wait v1.1 with the default unit to hours instead of seconds and drops the default amount of 
- [ ] The generic importer contains a customer-specific hack: JS Code nodes matching a few keywords are replaced by 
- [ ] GOWA importer turns unmapped operations into a different live action (send image or revoke becomes POST /send/
- [ ] Telegram import copies parameters verbatim: sendAndWait and additionalFields (parse_mode etc.) pass through wi
- [ ] Durable wait machinery (resume URL, approval page, webhook mode) shipped but no node can reach it; Wait still 
- [ ] A timer Wait can be cut short, and its output replaced, by anyone holding the resume URL
- [ ] A write-scoped embed session can publish a version (changing what production runs); FEAT-1500sp's publish-scop
- [ ] External-secret references in credentials (ext://binding/key) are documented but cannot work: resolver never a
- [ ] JavaScript sidecar for community nodes (FEAT-7cg0cd, done) is an unwired library, while the docs describe enab
- [ ] Deployment-level white-label config (branding.name/logo/favicon/powered_by) is dead; the dashboard hard-codes 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## ImporterTail slice — 2026-09-20

Status: `testing` (importer half). Commits: `47544b7` (content; swept into a
peer's commit), `41f1d50`.

Landed:
- Wait v1.1 defaults (5 seconds, not hours) and a missing amount defaulted
  rather than read as zero; the amount is converted, so an expression survives.
- The customer-specific Code hack is **gone**: `trivialcode.go` and its test are
  deleted, and the call site in `Import` with them. A JS Code node whose source
  merely mentioned a few keywords is no longer rewritten into fixed assignments;
  it imports as the blocking `foreignCode` placeholder that keeps its source and
  names the native node that most likely replaces it.
- GOWA: an unmapped resource/operation pair is refused (`refuseKilas`) and lands
  on `kilasflow.unsupported` instead of being sent as a different live GOWA call,
  and an unbound GOWA credential is blocking again rather than downgraded to
  lossy — a node that authenticated in n8n must not activate against a host
  nobody pointed it at.
- Telegram: Additional Fields mapped onto the pack's names, inline keyboards
  rebuilt as Bot API JSON, unknown operations (sendAndWait and the rich-message
  pair) blocking, `appendAttribution` named as dropped.
- Workflow settings: `executionTimeout` carried (EngineWaits' reader), the
  `"DEFAULT"` timezone sentinel omitted instead of stored as a literal that
  document validation refuses.

Scoped proof: `go test ./internal/interop/n8n/ -count=1` green.

Other findings on this ticket are other slices and untouched here: durable wait
modes and the resume-token scoping (EngineWaits), embed publish scope and ext://
secrets (SecurityFront2), the sidecar (DXOps2), deployment branding
(SecurityFront2).
