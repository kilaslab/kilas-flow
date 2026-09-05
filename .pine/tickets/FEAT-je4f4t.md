---
id: FEAT-je4f4t
title: Add the AI tool family and usable-as-tool nodes
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-cgm1y3
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:02:44Z"
updated: "2026-09-05T05:02:44Z"
---

## Scope

One tool node exists. `httpToolNode()` in `nodes/ai.go` builds `kilasflow.httpTool` by taking `httpRequestNode()`, swapping its ports for a single `ai_tool` output and prepending a name and a description; at run time `AgentExecutor.httpToolFrom` wraps the very same `HTTPExecutor` the HTTP Request node uses, so the tool inherits the SSRF policy, credential scoping, timeout and response limits instead of reimplementing them. That reuse is the correct pattern and this ticket generalises it rather than replacing it.

Three things are missing. There is no Workflow Tool, so an agent cannot call another workflow. There is no way to expose an ordinary node as a tool — n8n marks a node `usableAsTool` in its base description (`packages/workflow/src/interfaces.ts` in the reference checkout) and wraps it automatically. And there is no `$fromAI`.

`$fromAI` is the one that matters most, because without it a tool's parameters are fixed at design time. `httpRequestTool.Definition()` advertises exactly one schema property — a free-form `input` object described as "values the request's expressions read through `$json`" — so the model is told nothing about what fields the tool actually wants. n8n instead lets any parameter be written `{{ $fromAI('city', 'the city to look up', 'string') }}`, collects those calls into the tool's JSON Schema, and substitutes the model's arguments before the node executes. That is what turns a configured HTTP request into a tool a model can genuinely drive.

`$fromAI` is therefore an expression-engine change first. `internal/expression/expression.go` accepts six roots — `$json`, `$input`, `$node`, `$env`, `$execution`, `$itemIndex` — and no function calls at all, by design: the V1 evaluator's whole security argument is that a tenant-authored parameter can never become code. p1-10 widens the grammar with a function allowlist, and `$fromAI` is one entry in it, but a peculiar one: it is not evaluated like a function. It is extracted at schema-build time and substituted at invoke time.

## Acceptance criteria

- [ ] HTTP Request Tool, Workflow Tool, and at least one ordinary node exposed through `usableAsTool` can all be attached to one agent's `ai_tool` slot and called within a single run.
- [ ] `$fromAI(key, description, type, defaultValue)` in any tool parameter contributes a typed property to that tool's JSON Schema and is replaced by the model's argument at invoke time; `type` defaults to `string` and an unrecognised type fails validation with the offending call named.
- [ ] The tool schema is derived rather than hand-written: a tool with three `$fromAI` calls advertises three properties carrying their descriptions.
- [ ] A tool with no `$fromAI` calls keeps today's behaviour — the model's arguments arrive as `$json` — so existing workflows do not change meaning.
- [ ] Workflow Tool runs a sub-workflow with a bounded call depth and refuses recursion, naming the cycle rather than exhausting the execution timeout.
- [ ] A node exposed as a tool keeps one definition: same parameters, same credentials, same executor, differing only in ports and tool naming.
- [ ] Every tool call still passes through the deployment's SSRF policy and per-credential domain scope; no tool acquires a second outbound path.
- [ ] Tool arguments and results appear in the execution record as `ai.tool.*` events, redacted.

## Implementation Plan

Do the expression work first, in `internal/expression`, because both the schema builder and the argument substitution depend on it and p1-10 owns the grammar it lands in. Extraction is a separate pass from evaluation: walk the parameter map, collect the calls, build the schema; then at invoke time bind the model's arguments and evaluate normally.

Two details from n8n's own implementation are worth copying exactly, and both are in `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/from-ai-parse-utils.ts`. Its `extractFromAICalls` is a character-by-character parser rather than a regex, because arguments contain quotes, escapes and nested parentheses that a regex will get wrong on real workflows. And its detection pattern is case-insensitive (`/\$fromAI\s*\(\s*/gi`), so `$fromai` written by a user in an imported workflow must also match — read the file, do not copy it.

Then `nodes/ai.go`. Generalise `httpToolFrom` so a tool descriptor names the executor to run rather than hardcoding `HTTPExecutorID`; the descriptor already carries `nodeName`, `parameters` and `credentials`, so most of what a general wrapper needs is present.

For `usableAsTool`, add a flag to `node.Definition` and synthesise the tool variant at registration rather than hand-writing two definitions per node. Note the cost: `internal/node.Definition` is serialized directly as the `/api/v1/node-types` payload (`internal/api/handlers/nodes.go`), so a new field is an OpenAPI change — run `pnpm generate:api` and the SDK's type generation, or the CI drift check fails. Add the missing test p2-7 asks for while you are here: every registered `ExecutorID` must have an executor, because a synthesised definition with no executor behind it compiles and passes every test.

One decision. The synthesised tool variant can be a separate node type in the catalogue, which is n8n's model and what an import needs to map onto, or one node whose ports change with a parameter. Recommend the separate type: it keeps the registry key `{type, version}` stable and it does not depend on p2-8's still-open question of whether ports may be computed from a node's own parameters.

## References

- Roadmap plan, p5 section, entry V2-p5-6: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p1 entry V2-p1-10 (expression engine v2) and p2 entry V2-p2-7.
- `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/from-ai-parse-utils.ts` (`extractFromAICalls`, `FromAIArgument`), `constants.ts` (`FROM_AI_AUTO_GENERATED_MARKER`), `interfaces.ts` (`usableAsTool`, `UsableAsToolDescription`).
- `nodes/ai.go` (`httpToolNode`, `executeHTTPTool`, `httpToolFrom`, `httpRequestTool`), `internal/expression/expression.go`, `internal/node/registry.go`, `internal/api/handlers/nodes.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 07, 10 — an HTTP Request Tool attached to an agent, and how a tool-capable node is re-listed under Other Tools. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
