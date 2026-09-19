---
id: BUG-tcqkad
title: 'AI agent loop defects: parser+memory 400, chain shape/schema, timeout, vision, retries'
status: todo
priority: high
labels:
    - ai
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
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