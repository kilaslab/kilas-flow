---
id: BUG-mz8xrb
title: HTTP Request Tool reuses first call's $fromAI args for every later call
status: testing
priority: critical
labels:
    - ai
    - tools
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T00:48:14Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ai-ollama.

---
### HTTP Request Tool reuses the first call's $fromAI arguments for every later call, so answers use the wrong data [find:ai-ollama] (critical/bug) · area: AI tools / HTTP Request Tool · confidence: high

httpRequestTool.Invoke writes the substituted parameters back into the tool's own template (tool.node.Parameters = parameters). After the first call the $fromAI placeholders are gone, so every later call in the run, and every later item, repeats the first call's request. The agent then answers with the wrong data and the execution still shows success.

Evidence: wf "[ai-ollama] t5 http tool fromAI" on :8090 (agent + kilasflow.httpTool url `http://127.0.0.1:8095/weather?city={{ $fromAI('city','the city name','string') }}`). Prompt "What's the weather in Paris and in Tokyo right now?" -> exec_01a0b8e4-0f11-7870-a11c-8ed0abbb5b95. intermediateSteps: call 1 args {"city":"Paris"}, call 2 args {"city":"Tokyo"}. The stub log shows both HTTP requests hit /weather?city=Paris. Tool result 2 is Paris's body, and the agent says Tokyo is 18°C (Paris's data). Status: succeeded. Code: nodes/ai.go ~1664-1668 `parameters, err := ai.SubstituteFromAI(tool.node.Parameters, argsMap); tool.node.Parameters = parameters`. After this, Definition() on the next item falls back to the generic `input` schema. The workflow, calculator and MCP tools use a local variable and are not affected.

n8n behavior: Each tool call evaluates $fromAI again from that call's own arguments.

Impact: Any agent that calls an HTTP tool more than once per run (lookups for several cities or ids, retries, multi-item input) quietly gets the first response again. The HTTP tool is the general-purpose tool for anything without a dedicated node, and toolHttpRequest is used in 6 top-100 templates.

Suggested fix: Substitute into a copy inside Invoke (node := tool.node; node.Parameters = substituted) and never change the tool's template. Add a regression test with two calls that carry different arguments.

Files: nodes/ai.go

Existing tickets: FEAT-je4f4t (done; introduced $fromAI for the HTTP tool; this is a defect in that feature)

## Acceptance criteria

- [ ] HTTP Request Tool reuses the first call's $fromAI arguments for every later call, so answers use the wrong dat
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress — AINodes2 (2026-09-20)

Fix: `httpRequestTool.Invoke` substitutes `$fromAI` into a **copy** of the node (`node := tool.node; node.Parameters = parameters`) and executes the copy, so the tool's own template keeps its placeholders. `Definition()` therefore also keeps offering the real argument schema after the first call instead of degrading to the generic `input` object.

Proof (scoped): `go test ./nodes/ -run TestHTTPToolReevaluatesItsFromAIArgumentsOnEveryCall -count=1` — a stub agent asks for Paris then Tokyo; the stub HTTP server records `/weather?city=` twice and the test asserts `[Paris, Tokyo]`. Fails pre-fix (both requests were Paris), passes post-fix.

Remaining in this ticket: adversarial re-verify against a live stub/n8n instance (Main's final verification phase).
**Commits**: f246ea9 (internal/ai: memory window, loop, transport), 047b8d1 (nodes/ai.go: tools, memory key, chain, vision, retries). Both land every ticket in this batch because `nodes/ai.go` and `internal/ai/agent.go` are shared by all five.

**Scoped proof (final, tree at 047b8d1)**:
- `go test ./internal/ai/ -count=1` → ok
- `go test ./nodes/ -run 'TestAI|TestAgent|TestMemory|TestChain|TestHTTPTool|TestCalculator|TestChatModel|TestAnUnset|TestStreamed|TestReturnIntermediate|TestToolName|TestDuplicate' -count=1` → ok

**Known red, not mine**: `go test ./nodes/ -count=1` also runs `TestEveryAttachedToolReachesTheAgentInAStableOrder`, which fails with `node "AI Agent": connect an OpenAI Chat Model to the model port`. Cause is `internal/engine/runner.go` `push()`/`next()`: a pending invocation built by `push` carries only the main-port items, so a node started by a branch loses its typed ports (model/memory/tools) — the fallback path merges them via `nodeInput`, the pushed path does not. Reported to EngineFlow; the compiled IR is correct (verified: the `ai_languageModel` edge is present).

**Update**: the `internal/engine/runner.go` typed-port regression above is fixed by EngineFlow in 976b2ce (a pushed invocation now merges its typed inputs from the nodes that have run). Re-verified at 4cc80ff: `go test ./nodes/ -count=1` → ok (14s, whole package, including TestEveryAttachedToolReachesTheAgentInAStableOrder) and `go test ./internal/ai/ -count=1` → ok.
