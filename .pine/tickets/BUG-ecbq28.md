---
id: BUG-ecbq28
title: 'Reasoning models: `reasoning` dropped between tool turns (wrong answers), channel tokens leak, no think/effort option'
status: todo
priority: high
labels:
    - ai
    - reasoning-models
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-10-08T16:30:00Z"
---

# Description

With gemma4, answers after a tool call were garbled or wrong in 4 of 5 runs. A controlled replay: 4 of 12 correct as KilasFlow sends the request, 11 of 12 when the reasoning is sent back. Thinking also cannot be turned off: structured output took 46–52 s with thinking versus 2–3 s without.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-3, AI-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## AI-3: Reasoning is dropped between tool turns, so gemma4's answer after a tool call is garbled or wrong in most runs

*bug · high · ai-agent*

**n8n:** n8n's Ollama Chat Model uses Ollama's native chat API through LangChain's ChatOllama rather than the OpenAI-compatible `/v1` path. I did not verify live whether it round-trips thinking, so treat this as a KilasFlow correctness bug rather than a measured parity gap.

**Steps to reproduce:**

1. Agent + chatModel (gemma4 via `/v1`, stream on, timeout 300000) + Calculator Tool.
2. Ask "what is 1234*5678 minus 91?" 5 times.

**Actual:**

- The tool always returns `{"result":7006561}`.
- The final answers: 1 of 5 clean. The rest: "…is 7,006,472." (wrong); "…is 6,978,014. Wait, let me re-calculate that…" (1008 tokens, 417 s); and two more self-talk rambles.
- One streamed answer contained the raw control token: `…Let me re-run the calculation.<channel|>The result of 1234 * 5678 - 91 is 6,998,021.`
- Cause: Ollama returns the turn-1 thinking in `message.reasoning`. KilasFlow drops it (`wireMessage`/`chatCompletionChunk` have no reasoning field) and replays the assistant tool-call turn with no `content` key at all.
- Controlled replay of KilasFlow's exact turn-2 request against Ollama: 4 of 12 clean as sent, and 11 of 12 clean with `reasoning` and `content:""` added to the assistant turn.
- With thinking disabled (`reasoning_effort:"none"` injected by a proxy), 4 of 4 were clean in 2–4 s.

**Expected:**

Tool results are reported faithfully. Reasoning from a tool-calling turn is preserved and sent back (or thinking is disabled for the loop), and reasoning or channel tokens never reach `output`.

**Suggested fix:**

Parse `reasoning`/`reasoning_content` (stream and non-stream) into `ai.Message`, echo it on the replayed assistant tool-call turn, and send `content:""` rather than omitting it. Strip `<|channel>`/`<channel|>` and `<think>` blocks from the final output.

**Evidence:**

`case-2c-{0..4}.execution.json`, `case-2.execution.json`, `case-2b-stream-0.execution.json` (`<channel|>`), `kf-turn2-request.json`, `replay2-results.json`, `replay3-results.json`, `case-14.summary.json`, `case-2b-stream-nothink-*.execution.json`. Code `internal/ai/openai.go:396-401` (no reasoning field; `content,omitempty`) and `:500-515` (the chunk ignores `delta.reasoning`).

**Related:**

FEAT-kwxxd0 (done) recorded the `reasoning` field as "ignored harmlessly". It is not harmless in multi-turn tool use.


## AI-4: No way to turn thinking off or set reasoning effort, so every turn of a local thinking model pays 10–50 s of hidden reasoning

*perf · medium · ai-agent*

**n8n:** The OpenAI Chat Model has a Reasoning Effort option (low/medium/high). The Ollama Chat Model exposes Ollama-native options through LangChain's ChatOllama.

**Steps to reproduce:**

1. Run case 9 (Agent + Structured Output Parser on a product review) with the default node, then through a proxy that only adds `reasoning_effort:"none"`.
2. Run the case 2 calculator agent the same two ways.

**Actual:**

- Structured output: 46.4 s and 51.8 s with thinking, 3.0 s and 2.0 s without.
- Calculator: 9–64 s (and 30 s timeouts, AI-5) with thinking, 2–4 s without.
- A direct probe took 20.2 s and 248 completion tokens for "17*23" with thinking, against 0.58 s and 3 tokens without.
- The reasoning text is billed in `usage.completionTokens` (a one-word "Hi" costs 57 tokens) but is not visible anywhere: not in the output, the events, or the chat.
- `nodes/testdata/n8n_chat_model_options.json` lists `reasoningEffort` under `_unadopted`.

**Expected:**

A Reasoning Effort / Think option on the chat model nodes (sent as `reasoning_effort` on the OpenAI-compatible wire), and the reasoning surfaced as an optional field or event.

**Suggested fix:**

Add a `reasoningEffort` option (none/low/medium/high) to `chatModel`, `lmChatOpenAi` and `lmChatOpenRouter` and forward it in `chatRequest`. Emit `ai.model.reasoning` deltas and optionally include `reasoning` in the output.

**Evidence:**

`case-9-normal-*.execution.json` vs `case-9-nothink-*.execution.json`, `case-14.summary.json`, `ollama-direct-plain.json`, `case-1b.execution.json`.

**Related:**

none


# Acceptance Criteria
- [x] Reasoning from a tool-calling turn is preserved and sent back on the next turn (or thinking is disabled inside the loop)
- [x] Reasoning and channel tokens (`<think>`, `<channel|>`) never reach `output`
- [ ] The chat model nodes have a Reasoning Effort / Think option, sent as `reasoning_effort` (or the provider's equivalent)
- [x] Reasoning is optionally surfaced as a field or event for debugging

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-kwxxd0

# Related Files

# Attachments

## Fix

2026-10-08, three commits on the BUG-ecbq28 branch.

- **AI-3, round trip.** `internal/ai/openai.go` reads `reasoning` and `reasoning_content`, streamed (`delta.*`) and not (`message.*`), into the new `ai.Message.Reasoning`. `Message.ReasoningField` (not in JSON) records which name it came under, and `wireMessageFrom` replays the thinking under that same name, exactly as received. An assistant turn with empty content is now marshalled with `content: ""` instead of no key. `appendSessionMemory` strips reasoning, so it only goes back within the run whose tool turn produced it.
- **AI-3, leaks.** `withoutLeakedReasoning`/`splitLeakedReasoning` run on every decoded or stream-assembled turn. They cut `<think>…</think>` and gemma4's `<|channel>thought\n…<channel|>` out of the content. A closing tag with no opening (the template opened it in the prompt) counts everything before it as thinking, and an unclosed opening tag counts everything after it. The answer is trimmed. What was cut out becomes `Message.Reasoning` only when the provider sent none, and it is never sent back because it has no `ReasoningField`.
- **AI-4, option.** `ai.ModelRequest`/`ai.AgentRequest` gain `ReasoningEffort`, which goes on the wire as `reasoning_effort,omitempty`. `kilasflow.chatModel`'s Options collection gains `reasoningEffort` (none/low/medium/high). The descriptor carries it as written, and the agent forwards it on every turn. Ollama's `/v1` maps `reasoning_effort` onto its think setting (`"none"` sets think false; checked against ollama `openai/openai.go` `ThinkingFromReasoningEffort` via Context7), so this one field covers both OpenAI-compatible endpoints and Ollama.
- **Surfacing.** `ai.Event.Reasoning` is set on `ai.model.completed`. Since `eventDetail` marshals the whole event, it reaches the execution events. `Message.Reasoning` also appears as `reasoning` in `AgentResult.Messages`.

Tests: `internal/ai/reasoning_test.go` covers both field names, streamed and not, the `content:""` key, the event, six leak shapes streamed and not, and effort sent only when set. `nodes/ai_test.go` `TestTheChatModelsReasoningEffortReachesTheProvider`. `go test ./internal/ai/... ./nodes/ ./internal/interop/n8n/` and `go vet ./internal/ai/... ./nodes/` pass.

## Progress

Remaining for criterion 3 (left unticked):
- `kilasflow.lmChatOpenAi` and `kilasflow.lmChatOpenRouter` do not offer `reasoningEffort` yet. Their Options are pinned to `nodes/testdata/n8n_chat_model_options.json`, which lists it under `_unadopted`. Adopting it means recording n8n's own member (default, values, model-name gating) in that file, wiring `executeProviderChatModel` to copy it as a string the way `executeChatModel` now does, and mapping it in `internal/interop/n8n`.
- The Basic LLM Chain (`ChainExecutor`) does not forward `model.reasoningEffort` in its `ai.ModelRequest`. This was left alone on purpose because BUG-9dw5me is rewriting that request building. It is a one-field addition once that work lands.
- Live stream deltas (`ai.model.delta`) still carry leaked tags as they arrive. Only the assembled turn, and so `output`, is cleaned. A tag can be split across frames, so filtering deltas needs a small buffering state machine.
