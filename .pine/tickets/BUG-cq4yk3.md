---
id: BUG-cq4yk3
title: 'Webhook answering: Respond timing, responseData, $response leak, shape, CORS, JWT, WAHA, defaults'
status: testing
priority: high
labels:
    - webhook
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T03:12:18Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 17 finding(s) from dims: find:core-node-parity, find:engine-runtime, find:webhook-trigger-parity.

---
### Webhook responseData/options are dropped: lastNode+allEntries returns only the first item, and an empty last node echoes the raw request back [find:core-node-parity] (high/bug) · area: importer Webhook + webhook response · confidence: high

webhookToKilas never copies responseData (allEntries/noData) or options (responseCode, responseData for onReceived, and so on). The allEntries and noData branches in respondFromLastNode therefore cannot be reached from imports. Separately, lastNodeItems skips nodes that produced no items and falls back to the Webhook trigger's own item. That item (request headers, query, method, path) is returned to the caller.

Evidence: - wh_last_all (lastNode, allEntries, 2 items): n8n [{id:1},{id:2}]; KilasFlow {id:1}.
- wh_last_nodata: n8n empty body; KilasFlow the first item.
- Empty result (wh_last_all req1, wh_last_default req1, loop_v3 req1, shaping req1): n8n returns [] or 500 'No item to return was found'; KilasFlow returns {body, contentType, headers{...}, method, path, query}.
- wh_onreceived_code: n8n {"message":"Workflow was started"}; KilasFlow {executionId,status:"queued"}.
- wh_onreceived_data: the custom response text is ignored.
Code: internal/interop/n8n/parameters.go:796-830 and internal/webhook/webhook.go:440-490.

n8n behavior: Honours responseData (firstEntryJson/allEntries/noData) and the options. An empty last node gives [] or an error, never the trigger's data.

Impact: Every imported lastNode webhook that returns lists, and every custom response code or data option. The fallback can leak headers added by a proxy or gateway back to callers.

Suggested fix: Carry responseData and options.responseCode/responseData/responseHeaders/rawBody in webhookToKilas. In lastNodeItems, use the last executed node even when it is empty and answer like n8n. This overlaps the webhook-trigger-parity dimension.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

Existing tickets: FEAT-az620p (done; AC 33 plus its outcome note claims responseData parity, so this is a regression on the import path), FEAT-nbqye0

---
### Webhook item headers use Go canonical case, and credential-like header values become "[redacted]", so in-workflow header checks always fail [find:webhook-trigger-parity] (high/parity-gap) · area: webhook trigger payload · confidence: high

Header names reach the item as Go canonical case (X-Api-Key, Content-Type), not n8n's lower case. Values of Authorization, Cookie, *token*, *secret* and X-Api-Key-style headers are replaced with "[redacted]" before the item is built, so a workflow can never read them. Host is missing entirely.

Evidence: Script p_shape.py: POST with header X-Api-Key: abc to the same imported Webhook(lastNode) on both platforms. n8n item.headers = {"x-api-key":"abc","content-type":"application/json","host":...}. KF item.headers = {"X-Api-Key":"[redacted]","Content-Type":"application/json",...} with no Host. `$json.headers['x-api-key']` is undefined in KF, and even the canonical key holds the literal '[redacted]'. Code: internal/webhook/webhook.go:328-338 (values[0], then execution.RedactMap before the payload); internal/execution/redact.go:16 sensitiveKeys.

n8n behavior: The item's headers object holds all request headers with lower-case names and unmodified values, including host.

Impact: Template 5171's 'secret-dish' endpoint checks IF $json.headers['x-api-key'] == ..., so it always takes the 401 branch. The same breakage hits any imported workflow that does its own bearer/API-key check or reads cookies, content-type or user-agent in lower case.

Suggested fix: Emit lower-case header names like n8n/Node and keep real values in the live item. Redact only when persisting execution records (redact on write), not in the data the workflow runs on. Include host.

Files: /Users/izzadev/projects/k-flow/internal/webhook/webhook.go, /Users/izzadev/projects/k-flow/internal/execution/redact.go

---
### Respond to Webhook doesn't answer when it runs: the caller waits for the whole workflow, and a later failure turns into a 500 that includes internal error text [find:webhook-trigger-parity] (high/parity-gap) · area: webhook response (responseNode) · confidence: high

The Respond node only writes `$response` onto its item. The HTTP boundary waits for the execution to reach a terminal state and answers from it only if the run succeeded. So the fast-ack pattern doesn't work, and any failure after the Respond node discards its response and returns 500 with the internal error message.

Evidence: Script p_respond.py r1: Webhook(responseNode) -> Respond(default) -> HTTP call that sleeps 3 s -> HTTP call returning 500. n8n: 200 in 0.05 s with the Respond payload. KF: 500 after 3.03 s with body {"error":{"code":"execution.failed","message":"execute node \"Fail\": node \"Fail\": request failed with status 500"},"executionId":...,"status":"failed"}. Code: internal/webhook/webhook.go:186-191 (await terminal state) and 393-400 (non-success -> 500 envelope); nodes/webhook.go:420-424.

n8n behavior: The HTTP response is sent the moment the Respond to Webhook node executes; the rest of the workflow keeps running.

Impact: Affects every responseNode template (1435, 1750, 2679, 2431, 2846, and 5171's secret-dish) and any Slack/Telegram/WhatsApp-style webhook that must acknowledge within seconds. Responses are slow or hit the 30 s 504. Failures downstream of a successful Respond become 500s visible to the caller. Node names and internal errors leak to unauthenticated callers; n8n answers {"message":"Error in workflow"

Suggested fix: When the Respond node runs, signal the waiting HTTP handler: an execution-scoped response channel or event, or have await() return as soon as a node run carrying `$response` is persisted. Let the run continue in the background. Return a generic error body on failure.

Files: /Users/izzadev/projects/k-flow/internal/webhook/webhook.go, /Users/izzadev/projects/k-flow/nodes/webhook.go

---
### Importer defaults an n8n Webhook with no httpMethod to POST (n8n's default is GET); multi-method and HEAD are also mishandled [find:webhook-trigger-parity] (high/parity-gap) · area: importer / webhook · confidence: high

n8n omits default-valued parameters on export, so a GET webhook arrives without httpMethod. KF binds it as POST without raising any issue. Multi-method webhooks collapse to POST only. HEAD imports cleanly but then fails activation.

Evidence: Imported webhook v2 {path:'probe-a', options:{}}: node httpMethod POST, binding method POST, zero issues. Live n8n 2.33.7, same node: GET /webhook/wtp-probe-a -> 200 {"message":"Workflow was started"}; POST -> 404 'This webhook is not registered for POST requests. Did you mean to make a GET request?'. v2.1 {multipleMethods:true, httpMethod:['GET','POST']} imports as POST with no issue. httpMethod HEAD imports with no issue, then activation returns 422 'httpMethod "HEAD" is not supported'. Code: internal/interop/n8n/parameters.go:801 (defaultString(...,"POST")); nodes/webhook.go:101-106 and 244-249.

n8n behavior: The Webhook node's httpMethod defaults to GET; v2.1 can allow several methods; HEAD is supported.

Impact: Templates 2982, 1750 and 5171 (4 of its 5 webhooks) omit httpMethod. After import every GET caller gets a silent 404, and 5171 is one of the only 4 templates that activate.

Suggested fix: Default a missing httpMethod to GET. Support several methods per webhook node (one binding per method) and HEAD. Report anything that can't be mapped as an import issue.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/webhook.go

---
### Respond to Webhook with no respondWith imports as an empty text response (n8n's default is First Incoming Item) [find:webhook-trigger-parity] (high/bug) · area: importer / respondToWebhook · confidence: high

respondToKilas maps a missing respondWith to 'text', and its comment wrongly says that is n8n's default. n8n's default is firstIncomingItem, which returns the first item as JSON. Imported nodes therefore return an empty text/plain 200.

Evidence: Script p_respond2.py r4, Respond v1.1 {options:{}}. n8n: 200 application/json with the first incoming item. KF node params {respondWith:'text'}, response 200 text/plain with an empty body, and no import issue. Code: internal/interop/n8n/parameters.go:888-890.

n8n behavior: respondWith defaults to 'firstIncomingItem' and responds with the first item's JSON as application/json.

Impact: Respond nodes that rely on the default in templates 2679, 2786, 2431 (3 nodes, including the 500 error replies) and 2846. Their callers, often chat or web frontends, receive an empty body.

Suggested fix: Map a missing respondWith to firstIncomingItem for typeVersion 1.x and write it explicitly into the imported parameters.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go

---
### Webhook node options and responseData are silently dropped on import, including the IP allow-list and allowed origins [find:webhook-trigger-parity] (high/security) · area: importer / webhook · confidence: high

webhookToKilas reads only path, httpMethod, responseMode and authentication. Top-level responseData/responseCode and every `options` key are discarded without any import issue: responseCode, responseData, responseHeaders, rawBody, allowedOrigins, ipWhitelist, ignoreBots, binaryPropertyName, noResponseBody. The KF node has no equivalents.

Evidence: Script p_echo.py imported {responseMode:lastNode, responseData:'allEntries', options:{responseCode:{values:{responseCode:201}}, rawBody:true, allowedOrigins:'https://app.example.com', ipWhitelist:'10.0.0.1', ignoreBots:true, binaryPropertyName:'file'}}. Result: {authentication:none, httpMethod:POST, path, responseMode:lastNode}; the only import issue is the webhookId note. Immediate mode with options.responseData 'thanks!' and header X-Ack: n8n answers 'thanks!' with X-Ack: 1; KF answers {"executionId":...,"status":"queued"} with no header. Plain n8n onReceived answers {"message":"Workflow was started"}. Code: internal/interop/n8n/parameters.go:797-834; nodes/webhook.go:91-137.

n8n behavior: All of these options are honoured by the Webhook node: IP allow-list refuses other callers, allowedOrigins drives CORS, responseData/responseHeaders shape the immediate response.

Impact: An endpoint restricted by IP allow-list or origin in n8n arrives open, with no warning. lastNode+allEntries callers get an object instead of an array. Custom ack codes, bodies and headers are lost, as is template 2751's binaryPropertyName. The import report understates the loss, which contradicts FEAT-nbqye0.

Suggested fix: Import responseData, responseCode and each supported option. For the rest, emit issues (ipWhitelist and allowedOrigins as blocking, or implement them). Add an Options collection to kilasflow.webhook: response code/body/headers for immediate mode, allowed origins, IP allow-list, raw body, binary property, ignore bots, no response body.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/webhook.go

Existing tickets: FEAT-nbqye0

---
### Webhook bodies: multipart stays raw text, binary is corrupted into a UTF-8 string, XML and repeated form keys aren't parsed [find:webhook-trigger-parity] (high/parity-gap) · area: webhook body decoding · confidence: high

decodeBody handles only JSON and single-valued urlencoded bodies. Multipart becomes one raw string and uploaded files never become binary. Non-UTF-8 bytes are irreversibly replaced. XML isn't parsed. Repeated keys keep only their first value.

Evidence: Sent with curl to a lastNode echo webhook on both platforms. Multipart (-F name=alice -F tags=a -F tags=b -F upload=@f.txt): n8n body {name:'alice', tags:['a','b']} with the file in binary; KF body is the whole raw multipart text and there is no binary. application/octet-stream bytes 'PNG\x89\x00\x01binary': n8n puts them in binary and returns body {}; KF body is 'PNG�\x00\x01binary'. urlencoded `tags[]=a&tags[]=b&y=2&y=3`: n8n {tags[]:['a','b'], y:['2','3']}; KF {tags[]:'a', y:'2'}. application/xml `<a><b>1</b></a>`: n8n {a:{b:'1'}}; KF raw string. Code: internal/webhook/shape.go:186-218.

n8n behavior: Multipart fields go to body and files to binary; raw/binary bodies go to binary; XML is parsed; repeated keys become arrays.

Impact: File-upload webhooks (template 2751, voice-note or attachment intake such as 2846), SOAP/XML callbacks, and HTML forms with checkboxes. The binary store exists (FEAT-0f87fn) but the webhook never writes to it. This violates FEAT-5kv1jq's acceptance that binary bodies arrive uncorrupted.

Suggested fix: Parse multipart: fields go to body, files go to item.binary via the binary store. Send non-text bodies to binary (binaryPropertyName, default 'data'). Parse XML into an object. Keep repeated keys as arrays.

Files: /Users/izzadev/projects/k-flow/internal/webhook/shape.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

Existing tickets: FEAT-5kv1jq

---
### Webhooks don't answer CORS preflight (OPTIONS returns 404) and send no Access-Control-* headers [find:webhook-trigger-parity] (high/parity-gap) · area: webhook HTTP surface · confidence: high

Routing resolves bindings by request method, so OPTIONS never matches and returns 404. No Access-Control headers are ever sent, so browser pages can't POST JSON to a KF webhook.

Evidence: `curl -X OPTIONS -H 'Origin: https://app.example.com' -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: content-type` against both. n8n: 204 with Access-Control-Allow-Origin/Methods/Headers and Max-Age 300. KF: 404. On the actual POST with an Origin header, n8n echoes Access-Control-Allow-Origin and KF sends none. The n8n chat trigger also answers OPTIONS with 204 and CORS headers. Code: internal/webhook/webhook.go:103.

n8n behavior: OPTIONS preflight returns 204 with CORS headers; allowedOrigins defaults to '*'.

Impact: Chat widgets, landing-page forms and SPA backends (the common 'n8n webhook as API' use) fail in browsers with CORS errors.

Suggested fix: Answer OPTIONS for any bound route with the methods bound on it. Send Access-Control-Allow-Origin '*' by default, as n8n does, and allow a per-node allowedOrigins setting.

Files: /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

---
### JWT-protected n8n webhooks import as unauthenticated and activate; basic/header-auth webhooks without a credential activate and then return 500 on every call [find:webhook-trigger-parity] (high/security) · area: importer / webhook auth + activation validation · confidence: high

authentication 'jwtAuth' is rewritten to 'none' with only a lossy note. The import's 'blocking' unbound-credential issue doesn't prevent activation. Activation validation doesn't require a credential for basic or header auth either.

Evidence: Script p_auth.py. Webhook {authentication:'jwtAuth', credentials.jwtAuth}: import issues are a blocking 'imported unbound' and a lossy 'jwtAuth is not supported; the imported webhook is unauthenticated'. POST /activate returns 200, and an unauthenticated POST to the route returns 200 {executionId, status:'queued'}. With basicAuth and no credential: activate 200, then every call 500 'This webhook requires a credential that is not configured.' Code: internal/interop/n8n/parameters.go:825-830; nodes/webhook.go:236-261; internal/webhook/webhook.go:227-236.

n8n behavior: Webhook auth supports none, basicAuth, headerAuth and jwtAuth; a node without the required credential can't be activated.

Impact: A secured n8n endpoint silently becomes public after migration. The opaque route prevents guessing but doesn't help once the URL leaks. A misconfigured auth setup is only discovered when callers get errors.

Suggested fix: Keep 'jwtAuth' and refuse activation until JWT is supported, or implement a jwtAuth credential (passphrase or PEM key, HS/RS algorithms). Make validateWebhookConfiguration require an attached credential of the right type for basic, header and JWT auth.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/webhook.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

---
### A webhook's public URL can only be found in the one-time import report; there's no test URL or 'listen for test event' [find:webhook-trigger-parity] (high/ux) · area: editor webhook node + API · confidence: high

The public URL is a random opaque route minted at activation. It's shown only in the import report dialog. The node panel, the activation response and the workflow API never expose it, and there's no test-listen flow.

Evidence: Created a native kilasflow.webhook v1 workflow (path 'native') via POST /api/v1/workflows and activated it. The activation response has notices: [] and no URL. GET workflow has no URL. OpenAPI has no route-listing endpoint; WebhookRouteResource appears only in ImportedWorkflowResource. The node panel says 'The public URL uses an opaque route minted on activation' but never shows it, even while Active (ui-native-webhook-panel.png). The web UI renders routes only in routes/(dashboard)/app/workflows/import-report.svelte. The route is random (internal/repository/webhooks.go:165-188), so it can't be derived from the path. Only the WAHA trigger's activation notice carries a URL.

n8n behavior: The Webhook node panel always shows copyable Test and Production URLs; 'Listen for test event' captures a live request (/webhook-test/...) into the canvas.

Impact: A user who builds a webhook workflow in the editor, or closes the import dialog, can't find where to send requests. A webhook flow can't be tested with a real request before activation.

Suggested fix: Add GET /api/v1/workflows/{id}/webhooks; mint the route on save, reusing EnsureWebhookRoutes. Show copyable URLs in the Webhook, Telegram and WAHA trigger panels and in the activation response. Add a test-listen mode: a short-lived test route whose request feeds a manual run and pins the data.

Files: /Users/izzadev/projects/k-flow/internal/repository/webhooks.go, /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-report.svelte, /Users/izzadev/projects/k-flow/nodes/webhook.go

---
### WAHA trigger auto-register replaces the whole session config (restarting the session), never unregisters, and leaves out the HMAC key it then requires [find:webhook-trigger-parity] (high/bug) · area: pack.wahaTrigger lifecycle · confidence: high

With autoRegister on, activation sends one PUT of a full session config containing only this webhook, with no read-and-merge. Deactivation sends nothing, although the parameter description promises removal. The registration never includes the hmacSecret, so autoRegister plus hmacSecret gives a trigger that refuses every delivery.

Evidence: Stub on :8096 with a wahaApi credential whose baseUrl points to it. Activation sent exactly `PUT /api/sessions/default {"config":{"webhooks":[{"url":"http://127.0.0.1:8090/webhook/<route>","events":["*"]}]}}`. Deactivation sent no request. With hmacSecret 'topsecret' the body is identical, with no hmac.key. An unsigned delivery then gets 401 'this endpoint requires a X-Webhook-Hmac signature'. WAHA docs (devlikeapro/waha-docs, how-to/sessions) say PUT /api/sessions/{session} 'Updates a session with a full new configuration. If the session is not in a STOPPED status, it will be stopped and started with the new configuration.' The descriptor in packs/waha/pack-trigger-202502.json has only 'set'.

Impact: Each activation wipes the customer's other webhooks, proxy and engine settings on that WAHA session and restarts their live WhatsApp session. A second workflow on the same session evicts the first. Webhooks left pointing at dead routes make WAHA retry 15 times per event. WAHA is a first-class pack for the target market.

Suggested fix: GET the session first and merge, adding or replacing only this route's entry. Include hmac:{key} and the trigger's event filter instead of '*'. Add a 'remove' descriptor that deletes only this URL on deactivation, and a 'check' descriptor. Test against a WAHA container.

Files: /Users/izzadev/projects/k-flow/packs/waha/pack-trigger-202502.json, /Users/izzadev/projects/k-flow/packs/waha/pack-trigger-202409.json, /Users/izzadev/projects/k-flow/internal/webhook/request_lifecycle.go

Existing tickets: FEAT-bp0ytb

---
### Webhook item shape differs from n8n: no params, webhookUrl or executionMode; repeated query keys collapse to one value; path params unsupported [find:webhook-trigger-parity] (medium/parity-gap) · area: webhook trigger payload · confidence: high

kilasflow.webhook is registered with the envelope shape {method, path, headers, query, body, contentType}. Query keys and headers keep only their first value. Because the route is a single opaque segment, `user/:id`-style paths are only labels and params never exist.

Evidence: Same request `POST ...?a=1&a=2&b=x` to both. n8n item: {headers, params:{id:'42'}, query:{a:['1','2'], b:'x'}, body, webhookUrl, executionMode:'production'}, served at /webhook/<webhookId>/wtp-shape/42. KF item: {body, contentType, headers, method, path:'wtp-shape/:id', query:{a:'1', b:'x'}}. Appending /42 to the KF route returns 404. Code: internal/webhook/webhook.go:340-345; nodes/telegram_lifecycle.go:385 registers ShapeEnvelope, although internal/webhook/shape.go:73-83 already defines ShapeN8NCore (with params always empty).

n8n behavior: The Webhook item is {headers, params, query, body, webhookUrl, executionMode}; dynamic paths fill params.

Impact: REST-style n8n endpoints (/user/:id) can't be reproduced. Expressions reading $json.params.*, $json.webhookUrl or array query values break. Several templates read $json.query.* (2006, 2324, 2606, ...), and multi-value query parameters lose data.

Suggested fix: Give imported and new webhooks the n8n shape, optionally keeping method and path as extras. Keep repeated query keys as arrays. Support ':param' segments after the opaque route (/webhook/<route>/<rest>) and fill params. Add webhookUrl and executionMode.

Files: /Users/izzadev/projects/k-flow/internal/webhook/shape.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go, /Users/izzadev/projects/k-flow/nodes/telegram_lifecycle.go

Existing tickets: FEAT-5kv1jq

---
### Respond to Webhook options (responseHeaders, responseKey) are dropped silently on import, and the defaults differ (text is served as text/plain, redirect is 302) [find:webhook-trigger-parity] (medium/parity-gap) · area: importer / respondToWebhook · confidence: high

respondToKilas reads only options.responseCode. Header entries and responseKey are lost without an issue. KF also serves text responses as text/plain where n8n uses text/html, and defaults redirects to 302 where n8n uses 307.

Evidence: Script p_respond.py r2: Respond {respondWith:text, responseBody:'<b>hi</b>', options.responseHeaders.entries:[Content-Type text/html; charset=utf-8, X-Custom yes]}. n8n returns both headers; KF keeps neither, raises no issue, and returns text/plain without X-Custom. Script r5 (no headers set): n8n text/html; charset=utf-8, KF text/plain. Script r3 (redirect): n8n 307 with Location, KF 302. options.responseKey in template 2431 is dropped. Code: internal/interop/n8n/parameters.go:860-913; internal/webhook/webhook.go:568-581; nodes/webhook.go:446-451.

n8n behavior: Response headers from options are applied; text responses are text/html; redirect defaults to 307.

Impact: Template 2417 serves an HTML result page that KF shows as source. CORS, content-type and caching headers set in Respond nodes are lost, as is 2431's responseKey. A 302 turns a POST into a GET in browsers.

Suggested fix: Map options.responseHeaders.entries to responseHeaders and implement responseKey wrapping. Default text responses to text/html like n8n and redirects to 307. Report any unmapped option as an import issue.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go, /Users/izzadev/projects/k-flow/nodes/webhook.go

---
### Respond to Webhook leaks a '$response' field into the items it passes downstream [find:webhook-trigger-parity] (medium/bug) · area: respondToWebhook executor · confidence: high

The response is stored as `$response` on each output item's JSON, so every downstream node receives it as data.

Evidence: Execution of r2 (Webhook -> Respond -> NoOp 'After'): After's output item json is {"$response":{"body":"<b>hi</b>","headers":{},"statusCode":200}, body:..., headers:..., ...}. Code: nodes/webhook.go:469-475.

n8n behavior: Respond to Webhook passes its input items through unchanged.

Impact: Nodes after a Respond (DB insert or HTTP POST of $json, Set with 'include other fields', Aggregate) write an extra $response object. Execution records grow. In lastNode mode the response metadata is returned to the caller as data.

Suggested fix: Carry the response out-of-band, as node-run metadata or a dedicated execution field, or strip it from items passed to children.

Files: /Users/izzadev/projects/k-flow/nodes/webhook.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

---
### Path-label uniqueness blocks valid n8n layouts (same path with different methods, duplicated workflows) and fails as an opaque 500 'workflow operation failed' [find:webhook-trigger-parity] (medium/bug) · area: webhook activation · confidence: high

uidx_webhook_bindings_label is unique on (tenant, path) and ignores method, even though the public URL is a per-node opaque route. When it fires, the handler maps the conflict to a generic 500 and logs no cause.

Evidence: (a) One workflow with Webhook GET wtp-items and Webhook POST wtp-items: KF /activate returns 500 {detail:'workflow operation failed'}, while n8n answers both GET and POST with 200. (b) Importing the same webhook workflow twice: the first activates and the second gets the 500, although the two routes differ (/webhook/90fc... vs /webhook/d8a6...). The server log shows only the access line. Code: internal/repository/models.go:46,67; internal/repository/webhooks.go:140 ('already claimed' error); internal/api/handlers/workflows.go:666-674 (problem() → generic 500).

n8n behavior: The same path may be registered for different methods; a conflict produces a clear error naming the conflicting workflow.

Impact: Blocks REST-style webhook pairs, cloned workflows, and importing a template twice. Users get no actionable message.

Suggested fix: Drop the label uniqueness, since the URL is the opaque route, or scope it to (tenant, method, path). Map binding conflicts to 409 naming the conflicting workflow, and log unexpected activation errors on the server.

Files: /Users/izzadev/projects/k-flow/internal/repository/models.go, /Users/izzadev/projects/k-flow/internal/repository/webhooks.go, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go

---
### Respond to Webhook with default respondWith returns an empty body; Respond injects "$response" into items; redirect uses 302 [find:core-node-parity] (high/bug) · area: importer / Respond to Webhook · confidence: high

n8n omits respondWith when it has the default value, firstIncomingItem. The importer maps a missing value to "text" (the comment calls this n8n's default), so the response is empty. The Respond node also adds a "$response" object to its output items, which then flow downstream. Redirects answer 302 where n8n answers 307, and a Respond node that is never reached gives 500 instead of n8n's 200.

Evidence: - rw_default (Respond 1.1, options {}): n8n 200 application/json {"id":1,"name":"a"}; KilasFlow 200 text/plain with an empty body.
- Every rw_* case: Respond's output items, and the next node's input (rw_then_continue 'After'), contain "$response":{body,headers,statusCode}.
- rw_redirect: n8n 307; KilasFlow 302, so POST clients switch to GET.
- rw_not_reached: n8n 200 with an empty body; KilasFlow 500 JSON error.
Code: internal/interop/n8n/parameters.go:878-881; ResponseKey at nodes/webhook.go:536.

n8n behavior: The default respondWith is firstIncomingItem. Input items pass through unchanged. Redirects use 307 unless a code is set.

Impact: Respond to Webhook appears in 8 of the 100 templates (19 nodes). 7 of those nodes, in 4 templates, rely on the default respondWith.

Suggested fix: Default an absent respondWith to firstIncomingItem. Keep response metadata out of item JSON (use node run metadata). Use 307 for redirects and decide on n8n's not-reached behaviour.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/webhook.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

Existing tickets: FEAT-az620p (done; AC 34, so this is a regression)

---
### Respond to Webhook answers only after the whole execution ends, and a failure after the Respond node turns the reply into a 500 with internal error details [find:engine-runtime] (high/parity-gap) · area: internal/webhook responseNode mode · confidence: high

ServeHTTP waits, by polling the DB, until the execution is terminal (webhook.go:178-191, 364-386). Only then does respondFromExecution look for the Respond node's output. If the execution failed after the Respond node, the caller gets a 500 containing record.Error.

Evidence: Script respond_wf.py: Webhook(responseMode=responseNode) -> Respond({"accepted":true}) -> HTTP (slow stub /sleep?s=5, or failing stub /fail 500). n8n (<W>/t_respond_n8n.py): 200 {"accepted":true} in 0.02 s for both variants. KF :8090 (<W>/t_respond_kf.py): the slow variant returned 200 after 5.04 s. The failing variant returned HTTP 500 `{"error":{"code":"execution.failed","message":"execute node \"s\": node \"LongWork\": request failed with status 500"},"executionId":...}`. Work after Respond longer than response_timeout (30 s) gives 504.

n8n behavior: The HTTP response is sent the moment the Respond to Webhook node executes. Later failures do not change it.

Impact: The 'acknowledge fast, then process' pattern breaks: Slack's 3 s ack, Telegram/WhatsApp/WAHA/Stripe retry on timeout (causing duplicate deliveries), and callers see 500s along with leaked internal error text. 5+ top templates use respondToWebhook (15 nodes).

Suggested fix: Have the Respond to Webhook executor hand its response straight to the waiting handler (per-execution channel or event) and let the execution continue. Fall back to a failure reply only if the execution ends before any Respond node runs, without internal details.

Files: internal/webhook/webhook.go, nodes/routing.go

## Acceptance criteria

- [ ] Webhook responseData/options are dropped: lastNode+allEntries returns only the first item, and an empty last n
- [ ] Webhook item headers use Go canonical case, and credential-like header values become "(redacted)", so in-workf
- [ ] Respond to Webhook doesn't answer when it runs: the caller waits for the whole workflow, and a later failure t
- [ ] Importer defaults an n8n Webhook with no httpMethod to POST (n8n's default is GET); multi-method and HEAD are 
- [ ] Respond to Webhook with no respondWith imports as an empty text response (n8n's default is First Incoming Item
- [ ] Webhook node options and responseData are silently dropped on import, including the IP allow-list and allowed 
- [ ] Webhook bodies: multipart stays raw text, binary is corrupted into a UTF-8 string, XML and repeated form keys 
- [ ] Webhooks don't answer CORS preflight (OPTIONS returns 404) and send no Access-Control-* headers
- [ ] JWT-protected n8n webhooks import as unauthenticated and activate; basic/header-auth webhooks without a creden
- [ ] A webhook's public URL can only be found in the one-time import report; there's no test URL or 'listen for tes
- [ ] WAHA trigger auto-register replaces the whole session config (restarting the session), never unregisters, and 
- [ ] Webhook item shape differs from n8n: no params, webhookUrl or executionMode; repeated query keys collapse to o
- [ ] Respond to Webhook options (responseHeaders, responseKey) are dropped silently on import, and the defaults dif
- [ ] Respond to Webhook leaks a '$response' field into the items it passes downstream
- [ ] Path-label uniqueness blocks valid n8n layouts (same path with different methods, duplicated workflows) and fa
- [ ] Respond to Webhook with default respondWith returns an empty body; Respond injects "$response" into items; red
- [ ] Respond to Webhook answers only after the whole execution ends, and a failure after the Respond node turns the
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (WebhookParity 2026-09-20)

Slices landed so far, each with its own scoped proof:

- **Path labels are not identities** (commit 5693d98, repository): `uidx_webhook_bindings_label` dropped in migration `000011` (both dialects, spaces-indented) and replaced by a plain `(tenant_id, path)` index; the routable identity is the globally unique opaque `route`, which is what an inbound request resolves on. `WebhookRepository.ResolveRoute(ctx, route)` lists every method bound on one route (needed for CORS preflight). Conflicts now surface as `repository.ErrWebhookPathClaimed` / `*repository.WebhookConflictError{Path, Method, Route, WorkflowID}` instead of a string-matched guess; the API layer maps them to 409 (SecurityFront2's file, asked via hub). Proof: `go test ./internal/repository/ -count=1` and `./internal/database/ -count=1` green in an isolated worktree; pre-fix, one path bound to GET+POST and two workflows sharing a label both failed activation.
- **WAHA trigger registration merges instead of replacing** (packs/waha + internal/webhook/request_lifecycle.go): activation now GETs the session, replaces only this route's entry in `config.webhooks` and PUTs the rest of the document back byte-for-byte (name/status/engine/proxy/config.debug survive), includes `hmac.key` only when a secret is set, and deactivation removes only this route's entry. Registration is now a hand-written Go lifecycle because a declarative descriptor cannot merge; the merge description lives in `packs/waha/webhook-lifecycle.json` — a deviation from the ticket's "add check/remove descriptors" wording, because `nodepack.Decode` is strict and the merge fields cannot live inside `trigger.lifecycle`. Proof: `go test ./packs/waha/ -count=1` (and `-race`) green in an isolated worktree; the pre-fix body PUT a document containing only this webhook, which restarts the customer's live session and evicts their other webhooks.

## Progress 2 (WebhookParity 2026-09-20) — webhook HTTP surface

Landed in `internal/webhook/*`, `nodes/webhook.go`, `nodes/telegram_lifecycle.go`:

- **Respond answers when it runs.** The Respond to Webhook executor no longer writes `$response` onto its items: it emits an engine node event (`webhook.ResponseEventName`) and passes its input through unchanged, which is n8n's behaviour and stops the response leaking into every downstream `$json` and into the stored output. The HTTP boundary subscribes to the broker for the execution, replays retained events so a fast node is not missed, and answers the moment a response event arrives while the run continues; the durable record is still watched (through the cheap `ExecutionState` read when the runner offers one) for a run that fails before reaching any Respond node. A failure now answers n8n's generic `{"message":"Error in workflow"}` instead of leaking node names and upstream error text, and a graph that finishes without reaching a Respond node answers 200 empty rather than 500.
- **Item shape is n8n's.** `kilasflow.webhook` now registers `ShapeN8NCore`: `{body, headers, params, query, webhookUrl, executionMode}`. Header names are lower-cased, `host` is included, repeated query keys are arrays, `params` is filled from a `:name` segment after the route (`/webhook/<route>/42`), and the envelope's own `method`/`path`/`contentType` keys are gone.
- **Bodies.** Multipart is parsed (fields to the body, repeats to lists, file parts with name/mime/size and base64 bytes), XML is parsed into an object with n8n's conventions (`$` attributes, `_` text, repeated children as arrays), repeated form keys become lists, and a non-UTF-8 body keeps its bytes base64-encoded instead of being corrupted through a string. Multipart file *bytes* still ride in the payload rather than the execution-scoped binary store, which is not reachable from the HTTP boundary yet — recorded as remaining.
- **CORS and access rules.** OPTIONS is answered 204 with `Allow-Methods` (the methods actually bound, via the new `ResolveRoute`), `Allow-Headers` and `Max-Age`; `Access-Control-Allow-Origin` echoes the caller's origin when `options.allowedOrigins` permits it (default `*`); `options.ipWhitelist` refuses other callers with 403 (deliberately not trusting `X-Forwarded-For`, so it fails closed behind a proxy); `options.ignoreBots` acknowledges and does not run a crawler delivery.
- **Immediate mode.** Answers n8n's `{"message":"Workflow was started"}` with `options.responseCode`, `options.responseData`, `options.responseHeaders` and `options.noResponseBody` honoured. The execution id is no longer in the body (n8n does not send one).
- **Last node.** `responseData` `noData` is an empty 200 (was 204), `allEntries` is always an array, and an empty last node answers n8n's `{"message":"No item to return was found"}` rather than echoing the trigger's own request data — which could leak proxy-added headers to an unauthenticated caller.
- **Methods and validation.** `httpMethod` defaults to GET (n8n's default, which its exports omit), HEAD is accepted, and `multipleMethods` + `httpMethods` bind one route per method. Activation now refuses a `basicAuth`/`headerAuth` webhook with no matching credential attached and refuses `jwtAuth` outright (n8n's JWT mode has no verification here yet) instead of activating an endpoint that answers 500 to every caller or is silently open.
- **Disabled triggers** are skipped by `Extract` (EngineFlow's BUG-c241hm): a switched-off trigger must not open an endpoint the runner refuses to start.
- **Respond options.** `options.responseHeaders.entries` and `responseKey` are honoured; a text response is served as `text/html` like n8n; a redirect defaults to 307, not 302, so a POST stays a POST.
- **Path labels** are no longer identities (see the repository slice above).

Scoped proof: `go test ./internal/webhook/ -count=1` and `go test ./nodes/ -count=1` green in an isolated worktree (baseline HEAD + only my files, because siblings were mid-edit), except one assertion noted below. New coverage: item shape + path params + CORS preflight/echo/refusal + IP allow-list + immediate acknowledgement + default respondWith + no `$response` leak + disabled trigger + delivery-level headers/multipart/XML/repeated keys/non-UTF-8.

Open, blocked on another owner (ruled by Main, EngineCore landing it now): `repository.triggerPayload` still redacts the trigger input's `headers` map at write time, and the runner rehydrates the trigger item from the stored record — so `TestWebhookPayloadCarriesTheRequestShape`'s assertion that a Set node reads `{{ $json.headers['x-api-key'] }}` stays red until that commit lands. Main ruled YES (store the trigger input unredacted; read surfaces keep redacting). Once it lands I re-run and confirm.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (11):
  - `8f19f17d` — BUG-cq4yk3: point the README link at the repository's real location — docs
  - `64aaf8a9` — BUG-cq4yk3: find the reference host's run through the tenant key — docs
  - `2606e09f` — chore(pine): WebhookParity slices to testing (cq4yk3, kzkvv6, pwckhd, nqpvf6)
  - `f0956e86` — BUG-cq4yk3 BUG-kzkvv6 FEAT-j5s2n4: document trigger-header storage, the webhook answers and the HTTP node's output — docs
  - `677010c9` — BUG-cq4yk3: pin the ruled split — the run reads the caller's headers, the read surfaces redact them — webhook
  - `13e35e27` — BUG-cq4yk3: the stored trigger input is what the run executes on, so storage keeps it and the read surfaces redact — repository
  - `90a005b1` — BUG-cq4yk3: n8n webhook surface — item shape, CORS, allow-lists, immediate/lastNode/Respond answers, methods, auth validation, disabled triggers — webhook
  - `b264466b` — BUG-cq4yk3: a claimed webhook path answers 409, not an unexplained 500 — api/webhook
  - `9485cfdf` — BUG-cq4yk3: WAHA auto-register reads, merges and unregisters one route instead of replacing the session config — waha
  - `5693d985` — BUG-cq4yk3: webhook path labels are not identities — one path may bind several methods and be reused — repository
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
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
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
 434 files changed, 61969 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Critical path + Important
- **P1 cross-process regression**: `executeRespond` now publishes the answer only as an engine event (`nodes/webhook.go:875`) and the boundary reads it only from its own broker (`internal/webhook/webhook.go:730-750`); node events carry no Data across processes (`internal/engine/multiproc.go:62-80`, `:186-200`). In the documented api+worker split on PostgreSQL, a `responseMode=responseNode` webhook never sees the answer and answers an empty 200 (:871-874) — the durable `findResponse` this replaced worked. Fix: keep a durable copy (persist it with the Respond node's run) and consult it in the terminal fallback.
- **Multi-method binds only the stale single method**: `declaredMethods` (:1131-1139) reads `httpMethod` before `httpMethods`, while the node's own authority (`nodes/webhook.go:601-611`) checks `multipleMethods` first; a node storing `httpMethod:"POST"` beside `multipleMethods:true, httpMethods:[GET,POST]` binds only POST, so the GET half 404s. Fix: honour `multipleMethods`/`httpMethods` first.
- **Form page served before the allow-list and credential checks**: the hosted-page branch (`internal/webhook/webhook.go:114-121`) returns before `addressAllowed`/`authenticate`, so a form with `ipWhitelist` or basicAuth exposes its title/labels/options to any caller. Fix: run those checks before writing the page.

## Progress — FixImporterFindings (2026-09-20)

Landed from the Reopened-by-review list (commit IDs below). Status stays `doing`: the
ReviewImporter findings assigned to this slice are closed, the adversarial live re-verify
is not.

**P1 cross-process Respond (commit `f2bac0d`, with the runner hunks in `9b690d8`)** — the
answer a Respond to Webhook node produces is now captured by the runner while the node runs
and persisted with the node's own run row (new nullable `response` column on
`execution_node_runs`, migration 000013 sqlite+postgres; `execution.NodeRun.Response`,
`engine.NodeRun.Response`, `engine.ResponseEventName`). `internal/webhook.respondFromExecution`
consults that durable copy first — before the status gate — so a split api+worker deployment
answers the node's body instead of an empty 200.
Proof: `TestWebhookAnswersFromTheDurableResponseWhenTheBoundarySeesNoEvent` (boundary with no
broker, i.e. the relay carried identifiers only). Pre-fix it answered `200` with an empty body;
post-fix `201` `{"ok":true}` with `X-Kilas: yes`. `go test ./internal/webhook/ -count=1` ok.

**declaredMethods precedence (commit `86e690b`)** — `multipleMethods` now decides which
parameter is live, as the node's own definition does, so `httpMethod:"POST"` beside
`multipleMethods:true, httpMethods:[GET,POST]` binds both. Proof:
`TestWebhookBindsEveryMethodTheNodeSelected`; pre-fix `GET status = 404`.

**Form page before the allow-list/credential (commit `86e690b`)** — the hosted page and the
delivery now share one `admit` (address allow-list, then credential). Proof:
`TestFormPageIsRefusedToAnAddressOutsideTheAllowList`; pre-fix the page was `200` with the form
in the body.

Still open in this ticket (not in this slice): the webhook item shape/params/`webhookUrl`
medium item, the query/header array items, CORS-with-`allowedOrigins` per node, JWT auth
implementation, path-label uniqueness (repository/API), and the URL-discovery UX item —
`internal/api/**` was off-limits for this pass.
