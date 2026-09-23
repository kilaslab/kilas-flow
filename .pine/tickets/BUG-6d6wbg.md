---
id: BUG-6d6wbg
title: 'Agent tool calls: malformed arguments silently become {} and run; unknown tool names get no list of valid tools'
status: todo
priority: medium
labels:
    - ai
    - ai-tools
    - robustness
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T01:56:13Z"
---

# Description

Two robustness gaps turn model mistakes into wrong answers instead of self-corrections.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-14, AI-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## AI-14: Malformed tool-call arguments are silently replaced by `{}` and the tool still executes

*bug · medium · ai-agent*

**n8n:** LangChain validates tool arguments against the tool's schema and raises a parsing error (surfaced as a tool error) instead of calling the tool with empty input.

**Steps to reproduce:**

1. Point the chatModel (stream off) at `mangle.py badargs` (port 18916), which truncates the first tool call's arguments to `{"expression": "12*(3+`.
2. Agent + Calculator, ask "what is 1234*5678 minus 91?".

**Actual:**

`intermediateSteps[0]` = `Calculator {}` → `tool failed: node "AI Agent": expression is required`. The model recovered on the next turn. The model is never told its JSON was invalid, the raw arguments are lost from `intermediateSteps` and the events, and the tool *ran* with `{}`. An HTTP tool without `$fromAI` (for example a POST webhook) would fire its request with no arguments.

**Expected:**

Invalid JSON arguments produce a tool message such as "arguments were not valid JSON: <raw>", and the tool is not invoked. The raw string is kept in the step and the event.

**Suggested fix:**

Carry an `ArgumentsError` on `ToolCall` instead of substituting `{}`. In `LoopRuntime`, answer that call with the parse error and skip `Invoke`.

**Evidence:**

`case-13-malformed tool-call JSON.execution.json`, `mangle-log.jsonl`. Code `internal/ai/openai.go:144-146` (stream) and `:462-465` (non-stream).

**Related:**

none


## AI-15: An unknown tool name gets "not available" with no list of valid tools, and the model then invents the answer

*ux · low · ai-agent*

**n8n:** LangChain's AgentExecutor answers `<name> is not a valid tool, try another one.`; listing the valid names is better still.

**Steps to reproduce:**

`mangle.py garbage` (port 18917) renames the first tool call to `calculator_v2`. Agent + Calculator, same question.

**Actual:**

The tool message is `tool "calculator_v2" is not available`. The model did not retry and answered "The result is **6,998,461**" from its own (wrong) arithmetic. The status is succeeded.

**Expected:**

The message lists the available tool names so the model can retry.

**Suggested fix:**

Append `; available tools: a, b, c` to the message.

**Evidence:**

`case-13-unknown tool name.execution.json`. Code `internal/ai/agent.go:198-205`.

**Related:**

none


# Acceptance Criteria
- [ ] Invalid JSON arguments produce a tool message ("arguments were not valid JSON: <raw>"), the tool is not invoked, and the raw string is kept in the step and event
- [ ] An unknown tool name returns the list of available tool names

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
