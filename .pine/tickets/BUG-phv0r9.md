---
id: BUG-phv0r9
title: Conversational / OpenAI-Functions agents import as placeholders instead of Tools Agents
status: done
priority: medium
labels:
    - importer
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-10-08T16:14:00Z"
---

# Description

13 of the top 198 templates, including 3066 (205K views), lose their agents (prompt, system message and wiring) to placeholders. An older agent with no `agent` key is silently mapped instead, so the two cases are treated inconsistently. FEAT-j5s2n4 specified this fix and never implemented it.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-7). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Before v1.8 the AI Agent had an `agent` selector. n8n documents OpenAI Functions as superseded by the Tools Agent, and for modern chat models the Conversational agent behaves like it too. In the top 198, 13 templates set `agent` to conversationalAgent (11 nodes), openAiFunctionsAgent, sqlAgent or reActAgent. That includes 3066 (205K views; 3 agents) and 2859 "Chat with Postgresql Database" (73K views).

# Steps to Reproduce

1. `minimal_repros.py`, case `conversational_agent` (agent v1.7, `agent:"conversationalAgent"`, Ollama model).
2. Import 3066 and 2859.

# Expected

conversationalAgent and openAiFunctionsAgent import as the Tools Agent with a lossy note, so the prompt, system message and sub-node wiring survive. Only sqlAgent, planAndExecute and reAct stay blocking, or map with a stronger note.

# Actual

- Blocking: `this node uses the "conversationalAgent" agent, which KilasFlow does not implement. Only the Tools Agent imports: the node was kept as a placeholder and cannot run until it is replaced or rebuilt as a Tools Agent.`
- The whole agent, with its prompt and system message, becomes `kilasflow.unsupported`.
- Meanwhile an agent ≤v1.5 with no `agent` key (which n8n runs as the Conversational agent) is imported silently as a Tools Agent. The treatment of the same agent is therefore inconsistent.

# Acceptance Criteria
- [x] `conversationalAgent` and `openAiFunctionsAgent` import as the Tools Agent with a lossy note
- [x] `sqlAgent`, `planAndExecute` and `reActAgent` stay blocking, or map with a stronger note
- [x] Legacy agents with no `agent` key get the same lossy note

# Implementation Plan

Map these two agent types to the Tools Agent in `refuseNonToolsAgent`/`agentToKilas` and add a lossy issue. Keep the placeholder only for the agent types with really different semantics.

# Notes

Related tickets: FEAT-j5s2n4

Related (from the audit): FEAT-j5s2n4 (done). Its suggested fix says "Map conversationalAgent/openAiFunctionsAgent to the Tools Agent with a lossy note, consistently", and that was not implemented.

# Related Files

`internal/interop/n8n/parameters.go:4431-4439` (`refuseNonToolsAgent`), `minimal_repros.json`, `import/results.json` (3066, 2859)

## Fix

- `refuseNonToolsAgent` (`internal/interop/n8n/parameters.go`) lets `conversationalAgent` and `openAiFunctionsAgent` through. `sqlAgent`, `planAndExecuteAgent`, `reActAgent` and any other value are still refused with the blocking placeholder.
- `agentToKilas` adds `legacyAgentNote`, a lossy issue on field `agent`. It says the agent ran as n8n's Conversational or OpenAI Functions agent and now runs as the Tools Agent: the prompt, system message, model, memory and tools carry over, but answers and tool-call timing may differ. An agent at typeVersion 1.5 or below with no `agent` key gets the same note, because n8n's selector defaulted to `conversationalAgent` until 1.6. From 1.6 a missing key means the Tools Agent, so there is no note.
- The Conversational agent's `options.humanMessage` template (with its `{tools}` and `{format_instructions}` slots) is reported as dropped instead of being silently ignored.
- Tests: `internal/interop/n8n/legacy_agent_test.go` covers conversational v1.7, openAiFunctions v1.6, and no selector at 1.5 and at 1. Each imports as `kilasflow.agent` with the system message and the lossy note, with no blocking issue, and `workflow.Compile` passes. No note at 1.6, 1.9 or 2.2. humanMessage is reported as dropped. `TestImportRefusesANonToolsAgent` now pins sqlAgent, planAndExecuteAgent and reActAgent as blocking placeholders.

## Notes

- Not checked against templates 3066 and 2859 themselves (no copy in the repo). The fixtures follow their agent shape. n8n's v1.0 to v1.2 agents defaulted `text` to `$json.input` / `$json.chat_input` rather than `$json.chatInput`. The translator still fills the chatInput default when `text` is absent. This was not changed here.

# Attachments
