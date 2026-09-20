---
id: BUG-ztzxck
title: Simple Memory trims by raw count; window starts with orphan tool result (400)
status: testing
priority: high
labels:
    - ai
    - memory
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:48:14Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ai-ollama.

---
### Simple Memory trims by raw message count, so a window can start with an orphan tool result and the provider returns 400 [find:ai-ollama] (high/bug) · area: AI memory · confidence: high

Memory stores every message, including assistant tool_calls, tool results and repair turns. prune() then keeps the newest N messages without respecting turn boundaries, so the loaded history can begin with a role=tool message whose tool_call was cut off.

Evidence: wf "[ai-ollama] t3 memory window orphan (strict mock)" (agent + Simple Memory maxMessages=6 + Calculator Tool, strict stub). Turns 1 and 2 succeed; each stores 4 messages. Turn 3 fails: `model request failed with status 400: ... msg[0] tool message id=call_4 does not answer a preceding assistant tool_call`. The default of 40 hits the same problem when plain and tool turns are mixed (e.g. 9 tool turns + 1 plain + 1 tool = 42, so the window starts at a tool message). Cause: internal/ai/memory.go prune() `fresh = fresh[overflow:]`.

n8n behavior: memoryBufferWindow keeps the last k interactions (human/AI pairs) and does not store tool steps. Verified live (wf "[ai-ollama] n8n agent calc+memory (strict mock)", contextWindowLength 3, calculator called every turn): the turn-3 history is [user, assistant, user, assistant, user].

Impact: Long chat sessions on OpenAI or OpenRouter break with a 400 at seemingly random turns, until the window moves past the orphan message.

Suggested fix: Trim on turn boundaries (drop whole user-to-final-assistant groups), or store only human/AI pairs. Never let a window start with a tool or tool_calls message.

Files: internal/ai/memory.go, internal/ai/agent.go

Existing tickets: FEAT-096vs9 (done, retention/window semantics)

## Acceptance criteria

- [ ] Simple Memory trims by raw message count, so a window can start with an orphan tool result and the provider re
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress — AINodes2 (2026-09-20)

Fix (both halves of the suggested fix):
- `internal/ai/agent.go` `appendSessionMemory` now stores only replayable turns: `user` and `assistant`-without-tool-calls. Tool results, the assistant turn that asked for tools, the parser's format-tool turn and repair prompts are no longer written, so the stored conversation is human/AI pairs like n8n's buffer window. Stored turns drop their `Images` too (a stored picture would be re-sent on every later turn).
- `internal/ai/memory.go` `prune` applies the count bound and then advances the window to a turn boundary (`alignToTurnStart` + `Message.OpensATurn`), so a window can never begin with a tool result or with a tool-calls assistant turn. `loadSessionMemory` applies the same rule to whatever a store returns.

Proof (scoped): `go test ./internal/ai/ -run 'TestMemoryWindowNeverOpensWithAToolStep|TestStructuredRunLeavesNoUnansweredToolCallInMemory|TestAgentUsesAndUpdatesMemory|TestMemoryEnforcesItsRetentionContract|TestMemoryEnforcesPerNodeAgeThenCount' -count=1`.
**Commits**: f246ea9 (internal/ai: memory window, loop, transport), 047b8d1 (nodes/ai.go: tools, memory key, chain, vision, retries). Both land every ticket in this batch because `nodes/ai.go` and `internal/ai/agent.go` are shared by all five.

**Scoped proof (final, tree at 047b8d1)**:
- `go test ./internal/ai/ -count=1` → ok
- `go test ./nodes/ -run 'TestAI|TestAgent|TestMemory|TestChain|TestHTTPTool|TestCalculator|TestChatModel|TestAnUnset|TestStreamed|TestReturnIntermediate|TestToolName|TestDuplicate' -count=1` → ok

**Known red, not mine**: `go test ./nodes/ -count=1` also runs `TestEveryAttachedToolReachesTheAgentInAStableOrder`, which fails with `node "AI Agent": connect an OpenAI Chat Model to the model port`. Cause is `internal/engine/runner.go` `push()`/`next()`: a pending invocation built by `push` carries only the main-port items, so a node started by a branch loses its typed ports (model/memory/tools) — the fallback path merges them via `nodeInput`, the pushed path does not. Reported to EngineFlow; the compiled IR is correct (verified: the `ai_languageModel` edge is present).
