---
id: BUG-6jvcs5
title: 'AI memory/tools long tail: window semantics, tool mapping, streaming, iterations, UX'
status: testing
priority: medium
labels:
    - ai
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:48:14Z"
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
