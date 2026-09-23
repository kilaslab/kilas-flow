---
id: EPIC-tjnr1z
title: Run n8n JavaScript Code nodes in-process on an embedded Go JS engine (goja)
status: todo
priority: high
labels:
    - code-node
    - javascript
    - n8n
    - parity
created: "2026-09-23T01:32:49Z"
updated: "2026-09-23T01:32:49Z"
---

# Description

An imported n8n Code node written in JavaScript should run inside the `kilasflow` binary, with no Node.js process involved. It runs on [goja](https://github.com/dop251/goja), a pure-Go ECMAScript engine (MIT) that works with `CGO_ENABLED=0`, behind a new package `internal/jsrun`. On top of the engine sit:

- a clean-room shim of n8n's Code-node globals (`$input`, `items`, `$json`, `$('Node')`, `$now`, `DateTime`, `this.helpers`, and the rest);
- the bundled MIT libraries Luxon and lodash;
- Go-backed `crypto`, `Buffer` and `Intl` subsets.

Each node execution gets a **fresh VM**, which costs 17 µs. Only compiled programs are cached. Time limits follow the Go Code node's rule: the limit covers the user's program only. The importer maps `n8n-nodes-base.code` with `language: javaScript` to a new runnable `kilasflow.jsCode` node. Python keeps being refused as `kilasflow.foreignCode`.

The fallback engine, if a hard per-execution memory cap ever becomes a requirement, is `modernc.org/quickjs`.

Why this matters: 31% of the 500 most-viewed n8n templates contain a Code node (368 nodes, 361 of them JavaScript). Today every one of them imports as a node that refuses to activate.

The spike measurements are in the attachment `js-runtime-spike-results.md`.

**Why now, with evidence from the 2026-09-23 n8n-parity audit** (EPIC-8rbys7, finding NG-1):

- The Code node is the most-used node KilasFlow cannot run. It appears in 568 of 998 sampled templates (56.9%), and in **83.6% of the newest 499**.
- It is the single blocker in 11.9% of the newest templates.
- Adding it alone raises the share of fully runnable templates from 5.9% to 13.3%. It is the #1 unlock, ahead of Google Sheets.

The user asked for an embedded Go runtime and weighed Goja, Sobek, QuickJS and goquickjs ("github.com/go-quickjs/go-quickjs"). This epic records the selection spike and the phased plan. Each phase is a child ticket.

> **Owner decision (2026-09-23): approved.** "Yes, approve: at least JavaScript can run, and n8n workflows that already use JS code can be imported." The rest of this note is kept for the record.
>
> **Decision needed from the owner before Phase 0 closes.** This epic partly supersedes the FEAT-8qyfh1 Code-node decision, which says "do not relitigate". It also narrows the licensing note "KilasFlow ships no JS runtime" to "no Node.js runtime". See *Relationship to the FEAT-8qyfh1 decision*.

# Goals

- Imported n8n **JavaScript** Code nodes run inside the `kilasflow` binary, with `CGO_ENABLED=0` and **no Node.js process**.
- n8n's Code-node contract is honoured clean-room: both modes, the `$input`/`items`/`$json`/`$('Node')` roots, Luxon, `console.log`, `this.helpers.httpRequest`, and `require` for crypto, luxon and lodash.
- Every construct the engine cannot run faithfully is refused at import and at validate, and never runs silently wrong.
- Time limits cover the user's program only (the BUG-9s3htg lesson). Memory is bounded by a layered policy.

# Context / current state

**How an imported JS Code node runs today: it doesn't.**

- `internal/interop/n8n/parameters.go` `codeToKilas` maps `n8n-nodes-base.code` to `kilasflow.foreignCode` (`nodes/jscode.go`). It keeps `jsCode`/`pythonCode`, `language` and `mode` verbatim, and adds a `replacement` hint from `nodes.SuggestReplacement`, a curated table of needles such as `.filter(`.
- It attaches a **blocking** `unsupportedScript` issue.
- `validateForeignCodeConfiguration` refuses to compile. `executeForeignCode` exists only so the node has a binding, and it always errors.
- Export round-trips the source unchanged.
- The Sort node's `code` comparator is refused through the same `unsupportedScript`.

**No JS engine is linked today.**

- `go.mod` has no goja, sobek, otto or QuickJS. It has `github.com/tetratelabs/wazero v1.9.0`, which the Go Code node (`internal/runcode`) and WASM packs (`internal/wasmpack`) use.
- `internal/expression` is a hand-written Go evaluator for the `{{ }}` JavaScript *expression* subset, with its own Luxon-like `dateValue` in `luxon.go`. It is not a JS engine, and this ticket does not change it.

**The sidecar is not a Code-node path.** `sidecar/` and `nodes/sidecar.go` (FEAT-7cg0cd) run *programmatic community nodes* in an operator-installed Node 24 process, opt-in through `KILASFLOW_SIDECAR_ENABLED`, one process per tenant, over NDJSON on a Unix socket. It never runs Code nodes, and this ticket keeps it that way. EPIC-m42s3g's acceptance scenarios require "no Node.js process anywhere".

**The Go Code node is the model to copy.**

- `nodes/code.go` and `internal/runcode` provide `Limits{Timeout, MemoryPages, MaxOutputBytes, MaxHostCalls}` and `DefaultLimits()` (10 s, 16 MiB, 4 MiB, 100). A node's `scriptTimeoutSeconds` and `memoryMB` may *tighten* the deployment ceiling, never raise it.
- Modes are `runOnceForAllItems` / `runOnceForEachItem`, using n8n's names.
- Artifacts are cached by source hash.
- Availability reaches the editor through the catalogue's `Definition.Unavailable`, stamped by `internal/api/handlers/nodes.go` from `WithAvailability`.

**What the engine already offers** that the shim reuses:

- `engine.Request.ExpressionContext(item, input, index)` provides `$json`, `$input`, `$('X')` (`.first()`, `.last()`, `.all()`, `.item` via `PairNodeItems` lineage, `.params`), `$node[...]`, `$workflow`, `$execution` (`id`, `mode`, `resumeUrl`), `$env` (allowlisted), `$vars`, `$itemIndex`, `$runIndex`, and the workflow `Timezone`.
- `request.Binaries` is a tenant-scoped payload store, and `internal/safehttp` enforces the egress policy.
- Missing: workflow static data (`$getWorkflowStaticData`) and a place for console output on `engine.NodeRun`.

**Pine history**:

- `.pine/memory/code-node.md` records the FEAT-8qyfh1 decision: refuse, never translate; one refusal path; the Go toolchain behind `runcode.Compiler`. It also records the BUG-9s3htg lesson that time limits cover the user's program only.
- `.pine/memory/licensing.md` holds the boundary: no n8n source, packages or bytes; vendored third-party bytes need `LICENSE` and `PROVENANCE.md`, enforced by `TestVendoredThirdPartyCarriesItsLicence`; the sidecar position is "KilasFlow ships no JS runtime".
- BUG-4053h6 already suggested "a sandboxed JS expression evaluator (e.g. goja …)" for expressions. That is still out of scope here.

**The n8n Code-node contract the runtime must honour** (clean-room, taken from docs.n8n.io and not from n8n source):

- **Modes.**
  - *Run Once for All Items* (the default) runs the body once. It sees `items` (legacy), `$input.all()`, `.first()`, `.last()` and `$input.item`. `$json` is *not* available.
  - *Run Once for Each Item* runs the body per item, with `$json`, `$input.item` and `$itemIndex`.
- **Return shape.**
  - All-items mode returns an array of `{json, binary?, pairedItem?}`. Plain objects and a single object are accepted and wrapped.
  - Per-item mode returns one object.
  - These are named errors: nothing returned, `json` that is not an object, and a wrong shape.
- **Other roots.**
  - `$('Node')`: `.all(branch?, run?)`, `.first()`, `.last()`, `.item`, `.itemMatching(i)`, `.params`, `.isExecuted`.
  - `$node['Node'].json`
  - `$workflow` (`id`, `name`, `active`)
  - `$execution` (`id`, `mode`, `resumeUrl`, `customData`)
  - `$prevNode`, `$runIndex`, `$vars`, `$env`, `$secrets`, `$nodeVersion`
  - `$now` / `$today` (Luxon DateTimes in the workflow timezone), plus the Luxon globals `DateTime`, `Duration` and `Interval`
  - `$jmespath`, `$getWorkflowStaticData('global'|'node')`
- **Not available in the Code node:**
  - `$binary`
  - the expression-only helpers `$if`, `$ifEmpty`, `$max` and `$min`
  - credentials (`this.getCredentials` is not a function)
  - `import`/`export`: the docs tell users to use `require`
- **Asynchrony.** The body runs inside an async function, so `await` at the top of the body works and a returned Promise is awaited.
- **Console.** `console.log` goes to the UI.
- **Modules.** `require()` is gated by `NODE_FUNCTION_ALLOW_BUILTIN` and `NODE_FUNCTION_ALLOW_EXTERNAL`. Cloud allows `crypto` and `moment`.
- **Helpers.** `this.helpers.httpRequest`, `getBinaryDataBuffer` and `prepareBinaryData`.
- **Errors** carry the user's line, plus the item index in per-item mode. n8n's own task-runner timeout defaults to 300 s.

**Real usage.** Code nodes in the 500 most-viewed templates (results.md §Real-world usage):

- 31% use `items`, 21% `$input.all()`, 21% `$('Node')` and 14% `$input.first()`.
- 34% return `[{json}]`; 23% use a regex, 11% `console.log` and 9% `?.`.
- 3.6% use `Buffer`, about 3% `toLocale*` or `Intl` (with de-DE and id-ID among the locales), 2.5% `$getWorkflowStaticData`, 1.7% Luxon `DateTime` and 1.7% `require` (`crypto` 4, `util` 1, `youtube-transcript` 1). `this.helpers` appears in 0.6%.
- None use `$env`, `$vars`, `$jmespath`, `pairedItem`, `for await`, `\p{}` or BigInt.

# Options considered

All measured in the spike: M4, Go 1.27.1. Timings are medians over 3 runs; the machine was shared, so read them as ratios.

| | **goja** | sobek | go-quickjs | modernc.org/quickjs | QuickJS on wazero (fastschema/qjs) | QuickJS via cgo | v8go |
|---|---|---|---|---|---|---|---|
| Works with `CGO_ENABLED=0` and cross-compiles | yes | yes | yes | yes (listed platforms) | yes | **no** | **no** |
| ES coverage | ES2020+ with async/await, classes and private fields, generators, BigInt, `?.`/`??`. **No** async generators, `\p{}` (silently wrong), `v` flag, `Object.groupBy` or `Intl` | = goja, plus ES modules | ES2025, full `Intl` | ES2023, no `Intl` | ES2023, no `Intl` | ES2023 | full |
| Interrupt at deadline | 101 ms, VM reusable | same | 101 ms, reusable | 103 ms, reusable | only by destroying the runtime | JS_SetInterruptHandler | TerminateExecution |
| Memory cap | **none native**; heap watchdog stopped at 258 MiB | none | **ineffective** in v0.1.0 | **enforced**, off the Go heap | **ineffective** (4 GiB linear memory) | enforced | enforced |
| Luxon | works with the ~100-line Go-backed `IntlLite` | same | native | with `IntlLite` | with `IntlLite` | — | — |
| Cold VM | **0.017 ms** | 0.024 ms | 0.30 ms | 0.24 ms | 4.8 ms | — | — |
| 1000-item n8n body | 29 ms | 28 ms | **13 ms** | 57 ms | 121 ms | — | — |
| Δ on the `kilasflow` binary | +5.70 MB | +5.91 MB | +18.9 MB | +2.54 MB, plus a forced `modernc.org/libc` 1.22→1.75 upgrade under the SQLite driver | +1.47 MB | — | +tens of MB |
| Sandbox defaults | nothing ambient | same | nothing ambient | nothing ambient | **mounts cwd as `/`; `std`/`os` read host files** | — | — |
| Maintenance | 7.1k★, 44 contributors, commits Sep 2026, pseudo-versions only | 348★, Grafana; **k6 depends on it** | **5 days old, 1 author, v0.1.0** | 21★, one main maintainer, monthly tags | v0.0.6, last commit Oct 2025 | — | rogchap stale since 2024 |
| Licence | MIT | MIT | MIT | BSD-3 wrapper + MIT QuickJS | MIT | MIT | BSD-3 |
| Host interop | best: Go funcs, Promises settled from Go, shareable `Program` | = goja | good | reflective `RegisterFunc` | handle-based | — | — |

Rejected, and why:

- **cgo QuickJS and v8go** break the static, distroless, `CGO_ENABLED=0` build.
- **fastschema/qjs** is unsafe by default: filesystem exposure, no usable interrupt, no memory cap. It is also stale. Making QuickJS on wazero safe would mean owning a C-to-wasm build.
- **go-quickjs** is the fastest engine and the only one with native `Intl`, but it is too young to trust with multi-tenant untrusted code, and its memory limit did not hold. Revisit it later.
- **sobek** behaves identically to goja in every probe and has the same API. It is the drop-in answer if goja's maintenance stalls, not a different trade-off.

# Decision

**Primary: goja.**

- It is the only candidate that is pure Go, mature and well maintained, and has reliable interrupts, cheap VMs and first-class Go interop.
- Its 17 µs VM creation lets every node execution get a fresh VM. That removes cross-execution and cross-tenant state leakage (demonstrated in the spike) without a pooling scheme.
- Throughput is enough for the workload: a 15-line median body runs in about 0.1 ms, and a 1000-item transform in 13–30 ms.
- Its gaps are either shimmable (`Intl`, `Buffer`, `crypto`, `Object.groupBy`, `structuredClone`) or absent from the corpus and detectable statically (`\p{}`, `v` flag, async generators). Detected constructs are refused, never run silently wrong.
- The binary cost is +5.7 MB (+18%).

**Fallback: `modernc.org/quickjs`.** Switch to it if either of these happens:

1. Per-execution hard memory caps become a product requirement, for example hosted SaaS with hostile tenants. modernc is the only candidate whose cap held.
2. goja and sobek maintenance both stall.

The cost of switching: roughly 2x slower, a lockstep `modernc.org/libc` and SQLite-driver upgrade, and less convenient interop. `internal/jsrun` keeps the engine behind one internal seam (`engine.go`), and the conformance corpus below is engine-neutral, so a swap is one adapter plus a green corpus. sobek remains the zero-cost swap for goja-specific maintenance risk.

# Relationship to the FEAT-8qyfh1 decision

FEAT-8qyfh1 (`.pine/memory/code-node.md`) chose between two options: *refuse* an imported Code node, or *translate* its JavaScript to Go. It rejected translation because it is unsound: JavaScript converted to Go can compile and yet compute something different. An embedded JS engine is a third option that decision did not have, because at the time "the JS sidecar is deferred to p8" and no in-process engine was on the table.

**What changes**

- An imported **JavaScript** Code node now **runs**, as `kilasflow.jsCode`, instead of being refused. The source runs *as JavaScript*, on a JavaScript engine, with its original text. Nothing is translated.
- Documents that were already imported as `kilasflow.foreignCode` with `language: javaScript` become runnable without a re-import. The foreignCode validator and executor delegate to the jsCode ones for JavaScript.
- `.pine/memory/code-node.md` gets a new dated entry, "superseded in part by <this ticket>", in place of the "do not relitigate" line's scope. `.pine/memory/licensing.md` records a new position: KilasFlow now links an embedded JavaScript *engine* (goja, MIT) and ships Luxon and lodash (MIT) as vendored bytes. It still ships **no Node.js, no n8n code or bytes, and no community package**, so the sidecar's "KilasFlow ships no JS runtime" statement is narrowed to "no Node.js runtime".

**What stays**

- **No translation, ever.** JavaScript is executed as JavaScript or refused.
- **Python stays refused.** It keeps `kilasflow.foreignCode` and `unsupportedScript("pythonCode", "Python", …)`.
  - This is 7 of 368 Code nodes (1.9%) in the corpus.
  - No viable pure-Go Python exists. gpython is a partial Python 3.4, and starlark is not Python.
  - The only credible CGO-free route is CPython-WASI on the wazero runtime already in the binary. That costs about 25 MB and slow startup, and is a separate spike.
- **One refusal path.** `unsupportedScript` in `internal/interop/n8n/parameters.go` stays the single wording for everything that still cannot run:
  - Python;
  - JS that uses a construct the runtime cannot run faithfully: `\p{…}`/`\P{…}` in a regex, the `v` flag, async generators and `for await`, `import`/`export`;
  - `require()` of a module that is not available;
  - `this.getCredentials`;
  - any JS node when the operator has disabled the runtime.

  The static analyser in `internal/jsrun/analyze.go` produces the *reason*. `unsupportedScript` produces the *sentence*. The importer and the node's `Validate` call the same function, so the import diagnostic and the editor say the same thing. `nodes.SuggestReplacement` stays: for Python it remains the suggestion, and for JavaScript it becomes an optional, non-blocking "a native node can do this" hint.
- **Sort's `code` comparator becomes runnable too, in Phase 6.** It is the same runtime, and a comparator is a pure `(a, b) => number` function: compile it once and call it inside one VM, O(n log n) calls at microseconds each. Until Phase 6 lands it stays refused through `unsupportedScript`, and the test asserting both hatches refuse with the same words is updated rather than deleted.
- **Catalogue `unavailable`.**
  - The JS engine is always linked, so `kilasflow.jsCode` is stamped unavailable only when the operator sets `code.javascript.enabled=false`. The reason names that key, through the existing `WithAvailability` function, so the editor greys the node out before save.
  - `kilasflow.foreignCode` is stamped unavailable with the Python sentence, so the editor says so up front instead of waiting for activation.
  - A user must still never discover at run time that their deployment cannot run a node.
- **The Go Code node is untouched.** `kilasflow.code` on wazero and `internal/runcode` keep working as they do. The JS executor copies that package's lessons:
  - Limits are fields the node can only tighten.
  - Caches are keyed by source hash.
  - **The time limit covers the user's program only** (BUG-9s3htg). VM creation, prelude and library setup (Luxon and lodash `Program`s, preloaded when static analysis sees them referenced), parsing the input JSON, and decoding the output are all outside `Limits.Timeout`. The clock starts at the first user statement and stops when its promise settles. The suite runs under `-race` with the shipped defaults, so a slow host cannot make a trivial body "exceed" its limit.

# Plan

Each phase below is a child ticket of this epic.

## Phase 0 — Decision record and guardrails

1. Update `.pine/memory/code-node.md` and `.pine/memory/licensing.md` as described above. Get the owner's sign-off in the ticket before any code.
2. Vendor Luxon 3.7.x and lodash 4.17.x/4.18.x under `third_party/luxon/` and `third_party/lodash/`. Each gets the upstream `LICENSE`, a `PROVENANCE.md` (URL, version, sha256) and a tiny `embed.go` package (`//go:embed *.min.js`). This must pass `TestVendoredThirdPartyCarriesItsLicence`. The "only two exceptions" line in licensing memory is updated.
3. Guardrail tests in `internal/guardrails`:
   - `internal/jsrun` and `nodes/jscode*.go` never import `sidecar`, `os/exec`, `net`, `os` (file access) or `syscall`.
   - No file under `internal/jsrun` is derived from n8n. Test fixtures come from the gitignored corpus only.
   - `TestNoN8NDependency*` still passes.

## Phase 1 — `internal/jsrun` core (engine, limits, errors)

1. Add `github.com/dop251/goja` to `go.mod`, pinned to a pseudo-version.
2. `engine.go` is the only file that imports goja. It exposes a narrow interface: new VM, run program, bind function, interrupt, promise settle, and job drain. This is the fallback seam.
3. `jsrun.go` defines the core types:

   ```go
   type Mode string // "runOnceForAllItems" | "runOnceForEachItem"
   type Limits struct {
       Timeout         time.Duration // user program only
       MaxInputBytes   int64         // marshalled items in, default 32 MiB
       MaxOutputBytes  int64         // default 16 MiB
       MaxConsoleBytes int64         // default 64 KiB per node run
       MaxCallDepth    int           // default 10_000 (SetMaxCallStackSize)
       MaxHostCalls    int           // default 100, parity with runcode
   }
   func DefaultLimits() Limits // Timeout 10 s, parity with runcode.DefaultLimits
   type Task struct{ Source string; Mode Mode; Items []workflow.Item; Roots Roots; Host Host; Limits Limits }
   type Result struct{ Items []workflow.Item; Console []ConsoleLine; Paired [][]int }
   type Runner struct{ /* program cache, library programs, semaphore, watchdog */ }
   func (r *Runner) Run(ctx context.Context, task Task) (Result, error)
   ```

   Named errors: `ErrTimeout`, `ErrMemoryLimit`, `ErrOutputLimit`, `ErrInputLimit`, `ErrHostCallLimit`, `ErrInvalidReturn`, and `*ScriptError{Name, Message, Line, Column, ItemIndex, UserStack}`.
4. `programs.go` holds an LRU of `*goja.Program`, keyed by `sha256(wrapperVersion|mode|source)`. goja documents a `Program` as runnable in many runtimes at once. Library `Program`s are compiled once per process with `sync.Once`: IntlLite, Luxon, lodash and the Buffer shim.
5. VM lifecycle: **a fresh `goja.Runtime` per node execution**. Per-item mode reuses that execution's VM across its items, but calls a fresh closure per item. There is no VM pool: the spike measured 17 µs cold, so one isn't needed. The VM stays on one goroutine and is never shared.
6. Interrupts:
   - `context.AfterFunc(ctx, vm.Interrupt)`, whose context is the *user-time* deadline derived from `Limits.Timeout` and the parent execution context.
   - Set `regexp2.DefaultMatchTimeout` process-wide at `init` (proposed 1 s). The spike measured a lookahead regex running 9.7 s past a 100 ms deadline without it, and 303 ms with it.
   - Recursion is bounded with `SetMaxCallStackSize`.
7. Memory policy, layered because goja has no per-VM accounting (documented honestly in the node's help text):
   - (a) Refuse input over `MaxInputBytes` before any VM exists.
   - (b) Check output size inside the VM before stringifying across, and again in Go.
   - (c) A concurrency semaphore, `code.javascript.max_concurrent`, defaulting to `GOMAXPROCS`.
   - (d) `watchdog.go`: one process-wide sampler of `runtime/metrics` `/gc/heap/live:bytes`, every 10 ms while at least one JS run is active. When the live heap exceeds `code.javascript.heap_ceiling`, it interrupts every running JS VM with `ErrMemoryLimit`. Those node runs fail; the process survives. The default ceiling is 50% of `GOMEMLIMIT` when set, else 1 GiB.
   - The worst case is documented: the ceiling plus one allocation from a single builtin.
8. Error mapping in `errors.go`:
   - Convert goja `*Exception` stacks (`Code:L:C`) to user coordinates. The wrapper prelude is exactly **one line**, so `line−1`.
   - Keep only frames from the user's file.
   - Render `"<Name>: <message> [line N]"`, or `"… [line N, for item I]"` in per-item mode, in KilasFlow's own wording.
   - Non-Error throws become `String(value)`.
   - A syntax error at validate time reports line and column.
9. Config: add `config.Code.JavaScript{Enabled, Timeout, MaxConcurrent, HeapCeilingMB, MaxInputBytes, MaxOutputBytes, MaxConsoleBytes, AllowModules}`, following the `koanf`/`KILASFLOW_CODE_JAVASCRIPT_*` conventions. Document it in `config.example.yaml` and the docs site. A node's `scriptTimeoutSeconds` may only tighten `Timeout`, as in `nodes/code.go`. The engine's per-node timeout and the execution deadline still apply through the context.
10. Tests for this phase:
    - interrupt of `while(true)`, of a loop inside a promise job, and of ReDoS; each must stop within deadline + 50 ms (ReDoS: + regex timeout);
    - VM reuse after an interrupt;
    - the watchdog stops a runaway allocation with `ErrMemoryLimit` and the process survives;
    - the output and input caps;
    - line mapping in both modes;
    - `-race` with `DefaultLimits()`;
    - benchmarks for 10 and 1000 items in both modes.

## Phase 2 — The n8n globals shim (clean-room)

1. `js/prelude.js`: KilasFlow-authored, `go:embed`ed, a single line. The roots live in an **enclosing** function scope, as `let` bindings or accessor properties, so user code may redeclare them (`const items = $input.all()` is the commonest first line; the spike caught this). The user body is the body of an `async function` invoked with `.call(thisObj)`, so `this.helpers` works and a top-level `await` is function-level.
2. Roots are built from `request.ExpressionContext(item, input, index)`, so the Code node sees exactly the data an expression sees:
   - `$input`: `all`, `first`, `last`, `item`, `params`, `context.noItemsLeft`
   - `items` (all-items mode); `$json` and `$itemIndex` (per-item mode)
   - `$('X')`: `all`, `first`, `last`, `item`, `itemMatching`, `params`, `isExecuted`
   - `$node['X']`, `$prevNode`, `$runIndex`, `$workflow`, `$execution` (`id`, `mode`, `resumeUrl`; `customData` is a named "not supported" error), `$env` (the allowlisted env), `$vars`, `$nodeVersion`
   - `$jmespath`, backed by a Go JMESPath library such as `github.com/jmespath-community/go-jmespath` (check the licence first)
3. Data crossing: items are marshalled to JSON in Go and parsed inside the VM, outside the clock, so user code gets real JS objects with exact semantics (2.5–3 ms per 1000 items). The result comes back through `JSON.stringify` and a size check, then `json.Unmarshal`. Binary crosses as metadata only (`{id, fileName, mimeType, fileExtension, fileSize}`), never payload.
4. Return normalisation:
   - **All-items mode:**
     - An array of items is kept, with `json`, `binary` and `pairedItem` honoured.
     - A plain object inside the array becomes `{json: o}`.
     - A single object is wrapped.
     - `undefined`, primitives, and a `json` that is not an object are `ErrInvalidReturn` with a pointed message.
   - **Per-item mode:**
     - The result must be one object, which is wrapped the same way. An array is `ErrInvalidReturn`.
     - `pairedItem` is set to the item index automatically.
   - **Lineage:** explicit `pairedItem` wins. Otherwise the runner's positional inference applies when the counts match.
5. Execution modes: all-items mode calls the compiled wrapper once. Per-item mode compiles once and calls a per-item closure for each item in the same VM, charging each call's time to the same budget. `continueOnFail` and `onError` follow the engine's existing node-level semantics; per-item error items are a follow-up.
6. Console:
   - `console.log`, `info`, `warn`, `error` and `debug` are captured as `ConsoleLine{Level, Text, At}`.
   - Strings are kept as they are. Other values use a bounded JSON-ish inspect: depth 4, circular references marked.
   - The capture is capped at `MaxConsoleBytes`, with a truncation marker.
   - It is stored on a new `engine.NodeRun.Console` field and persisted with the trace row, the same way `Response` is. It is also emitted live through `request.Events` for manual runs.
   - The editor output panel gets a "Console" tab.
7. Tests are re-authored in KilasFlow's own words, one per documented behaviour: modes, return shapes, every root, lineage, and the console. n8n-docs is under the Commons Clause, so doc snippets are **not** copied verbatim.

## Phase 3 — Libraries and shims (Luxon, lodash, crypto, Buffer, Intl)

1. **Luxon.**
   - The vendored bundle loads through a precompiled `Program`: 2.5 ms per VM, and 0 ms when unused.
   - It is preloaded before the clock starts when static analysis sees `DateTime`, `Duration`, `Interval`, `$now`, `$today`, `luxon` or `require('luxon')`. A lazy accessor covers dynamic access, and that cost is charged to user time.
   - After load: `Settings.defaultZone = workflow.Timezone` (the workflow timezone, as n8n does) and `Settings.defaultLocale = 'en-US'`.
   - `$now` and `$today` are Luxon DateTimes in that zone.
2. **`IntlLite`.** This is the spike's shim hardened: `Intl.DateTimeFormat` with `format`, `formatToParts`, `resolvedOptions`, `dateStyle` and `timeStyle`, over the Go tz database via `time.LoadLocation`. The distroless static image carries zoneinfo.
   - With it, every Luxon probe in the spike matches Node 24.
   - Add `Intl.NumberFormat` backed by `golang.org/x/text/number` (CLDR decimals for any locale, plus currency and percent), and route `Number.prototype.toLocaleString` and `Date.prototype.toLocale{,Date,Time}String` through the shims. goja's own `toLocaleString` treats its argument as a radix.
   - **Non-English *date* formatting is a named error** ("date formatting in locale de-DE is not supported"), never silent English. Numbers work in every locale x/text knows.
   - `Intl.RelativeTimeFormat` stays absent: Luxon falls back to English on its own, and the spike matched Node's "2 days ago".
3. **`require()`.** It uses an allowlist from `code.javascript.allow_modules`, defaulting to `crypto`, `luxon` and `lodash`. `util` gets a subset (`inspect`, `format`, `promisify`, `types`). Anything else is refused at analysis time and at run time with the same sentence. There is no `fs`, `child_process`, `net`, `http`, `process` or `worker_threads`.
4. **crypto.** A Go-backed subset of Node's API:
   - `createHash` (md5, sha1, sha256, sha384, sha512)
   - `createHmac`
   - `randomBytes`, `randomUUID`, `randomInt`
   - `timingSafeEqual`
   - `pbkdf2Sync`, `scryptSync` (via `x/crypto/scrypt`)
   - `createCipheriv` / `createDecipheriv` for AES-128/192/256 in CBC, CTR and GCM
   - `webcrypto.getRandomValues`

   An unknown algorithm is a named error. It is also exposed as `globalThis.crypto` for `randomUUID` and `getRandomValues`.
5. **Buffer.** A `Uint8Array` subclass with:
   - `from` (string with the utf8, base64, base64url, hex or latin1 encodings, array, ArrayBuffer)
   - `alloc`, `concat`, `byteLength`, `isBuffer`
   - `toString(encoding)`, `slice`/`subarray`, `equals`, `toJSON`

   Encodings are Go-backed. Also add `TextEncoder`/`TextDecoder`, `atob`/`btoa`, `URL` and `URLSearchParams` (over `net/url`), `structuredClone` (JSON-safe values, Date, Map, Set), and polyfills for `Object.groupBy` and `Map.groupBy`.
6. **Timers.** `setTimeout`, `setInterval`, `clearTimeout` and `clearInterval` run on the per-execution job loop (proven in the spike's `gojahost`). They are bounded by user time and never outlive the execution.
7. **Tests.** Parity golden files (`testdata/parity/*.json`) are recorded once by a **dev-only** script (`scripts/js-parity/record.mjs`) under Node. CI compares against the committed goldens and never runs Node. They cover every Luxon probe in the spike plus zones with DST transitions, the crypto test vectors from Go's own test data, and Buffer round-trips.

## Phase 4 — Node type, executor, importer and catalogue

1. `nodes/jscode.go` gets `kilasflow.jsCode` ("Code (JavaScript)"). Its parameters use n8n's names so import is a copy: `jsCode` and `mode`, plus `scriptTimeoutSeconds`.
   - `Validate` runs the syntax check and static analysis; it does not run the code.
   - `ExecutorID` is `core.jsCode`, registered in `nodes/executors.go` behind `WithJSRunner(...)`.
   - The Go `kilasflow.code` node is unchanged.
2. `JSCodeExecutor.Execute` builds a `jsrun.Task` from the IR, the input and `engine.Request`, then maps the result:
   - items and binary refs;
   - `Console` onto the run;
   - errors, with node-name prefixes consistent with `nodes/code.go`.
3. `kilasflow.foreignCode` with `language: javaScript` delegates `Validate` and `Execute` to jsCode, which makes existing imports runnable. Python still refuses.
4. `internal/analyze.go`:
   - It uses goja's parser AST, not regexes, so strings and comments don't produce false hits.
   - It extracts: the roots used (for lazy libraries); `require` targets; regex literals containing `\p{`/`\P{` or the `v` flag; async generators; `import`/`export`; `this.getCredentials`; and whether `$getWorkflowStaticData` is used, which is refused until Phase 5.
   - The output is a list of `Unsupported` reasons fed to `unsupportedScript`.
5. Importer changes in `internal/interop/n8n/parameters.go`:
   - **`codeToKilas`**:
     - A missing `language` means JavaScript (typeVersion 1 has no `language`).
     - JavaScript maps to `JSCodeNodeType`, with `jsCode` and `mode` copied and no blocking issue unless the analyser finds one.
     - Python maps to `foreignCode`, as today.
   - **`codeToN8N`** serves both types: it writes back `n8n-nodes-base.code` typeVersion 2 with `language`, `mode` and `jsCode` byte-for-byte.
   - The mirrored node-type constant is kept in step by the existing mirror test.
   - The import report's "blocked only by Code nodes" becomes "blocked only by Python Code nodes / unsupported JS constructs".
6. Catalogue: `WithAvailability` stamps jsCode (when disabled) and foreignCode (always, with the Python reason).
7. Tests:
   - an import round-trip that is byte-exact on `jsCode`;
   - importing a template with each mode;
   - old foreignCode+JS documents run;
   - `unavailable` stamping;
   - Python still refused with the same sentence as before.

## Phase 5 — Host helpers and state

1. **`this.helpers.httpRequest(options)`.** It returns a Promise, settled from a worker goroutine through the per-execution job loop (pattern proven in the spike). It goes through `internal/safehttp` with the tenant's egress policy, counts against `MaxHostCalls`, respects context cancellation, and supports the documented `url`/`method`/`headers`/`body`/`qs`/`json`/`returnFullResponse` options. There is no credential access; the Code node never sees credentials, as in n8n.
2. **`this.helpers.getBinaryDataBuffer(itemIndex, prop)`** returns a `Buffer` read from `request.Binaries`. **`prepareBinaryData(buffer, fileName?, mimeType?)`** returns a binary ref. Both are tenant-scoped by construction, and output binary refs are validated.
3. **`$getWorkflowStaticData('global'|'node')`** needs a new tenant-scoped store: a migration adding a `kflow_workflow_static_data` table, and a repository behind an engine interface.
   - Data is loaded before the run and saved only after a *successful* non-manual execution, when it changed. That matches the documented n8n behaviour: static data is not saved in test runs.
   - Size is capped at 256 KiB, as a named error.
   - Until this step lands, the analyser refuses the call.
4. **Console in the editor.** The output panel gets a Console tab with live lines for manual runs; persisted lines appear in execution detail.

## Phase 6 — Sort `code` comparator

1. `sortToKilas` carries `type: code` and the comparator source. The Sort executor compiles `(function (a, b) { <code> })` once per node run and sorts with `vm` calls under the same limits.
2. Update the "both hatches refuse in the same words" test so it asserts that Python and unsupported constructs still share one sentence.

## Phase 7 — Compatibility corpus and end-to-end

1. **Corpus sync.** `scripts/code-corpus-sync.sh` fetches Code nodes from the N most-viewed templates via `https://api.n8n.io/api/templates/search?sort=views:desc` and `/api/templates/workflows/<id>`. They go into a **gitignored** `internal/jsrun/corpus/fixtures/`, pinned in `MANIFEST.json` by template id and sha256. This follows the `internal/interop/n8n/corpus` pattern: third-party user content is never committed, only digests.
2. **Scoreboard test.** It reports, for the JS Code nodes:
   - (a) the parse rate;
   - (b) the rate accepted by the analyser;
   - (c) the rate that runs without a runtime error on synthesised input: the template's `pinData` when present, otherwise a schema-shaped stub;
   - (d) the median and p95 run time.

   The baseline goes in `BASELINE.md`, and CI fails on regression. The spike's data sets the expectation that (a) and (b) are at least 97%, since none of the refused constructs appear in the top 500.
3. **Differential mode (dev-only, `make js-diff`, needs Node).** Run the same body and input under jsrun and under Node with a KilasFlow-authored roots harness, not n8n's, and diff the outputs. This catches goja/V8 semantic drift. It is never part of the product or the default CI job.
4. **E2E** in the existing Playwright/Go e2e suite. Import an n8n workflow containing all-items and per-item Code nodes, including Luxon, `console.log`, `$('Node').first()`, `Buffer` and `crypto`. Then:
   - activate it;
   - trigger it;
   - assert the outputs and the console;
   - assert that **no `node` process exists** during the run (process-table check).

   Wire this into FEAT-5fhj6p's scenario 2 and 4 runs.

## Phase 8 — Docs and hardening

1. Add a docs page "Code (JavaScript)": what runs; the differences from n8n (engine speed on CPU-heavy code, no npm install, English-only date locales, the memory model); configuration keys; the limits.
2. Run a security review pass. It covers:
   - prototype pollution confined to one execution;
   - host functions validate their arguments and never expose Go structs through reflection (`Set` only plain funcs and data);
   - `eval` and `new Function` stay enabled, as in n8n, because they cannot reach the host.

# Acceptance Criteria

- [ ] `.pine/memory/code-node.md` and `.pine/memory/licensing.md` record the partial supersession of FEAT-8qyfh1 and the new licence position (goja MIT linked; Luxon and lodash MIT vendored with `LICENSE` and `PROVENANCE.md`; no Node.js, no n8n bytes), and the owner has signed off in this ticket.
- [ ] `make build` still produces a `CGO_ENABLED=0` static binary for linux/amd64 and linux/arm64. The distroless image is unchanged apart from the binary. The binary grows by no more than 7 MB.
- [ ] An imported `n8n-nodes-base.code` with `language: javaScript` (or no language) becomes `kilasflow.jsCode` with **no blocking import issue**, activates and runs. Exporting it gives `n8n-nodes-base.code` with `jsCode` byte-identical to the import.
- [ ] Previously imported `kilasflow.foreignCode` nodes with `language: javaScript` run without re-import.
- [ ] Python Code nodes are still refused, through `unsupportedScript`, with the same sentence as before, and the catalogue stamps `kilasflow.foreignCode` as `unavailable`.
- [ ] Both modes behave as documented:
  - all-items mode sees `items` and `$input.all()`, `.first()` and `.last()`;
  - per-item mode sees `$json`, `$input.item` and `$itemIndex`;
  - user code may redeclare any root (`const items = $input.all()`);
  - return normalisation wraps plain objects;
  - invalid returns are named errors.
- [ ] `$('Node').first()`, `.last()`, `.all()`, `.item`, `.itemMatching()` and `.params`, plus `$node`, `$workflow`, `$execution` (`id`, `mode`, `resumeUrl`), `$env`, `$vars`, `$now`, `$today` and `$jmespath` all return the same data an expression sees for the same item.
- [ ] Top-level `await`, a returned Promise, and `this.helpers.httpRequest` all work, and HTTP goes through the tenant's egress policy and is counted against `MaxHostCalls`.
- [ ] `console.log` output is captured into the node run (persisted and live), capped at `MaxConsoleBytes`, and visible in the editor.
- [ ] Luxon: every probe in the spike's `LuxonProbes`, plus DST-transition cases, matches the committed Node goldens. `DateTime` defaults to the workflow timezone.
- [ ] `require('crypto')` (the listed subset), `require('luxon')` and `require('lodash')` work. `require('fs')` and other unlisted modules are refused with the `unsupportedScript` sentence, both at import and at run.
- [ ] `Buffer`, `TextEncoder`, `atob`/`btoa`, `URL`, `structuredClone`, `Object.groupBy`, `Intl.NumberFormat` (any CLDR locale) and en-US `Intl.DateTimeFormat` pass their golden tests. A non-English date locale is a named error, never English output.
- [ ] A regex with `\p{…}`, the `v` flag, an async generator or `import`/`export` is refused at import and at validate. None of them ever runs silently wrong.
- [ ] Limits:
  - `while(true)`, a loop in a promise job, and a catastrophic regex are each stopped within the deadline + 50 ms (regex: + `regexp2.DefaultMatchTimeout`), as `ErrTimeout`;
  - a runaway allocation is stopped by the watchdog as `ErrMemoryLimit`, the server process survives, and other executions continue;
  - the input and output caps are enforced as named errors.
- [ ] **The time limit covers the user's program only.** A test with a 50 ms limit and a Luxon-using body passes under `-race` with `DefaultLimits`, proving that VM creation, library load and marshalling are not charged.
- [ ] One fresh VM is used per node execution. A test proves that a global or prototype mutation in one execution is invisible to the next execution and to other tenants.
- [ ] Errors report `[line N]`, or `[line N, for item I]` in per-item mode, mapped to the user's own line numbers, with a user-only stack.
- [ ] The Sort node's `code` comparator runs (Phase 6).
- [ ] The corpus scoreboard is committed as digests plus a baseline, never as fixtures: at least 97% of the top-500 JS Code nodes pass parse and analysis, and the runtime-error rate on synthesised input is recorded. CI fails on regression.
- [ ] The e2e scenario imports, activates and runs a Code-node workflow and asserts that **no Node.js process exists** at any point. The EPIC-m42s3g "no Node.js process anywhere" criterion holds with Code nodes present.
- [ ] `go test ./...` passes, including `internal/guardrails`, where a new import-graph test forbids `internal/jsrun` from importing `sidecar`, `os/exec` or `net`.

# Risks

| Risk | Impact | Mitigation |
|---|---|---|
| goja has no per-VM memory cap | A hostile or buggy body can grow the shared Go heap. The watchdog attributes memory per process, not per execution, and one builtin allocation can overshoot | Layered policy (input and output caps, concurrency cap, heap watchdog, `GOMEMLIMIT` guidance); document the worst case; `modernc.org/quickjs` fallback behind `engine.go` if hard caps become a requirement |
| Silent semantic gaps in goja (the spike found `\p{}` regex false negatives and `toLocaleString(locale)` throwing) | Wrong output that looks plausible, which is the outcome this codebase refuses | Static refusal of the constructs; shims for `toLocale*`; the differential Node corpus (`make js-diff`) run before each goja bump |
| goja interprets and does not JIT. It is 10–50x slower than V8 on CPU-heavy loops | Heavy compute bodies may hit the 10 s limit where n8n would not | Document it; the timeout is configurable up to the ceiling; benchmarks in CI; most bodies are tiny (median 15 lines) |
| GC pressure: JS objects live on the Go heap | Latency for other executions under heavy Code-node load | Concurrency cap; reuse compiled programs; measure p99 under load in Phase 8 |
| Uninterruptible builtins and regexes | The deadline overruns by one builtin's duration | `regexp2.DefaultMatchTimeout`; input caps bound the size of any one builtin's work |
| goja maintenance (one main maintainer, no tagged releases) | Stuck on bugs | Pin a pseudo-version; sobek is a same-API drop-in (Grafana, k6); the engine seam plus the corpus make the swap mechanical |
| Intl coverage | Non-English date formatting refused (about 1% of the corpus) | Named error; locale-aware numbers through x/text; revisit with go-quickjs (native Intl) when it matures |
| Licensing | Contaminating an Apache-2.0 white-label product | goja, Luxon and lodash are MIT; vendored bytes carry `LICENSE` and `PROVENANCE.md` with a guardrail test; the shim is written from documented behaviour only (no n8n task-runner source, no verbatim n8n-docs snippets, which are Commons Clause); corpus fixtures are never committed |
| Host binding leaks (a Go struct passed to `vm.Set` exposes its methods through reflection) | Sandbox escape to host APIs | Bind only plain funcs and plain data; guardrail review; tests enumerating the globals |
| Divergence from the expression engine (Code nodes get real Luxon while `{{ }}` keeps the Go `dateValue`) | Small behaviour differences between a Code node and an expression | Parity tests on shared cases; a later ticket may move expressions onto `jsrun` (BUG-4053h6 already suggested goja) |

# Out of scope

- Python Code nodes. They stay refused; a CPython-WASI-on-wazero spike is a separate ticket.
- Arbitrary npm modules and `NODE_FUNCTION_ALLOW_EXTERNAL` beyond the bundled Luxon and lodash: no `npm install` and no module directory. `moment` is not bundled (1 use in the corpus); `youtube-transcript`-style packages are refused.
- Node.js APIs beyond the listed shims: `fs`, `child_process`, `net`/`http`/`fetch` (use `this.helpers.httpRequest`), streams, `process`, and worker threads.
- Full `Intl` date formatting for non-English locales, `Intl.Collator`, and `Intl.Segmenter`.
- ES modules and `import`/`export`, which n8n's Code node forbids too.
- LangChain Code node, AI Transform node, and the legacy `n8n-nodes-base.function` / `functionItem` types (a cheap follow-up on the same runtime).
- Replacing `internal/expression` with goja.
- Any use of the Node.js sidecar for Code nodes. The sidecar stays an opt-in path for programmatic community nodes only.
- Per-item error items under `continueOnFail`, beyond the engine's node-level semantics.
- `$execution.customData`, `$secrets` and `$evaluateExpression`. They are named errors until there is demand; the corpus has 0 uses.

# References

- Spike: `results.md` and `spike/` beside this file (harness `spike/common`, adapters `spike/cmd/*`, `gojahost`, `gojaprof`, `gojaredos`), and the corpus analysis `templates/analyze.py`.
- Code:
  - Current Code-node paths: `nodes/jscode.go`, `nodes/code.go` and `internal/interop/n8n/parameters.go` (`codeToKilas`, `unsupportedScript`, `sortToKilas`).
  - Engine: `internal/runcode/{runcode.go,sandbox.go}`, `internal/engine/runner.go` (`Request`, `NodeRun`, `PairNodeItems`) and `internal/engine/authenticate.go` (`ExpressionContext`).
  - Catalogue and config: `internal/api/handlers/nodes.go` (`WithAvailability`) and `internal/config/config.go` (`Code`).
  - Guardrails and sidecar: `internal/guardrails/licence_boundary_test.go` and `sidecar/doc.go`.
- Pine: FEAT-8qyfh1, BUG-9s3htg, FEAT-7cg0cd, FEAT-5fhj6p, BUG-4053h6, EPIC-m42s3g, `.pine/memory/code-node.md` and `.pine/memory/licensing.md`.
- n8n docs (behaviour only, not copied):
  - `build/code-in-n8n/using-the-code-node`
  - `build/code-in-n8n/use-built-in-shortcuts/n8n-metadata`
  - `build/work-with-data/reference-data/reference-previous-nodes`
  - `build/work-with-data/handle-special-data-types/work-with-dates-and-times`
  - `integrations/builtin/core-nodes/n8n-nodes-base.code/common-issues`
  - `deploy/.../enable-modules-in-code-node`
  - `deploy/.../use-environment-variables/task-runners`

# Attachments

- `js-runtime-spike-results.md`: benchmark and feature-matrix results of the engine spike (goja, sobek, go-quickjs, modernc.org/quickjs, fastschema/qjs). The throwaway harness was not committed.
