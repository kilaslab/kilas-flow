---
id: BUG-tcqkad
title: 'AI agent loop defects: parser+memory 400, chain shape/schema, timeout, vision, retries'
status: doing
priority: high
labels:
    - ai
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T03:11:24Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 8 finding(s) from dims: find:ai-ollama.

---
### Agent + Structured Output Parser + Memory: every follow-up turn gets a 400 from OpenAI-strict providers [find:ai-ollama] (high/bug) · area: AI Agent loop / memory · confidence: high

When a parser is attached, the agent saves the assistant turn that calls format_final_json_response to memory, with no matching tool result. On the next run this unanswered tool_call is replayed, and OpenAI-compatible providers that enforce message order reject the request.

Evidence: wf "[ai-ollama] t4 parser+memory (strict mock)": AI Agent with kilasflow.chatModel pointed at http://127.0.0.1:8095/strict/v1 (a stub that enforces OpenAI's tool-message ordering rules), Simple Memory (customKey) and Structured Output Parser. Turn 1 returns {"answer":"ok"}. Turn 2 in the same session fails: `model request failed with status 400: ... msg[2] role=user arrives while tool_calls ['call_9'] are unanswered`. Cause: internal/ai/agent.go Run() appends the format-tool assistant turn to newTurns, and finishStructuredRun() saves it via appendSessionMemory with no tool message. Ollama accepts this history, so the opt-in Ollama test suite cannot catch it.

n8n behavior: Verified on live n8n 2.33.7 with the same strict stub (wf "[ai-ollama] n8n agent parser+memory (strict mock)"): 3 turns all return 200 {"output":{"answer":"ok"}}. The turn-3 history is only user and assistant messages; the format tool call is never stored.

Impact: Every chat agent that combines memory with a structured output parser fails from the second message on with OpenAI, OpenRouter or Azure, the providers KilasFlow ships nodes for. In the top-100 templates, 30 use memoryBufferWindow and 20 use outputParserStructured.

Suggested fix: Store only the user turn and the final assistant answer, as n8n does. Failing that, add a synthetic tool result for the format call, or strip trailing unanswered tool_calls before saving. Also check that the history is a valid sequence when memory is loaded.

Files: internal/ai/agent.go

Existing tickets: FEAT-sbnejr (done, structured output parser), FEAT-096vs9 (done, memory semantics)

---
### Basic LLM Chain outputs {output} where n8n outputs {text}, so every imported $json.text reference to a chain result is empty [find:ai-ollama] (high/bug) · area: Basic LLM Chain output shape · confidence: high

Without a parser, ChainExecutor returns {"output": content}; n8n's chainLlm returns {"text": content}. The importer does not rewrite downstream references, so imported flows run but pass empty values downstream.

Evidence: nodes/ai.go ChainExecutor.completeChainItem: `workflow.Item{JSON: map[string]any{"output": response.Message.Content}}`. Live n8n 2.33.7 wf "[ai-ollama] n8n chain output shape (strict mock)" (Webhook -> chainLlm 1.5, lastNode) responds with {"text":"final answer"}. End to end: template 2729 re-bound to Ollama on private :8185 (exec_01a0b8ff-da42-74ca-90f6-dfeb0cc7ef86) fails at the next node with `node "JSON to Object": assignment "response" is declared an object and its value is not`, because `{{ $json.text }}` is empty. Top-100 templates that read a parser-less chain's result as .text: 2271, 2466, 2679, 2729, 2878.

n8n behavior: chainLlm returns {text} without an output parser and {output} with one.

Impact: Imported chain workflows succeed while sending empty strings downstream: email drafts with empty bodies, empty JSON objects, empty HTML. At least 5 top-100 templates are affected, with no warning.

Suggested fix: Emit `text` when no parser is attached and keep `output` for the parser case. Optionally also emit `output` as an alias for native documents created before the fix.

Files: nodes/ai.go

Existing tickets: FEAT-96p7m3 (done; its spec chose {"output": text})

---
### Basic LLM Chain + Structured Output Parser never sends the schema to the model, so the first answer is free text and the run usually fails [find:ai-ollama] (high/bug) · area: Basic LLM Chain / output parser · confidence: high

The chain uses the parser schema only to validate the answer afterwards. No format instructions or schema are added to the prompt, and each repair turn reveals only one validation error, so the model has to guess field names.

Evidence: Private :8185 wf "[ai-ollama] t10 chain + parser" (chainLlm define "Extract the person from this text: ...", JSON Schema {fullName, ageYears integer, homeCity}, all required, lmChatOpenAi -> Ollama). exec_01a0b8f0-0b4f-7f03-bd9c-f6eb8208c549 FAILED after 3 model calls: `output did not match the required format: output: missing required property "ageYears"; raw output: {"fullName":"Budi"}`. The logged first request is a single user message with no schema. The repair turns say only "output is not valid JSON" and then "missing required property fullName". The agent path works (t11 succeeded in 11 s) because it offers the schema as the format_final_json_response tool.

n8n behavior: When a parser is connected, the chain appends the parser's format instructions, including the JSON Schema, to the prompt, so the first answer is normally valid.

Impact: Extraction and classification flows built on "LLM Chain + Structured Output Parser" fail or burn retries; with local models they almost always fail. In the top-100, 18 templates use chainLlm and 20 use outputParserStructured.

Suggested fix: When a parser is attached, append format instructions that contain the schema, and/or send response_format json_schema where the provider supports it. Include the full schema in repair turns.

Files: nodes/ai.go

Existing tickets: FEAT-sbnejr (done, structured output parser), FEAT-96p7m3 (done, chain)

---
### Importer silently drops every n8n Workflow Tool v2 input mapping (reads value.mapping; n8n stores the fields directly under value) [find:ai-ollama] (high/bug) · area: importer / Workflow Tool · confidence: high

workflowInputsToKilas looks for workflowInputs.value.mapping, but n8n toolWorkflow 2.x stores the field map directly under workflowInputs.value. Every mapping is therefore dropped with no import issue. The exporter writes value.{field}, so KilasFlow cannot even round-trip its own export.

Evidence: internal/interop/n8n/parameters.go workflowInputsToKilas: `entries, _ := inner["mapping"].(map[string]any)`; an empty result returns reported=false. The n8n shape (templates 2085, 3050, 3443, 3514, 3770, 3790) is {mappingMode:"defineBelow", value:{ticker:"={{ $fromAI('ticker', ...) }}"}, schema:[...]}. Imported template 3790 (wf_01a0af16-37ee-76f1-bae3-4132dbea2507), node "Technical Analysis Tool", has only {toolDescription, toolName, workflowId}, and the import report has no issue about it. A native workflowInputs map {orderId: $fromAI(...)} works (t6 on :8090 returned the shipped status once the sub-workflow was active).

n8n behavior: The model gets one typed argument per mapped $fromAI field, and the sub-workflow receives them as named fields, including fixed values.

Impact: Imported agent tools lose their argument schema: the model is offered a generic {input:{}} object, and fixed values such as operation:"column_names" in 2085 or function_name in 3514 disappear, so sub-workflows take the wrong branch. Affects 6 top-100 templates, with nothing in the import report.

Suggested fix: Read `value` as the field map (fall back to value.mapping). Carry fixed values and $fromAI expressions, and report anything unreadable as lossy. Add an import(export(x)) round-trip test.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-347egc (done; claims workflowInputs resourceMapper entries are carried, so this is a regression or incomplete)

---
### OpenAI-Compatible Chat Model (the Ollama path) has no timeout option, so the whole agent run is killed at the 30 s outbound timeout [find:ai-ollama] (high/bug) · area: AI chat model / timeouts · confidence: high

kilasflow.chatModel has no Options collection, so modelTimeout() falls back to outbound.timeout (30 s). That deadline covers the whole agent loop (every model turn and tool call), not a single request. Local models doing a long answer or 2-3 tool iterations routinely exceed it.

Evidence: wf "[ai-ollama] t1 basic agent" on :8090 (chatModel -> Ollama gemma4:12b-mlx). Prompt "Write a detailed 700-word essay..." -> exec_01a0b8e0-c3fa-75fb-81fa-e16cdabee2fb failed after exactly 30.0 s: `node "agent": model turn 1: call model: Post ".../chat/completions": context deadline exceeded`. The same happened on turn 2 of a calculator run (exec_01a0b8e7-af5b-...). The provider nodes' timeout option (tested at 240000 ms) is still capped by execution.default_timeout = 1m (t9 on :8090 failed with execution.timeout at 60 s; engine-runtime F17 covers that part). nodes/ai_ollama_test.go notes "The generic chat model node exposes no per-node timeout" and uses its own 5-minute budget, so CI never sees this.

n8n behavior: lmChatOllama and lmChatOpenAi apply the timeout per request (OpenAI default 60 s) with maxRetries, and executions have no timeout by default. The live n8n Ollama agent runs finished in 6.8-14.8 s.

Impact: Most runs on Ollama, vLLM or LM Studio (the node's stated purpose) that need a long answer or several tool turns fail. The error message does not say which setting to raise.

Suggested fix: Give kilasflow.chatModel the same Options collection as the provider nodes (timeout, maxRetries, topP, penalties). Apply the timeout per model call rather than per agent run, and name the setting in the deadline error.

Files: nodes/ai.go, config.example.yaml

Existing tickets: FEAT-kwxxd0 (done), FEAT-xr7ga9 (done; local-model proof used a private 5-minute budget)

---
### No vision: images on the agent's input are never sent to the model, and passthroughBinaryImages now means something different [find:ai-ollama] (high/parity-gap) · area: AI Agent / multimodal · confidence: high

ai.Message.Content is a plain string and the OpenAI adapter has no image_url parts. passthroughBinaryImages was re-defined as "carry binaries to the output item", so imported vision agents silently answer without seeing the image.

Evidence: Private :8185 (binary root enabled), wf "[ai-ollama] t19 vision passthrough": HTTP Request downloads img742.png (responseFormat file) -> AI Agent (passthroughBinaryImages true, lmChatOpenAi -> Ollama gemma4, a vision-capable model). exec_01a0b905-e712-78c2-800b-3d71e11d8e5d outputs "NO IMAGE". The proxied request has one text-only user message, and the PNG only reappears as the agent's output binary. Chain image rows (messageType imageBinary) are dropped on import (template 2466).

n8n behavior: Same workflow on live n8n 2.33.7 with lmChatOllama (wf "[ai-ollama] n8n ollama vision passthrough"): {"output":"742"} in 6.8 s. Automatically Passthrough Binary Images (default on) sends the input item's images to the model as message parts.

Impact: Vision agents and chains (screenshot scraping 2563, photo explainers 2466, receipt/invoice readers) produce answers that ignore the image, and an imported flag now does something else.

Suggested fix: Add content parts (text plus image_url data URI from binary storage) to ai.Message and the OpenAI adapter. Give passthroughBinaryImages n8n's meaning, and implement the imageBinary/imageUrl chain rows.

Files: internal/ai/ai.go, internal/ai/openai.go, nodes/ai.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-cgm1y3 (done; added passthroughBinaryImages with a different meaning)

---
### No agent log in the UI: model turns, tool calls and token usage are emitted as ai.* events but never shown [find:ai-ollama] (high/ux) · area: executions UI / AI observability · confidence: high

The engine emits ai.model.* and ai.tool.* events (arguments, results, usage), but no web code consumes them. On the execution page, tool and model sub-nodes show only their configuration descriptor, so you cannot see what the agent did.

Evidence: exec_01a0b8e4-0f11-7870-a11c-8ed0abbb5b95 (get_weather called twice). GET /api/v1/executions/{id}/events contains ai.tool.started {detail:{city:Paris}} and ai.model.completed {usage}. A grep of web/src for ai.tool, ai.model, intermediateSteps and toolCalls finds no UI code. event-stream.svelte.ts listens only to execution.* and node.* events. On /executions/<id> (screenshots work/ai-ollama/exec_t5.png and exec_t5_agent.png), clicking get_weather shows Input {} and Output = the `$ai` descriptor with the unresolved $fromAI template, 0 ms, 1 item, and nothing about its two calls. The agent shows raw descriptors, and AI edges are labelled "1 item". The ai.* events are also only kept in the in-memory broker (256 per execution).

n8n behavior: The agent's Logs view shows a tree of every chat-model call (messages, response, tokens) and every tool call (input, output, timing), and clicking a sub-node shows each of its runs.

Impact: Agent behaviour cannot be debugged. For example, the wrong-city HTTP call is invisible unless returnIntermediateSteps was switched on in advance, and token usage never appears in the UI.

Suggested fix: Persist ai.* events with the node run and render them as a per-agent timeline in the execution inspector. Attribute each tool invocation to its tool node (run count, per-call input and output). Replace raw $ai descriptors with a readable summary.

Files: web/src/routes/(dashboard)/executions/[id], web/src/lib/workflow-editor/event-stream.svelte.ts, nodes/ai.go, internal/events/events.go

Existing tickets: FEAT-0j7r5s (done, read-only inspector)

---
### Native Calculator Tool and MCP Client Tool exist, but n8n's toolCalculator, mcpClientTool and lmChatOllama import as blocking placeholders [find:ai-ollama] (high/parity-gap) · area: importer / AI cluster · confidence: high

The LangChain mapping table covers only agent, chainLlm, lmChatOpenAi, lmChatOpenRouter, memoryBufferWindow, toolHttpRequest, toolWorkflow and outputParserStructured. Several n8n AI nodes that KilasFlow can already run (calculator, MCP over streamable HTTP, Ollama via chatModel with an OpenAI-compatible base URL) are blocked instead.

Evidence: internal/interop/n8n/n8n.go mapping table (~415-462). Importing the Ollama templates on :8185 gives `blocking | Ollama Chat Model | @n8n/n8n-nodes-langchain.lmChatOllama` (2384, 2454, 2777) and lmOllama (2729). import-baseline shows toolCalculator blocked (3050) and mcpClientTool blocked (3514). The MCP client itself works: t8 listed and called a stub MCP tool successfully. After manual re-binding to kilasflow.chatModel or lmChatOpenAi with base URL http://127.0.0.1:11434/v1, the same models run. Also reported by importer-fidelity F24.

n8n behavior: These nodes run natively. lmChatOllama takes an ollamaApi credential (base URL) and Ollama options (numCtx, keepAlive, format).

Impact: 12 node instances across 9 top-100 templates: toolCalculator in 2098, 2783, 3050; mcpClientTool in 3514, 3770; lmChatOllama in 2384, 2454, 2777; lmOllama in 2729.

Suggested fix: Map toolCalculator -> kilasflow.calculatorTool (with the expression taken from the model's input). Map mcpClientTool (endpointUrl/sseEndpoint, include/includeTools/excludeTools, auth) -> kilasflow.mcpClientTool and report SSE transport as unsupported. Map lmChatOllama/lmOllama -> kilasflow.chatModel with baseUrl = <ollama baseUrl>/v1; carry temperature/topP/numPredict and report numCtx/keepAlive/format as lossy.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-c2a081 (done; MCP client tool shipped without an import mapping)

## Acceptance criteria

- [ ] Agent + Structured Output Parser + Memory: every follow-up turn gets a 400 from OpenAI-strict providers
- [ ] Basic LLM Chain outputs {output} where n8n outputs {text}, so every imported $json.text reference to a chain r
- [ ] Basic LLM Chain + Structured Output Parser never sends the schema to the model, so the first answer is free te
- [ ] Importer silently drops every n8n Workflow Tool v2 input mapping (reads value.mapping; n8n stores the fields d
- [ ] OpenAI-Compatible Chat Model (the Ollama path) has no timeout option, so the whole agent run is killed at the 
- [ ] No vision: images on the agent's input are never sent to the model, and passthroughBinaryImages now means some
- [ ] No agent log in the UI: model turns, tool calls and token usage are emitted as ai.* events but never shown
- [ ] Native Calculator Tool and MCP Client Tool exist, but n8n's toolCalculator, mcpClientTool and lmChatOllama imp
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress — AINodes2 (2026-09-20)

Landed in this pass (all in `nodes/ai.go` + `internal/ai/*`):
- **parser + memory 400**: the loop remembers only the user turn and the final answer (`finishStructuredRun` stores `remembered`), never the assistant turn that called `format_final_json_response`. Regression: `internal/ai` `TestStructuredRunLeavesNoUnansweredToolCallInMemory` asserts the next turn's history contains no tool-calls assistant turn and no tool message.
- **chain output shape**: a parser-less chain now answers `{text}` (n8n's key, so imported `{{ $json.text }}` resolves) and keeps `{output}` when a parser is attached.
- **chain + parser never sent the schema**: the schema is now stated in the last human turn (`withFormatInstructions`) and repeated in every repair turn.
- **timeout**: `kilasflow.chatModel` gained the same `Options` collection the provider nodes have (`timeout`, `maxRetries`); the timeout is now applied **per model request** by the adapter (`ai.ModelRequest.Timeout`) instead of once for the whole agent run, the run stays bounded by the deployment ceiling, and the deadline error names the option to raise.
- **retries**: exponential backoff with jitter plus `Retry-After` honouring in `internal/ai/openai.go`; an unset `maxRetries` now means n8n's default of 2 instead of 0.
- **vision**: `ai.Message.Images` / `ai.AgentRequest.Images` carry data URIs, the OpenAI adapter emits `content` as `[text, image_url]` parts, and the AI Agent builds them from the input item's image binaries when `passthroughBinaryImages` is on (still carried through to the output). `kilasflow.chatModel`'s credential is now optional and a request with no credential sends no `Authorization` header, so a local endpoint needs no fake token.

Proof (scoped): `go test ./internal/ai/ -count=1` (all pass, including the new retry-pacing, Retry-After, per-request-timeout and content-parts tests) and `go test ./nodes/ -run 'TestChain|TestAgentSendsTheItemsImages|TestChatModelNode|TestAnUnsetRetryOption|TestCalculator|TestHTTPTool' -count=1`.

Not in my slice (owned elsewhere, reported to the owner): the importer's `workflowInputs.value` mapping, the `<base>Tool` variant mapping, `contextWindowLength -> maxMessages` (ImporterTail); the agent log in the executions UI and the execution event history (EngineWaits/FrontendCore2).
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
 434 files changed, 70597 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Important
- **Max-iterations fallback parsed as the parser schema**: the loop now completes with `MaxIterationsMessage` (`internal/ai/agent.go:230`), but the parser branch (`nodes/ai.go:1377-1384`) still unmarshals that string, so a graph with a Structured Output Parser attached fails with "parser output is not valid JSON" naming the wrong cause (verified: same graph without the parser succeeds). Fix: don't parse the fallback (a boolean on `ai.AgentResult` set at the bound is cleaner than matching the message), plus a test.
- **Sampling options from an imported model are ignored**: `chatModel` reads sampling keys from the node top level only (`nodes/ai.go:781-786`), while `chatModelToKilas` (`internal/interop/n8n/parameters.go:4689`) writes temperature/topP/maxTokens/penalties/timeout/maxRetries into the `options` collection and marks them consumed — so every imported Ollama/Gemini/DeepSeek/Groq/Mistral/xAI model silently runs at provider defaults (verified by descriptor probe). Fix: read the same names from the options collection when the top level did not set them.
- **Run-ceiling timeout error names a knob that cannot raise it** (`nodes/ai.go:1367-1371`): it points at the chat model's Timeout option, which is refused above the ceiling (`ErrModelTimeoutAboveCeiling`), while the real bound is the deployment ceiling with no config value behind it. Fix: name the ceiling (or wire it to configuration) instead of the node option.

## Addendum done (FixAiHttpRemnants 2026-09-20, commit `209e150`)

- **(a) parser + max iterations** — `ai.AgentResult` gained `MaxIterationsReached`, set by `LoopRuntime` when the loop ends on the bound (`internal/ai/agent.go`), and the parse branch in `nodes/ai.go` is skipped for that result, so the run's stated fallback is returned rather than json.Unmarshal'd. Proof: `TestAParserRunThatHitsTheIterationBoundAnswersWithTheFallback`; pre-fix `Execute() error = node "AI Agent": parser output is not valid JSON: invalid character 'A' looking for beginning of value`.
- **(b) imported sampling options** — `executeChatModel` reads the `options` collection first and the top-level fields second, so an imported model's temperature/topP/maxTokens/penalties/timeout/maxRetries reach the descriptor and a value the node's own form shows still wins. Proof: `TestAnImportedModelsSamplingOptionsReachTheDescriptor`; pre-fix `descriptor["temperature"] = <nil>, want 0.2 from the options collection` (all five nil).
- **(c) run-ceiling message** — the node now names the deployment's model timeout ceiling and says no node option raises it, and the model adapter no longer reports a request timeout that had not elapsed: `internal/ai/openai.go` said "did not answer within 30s (raise the model node's Timeout option …)" for a run that ended at the deployment's 300ms ceiling, and now only names that timeout when it is what expired. Proof: `TestTheRunCeilingErrorNamesTheCeilingAndNotTheNodeTimeout`; pre-fix `error = node "AI Agent": the agent did not finish within 300ms; raise the chat model node's Timeout option to allow a slower model: … model request did not answer within 30s (raise the model node's Timeout option …)`.

`go test ./nodes/ -run 'Agent|Parser|ChatModel|HTTP' -count=1` and `go test ./internal/ai/ -count=1` ok. Status left `doing` for the re-verify phase.
