---
id: BUG-277a2m
title: Simple Memory sessionKey evaluated before main chain; per-item/upstream refs fail
status: todo
priority: high
labels:
    - ai
    - memory
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ai-ollama.

---
### Simple Memory's session key is evaluated before the main chain runs: $('Trigger') fails, $json is the execution input, and a batch shares one session [find:ai-ollama] (high/bug) · area: AI memory / sub-node expression context · confidence: high

The memory sub-node runs as a standalone node before the trigger and the agent's upstream nodes. Its sessionKey expression is resolved once against request.Input and is not re-evaluated for the agent's current item. As a result, references to upstream nodes fail, values computed by intermediate nodes are empty, and every item in a batch shares one session.

Evidence: Private :8185, strict stub. (1) wf "[ai-ollama] t17 memory key refs trigger": sessionKey `{{ $('trigger').first().json.sessionId }}` or `.item.json.sessionId` -> failed `node "memory": parameter "sessionKey": $('trigger') names a node that has not produced output in this run`. The node-run order shows memory at sequence 1, before the trigger. (2) wf "[ai-ollama] t16 per-item session": manual -> Split Out(msgs) -> agent + memory `{{ $json.sessionId }}` with items [{sessionId:alice},{sessionId:bob}] -> failed `sessionKey resolved to an empty value`. Cause: nodes/ai.go executeMemory resolves with expressionContext(request.Input, ...), and AgentExecutor.Execute uses one descriptor for all items. For contrast, an HTTP tool URL `{{ $('setnode').item.json.city }}` resolves correctly because tools evaluate at call time (t18 hit /weather?city=Oslo).

n8n behavior: Sub-node parameters are evaluated per item against the root node's current input item, so $json.<field> and $('Telegram Trigger').item.json.message.chat.id work, and each item gets its own session.

Impact: Top-100 memory keys that reference a node: 2035 and 2534 ($('Listen for incoming events').first()...chat.id), 2098 and 2872 ($('When chat message received')...sessionId), 3586 and 4827 ($('WhatsApp Trigger').item.json.contacts[0].wa_id), and 2752 (Postgres memory with $('Telegram Trigger')). Telegram and WhatsApp bots fail on their first message, and batches mix conversations.

Suggested fix: Leave sessionKey unresolved in the memory descriptor and evaluate it in AgentExecutor.Execute per item with expressionContext(item, input, request, index). Tools already defer evaluation this way.

Files: nodes/ai.go

Existing tickets: FEAT-096vs9 (done, session key modes)

## Acceptance criteria

- [ ] Simple Memory's session key is evaluated before the main chain runs: $('Trigger') fails, $json is the executio
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)