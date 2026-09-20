---
id: BUG-6jvcs5
title: 'AI memory/tools long tail: window semantics, tool mapping, streaming, iterations, UX'
status: doing
priority: medium
labels:
    - ai
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T02:37:46Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 11 finding(s) from dims: find:ai-ollama.

---
### Data table Tool offers every operation (insert, update, upsert, delete...) but always performs a filtered read [find:ai-ollama] (medium/bug) · area: AI tools / Data table Tool · confidence: high

The tool variant keeps the full resource/operation/columns UI from the step node, but its Definition and Invoke ignore them and always expose a read schema (match, conditions, limit). A tool configured to insert does nothing, and the agent tells the user it recorded the data.

Evidence: Private :8185 wf "[ai-ollama] t9 datatable tools": log_order = datastoreTool operation=insert, columns {sku: $fromAI, qty: $fromAI} on table "[ai-ollama] orders". exec_01a0b8eb-ed1f-769b-9587-7e3f6c015c1d: the model is offered the read schema, calls log_order twice with conditions [{sku eq A1}], gets {"rows":[]}, and answers "I've successfully ... recorded an order for 2 units". GET /datastores/<orders>/rows returns []. Status: succeeded. The importer's dataTableToolToKilas carries write operations without any issue.

n8n behavior: The Data Table tool performs the configured operation (insert, update, upsert, delete or get) with $fromAI-filled columns.

Impact: Writes from agent workflows silently do nothing ("log to a table" is a common pattern), and the operation dropdown has no effect.

Suggested fix: Implement the write operations (behind an opt-in toggle, as FEAT-n19dch proposed). Otherwise hide resource/operation/columns on the tool variant, refuse operation != get at validation, and report a blocking issue when importing a write-mode dataTableTool.

Files: nodes/datastore.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-n19dch (done; chose read-only scope but did not hide or refuse the other operations)

---
### Token streaming has no consumer and floods the execution event feed; late subscribers never receive execution.completed [find:ai-ollama] (medium/bug) · area: AI streaming / execution events · confidence: high

Each streamed token is published as its own ai.model.delta event. The broker keeps 256 events per execution and replays them into a 64-slot subscriber queue, dropping the overflow silently. After a long streamed answer, a client opening /events receives 64 deltas, never gets the terminal event, and the connection never closes. No chat or webhook streaming uses the deltas.

Evidence: Private :8185 wf "[ai-ollama] t13 streaming chain long" (lmChatOpenAi stream=true -> Ollama, about 350 words), exec_01a0b8f5-960c-71d4-aae2-98d07125f59c succeeded. `curl -m 15 /api/v1/executions/<id>/events` after completion returned exactly 64 events, all ai.model.delta (ids 113-176), then only heartbeats until the client timed out. Short streamed run t12 (42 deltas) replayed completely. internal/events/events.go defaultBufferPerExecution=256, defaultSubscriberQueue=64, and deliver() only sets lagged, which StreamEvents never reports. The node definitions declare stream default true.

n8n behavior: Tokens stream to the Chat Trigger or webhook streaming response and are not stored as execution events.

Impact: Live execution views opened mid-run, or reconnecting, on AI runs with long answers stay "Running". Tool and model events are pushed out of the replay history, and per-token events add load with no user benefit.

Suggested fix: Coalesce deltas (e.g. every 250 ms) or keep them out of the replay history. Always deliver terminal events (replay from the durable record) and tell the client when it has fallen behind. Default stream=false until a streaming consumer exists.

Files: nodes/ai.go, internal/events/events.go, internal/api/handlers/executions.go, web/src/lib/workflow-editor/event-stream.svelte.ts

Existing tickets: FEAT-cgm1y3 (done, enableStreaming), FEAT-mvegj5 (done, stream option)

---
### Reaching max iterations fails the execution, where n8n returns "Agent stopped due to max iterations." and continues [find:ai-ollama] (medium/parity-gap) · area: AI Agent loop · confidence: high

LoopRuntime returns an error at the iteration limit, and AgentExecutor turns it into a node failure. The partial conversation is discarded, even though the loop keeps it for the inspector.

Evidence: Private :8185 wf "[ai-ollama] t14 max iterations" (maxIterations 4, stub that always calls a tool): failed with `agent stopped after 4 iterations without a final answer`, and the agent has no output.

n8n behavior: Verified live on 2.33.7 (wf "[ai-ollama] n8n agent maxiter (strict mock)", maxIterations 3): success, 3 model runs, item {"output":"Agent stopped due to max iterations."}.

Impact: An agent that keeps retrying a flaky tool, which local models do often, breaks the workflow and the chat reply instead of returning a fallback answer.

Suggested fix: At the limit, return a successful item with n8n's message (plus intermediateSteps when enabled). Optionally make fail-vs-return configurable.

Files: internal/ai/agent.go, nodes/ai.go

Existing tickets: FEAT-cgm1y3 (done, agent parity)

---
### Model retries fire back-to-back with no backoff, and an unset Max Retries means 0 retries although the UI shows a default of 2 [find:ai-ollama] (medium/bug) · area: AI model transport · confidence: high

openai.go post() retries 429/5xx responses immediately, with no delay and no Retry-After handling. resolveModel treats a missing maxRetries option as 0, while the option declares a default of 2, and the generic chat model has no retry option at all.

Evidence: Private :8185 wf "[ai-ollama] t14 err500 retries2" (maxRetries 2, stub /err500): 3 requests 1 ms apart, failure in under 1 s. wf "[ai-ollama] t14 err429 default" (maxRetries not set): 1 request, immediate failure on 429. Unknown model and connection errors produce clear messages.

n8n behavior: OpenAI and OpenRouter models retry rate-limit and 5xx errors with exponential backoff, with maxRetries 2 by default when the option is not set.

Impact: A 429 fails the run immediately, or all retries are used up within milliseconds. Imported workflows, which rarely set maxRetries, lose n8n's default retries.

Suggested fix: Use exponential backoff with jitter and honour Retry-After. Default maxRetries to 2 when unset, and add the option to kilasflow.chatModel.

Files: internal/ai/openai.go, nodes/ai.go

Existing tickets: FEAT-mvegj5 (done, provider chat models)

---
### Imported memory window changes meaning: contextWindowLength (interactions) is copied 1:1 into maxMessages (raw messages including tool turns) [find:ai-ollama] (medium/parity-gap) · area: importer / Simple Memory · confidence: high

n8n's contextWindowLength counts human/AI interactions; KilasFlow's maxMessages counts every stored message, including tool calls, tool results and repair turns. The importer copies the number unchanged, and the defaults differ (5 interactions, i.e. 10 messages, versus 40 messages).

Evidence: internal/interop/n8n/parameters.go memoryToKilas: `parameters["maxMessages"] = length`. One calculator turn stores 4 messages (t2 proxy log). Top-100 contextWindowLength values: 4 (2413), 10 (6 templates), 20 (3), 30 (2, including 6270), 40, 50; 15 templates use the default.

n8n behavior: The window is the last k human/AI interactions (2k messages, no tool steps); default k=5.

Impact: Imported agents remember far less than their authors intended when they use tools (a window of 4 keeps one exchange), or far more tokens by default, which matters with a 4096-token Ollama context. Small windows also make the orphan-tool-message 400 more frequent.

Suggested fix: Map k to k interactions (pair-aligned trimming, or maxMessages = 2k with pair alignment), default to 5 on import, and label the field in interactions.

Files: internal/interop/n8n/parameters.go, nodes/ai.go, internal/ai/memory.go

Existing tickets: FEAT-347egc (done, LangChain mapping), FEAT-096vs9 (done)

---
### Workflow Tool pointing at an inactive sub-workflow: the parent activates, the call fails at run time, and the agent tells the user the data doesn't exist [find:ai-ollama] (medium/ux) · area: Workflow Tool / activation · confidence: high

Activation does not check that the Workflow Tool's target is active. At run time the tool returns "repository record not found: active workflow" to the model, which then tells the user the data doesn't exist, while the execution shows success.

Evidence: On :8090, before activating the sub-workflow: exec_01a0b8e5-ceb7-746c-b121-2523a947a75c succeeded. The tool message was `tool failed: repository record not found: active workflow`, and the agent answered "I couldn't find any information for order A-1001". Private :8185 wf "[ai-ollama] parent refs inactive sub" (Webhook -> agent + Workflow Tool -> inactive sub): /activate returned 200. After the sub was activated, t6 returned the correct shipped status.

n8n behavior: Verified live on 2.33.7: publishing the parent is refused with `Cannot publish workflow: Node "Exec" references workflow ... which is not published. Please publish all referenced sub-workflows first.`

Impact: In production, agents give false "not found" answers while executions show success, and the error message is internal jargon.

Suggested fix: At activation, check Workflow Tool and Execute Sub-workflow targets and refuse with a message naming them. At run time, show tool failures as node warnings or events, worded for humans.

Files: nodes/ai.go, internal/engine (activation validation)

---
### Only in-process Simple Memory exists: conversations are lost on restart and not shared across workers, and Postgres chat memory has no equivalent [find:ai-ollama] (medium/parity-gap) · area: AI memory · confidence: high

The only ai.Memory is BufferMemory, which lives in the process and is deliberately not durable. No durable chat memory node exists, and memoryPostgresChat imports as a placeholder.

Evidence: cmd/kilasflow/main.go:171 wires BufferMemory. Private :8185 wf "[ai-ollama] t15 memory restart": before the restart, turn 2 sent the full history; after restarting the server, the same session sent only ['what is my code?']. memoryPostgresChat (templates 2752, 3859) is blocked.

n8n behavior: Simple Memory is also in-process in n8n (so that node matches), but n8n offers Postgres, Redis, MongoDB, Xata and Zep chat memories for production and queue mode.

Impact: Production chatbots (the WhatsApp and Telegram packs are core use cases) forget every conversation on deploy. Multi-worker Postgres deployments split a session's history across processes.

Suggested fix: Implement a database-backed ai.Memory (PolicyMemory already carries the retention bounds), expose it as a Database/Postgres Chat Memory node, and map memoryPostgresChat onto it.

Files: internal/ai/memory.go, cmd/kilasflow/main.go, nodes/ai.go, internal/interop/n8n/n8n.go

Existing tickets: FEAT-096vs9 (done; kept memory in-process)

---
### n8n's current tool variants (n8n-nodes-base.httpRequestTool, postgresTool...) are not mapped, which blocks n8n's own "Build your first AI agent" template [find:ai-ollama] (medium/parity-gap) · area: importer / AI tools · confidence: high

Only the legacy @n8n/n8n-nodes-langchain.toolHttpRequest is mapped. Current n8n editors export "<base>Tool" types (HTTP Request v4.2 parameters plus toolDescription and $fromAI), which import as blocking placeholders even though kilasflow.httpTool is literally httpRequestNode() plus a tool name and description.

Evidence: Import of template 6270 on :8185: `blocking | Get Weather | n8n-nodes-base.httpRequestTool`. After re-binding by hand (httpRequestTool -> kilasflow.httpTool with parameters converted via the httpRequest mapping, the held-back ai_tool edge re-added, chatTrigger -> manual, Gemini -> lmChatOpenAi with the Ollama base URL, rssFeedReadTool dropped), wf "[ai-ollama] tpl 6270 rebound to Ollama" succeeded in 67 s with a correct Lyon forecast from api.open-meteo.com via the $fromAI query parameters. Base *Tool usage in the top-100: googleCalendarTool 8 nodes, postgresTool 3 (2859), googleSheetsTool 3, googleDocsTool 2, baserowTool 2, gmailTool 1, rssFeedReadTool 1, httpRequestTool 1 (6270).

n8n behavior: Any usableAsTool node is exported as <type>Tool with the base node's parameters plus toolDescription.

Impact: Agents built in a current n8n editor with HTTP or SQL tools import blocked, including n8n's onboarding template 6270.

Suggested fix: Add a generic mapping: when <base> is mapped, map <base>Tool to the KilasFlow tool variant (httpRequestTool -> kilasflow.httpTool via httpToKilas plus toolDescription) and keep the ai_tool edge. Add SQL tool variants.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-je4f4t (done; usable-as-tool family)

---
### Calculator Tool needs a hand-written expression parameter, and the obvious literal value silently ignores the model [find:ai-ollama] (medium/ux) · area: AI tools / Calculator Tool · confidence: high

kilasflow.calculatorTool fails validation without `expression`. If a user fills it with a literal (as the field label and example suggest), every call returns that literal's result whatever the model asked for. It only works with {{ $json.expression }} or $fromAI, which nothing in the UI suggests.

Evidence: Activation of a calculatorTool with only toolDescription -> 422 `expression is required`. With expression "1+1": exec_01a0b8e7-3b85-72a7-a9a1-22ca528fb689, the model asked for "17 * 23" twice and got {"result":2} both times, with status success.

n8n behavior: toolCalculator has no parameters; the model's input string is evaluated. Verified on 2.33.7: 17*23 -> 391, 2^10 -> 1024, pi*2 -> 6.28; sqrt(), round(), ** and % are not supported there either, so arithmetic coverage is comparable (KilasFlow lacks pi but supports %).

Impact: The natural configuration gives constant wrong results with a green execution, and imported n8n calculators would need this parameter synthesised.

Suggested fix: Make the tool zero-config (the model supplies the expression; the parameter becomes optional or hidden on the tool variant) and add pi and e constants.

Files: nodes/ai.go

Existing tickets: FEAT-je4f4t (done)

---
### AI Agent item shape differs from n8n: extra usage/iterations/toolCalls keys, and intermediateSteps is a raw message list [find:ai-ollama] (low/parity-gap) · area: AI Agent output · confidence: high

KilasFlow agent items always contain usage, iterations and toolCalls. intermediateSteps is the full message list, including the system prompt, instead of n8n's [{action, observation}].

Evidence: KilasFlow item: {output, usage, iterations, toolCalls, intermediateSteps:[{role:system...},{role:assistant,toolCalls}...]}. Live n8n 2.33.7 wf "[ai-ollama] n8n agent intermediate steps (strict mock)": {output, intermediateSteps:[{action:{tool, toolInput, toolCallId, log, messageLog}, observation}]}; without the option n8n returns only {output}.

n8n behavior: {output} only, or {output, intermediateSteps:[{action, observation}]} when enabled.

Impact: Low: no top-100 template reads intermediateSteps. However, Respond to Webhook with the incoming item exposes usage, iterations and toolCalls to API callers, and the system prompt is echoed into output data.

Suggested fix: Emit n8n's step shape, and move usage/iterations/toolCalls into execution metadata or behind an opt-in option.

Files: nodes/ai.go

Existing tickets: FEAT-cgm1y3 (done; chose the message-list shape)

---
### Local models need a fake API key: there is no Ollama credential type, and the OpenAI-Compatible Chat Model requires a non-empty bearer token [find:ai-ollama] (low/ux) · area: AI credentials / chat model · confidence: high

Ollama needs no authentication, but kilasflow.chatModel refuses to run without an httpBearerAuth credential, and the credential API rejects an empty token. The node's defaults point at api.openai.com and gpt-4o-mini.

Evidence: GET /credential-types lists no Ollama type. POST /credentials {type:httpBearerAuth, fields:{token:""}} -> 422 `credential field "token" is required`. validateChatModelConfiguration requires the credential. The working setup used a dummy token "ollama", which is sent as Authorization: Bearer ollama. Model listing via load-options works once a credential is attached.

n8n behavior: The ollamaApi credential has a base URL and an optional API key. The live n8n run "[ai-ollama] n8n ollama agent + calculator" worked with it (14.8 s).

Impact: Adds friction for self-hosted and air-gapped users, the target audience for an embeddable engine, and stores a meaningless secret.

Suggested fix: Make the credential optional on kilasflow.chatModel, or add an ollamaApi-style credential (base URL plus optional key), which the lmChatOllama mapping also needs.

Files: nodes/ai.go, internal/credentials

Existing tickets: FEAT-kwxxd0 (done)

## Acceptance criteria

- [ ] Data table Tool offers every operation (insert, update, upsert, delete...) but always performs a filtered read
- [ ] Token streaming has no consumer and floods the execution event feed; late subscribers never receive execution.
- [ ] Reaching max iterations fails the execution, where n8n returns "Agent stopped due to max iterations." and cont
- [ ] Model retries fire back-to-back with no backoff, and an unset Max Retries means 0 retries although the UI show
- [ ] Imported memory window changes meaning: contextWindowLength (interactions) is copied 1:1 into maxMessages (raw
- [ ] Workflow Tool pointing at an inactive sub-workflow: the parent activates, the call fails at run time, and the 
- [ ] Only in-process Simple Memory exists: conversations are lost on restart and not shared across workers, and Pos
- [ ] n8n's current tool variants (n8n-nodes-base.httpRequestTool, postgresTool...) are not mapped, which blocks n8n
- [ ] Calculator Tool needs a hand-written expression parameter, and the obvious literal value silently ignores the 
- [ ] AI Agent item shape differs from n8n: extra usage/iterations/toolCalls keys, and intermediateSteps is a raw me
- [ ] Local models need a fake API key: there is no Ollama credential type, and the OpenAI-Compatible Chat Model req
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress — AINodes2 (2026-09-20)

Landed in this pass:
- **max iterations**: `LoopRuntime` now finishes with `ai.MaxIterationsMessage` ("Agent stopped due to max iterations.") and reports the run completed, as n8n does, instead of failing the node; the partial conversation and the answer are stored in memory.
- **window semantics**: only human/AI turns are stored and the window is trimmed to turn boundaries, so `maxMessages` counts real conversation messages (two per exchange) and an imported window keeps whole exchanges.
- **streaming flood**: `nodes/ai.go` coalesces `ai.model.delta` events to at most one per 250 ms (flushing before every other event and at the end), so a long streamed answer no longer evicts the tool/model events from the execution's bounded history. Terminal-event delivery for late subscribers remains `internal/events` (EngineWaits).
- **Calculator Tool**: no configured expression is required any more, the model's `expression` argument wins over a literal parameter (the literal is only a fallback), and `pi`/`e` are evaluated.
- **AI Agent item shape**: `intermediateSteps` is now n8n's `[{action:{tool,toolInput,toolCallId,log,messageLog}, observation}]` rather than the raw message list, so the system prompt is no longer echoed into output data. `output`/`usage`/`iterations`/`toolCalls` are unchanged (cost reporting reads them).
- **Workflow Tool on an inactive target**: the runtime failure is translated for the model — it now says the sub-workflow is not active and that this is a configuration problem, instead of "repository record not found", which agents repeated to users as "the data does not exist". Activation-time refusal (checking tool targets before publishing) is `internal/engine`/`internal/api`, not in this slice — reported for the owner.
- **Ollama credential**: covered in BUG-tcqkad (credential optional, no `Authorization` header without one).

Proof (scoped): `go test ./internal/ai/ -run 'TestAgentStopsAtTheIterationBound|TestMaxIterationsAnswerIsRememberedAsTheReply|TestMemoryWindowNeverOpensWithAToolStep' -count=1`, `go test ./nodes/ -run 'TestReturnIntermediateSteps|TestStreamedTokensReachTheFeedCoalesced|TestCalculatorTool|TestCalculatorUnderstandsNamedConstants' -count=1`.
**Commits**: f246ea9 (internal/ai: memory window, loop, transport), 047b8d1 (nodes/ai.go: tools, memory key, chain, vision, retries). Both land every ticket in this batch because `nodes/ai.go` and `internal/ai/agent.go` are shared by all five.

**Scoped proof (final, tree at 047b8d1)**:
- `go test ./internal/ai/ -count=1` → ok
- `go test ./nodes/ -run 'TestAI|TestAgent|TestMemory|TestChain|TestHTTPTool|TestCalculator|TestChatModel|TestAnUnset|TestStreamed|TestReturnIntermediate|TestToolName|TestDuplicate' -count=1` → ok

**Known red, not mine**: `go test ./nodes/ -count=1` also runs `TestEveryAttachedToolReachesTheAgentInAStableOrder`, which fails with `node "AI Agent": connect an OpenAI Chat Model to the model port`. Cause is `internal/engine/runner.go` `push()`/`next()`: a pending invocation built by `push` carries only the main-port items, so a node started by a branch loses its typed ports (model/memory/tools) — the fallback path merges them via `nodeInput`, the pushed path does not. Reported to EngineFlow; the compiled IR is correct (verified: the `ai_languageModel` edge is present).

**Update**: the `internal/engine/runner.go` typed-port regression above is fixed by EngineFlow in 976b2ce (a pushed invocation now merges its typed inputs from the nodes that have run). Re-verified at 4cc80ff: `go test ./nodes/ -count=1` → ok (14s, whole package, including TestEveryAttachedToolReachesTheAgentInAStableOrder) and `go test ./internal/ai/ -count=1` → ok.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (5):
  - `a241fe30` — BUG-mz8xrb BUG-277a2m BUG-ztzxck BUG-tcqkad BUG-6jvcs5: re-verify after engine 976b2ce — full nodes package green
  - `2b632614` — BUG-mz8xrb BUG-277a2m BUG-ztzxck BUG-tcqkad BUG-6jvcs5: record testing state — AI node/memory slices landed with scoped proof
  - `047b8d12` — BUG-mz8xrb BUG-277a2m BUG-tcqkad BUG-6jvcs5: per-call tool args, per-item memory key, chain shape/schema, vision, retries — AI nodes
  - `f246ea97` — BUG-ztzxck BUG-tcqkad BUG-6jvcs5: a memory window is a conversation, not a log — AI memory/loop
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
 .pine/tickets/BUG-6jvcs5.md                        |  238 ++
 .pine/tickets/BUG-8dmp5y.md                        |  183 ++
 .pine/tickets/BUG-8h4yy1.md                        |   46 +
 .pine/tickets/BUG-8sb0jw.md                        |  239 ++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
 .pine/tickets/BUG-c241hm.md                        |  154 ++
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
 434 files changed, 58255 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Important
- **Calculator Tool unreachable**: `nodes/ai.go:2162-2166` relaxes the *validator* so the expression is optional, but the property is still declared `Required: true` with no Default (:2150-2153) and the tool variant inherits it, so `registry.RequiredFor` (:628-648) → `compiler.go:289-300` refuses the node with `ErrorRequiredConfig`. Any imported calculator (importer writes only toolName/toolDescription) still cannot be activated; the test misses it because it calls `definition.Validate` instead of compiling a document. Fix: clear the requirement on the tool variant (Required false or a Default) and add a compile-level test.
