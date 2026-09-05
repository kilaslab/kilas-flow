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
