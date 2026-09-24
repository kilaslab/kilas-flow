# The Code node compatibility decision

**Decided in FEAT-8qyfh1. Do not relitigate it in a later ticket.** The owner reopened it once, on 2026-09-23, for JavaScript only: see the dated entry at the end of this section and EPIC-tjnr1z.

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
- 2026-09-23: Owner approved (EPIC-tjnr1z): imported n8n JavaScript Code nodes are to RUN on an embedded pure-Go JS engine (goja, modernc.org/quickjs fallback) instead of being refused as foreignCode. JS is executed as JS and never translated to Go. Python Code nodes stay refused. This supersedes only the 'refused' half of the FEAT-8qyfh1 decision recorded above.
- 2026-09-23: How the supersession is scoped (EPIC-tjnr1z, P0).
  - JavaScript is **executed as JavaScript, or refused**. There is still no third option and no translation.
  - Everything that still cannot run goes through one sentence template: `jsrun.Refusal(subject, alternative)`, which renders "this node's code <subject>, which this server does not run. <alternative>". `unsupportedScript` delegates to it. That covers Python, `\p{…}` regexes, the `v`/`d` flags, async generators, `import`/`export`, unlisted `require`, `this.getCredentials`, and a disabled runtime. The Python wording stays byte-identical.
  - The "refused, never translated" paragraphs above now describe Python and unsupported JS constructs only.
- 2026-09-23: **The JS runtime's clock is charged per VM entry.**
  - VM entries before the first user statement are free: prelude, libraries the analyser asked for, input `JSON.parse`, and the wrapper factory.
  - Every VM entry after it is charged: the body, promise jobs, result normalisation, and `JSON.stringify` of the result, because user `toJSON`, getters and Proxy traps run inside those.
  - Go-side work between entries is free.
  - Per-item mode spends one pausable budget across all its items.
  - This is BUG-9s3htg's rule restated for goja. Do not "simplify" it into one wall-clock deadline.

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

# goja's hazards, and the guards jsrun keeps against them

**Found by the P1 security review (2026-09-23). Keep the guards whatever the goja version.**

- **A Go stack overflow is fatal, and goja's parser and compiler recurse on nesting with no limit of their own.** 300,000 nested brackets killed the whole process, and a crash at `Analyze` happens at save or import time. jsrun therefore checks every body before goja sees it:
  - `MaxSourceBytes` (128 KiB) bounds the length, and so how deep anything can nest while it parses.
  - Arrow functions are counted in the raw text, where the count can only err high. Nested arrows parse in quadratic time.
  - The tree's depth is capped at 1000, checked before compile. Nested blocks compile in quadratic time.
  - Constant-expression depth is capped at 16. goja's `constant()` is not memoised, so `0 || 0 || …` 40 deep, which is 147 bytes, compiles in exponential time.
  - Parsing and compiling cannot be interrupted, so they run under a process-wide semaphore of GOMAXPROCS.
- **goja cannot interrupt one built-in call, and the watchdog can only interrupt.** `[...Array(2**26).keys()]`, `Array.from({length: 2**26})`, `.fill`, `new Uint8Array(2**30)` and `'x'.repeat(2**28)` each held seconds and gigabytes past every limit. `runtime.js` `boundAllocations` wraps every `Array.prototype` method, `Array.from`, `apply`/`construct`, `repeat`/`pad*` and the typed-array and ArrayBuffer constructors with per-call caps (`MaxElementsPerCall` and its siblings). Any new built-in that allocates from a number (Buffer.alloc, in P3) needs the same guard.
- **A panic on any goroutine other than the VM's is outside `guard`.** Every goroutine that runs host code (see `hostCall`) must recover its own panics.
- **A destructuring parameter list makes goja panic on a direct `eval()`** in a nested function. The wrapper uses plain positional parameters.
- 2026-09-24: 2026-09-24 (FEAT-x9gq0s): a Code node's this.helpers call is carried out by the SERVER, never the worker (FEAT-21h6xp will take the network away from workers): jsrun hands the request to a goroutine through Host.Call and settles the promise on the job loop, and in the server that call crosses the worker protocol by ID and is answered on a server goroutine. Waiting on the server is not charged: jsrun's clock pauses while the VM is idle with only helper calls pending (a timer armed beside them keeps it running), and the pool holds its kill deadline while any helper call is in flight. Keep both halves if either changes, or a slow API turns into a time-limit or a killed worker. Every helper call counts against MaxHostCalls, which is what bounds that free waiting.
