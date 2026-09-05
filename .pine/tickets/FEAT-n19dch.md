---
id: FEAT-n19dch
title: Expose a Datastore as an agent tool
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-3xqky1
    - FEAT-je4f4t
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`nodes/ai.go`'s `httpRequestTool.Definition` (lines 446-460) is the only tool schema the product emits, and it is open by construction: one property, `input`, typed `"object"` with a description and nothing else — no nested `properties`, no `required`, no `additionalProperties: false`. `Invoke` (lines 465-490) unmarshals whatever the model returned and, absent an `input` key, assigns the whole decoded object as `item.JSON` at line 473, so the model populates the node's item wholesale and every `{{ $json.… }}` in the configured request resolves from text a model wrote. That shape must not be copied for a datastore.

Nothing on this side of the wire checks the arguments against the schema. `internal/ai/ai.go`'s `ToolDefinition` (lines 47-53) carries `Parameters map[string]any` — free-form JSON Schema — and `internal/ai/openai.go` (lines 172-183) copies it into `wireToolFunction` (lines 291-295) and onto the wire, substituting an empty object schema only when it is `nil`. `Tool.Invoke` (`internal/ai/ai.go:98-102`) then receives a raw `json.RawMessage`. The schema is a hint to a provider, and the provider is whatever OpenAI-compatible endpoint the chat model's `baseUrl` names — a per-node parameter defaulting to `https://api.openai.com/v1` (`nodes/ai.go:53`). A closed schema that is only declared is not closed.

A datastore tool is exposed where the HTTP tool is not. `NewAgentExecutor` (lines 296-309) builds its tool executor from the deployment's `safehttp.Policy`, so a fabricated URL still meets a policy that refuses private targets; a datastore tool reaches KilasFlow's own storage with no equivalent backstop. The rows are themselves the likeliest injection source — a row a webhook wrote is read back into the model's context on the next call — so what the model reasons over is what an outsider supplied.

The tool therefore binds one datastore at construction and never accepts a datastore identifier as an argument. `AgentExecutor.Execute` builds tools once, before the item loop (lines 331-338), from descriptors the sub-nodes emitted, which is the right moment to read a datastore's stored column list and freeze it into the schema.

This matters because the agent is the surface a host application's end users touch most directly in an embedded install, and the one place where an untrusted party composes the arguments of a privileged call. Every other node takes its identifiers from a workflow an operator authored.

## Acceptance criteria

- [ ] The emitted `ai.ToolDefinition.Parameters` enumerates the bound datastore's columns and the supported operators as closed `enum` lists, proven by a test asserting the exact schema.
- [ ] Arguments naming a column or operator outside those enumerations are refused inside `Invoke` before any statement is built, proven by a test per rejection case.
- [ ] The schema carries no datastore identifier and an argument naming another datastore changes nothing about which is read, proven by a test using two datastores.
- [ ] A refused call becomes a tool-role turn the loop reports and an `ai.tool.failed` event carrying the reason, captured as evidence on this ticket.
- [ ] Rows returned to the model are bounded by an explicit row cap and a total size cap, proven by a test that exceeds both.
- [ ] A row whose contents read as an instruction is returned as data and widens neither the schema nor what the tool accepts next, proven by a test.
- [ ] Every test above is run by hand through `make test` and its result recorded here, there being no automated pipeline in this repository to run it.

## Implementation Plan

Build the schema from the stored column list first, because the validator, the argument decoder and the tool description are all projections of it. The columns are the catalogue V2-p9-1 owns, read once when the tool is constructed, so the schema is a value computed at agent start rather than assembled per call.

Shape it so the model supplies value leaves only. A filter argument names its column through a `columnName` field whose `enum` is the datastore's own column list, its operator through a `condition` field whose `enum` is the fixed vocabulary V2-p9-2 defines, and its comparison through a `value` leaf typed to that column. Everything structural is enumerated; everything free is data.

Reject reusing `httpRequestTool`'s design outright, and say so in the code comment. Its bargain is that the model fills `$json` and the node's expressions do the rest — precisely the identifier slot V2-p9-10 closes. Resolving a column name from a model-supplied item would reopen that hole one phase after it was shut.

Reject leaning on a provider's strict function-calling mode as the enforcement. `wireToolFunction` carries name, description and parameters and nothing else, and the endpoint is user-configurable, so a guarantee available on one vendor's API is unavailable on the compatible endpoints this product supports. Enforcement belongs in `Invoke`, where the arguments arrive.

The trap is a schema that declares an enum and an `Invoke` that trusts it. The declaration never meets a validator here — `openai.go` marshals it and forgets it — so a tool that skips the check works perfectly against a compliant model and fails open against any other. The failure is silent in the worst way: `internal/ai/agent.go:143-150` turns a tool error into a tool-role message and continues the loop, so neither a rejected call nor an accepted bad one appears in the node's output items, and a model quietly retrying with a different column reads exactly like ordinary agent behaviour.

Carry the datastore identity in the descriptor item the sub-node emits, following the `$ai` convention at `nodes/ai.go:40`, and carry only the identifier — never the rows. That item is persisted in the execution record and streamed to the live feed, which is why `executeChatModel` puts a credential ID in its descriptor rather than the key (lines 217-226).

**Write access in slice one.** The choice is exposing the full Row surface to the model at once, or read-only first. Recommend read-only — Get plus a filtered list — with insert, update, upsert and delete behind a per-node toggle defaulting to off, because a wrong read costs a bad answer while a wrong write costs the tenant's data. Reopen it if the cross-run key-value pattern V2-p9-6 names proves the dominant demand, since that use case is a write by definition.

## References

- Roadmap plan, p9 section, entry V2-p9-11: `.pine/roadmap.md`.
- `nodes/ai.go` — `httpRequestTool.Definition` at 446-460 and `Invoke` at 465-490, the open schema and the wholesale `item.JSON` assignment at line 473.
- `nodes/ai.go` — `NewAgentExecutor` at 296-309 and `AgentExecutor.Execute` at 312-338, the `safehttp` backstop a datastore tool lacks and the point where tools are constructed.
- `nodes/ai.go` — `executeChatModel` at 198-234, the precedent for putting an identifier rather than a secret into a descriptor item.
- `internal/ai/ai.go` — `ToolDefinition` at lines 47-53 and the `Tool` interface at 98-102, the contract a closed schema lives inside.
- `internal/ai/openai.go` — lines 172-183 and `wireToolFunction` at 291-295, where the schema goes onto the wire unexamined.
- `internal/ai/agent.go` — lines 131-150, where an unknown tool and a failed tool both become tool-role messages and the loop continues.
- `.pine/roadmap.md`, p9 section, entry V2-p9-2 — the filter shape and operator vocabulary the enums must match exactly.
