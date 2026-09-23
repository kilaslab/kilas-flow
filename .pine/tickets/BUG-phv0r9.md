---
id: BUG-phv0r9
title: Conversational / OpenAI-Functions agents import as placeholders instead of Tools Agents
status: todo
priority: medium
labels:
    - importer
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
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
- [ ] `conversationalAgent` and `openAiFunctionsAgent` import as the Tools Agent with a lossy note
- [ ] `sqlAgent`, `planAndExecute` and `reActAgent` stay blocking, or map with a stronger note
- [ ] Legacy agents with no `agent` key get the same lossy note

# Implementation Plan

Map these two agent types to the Tools Agent in `refuseNonToolsAgent`/`agentToKilas` and add a lossy issue. Keep the placeholder only for the agent types with really different semantics.

# Notes

Related tickets: FEAT-j5s2n4

Related (from the audit): FEAT-j5s2n4 (done). Its suggested fix says "Map conversationalAgent/openAiFunctionsAgent to the Tools Agent with a lossy note, consistently", and that was not implemented.

# Related Files

`internal/interop/n8n/parameters.go:4431-4439` (`refuseNonToolsAgent`), `minimal_repros.json`, `import/results.json` (3066, 2859)

# Attachments
