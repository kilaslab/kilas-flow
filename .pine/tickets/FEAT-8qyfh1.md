---
id: FEAT-8qyfh1
title: Decide and deliver the Code node compatibility story
status: todo
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-v8k1tc
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:03:55Z"
updated: "2026-09-05T05:03:55Z"
---

## Scope

KilasFlow's Code node runs Go. `codeNode()` in `nodes/code.go` registers `kilasflow.code` with a `code` parameter whose description is "The body of func run(items []Item) ([]Item, error)", plus `timeoutSeconds` and `memoryMB`; `internal/runcode/runcode.go` compiles that source with `GOOS=wasip1 GOARCH=wasm` through `exec.CommandContext(… "build" …)` and runs the module under wazero. n8n's Code node runs JavaScript. The importer has no entry for `n8n-nodes-base.code` in `mappings` (`internal/interop/n8n/n8n.go`), so every imported Code node becomes `kilasflow.unsupported`, whose validator always fails — one Code node makes the whole workflow unactivatable, and the Code node is among the most-used nodes in the corpus.

The owner has already settled the JS question at the roadmap level: the JS sidecar is deferred to p8-2, so emulating n8n's Code node is not on the table in this phase. What is undecided, and what this ticket must decide and then deliver, is what actually happens to an imported JavaScript Code node — refused with a first-class diagnostic, or translated onto the Go node. That decision must be made once, here, and every other p4 ticket that meets a JavaScript escape hatch (Sort's `code` comparator, for one) must call into this ticket's mechanism rather than inventing its own.

Refusing only works if the alternative exists, and today it does not reliably. `internal/runcode/doc.go` still carries the open question in its own words — "compiling Go requires the full toolchain (~270MB), which cannot ship inside the distroless single binary. Resolve before Milestone 5 -- likely a separate compiler service" — and `ErrCompilerUnavailable` surfaces to the user as "this deployment cannot compile Code nodes" at run time, after the workflow was activated. The Go node also drops binary data in both directions (`CodeExecutor.Execute` moves only `item.JSON` into `runcode.Item` and only `JSON` back out) and has no equivalent of n8n's `runOnceForEachItem`, so it always runs once over the whole batch.

## Acceptance criteria

- [ ] The decision is written into the ticket body and into `.pine/memory/` as a durable learning, stating what an imported JavaScript or Python Code node becomes and why, so no later ticket relitigates it.
- [ ] An imported `n8n-nodes-base.code` becomes a dedicated node type — not the generic `kilasflow.unsupported` — that preserves `jsCode`, `pythonCode`, `mode` and `language`, shows the original source read-only in the editor, and fails compilation with a message naming the node and the alternative.
- [ ] The import diagnostic distinguishes "this workflow is blocked only by Code nodes" from "this workflow has other unsupported nodes", and names each Code node individually.
- [ ] One refusal mechanism serves every JavaScript escape hatch in p4, and the p4-2 Sort `code` comparator uses it.
- [ ] The Go toolchain question in `internal/runcode/doc.go` is answered, the doc comment no longer describes it as open, and a deployment that cannot compile reports it through `/api/v1/node-types` so the editor can say so before the workflow is saved, not after it runs.
- [ ] The Go Code node carries binary through in both directions over the store from p3-8, instead of silently dropping it.
- [ ] The Go Code node supports running once per item as well as once per batch, matching n8n's `mode`.
- [ ] The p0 corpus report separates workflows blocked only by a Code node from workflows blocked for other reasons, and the counts are recorded in the work evidence.

## Implementation Plan

Take the refusal, not the translation. Translating JavaScript to Go is a compiler project with no correct stopping point: a Code node body that looks like a one-line `items.map(…)` will translate, and the next one — closures over `$input`, a regex with JavaScript semantics, `JSON.parse` on a string that is not JSON — will translate into Go that compiles and computes something different. Silently different is the one outcome this codebase has consistently refused, from `kilasflow.unsupported` onward. The refusal must be much better than today's generic placeholder, though: a distinct node type in a new `nodes/jscode.go` that keeps the original source visible, and a diagnostic that tells the user which native node now does the job — p4-1 and p4-2 add Filter, Switch, Aggregate, Sort, Split Out, Summarize and Remove Duplicates precisely because those replace most real Code nodes. A curated pattern table that recognises common bodies and *suggests* a replacement is worth building; an automatic rewrite is not.

Keep it blocking. The placeholder's `Validate` must fail, like `nodes/unsupported.go`'s does, because a Code node that quietly passes items through is worse than one that refuses. What changes is the reporting: the import result should say "this workflow is one Code node away from running" rather than burying it in a list.

The harder half is making the Go node a real alternative. Decide the compiler story explicitly. There are three shapes: bundle the toolchain in the runtime image, which contradicts the single-binary distribution the PRD builds on; ship an optional compiler sidecar image that the main binary talks to over the existing `runcode.Compiler` interface, which `ToolchainCompiler` already abstracts; or precompile at save time in CI-like environments and ship artifacts. Recommend the sidecar: `runcode.Compiler` is already the seam, `Cache` already keys artifacts by source hash, and `CodeExecutor.Status` already exists so the editor can show compilation state without running the workflow. Whatever is chosen, surface availability in the node-types payload so `internal/api/handlers/nodes.go` reports it and the editor greys the node out — a user must not discover at run time that their deployment cannot compile.

Then close the two Go-node gaps in `nodes/code.go`: carry `item.Binary` into and out of `runcode.Item` over p3-8's store, and add a `mode` parameter so the executor can loop per item. Note that per-item mode multiplies compilation cache hits, not compilations — the artifact is keyed by source hash, so this is cheap.

Finally, keep p8-5's three prerequisites in view but out of scope: a new wazero runtime is built per call with the compilation cache unused, and the guest has zero host functions, so HTTP, credentials and binary data are impossible from inside user code today. The binary work in this ticket is plumbing at the executor boundary, not host functions inside the sandbox.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (code) and the locked decision "JS sidecar: deferred to the long tail"; p8-2 JS sidecar for programmatic community nodes; p8-5 native community module SDK and its three prerequisites; p3-8 binary data storage.
- PRD `gflow-prd-v1.md` §30 Go Code Node, §31 Go Code Security, §32 Go Code V1 Restrictions.
- Verified in this repository: `nodes/code.go` (`codeNode`, `CodeExecutor.Execute`, `CodeExecutor.Status`, `CompilationStatus`), `internal/runcode/doc.go` (the open toolchain question, verbatim), `internal/runcode/runcode.go` (`ErrCompilerUnavailable`, `ToolchainCompiler`, the `GOOS=wasip1 GOARCH=wasm` build), `internal/interop/n8n/n8n.go` (`mappings` has no `code` entry), `nodes/unsupported.go` (`validateUnsupportedConfiguration` always fails), `internal/api/handlers/nodes.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): the type string `n8n-nodes-base.code` is confirmed present, shipping from `packages/nodes-base/nodes/Code/`.
