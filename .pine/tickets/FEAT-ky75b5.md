---
id: FEAT-ky75b5
title: 'AI logs view: each model call (messages, response, tokens, latency) and tool call (input, output, duration)'
status: todo
priority: high
labels:
    - ai
    - observability
    - executions
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T01:56:13Z"
---

# Description

The only record of an agent's loop is `intermediateSteps` in its output JSON. Per-call data exists as ai.* events, but it is neither persisted nor shown. This depends on the ai.* SSE naming fix (BUG-z0s4zg).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-17). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Logs panel under the canvas shows the agent's tree of LLM calls and tool calls, each with input messages, output, token usage and duration. Clicking the chat model sub-node shows each call's prompt and response.

# Steps to Reproduce

1. Open exec_01a0cbc4-134f-791c-86eb-4b354917f44f (`[ai-ollama] case2 agent + calculator`). 2. Click `Ollama gemma4`, `Calculator` and `AI Agent`.

# Expected

A logs panel, or an inspector tab, listing each model call with messages, response, tokens and latency, and each tool call with input, output and duration, available both live and after the run.

# Actual

The model node shows `{"$ai":{"baseUrl":"http://127.0.0.1:11434/v1","kind":"model","model":"gemma4:12b-mlx","stream":true}}` and `0 ms`, which is a config descriptor, not the calls. The only record of the loop is the agent's `intermediateSteps` and `usage` in its output JSON. Per-call data exists as `ai.model.started/completed`, `ai.tool.started/completed` and `ai.model.delta` events, but it is not persisted or shown anywhere (see UXD-18).

# Acceptance Criteria
- [ ] ai.* events, or a structured call log, are persisted on the agent's node run
- [ ] A logs panel under the canvas (live and replay) lists the model and tool calls as a tree, with inputs, outputs, tokens and durations
- [ ] Clicking the chat model sub-node shows its calls, not its config descriptor

# Implementation Plan

Persist the ai.* events (or a structured call log) on the agent's node run and render them as a timeline under the replay canvas.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/ai-01-agent-exec.png`, `ai-02-model-node.png`, `cli/get_ai.json`, `cli/trace_ai.json`.

# Attachments
