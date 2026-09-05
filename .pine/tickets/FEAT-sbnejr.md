---
id: FEAT-sbnejr
title: Add the structured output parser
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-cgm1y3
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:03:08Z"
updated: "2026-09-05T05:03:08Z"
---

## Scope

There is no output parser, and there is no place to put one: `internal/workflow/document.go` declares four connection kinds — `main`, `ai_languageModel`, `ai_memory`, `ai_tool` — and `knownConnectionKind` rejects anything else, so an imported `ai_outputParser` edge cannot even be represented in a KilasFlow document. p2-8 widens the kind set toward n8n's thirteen; this ticket adds the node that lives behind the new one.

The mechanism matters more than the feature. n8n's Structured Output Parser does not ask the model to reply in JSON and hope. It attaches a synthetic tool named `format_final_json_response` whose schema is the requested output shape, and the agent's final answer is that tool call's arguments. Copying this is not aesthetic preference: a customer's imported workflow was tuned against that behaviour, and a prompt-instruction implementation produces different output from the same prompt against the same model. The difference is invisible until the data downstream is wrong.

The pieces are already in place. `ai.ToolDefinition` carries a name, a description and a JSON Schema `Parameters` map; `LoopRuntime.Run` builds its `definitions` slice from the attached tools and terminates when the model answers without asking for one. A synthetic tool changes only the stop condition: the run ends when `format_final_json_response` is called, and its arguments are the result.

## Acceptance criteria

- [ ] A structured output parser node registers with a single `ai_outputParser` output, attaches to the AI Agent or the Basic LLM Chain, and is capped at one connection per root.
- [ ] The output shape can be given either as a JSON Schema or as an example JSON document, and an invalid schema fails validation naming the offending path.
- [ ] With a parser attached, the root node's output item carries the parsed object — not a JSON string — and it validates against the declared schema.
- [ ] The mechanism is n8n's: a synthetic tool named `format_final_json_response` is offered to the model, verifiable in the recorded `ai.tool.*` events.
- [ ] A model response that fails schema validation is retried a bounded number of times and then fails with both the validation error and the raw text, so the failure is diagnosable.
- [ ] A parser connected to a node that cannot accept one is refused at compile time, not at run time.
- [ ] Attaching a parser does not disturb the agent's ordinary tools: a run with two real tools and a parser calls all three correctly.

## Implementation Plan

Start in `internal/ai/agent.go`. `AgentRequest` gains an output-schema field; `LoopRuntime.Run` appends the synthetic definition to `definitions` and treats an invocation of it as the terminal turn — set `result.Output` from the call's arguments and return.

The trap is implementing it as an ordinary `ai.Tool`. If it goes into `request.Tools`, the loop finds it in the `tools` map, invokes it, appends a tool-role turn and asks the model again — the run continues past the answer, burns iterations, and may produce a second, different final answer. The synthetic tool must be defined to the model but not invocable through the tool map.

Then the node in `nodes/ai.go`, its executor id in `nodes/executors.go`, and the slot on the chain from p5-3, which shares it. The parser's descriptor travels the same `$ai` route the model and memory descriptors already use, so no new plumbing is needed on the item channel.

The tool name is part of the contract with the model, not an implementation detail. n8n's name is `format_final_json_response`; a different name changes model behaviour on prompts customers already have, and there is no upside to choosing another.

One decision to settle. When the model answers in plain text without calling the tool — which happens, especially with smaller models — the parser can either fail immediately or validate the text as a fallback. Recommend the fallback as the first retry step: parse the plain text against the schema, and only re-prompt if that fails. It costs nothing when the model complied and saves an entire extra model call when it merely forgot the wrapper.

## References

- Roadmap plan, p5 section, entry V2-p5-7: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p2 entry V2-p2-8, the `ai_outputParser` connection kind.
- `internal/workflow/document.go` (`ConnectionKind`, `knownConnectionKind`), `internal/ai/ai.go` (`ToolDefinition`, `AgentRequest`), `internal/ai/agent.go` (`LoopRuntime.Run`), `nodes/ai.go`, `nodes/executors.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 03 — the "Require Specific Output Format" toggle that reveals the output parser slot. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
