---
id: FEAT-cgm1y3
title: Bring the AI Agent node to n8n parity
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-ybm2pd
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:00:34Z"
updated: "2026-09-05T05:00:34Z"
---

## Scope

`agentNode()` in `nodes/ai.go` declares three parameters: `prompt`, `systemPrompt` and `maxIterations` (default 8). n8n's Tools Agent carries `systemMessage`, `maxIterations` (default 10), `returnIntermediateSteps`, `passthroughBinaryImages`, a streaming toggle and batching options. Every one of those keys arrives in an imported workflow and every one is dropped today, so an imported agent runs with different limits and a different output shape than it did in n8n, with nothing said about it.

Only the Tools Agent is in scope. n8n deprecated the agent-type selector in 1.82.0 and removes the v1 agent in 3.0, but workflows exported before that still carry `parameters.agent`, so the importer must recognise the key and refuse anything that is not a tools agent by name rather than quietly running it as one.

Two live defects sit in the way. First, `AgentResult.Messages` is populated on exactly one path: `LoopRuntime.Run` in `internal/ai/agent.go` sets `result.Messages = messages` when the iteration bound is exhausted, and never on the success path — so `returnIntermediateSteps` has nothing to return until that is fixed. Second, tool names do not come from the canvas. `executeHTTPTool` reads `ir.Parameters["toolName"]` with the default `"http_request"`, so two HTTP Request Tools left at their defaults both claim the same name. `LoopRuntime.Run` already refuses that loudly — `tool %q is attached more than once` — which is the right behaviour, but it fires at run time on a workflow the compiler already accepted and the user already activated.

## Acceptance criteria

- [ ] The agent node exposes n8n's parameter surface: `systemMessage` (with `systemPrompt` still accepted for graphs already saved), `maxIterations` defaulting to 10, `returnIntermediateSteps`, `passthroughBinaryImages`, and streaming.
- [ ] With `returnIntermediateSteps` on, the output item carries the full ordered message list including tool turns; with it off the output item is byte-identical to today's.
- [ ] `AgentResult.Messages` carries the conversation on the success path as well as the failure path.
- [ ] Tool names derive from the connected node's canvas name, normalised to the character set the model API accepts, with an explicit per-tool override still available; a duplicate name fails compilation naming both nodes rather than failing mid-run.
- [ ] An imported workflow whose `parameters.agent` is not a tools agent imports with a diagnostic naming the node and the agent type, and never runs as a Tools Agent by default.
- [ ] The batching options are either implemented or reported as unmapped on import — never accepted into the document and silently ignored.
- [ ] Iterations, tool-call count and token usage are recorded per run, and the nested `ai.*` events still reach the live execution feed.

## Implementation Plan

Work in `nodes/ai.go` first: `agentNode()` for the parameter surface and `AgentExecutor.Execute` for the wiring, then `internal/ai/agent.go` for `AgentRequest`, `AgentResult.Messages` and the loop's bound. Keep the split as it is — the node knows about parameters and descriptors, the runtime knows about the loop — because that boundary is what lets p5-9 evaluate a different runtime at all.

Tool naming moves out of the `toolName` parameter and onto the descriptor's `nodeName`, which `executeHTTPTool` already writes. So the change is mostly deleting a parameter and reading a field that is already there. Keep an override: a canvas name in Indonesian, or with spaces and punctuation, will not survive normalisation to `[A-Za-z0-9_]`, and a silently mangled name is worse than an explicit one. Duplicate detection belongs in the compiler rather than in `validateAgentConfiguration`, because only the compiler sees the whole graph and can name both offending nodes.

The trap is the iteration default. `ai.DefaultMaxIterations` is 8, the node's declared default is 8, and `AgentExecutor.Execute` passes `int(numberValue(parameters["maxIterations"]))`, which is 0 for any node saved before the parameter existed — falling back to the package constant. Moving the node's default to 10 without moving the constant leaves the number the user sees and the number that runs disagreeing for exactly those older nodes. Change both, and add a test that pins them together.

One decision to make. `systemPrompt` can stay as a permanent alias for `systemMessage`, or stored documents can be migrated. Recommend the alias, with the importer writing `systemMessage`: a stored-document migration for a V1-era parameter name is not worth building a migration path this early, and p6-1's versioned migrations do not exist yet.

## References

- Plan `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, p5 entry V2-p5-2.
- `nodes/ai.go` (`agentNode`, `executeHTTPTool`, `AgentExecutor.Execute`), `internal/ai/agent.go` (`LoopRuntime.Run`), `internal/ai/ai.go` (`AgentRequest`, `AgentResult`, `DefaultMaxIterations`).
- Parameter names to be confirmed against `packages/@n8n/nodes-langchain` in the reference checkout once p0-1 has widened it — that package is not in the checkout today.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 03 — the Agent NDV parameter set this ticket matches. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
