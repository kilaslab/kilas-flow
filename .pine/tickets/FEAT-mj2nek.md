---
id: FEAT-mj2nek
title: 'Persistent chat memory: Postgres Chat Memory, Redis Chat Memory, Chat Memory Manager'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
    - memory
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

The only memory is the in-process Simple Memory. It is lost on restart and not shared between workers, so template 16706 still blocks on Postgres Chat Memory even after its RAG parts map.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Postgres Chat Memory appears in 16 templates, Chat Memory Manager in 5 and Redis Chat Memory in 3; Motorhead and Zep appear in 0 (both deprecated in n8n). The cluster appears in 2.3% of templates. KilasFlow's only memory is the in-process Simple Memory.

# Steps to Reproduce

Import Manual → AI Agent ← OpenAI model + memory X, using real instances: memoryPostgresChat 19520 (`tableName`, `sessionIdType: customKey`, `sessionKey`, `contextWindowLength`), memoryRedisChat 19362, and docs-derived Motorhead and Zep nodes.

# Expected

Postgres chat memory, using the customer's postgres credential (the PGVector precedent) and n8n's table shape, and Redis chat memory import and run.

# Actual

- Every one is a blocking placeholder.
- Real template 16706 therefore blocks on "Postgres Chat Memory" even after its RAG parts map.
- A workflow rebuilt with Simple Memory loses its conversations on restart and does not share them across workers.

# Also found by the audit

## AI-13: Only Simple Memory exists, and it is process-local, so conversations split across worker processes and vanish on restart

*gap · low · ai-memory*

**n8n:** Window Buffer (Simple) Memory plus Postgres Chat Memory, Redis Chat Memory, MongoDB, Xata, Zep and Motorhead (the last two deprecated), and the Chat Memory Manager node to read, insert or delete history. n8n documents Simple Memory as unsuitable for queue mode.

**Steps to reproduce:**

Code review, plus case 8 for the in-process behaviour.

**Actual:**

- Case 8 passes: "Rina" was recalled, `maxMessages: 2` forgot it two turns later, session B was isolated, and messages go to the model in order (`proxy-log.jsonl`).
- The only backend is `ai.BufferMemory` (`cmd/kilasflow/main.go:262`, `internal/ai/memory.go`): in-process, capped at 1000 sessions per tenant and 24 h by default (`maxAgeMinutes: 1440`, which n8n's Simple Memory does not have).
- With multi-process workers (FEAT-9555xz), consecutive turns of one session can run on different processes and see different histories.
- The chat panel's notice mentions restarts but not workers. There is no Chat Memory Manager equivalent to inspect or clear a session.

**Expected:**

A durable memory backend (Postgres or SQLite, in the product's own DB) and a Chat Memory Manager. At minimum, a warning when memory is used with more than one worker process.

**Suggested fix:**

Implement `Memory` over the main database (it already stores executions), and add `memoryPostgresChat` and `memoryManager` mappings.

**Evidence:**

`case-8-*.execution.json`, `proxy-log.jsonl`. Code `internal/ai/memory.go:48-66`, `cmd/kilasflow/main.go:1303`.

**Related:**

NG-12. BUG-6jvcs5 (done): its checklist item "Only in-process Simple Memory exists … not shared across workers" is unchecked and still reproduces.


# Acceptance Criteria
- [ ] `memoryPostgresChat` over the existing postgres credential, with an `n8n_chat_histories`-compatible table
- [ ] `memoryRedisChat` once a redis credential exists
- [ ] Chat Memory Manager (get/insert/delete messages)
- [ ] A conversation survives a server restart
- [ ] A durable memory backend in the product's own database (SQLite or Postgres), so no external service is needed
- [ ] Until then, a warning when Simple Memory is used with more than one worker process

# Implementation Plan

Implement `kilasflow.memoryPostgresChat` over the existing postgres credential, with the table `n8n_chat_histories`-compatible. Add Redis memory once a Redis credential type exists (see NG-22).

# Notes

Related tickets: BUG-6jvcs5

BUG-6jvcs5 (done) left the Postgres memory item unlanded.

Related (from the audit): BUG-6jvcs5 (done). Its checklist item "Only in-process Simple Memory exists … Postgres" was never landed and still reproduces. The roadmap's V2-p5-5 defers Postgres memory to p6.

# Related Files

`probes/Postgres_Chat_Memory.json`, `results-ai.json` (Redis Chat Memory, Motorhead Memory, Zep Memory).

# Attachments
