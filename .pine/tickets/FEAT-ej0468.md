---
id: FEAT-ej0468
title: Spike the Microsoft Agent Framework as an agent runtime
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-cgm1y3
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:04:01Z"
updated: "2026-09-05T05:04:01Z"
---

## Scope

`internal/ai/maf/` contains one file, `doc.go`, and that file contains only a package comment. No Go file in it implements anything, and `go.mod` has no requirement on `github.com/microsoft/agent-framework-go`. The comment claims the module "publishes no tagged releases, so it resolves to a pseudo-version" — the plan records that `agent-framework-go` reached v0.1.0 on 2026-09-01 with tool calling, MCP, approvals, multi-agent routing and OpenTelemetry, so the comment is stale whichever way the evaluation goes and this ticket corrects it either way.

The boundary the framework would sit behind already exists and is honest. `ai.AgentRuntime` has exactly one method; `LoopRuntime` in `internal/ai/agent.go` is 186 lines with no dependencies; `nodes.NewAgentExecutor` accepts any implementation; `cmd/kilasflow/main.go` passes `ai.NewLoopRuntime()` at composition. Swapping the runtime is a wiring change, not an engine change — which is exactly the property that makes this evaluable as a spike rather than a rewrite.

This is a spike. Its deliverable is a decision with evidence, not a merged runtime. Three pass criteria are fixed in advance so the answer cannot drift into "it was interesting":

1. **It must accept an injected `*http.Client`.** `safehttp.NewClient(policy)` is where the SSRF dialer and per-credential domain scoping live — the dialer re-checks the resolved IP on every connection including each redirect hop. A framework that constructs its own transport loses both, and no configuration puts them back.
2. **It must expose streaming deltas**, or `ai.EventModelDelta` stops reaching the live execution feed and the editor's streamed output goes dark.
3. **It must report per-turn token usage**, or `AgentResult.Usage` is zero and every cost figure built on it is wrong.

Failing any one of the three means it stays out, and `LoopRuntime` remains the default.

## Acceptance criteria

- [ ] The spike lives on a throwaway branch or an unmerged directory; nothing in `cmd/` changes the default runtime.
- [ ] Each of the three pass criteria is answered with a reference to the framework's own code, not to its documentation's claims.
- [ ] The licence, the transitive additions to `go.mod`, and whether `CGO_ENABLED=0` builds still work are all recorded.
- [ ] A decision is written into the ticket body: adopt as an optional runtime, or refuse with the reason.
- [ ] `internal/ai/maf/doc.go` is corrected — either the package is implemented, or the comment states the evaluated version, the date, and why it stays out.
- [ ] `LoopRuntime` is still the default runtime after this ticket closes, whatever the decision.
- [ ] If adopted: the runtime is selectable by configuration, and the same workflow produces the same output item shape under both runtimes, proven by running one fixture through each.

## Implementation Plan

Read the framework's agent loop before writing a line of Go. Criterion 1 is answered by whether its client or agent constructor takes an `*http.Client`, or an options struct carrying one; if it does not, stop there, write the refusal, fix `doc.go`, and close the ticket. That is a successful outcome, not a failed one.

If it passes, implement `ai.AgentRuntime` in `internal/ai/maf` and nothing else — that package is the only one permitted to import the module, which is what keeps the churn of a preview API off the workflow contract. Wire it as an alternative in `cmd/kilasflow/main.go` behind configuration, never as a replacement.

The trap: do not add the dependency to the main `go.mod` while the evaluation is inconclusive. A `go.sum` entry for a module nothing imports survives review easily and then surfaces months later as a broken distroless build or an unexpected cgo requirement. Keep the spike's dependency in its own module or branch until the decision is made.

One decision remains if it is adopted: whether MAF replaces `LoopRuntime` or sits beside it. Recommend beside it, chosen per deployment. `LoopRuntime` is 186 lines with zero dependencies and it is a large part of what makes the single-binary claim true; a framework that is optional costs nothing to keep optional, and a framework that is mandatory has to be right forever.

## References

- Roadmap plan, p5 section, entry V2-p5-9: `.pine/roadmap.md`.
- `.pine/tickets/EPIC-m42s3g.md` — the decision-table row on AI nodes.
- `internal/ai/maf/doc.go`, `internal/ai/ai.go` (`AgentRuntime`, `AgentRequest`, `AgentResult`, `EventSink`), `internal/ai/agent.go` (`LoopRuntime`), `nodes/ai.go` (`NewAgentExecutor`), `cmd/kilasflow/main.go`, `internal/safehttp/safehttp.go`, `go.mod`.
