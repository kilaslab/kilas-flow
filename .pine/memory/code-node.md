# The Code node compatibility decision

**Decided in FEAT-8qyfh1. Do not relitigate it in a later ticket.**

An imported JavaScript or Python Code node is **refused, never translated**. It
becomes `kilasflow.foreignCode`, a first-class placeholder that keeps the
original `jsCode` / `pythonCode`, `language` and `mode`, shows the source in the
editor, exports back to `n8n-nodes-base.code` unchanged, and refuses to
compile with a message naming the native node that most likely replaces it.

**Why not translate.** Turning JavaScript into Go is a compiler project with no
correct stopping point. A one-line `items.map(…)` translates cleanly; the next
body — a closure over `$input`, a regex whose semantics differ, `JSON.parse` on
something that is not JSON — translates into Go that compiles and computes
something *different*. Silently different is the one outcome this codebase has
consistently refused. A curated table that **suggests** a replacement is worth
having; an automatic rewrite is not.

**Why blocking rather than pass-through.** A Code node that quietly passed its
items along would let the workflow activate, run, produce plausible output, and
be missing whatever the code was there to do.

**One mechanism for every JavaScript escape hatch.** `unsupportedScript` in
`internal/interop/n8n/parameters.go` is the single refusal — the Code node and
Sort's `code` comparator both go through it, and so must anything added later.
Three wordings for one situation is how a user concludes the three are
different problems.

**Where the Go toolchain lives** (also settled here; see
`internal/runcode/doc.go`): the `runcode.Compiler` interface is the seam. A
machine with `go` on its PATH uses `ToolchainCompiler`; the distroless image
has none and says so through the node catalogue's `unavailable` field; a hosted
install points the same interface at a compiler service, and the artifact cache
keyed by source hash means each distinct body is built once for the whole
installation. Bundling the toolchain into the runtime image is **not** the
answer — it triples the image and buys nothing a cache in front of one compiler
does not buy more cheaply.

The rule that follows: a user must never discover at run time that their
deployment cannot compile.
- 2026-09-05: Code node compatibility: imported JS/Python Code nodes are refused as kilasflow.foreignCode, never translated; the Go toolchain lives behind runcode.Compiler and availability is reported through the node catalogue.

# The Code node's time limit covers the user's program only

**Established in BUG-9s3htg.** There are three separate costs in running a Code
node, and each is bounded by a different thing. Do not merge them again.

1. **The Go toolchain build** — source to a 4.8 MB wasip1 module. Cached by
   source hash in `runcode.Cache`, bounded by `ToolchainCompiler.Timeout` (90s).
2. **wazero's translation** of that module to machine code. Roughly 0.8s warm,
   and **12s under `-race`**, because wazero's compiler is host-side Go and the
   race detector instruments all of it. Cached per process in
   `runcode.ModuleCache`, bounded by the caller's context.
3. **The user's program running.** This, and only this, is what
   `Limits.Timeout` / the node's `scriptTimeoutSeconds` bounds.

Charging (2) to the user's limit is what made a body of `return items, nil`
report "code exceeded its 10s time limit" under `-race`. If a Code node ever
reports a limit for work that plainly does not take that long, look at what has
crept back inside `context.WithTimeout` in `Runner.Execute` before looking at
the number.

Two consequences worth keeping:

- **Do not raise a limit or widen a test's limit to make this class of failure
  go away.** The tests deliberately run under `runcode.DefaultLimits()` so the
  suite proves the shipped default works. A test-only limit is how this went
  unnoticed until CI existed.
- **Translations are shared between executions; sandbox ceilings are not.**
  wazero keys its engine cache on the module binary and decodes memory limits
  per runtime, so two nodes with the same source and different `memoryMB` are
  correctly isolated. `TestASharedTranslationDoesNotCarryAMemoryLimitWithIt`
  pins that. Never close a `CompiledModule` that came from a shared cache —
  closing it evicts the translation the cache exists to hold.
