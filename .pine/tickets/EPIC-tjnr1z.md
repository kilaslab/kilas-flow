---
id: EPIC-tjnr1z
title: Run n8n JavaScript Code nodes in-process on an embedded Go JS engine (goja)
status: done
priority: high
labels:
    - code-node
    - javascript
    - n8n
    - parity
created: "2026-09-23T01:32:49Z"
updated: "2026-09-25T04:10:24Z"
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

# Implementation amendments (2026-09-23)

A check against the real code and the goja source (`v0.0.0-20260917113740`) changed parts of the plan below. **Where this section and a phase disagree, this section wins.**

1. **Config keys are flat.** `envKeyToPath` splits on the first underscore, and the reference generator cannot describe nested structs. The keys are:
   - `code.javascript_enabled`
   - `code.javascript_timeout`
   - `code.javascript_max_concurrent`
   - `code.javascript_heap_ceiling_mb`
   - `code.javascript_max_input_bytes`
   - `code.javascript_max_output_bytes`
   - `code.javascript_max_console_bytes`

   The env names stay `KILASFLOW_CODE_JAVASCRIPT_*`. `allow_modules` is dropped: `Validate` and the importer cannot see config, and without npm nothing could be added. The `require` allowlist is fixed.
2. **The engine seam, precisely.**
   - The goja runtime, `goja_nodejs/*` and `regexp2/v2` are imported only by `internal/jsrun/engine*.go`.
   - The syntax-only packages (`goja/{ast,parser,file,token,unistring}`) are imported only by `engine*.go` and `analyze*.go`.
   - Nothing outside `internal/jsrun` imports the `dop251` modules.
3. **Security: source maps.** goja's parser calls `os.ReadFile` on a trailing `//# sourceMappingURL=` comment, and runtime `eval` and `new Function` do the same. Every parse uses `parser.WithDisableSourceMaps`, and every VM gets `SetParserOptions(parser.WithDisableSourceMaps)`.
4. **The regexp2 timeout must be at least the user-time ceiling.** goja turns a match timeout into "no match" (`regexp.go:327,350`), so a short timeout silently changes results. With the invariant, a timeout can only land after the interrupt, and the interrupt aborts first. The worst-case overrun is therefore about the ceiling. `code.javascript_timeout` is capped at 5m.
5. **Errors.** The body is an async function, so user throws arrive as promise rejections, not `*goja.Exception`. `errors.go` maps:
   - parser `ErrorList`;
   - `CompilerSyntaxError`;
   - rejection values, by parsing the `Code:L:C` stack;
   - `InterruptedError`.

   `ErrCallDepth` is added, because goja's stack overflow cannot be caught.
6. **Wrapper escape.** A body that closes the wrapper's brackets is refused: the analyser checks the shape of the wrapped AST.
   - **Clock rule:** VM entries before the first user statement are free. Every later VM entry is charged, including normalisation and `JSON.stringify` of the result. Go-side work between entries is free.
   - The clock is a pausable budget shared across the items of per-item mode.
7. **The analyser is `internal/jsrun/analyze.go` and lands in P1.** It parses the *wrapped* text. It also refuses `this.helpers.*` and `$getWorkflowStaticData` until P5, and refuses the `d` regex flag, which goja also rejects.
8. **Roots.**
   - `Request.RunIndex` is added, which fixes `$runIndex` in expressions too.
   - `$vars` is `{}`.
   - `$jmespath`, `$prevNode`, and `.all(branch, run)` beyond `.all()`/`.all(0)` are **named errors**, matching expressions, so there is no JMESPath dependency.
   - `$now`/`$today` land in P3 with Luxon.
9. **Per-item roots** are one shared snapshot per execution, plus `{index, paired}` per item. `engine.PairedIndexes` is factored out of `pairNodeItem`.
10. **Binary** crosses as `{id, fileName, mimeType, fileExtension, fileSize}` with no `data` field. It is dropped unless returned, as in n8n, and a returned id must come from this node's input.
11. **Lineage precedence:**
    1. an explicit `pairedItem`;
    2. object identity (a prelude `WeakMap`, so `items[i] === $input.all()[i]`);
    3. positional inference.
12. **`WholeBatch`** on the node definition stops `perItemTolerance` from splitting a Code node into one-item calls under continueOnFail.
13. **No `foreignCode` unavailable stamp.** JS foreignCode now runs, and Python is refused by the import issue and by `Validate`. Only `jsCode` is stamped, and only when disabled.
14. **One refusal template:** `jsrun.Refusal(subject, alternative)` gives "this node's code %s, which this server does not run. %s". The Python sentence stays byte-identical. The JS "native node" hint is dropped, since there is no non-blocking severity.
15. **P1 acceptance changes.**
    - "VM reusable after an interrupt" is dropped, because VMs are never reused.
    - The 50 ms Luxon-body test moves to P3; P1 proves the rule with lodash.
16. **Watchdog.** It reads `/memory/classes/heap/objects:bytes`, gives each registration its own ceiling, and runs a GC after firing. The default ceiling is computed in `cmd/kilasflow` from `GOMEMLIMIT`.
17. **Console persists from P2**, as planned:
    - `NodeRun.Console` and migration 000021;
    - the API field and a typed `code.console` event;
    - a Console section on the execution detail page.

    The editor's live tab stays in P5.
18. **`kilasflow.jsCode` can be added from the palette**, not only by import.
19. **Buffer and URL come from `goja_nodejs`** (MIT, same author), with a registry loader that always refuses. Small polyfills fill the Buffer gaps.

# Progress (2026-09-23)

P0–P4 are done (FEAT-rkj8ry, FEAT-7q13t6, FEAT-yxhgeh, FEAT-zjrw76,
FEAT-pxcbqj): imported and new JavaScript Code nodes run, on goja, with
Node's crypto, Buffer, URL, web globals, timers, Intl, Luxon and lodash, and
console output kept with each node run. Beyond the plan:

- **Worker processes (FEAT-g6k3y9).** The owner chose process isolation over
  a documented residual risk: goja cannot interrupt one built-in call, so the
  in-process bounds are a denylist. Every script runs in a pool of the
  kilasflow binary's own worker processes; the server prepares a job and
  decodes its result, and trusts a worker only as far as its code could go.
  They are a resource boundary, not a privilege boundary (FEAT-21h6xp).
- **Per-item continue-on-failure** runs as n8n's item loop does: the failed
  item alone goes to the error output, without splitting the batch.
- **The binary grew 7.47 MB (amd64) / 7.08 MB (arm64)**, past the +7 MB
  criterion below; goja's own x/text/collate is 1.24 MB of it. Reported to
  the owner (FEAT-pxcbqj notes).
- **Follow-ups filed:** FEAT-21h6xp (workers as a privilege boundary),
  FEAT-9we7kw (en-CA and en-GB dates), BUG-548bk9 (unhandled rejections),
  BUG-14gp8r (pairing after a fan-out and a reorder), BUG-fthahg (engine-wide
  error-item and $('X') differences from n8n).

P5–P8 follow.

# Progress (2026-09-24, stopped for the usage limit)

Done and merged on main this session: P5 (FEAT-x9gq0s), P6 (FEAT-mammrz), P7
(FEAT-afkx3k), worker confinement (FEAT-21h6xp), BUG-548bk9, BUG-14gp8r,
BUG-k99658, BUG-h6tj4e (found by the P8 security review), and the corpus
parity bugs BUG-pdsydm, BUG-kvpx6x, BUG-djp647, BUG-9hx5xm, BUG-jwhj6y,
BUG-3mem9s. Each went through task review and fix rounds; the last fix rounds
of BUG-jwhj6y/BUG-3mem9s and of the $items run-numbering fix were checked by
the controller (diff and tests) without a separate re-review.

Open, where to resume:
- FEAT-9we7kw (en-CA/en-GB): branch `epic/feat-9we7kw`; its last commit is a
  WIP of review fix round 3 (the -u-hc- extension width rule, a new
  en-GB-u-hc-h24 regression, toLocale* hourCycle h11/h24, narrowing the
  offset refusal). The reviewer's Node probe harness notes are in the ticket.
- BUG-46g75c (Buffer UTF-8 as WHATWG): branch `epic/buffer-utf8`, WIP commit,
  unreviewed.
- BUG-a9d2hb (Europe/Dublin on Debian tzdata): not started; after FEAT-9we7kw.
- FEAT-vjjs8t (P8): merged, status testing; needs a docs touch-up for en-CA/
  en-GB once that merges, and the review's minor items (symbol-keyed globals
  in the surface walk, async-function constructor test, comparator surface).
- Low: BUG-2vcwjf, BUG-c19kyx, BUG-qe71kf.
Detached (not epic children): FEAT-0ynje5 (seccomp profile by a person),
FEAT-f40kg4 (per-tenant workers), BUG-5fhcx7 (batch nodes under
continue-on-fail).

# Progress (2026-09-25, epic closed)

All 27 children are done and merged on `main`. The remaining work from the
2026-09-24 stop (FEAT-9we7kw, BUG-46g75c, BUG-a9d2hb, FEAT-vjjs8t touch-up,
BUG-2vcwjf, BUG-c19kyx, BUG-qe71kf) went through subagent-driven development
(implement → review → fix → re-review) and closed with evidence. Acceptance
criteria above are ticked from current-tree evidence; the binary-size
ceiling overrun stays the reported exception. Detached follow-ups
(FEAT-0ynje5, FEAT-f40kg4, BUG-5fhcx7, BUG-0592hz) are not epic children.

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

- [x] `.pine/memory/code-node.md` and `.pine/memory/licensing.md` record the partial supersession of FEAT-8qyfh1 and the new licence position (goja MIT linked; Luxon and lodash MIT vendored with `LICENSE` and `PROVENANCE.md`; no Node.js, no n8n bytes), and the owner has signed off in this ticket.
- [x] `make build` still produces a `CGO_ENABLED=0` static binary for linux/amd64 and linux/arm64. The distroless image is unchanged apart from the binary. The binary grows by no more than 7 MB.
      **Exception (reported):** growth was +7.47 MB (amd64) / +7.08 MB (arm64) past the +7 MB ceiling; reported to the owner in FEAT-pxcbqj and left as-is rather than traded for a feature.
- [x] An imported `n8n-nodes-base.code` with `language: javaScript` (or no language) becomes `kilasflow.jsCode` with **no blocking import issue**, activates and runs. Exporting it gives `n8n-nodes-base.code` with `jsCode` byte-identical to the import.
- [x] Previously imported `kilasflow.foreignCode` nodes with `language: javaScript` run without re-import.
- [x] Python Code nodes are still refused, through `unsupportedScript`, with the same sentence as before. (Amendment 13: the catalogue does not stamp `kilasflow.foreignCode`, because its JavaScript form now runs.)
- [x] Both modes behave as documented:
  - all-items mode sees `items` and `$input.all()`, `.first()` and `.last()`;
  - per-item mode sees `$json`, `$input.item` and `$itemIndex`;
  - user code may redeclare any root (`const items = $input.all()`);
  - return normalisation wraps plain objects;
  - invalid returns are named errors.
- [x] `$('Node').first()`, `.last()`, `.all()`, `.item`, `.itemMatching()` and `.params`, plus `$node`, `$workflow`, `$execution` (`id`, `mode`, `resumeUrl`), `$env`, `$vars`, `$now` and `$today` all return the same data an expression sees for the same item. `$jmespath` is a named error, as it is in expressions (amendment 8).
- [x] Top-level `await`, a returned Promise, and `this.helpers.httpRequest` all work, and HTTP goes through the tenant's egress policy and is counted against `MaxHostCalls`.
- [x] `console.log` output is captured into the node run (persisted and live), capped at `MaxConsoleBytes`, and visible in the editor.
- [x] Luxon: every probe in the spike's `LuxonProbes`, plus DST-transition cases, matches the committed Node goldens. `DateTime` defaults to the workflow timezone.
- [x] `require('crypto')` (the listed subset), `require('luxon')` and `require('lodash')` work. `require('fs')` and other unlisted modules are refused with the `unsupportedScript` sentence, both at import and at run.
- [x] `Buffer`, `TextEncoder`, `atob`/`btoa`, `URL`, `structuredClone`, `Object.groupBy`, `Intl.NumberFormat` (any CLDR locale) and en-US `Intl.DateTimeFormat` pass their golden tests. A non-English date locale is a named error, never English output.
      (en-CA and en-GB date formatting also ship, FEAT-9we7kw; every other date locale still refuses by name.)
- [x] A regex with `\p{…}`, the `v` flag, an async generator or `import`/`export` is refused at import and at validate. None of them ever runs silently wrong.
- [x] Limits:
  - `while(true)`, a loop in a promise job, and a catastrophic regex are each stopped within the deadline + 50 ms (regex: + `regexp2.DefaultMatchTimeout`), as `ErrTimeout`;
  - a runaway allocation is stopped by the watchdog as `ErrMemoryLimit`, the server process survives, and other executions continue;
  - the input and output caps are enforced as named errors.
- [x] **The time limit covers the user's program only.** A test with a 50 ms limit and a Luxon-using body passes under `-race` with `DefaultLimits`, proving that VM creation, library load and marshalling are not charged.
- [x] One fresh VM is used per node execution. A test proves that a global or prototype mutation in one execution is invisible to the next execution and to other tenants.
- [x] Errors report `[line N]`, or `[line N, for item I]` in per-item mode, mapped to the user's own line numbers, with a user-only stack.
- [x] The Sort node's `code` comparator runs (Phase 6).
- [x] The corpus scoreboard is committed as digests plus a baseline, never as fixtures: at least 97% of the top-500 JS Code nodes pass parse and analysis, and the runtime-error rate on synthesised input is recorded. CI fails on regression.
      (Baseline: parse 100%, parse+analysis 99.7%, run-without-error 74.8% of accepted / runtime-error 25.2%.)
- [x] The e2e scenario imports, activates and runs a Code-node workflow and asserts that **no Node.js process exists** at any point. The EPIC-m42s3g "no Node.js process anywhere" criterion holds with Code nodes present.
- [x] `go test ./...` passes, including `internal/guardrails`, where a new import-graph test forbids `internal/jsrun` from importing `sidecar`, `os/exec` or `net`.
      (Verified 2026-09-25 on main: `go test ./... -count=1` exit 0.)

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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (10):
  - `f02fbbd0` — chore(pine): EPIC-tjnr1z records where it stands and where to resume
  - `a6817131` — EPIC-tjnr1z: n8n's source may be read to learn what n8n does, never ported
  - `9af3a816` — merge: main brings the JavaScript Code node into the stabilisation sprint (EPIC-tjnr1z)
  - `804610c1` — merge: EPIC-tjnr1z JavaScript Code node (P0–P4), run in worker processes
  - `0e9f64be` — EPIC-tjnr1z: P2, P3, P4 and the worker pool close with their evidence; the epic records where it stands
  - `3913bca9` — EPIC-tjnr1z: whole-branch review fixes
  - `e03cea3a` — EPIC-tjnr1z: notes: the worker review, and the binary size with every lane in
  - `c2d231e8` — EPIC-tjnr1z: follow-ups from the P3 lanes: en-CA and en-GB dates, and unhandled rejections
  - `653f138c` — FEAT-rkj8ry: the JavaScript runtime gets its licence record, its vendored libraries and its import boundary
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |    4 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   15 +
 .pine/tickets/BUG-0xv7bg.md                        |   54 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  162 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   54 +
 .pine/tickets/BUG-2n4rfz.md                        |   51 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   41 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  163 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   59 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   52 +
 .pine/tickets/BUG-5bgx5c.md                        |   59 +
 .pine/tickets/BUG-5fhcx7.md                        |   26 +
 .pine/tickets/BUG-605n21.md                        |  131 +
 .pine/tickets/BUG-66fhea.md                        |   97 +
 .pine/tickets/BUG-6d6wbg.md                        |   98 +
 .pine/tickets/BUG-6gkd12.md                        |  107 +
 .pine/tickets/BUG-719gaz.md                        |   88 +
 .pine/tickets/BUG-9296bf.md                        |   53 +
 .pine/tickets/BUG-9dw5me.md                        |   53 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        |   61 +
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-asdh5q.md                        |   47 +
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        |  119 +
 .pine/tickets/BUG-b8bwhw.md                        |   55 +
 .pine/tickets/BUG-bcahaj.md                        |  100 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   47 +
 .pine/tickets/BUG-c73h98.md                        |   42 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   54 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-ecbq28.md                        |  111 +
 .pine/tickets/BUG-epy2se.md                        |  122 +
 .pine/tickets/BUG-fthahg.md                        |   58 +
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hmp85t.md                        |   99 +
 .pine/tickets/BUG-j7qrp2.md                        |   52 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |   45 +
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   35 +
 .pine/tickets/BUG-n6p7qy.md                        |  101 +
 .pine/tickets/BUG-n9a6bz.md                        |   54 +
 .pine/tickets/BUG-namghh.md                        |   48 +
 .pine/tickets/BUG-nbymq4.md                        |   52 +
 .pine/tickets/BUG-ngt25j.md                        |   52 +
 .pine/tickets/BUG-nn74ph.md                        |   51 +
 .pine/tickets/BUG-nzy3pa.md                        |  186 +
 .pine/tickets/BUG-p334yw.md                        |   53 +
 .pine/tickets/BUG-p3j233.md                        |   53 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   56 +
 .pine/tickets/BUG-ppvyzr.md                        |   58 +
 .pine/tickets/BUG-pzkpfr.md                        |   53 +
 .pine/tickets/BUG-q6b75c.md                        |   52 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  140 +
 .pine/tickets/BUG-rh7mpa.md                        |   52 +
 .pine/tickets/BUG-rs0xq1.md                        |   46 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   53 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |   94 +
 .pine/tickets/BUG-tpyg0q.md                        |   46 +
 .pine/tickets/BUG-txafja.md                        |   55 +
 .pine/tickets/BUG-v8ksv8.md                        |   49 +
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w9k234.md                        |   48 +
 .pine/tickets/BUG-x28fsx.md                        |  142 +
 .pine/tickets/BUG-x2sxzt.md                        |   42 +
 .pine/tickets/BUG-x6gyc1.md                        |   54 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        |   36 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   44 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       |  601 +++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   35 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   31 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   33 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   32 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4jhtny.md                       |   30 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   30 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   32 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   31 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   32 +
 .pine/tickets/FEAT-9ep5pw.md                       |   31 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |   38 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gzd32h.md                       |   31 +
 .pine/tickets/FEAT-hxztwz.md                       |   37 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   38 +
 .pine/tickets/FEAT-mh4e8g.md                       |   32 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   32 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   34 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   34 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qdwm1k.md                       |   45 +
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   35 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   32 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   31 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   32 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   32 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   32 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       |   99 +
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  150 +-
 CONTRIBUTING.md                                    |   22 +
 Makefile                                           |   35 +
 README.md                                          |  450 +--
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  115 +-
 config.example.yaml                                |   77 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/execution-model.md  |   17 +-
 .../src/content/docs/concepts/items-and-lineage.md |   16 +
 docs/src/content/docs/concepts/node-registry.md    |    5 +-
 .../src/content/docs/concepts/safety-boundaries.md |  157 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   22 +-
 docs/src/content/docs/concepts/webhooks.md         |   33 +
 docs/src/content/docs/guides/code-javascript.md    |  391 ++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |   27 +-
 docs/src/content/docs/guides/n8n-migration.md      |  139 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  147 +-
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/reference/api-contract.md    |   16 +-
 docs/src/content/docs/reference/api.md             |    2 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    2 +-
 docs/src/content/docs/reference/cli.md             |   62 +-
 .../content/docs/reference/expression-grammar.md   |    2 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   17 +-
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |    4 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/datastore.spec.ts                        |  103 +
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/js-code.spec.ts                          |  695 ++++
 e2e/tests/n8n-compare.spec.ts                      |   22 +-
 e2e/tests/node-coverage.spec.ts                    |   25 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/fromai.go                              |   30 +
 internal/ai/fromai_test.go                         |   37 +
 internal/ai/openai.go                              |   77 +-
 internal/ai/openai_test.go                         |   66 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   30 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    4 +-
 internal/api/handlers/credentials.go               |    5 +-
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |    9 +-
 internal/api/handlers/executions.go                |   54 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/oauth.go                     |   17 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/workflows.go                 |   29 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    4 +-
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  132 +-
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |   38 +-
 internal/cli/openapi_contract_test.go              |    8 +-
 internal/cli/verbs_api.go                          |   59 +-
 internal/cli/verbs_api_test.go                     |  256 ++
 internal/cli/verbs_credential_test.go              |   71 +
 internal/config/config.go                          |  172 +-
 internal/config/config_test.go                     |  142 +
 internal/credentials/credentials_test.go           |   26 +-
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    1 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   29 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |    1 +
 internal/engine/checkpoint.go                      |   27 +-
 internal/engine/console_test.go                    |  397 ++
 internal/engine/eval.go                            |    2 +-
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  114 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          |  691 +++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/service.go                         |   75 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  168 +-
 internal/engine/wait_service.go                    |    7 +-
 internal/engine/wait_service_test.go               |  492 ++-
 internal/execution/records.go                      |    7 +
 internal/expression/expression.go                  |   12 +
 internal/expression/expression_test.go             |   63 +
 internal/expression/globals.go                     |    9 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  150 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/n8n.go                        |   48 +-
 internal/interop/n8n/n8n_test.go                   |  537 ++-
 internal/interop/n8n/parameters.go                 |  246 +-
 internal/interop/n8n/rag_import_test.go            |   40 +-
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  260 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  211 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  562 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  331 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  245 ++
 internal/jsrun/js/runtime.js                       | 1707 +++++++++
 internal/jsrun/jsrun.go                            |  241 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  149 +
 internal/jsrun/roots_test.go                       |  580 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  208 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  234 ++
 internal/jsworker/doc.go                           |   75 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  905 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   51 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |    8 +-
 internal/nodepack/trigger.go                       |   75 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/repository/executions.go                  |   43 +-
 internal/repository/models.go                      |   11 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   11 +
 internal/repository/tenant_rows.go                 |    5 +-
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflows.go                   |   12 +-
 internal/routing/executor.go                       |    2 +-
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/tenantpurge/harness_test.go               |    5 +
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  563 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/webhook.go                        |   29 +-
 internal/workflow/compiler.go                      |    4 +
 internal/workflow/document.go                      |    9 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 nodes/ai.go                                        |   93 +-
 nodes/ai_test.go                                   |   59 +
 nodes/core.go                                      |    1 +
 nodes/datastore.go                                 |  718 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_tool_test.go                       | 1299 ++++++-
 nodes/embedscope.go                                |   36 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   36 +-
 nodes/jscode.go                                    |  271 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  192 +
 nodes/jscode_roots.go                              |   48 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  190 +
 nodes/jscode_test.go                               |   41 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/transform.go                                 |  109 +-
 packs/waha/waha_test.go                            |   55 +-
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 sdk/src/generated/models.ts                        |  279 +-
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |    2 +
 skills/kilasflow-datastore/SKILL.md                |    3 +-
 skills/kilasflow-datastore/references/FILTERS.md   |    2 +-
 skills/kilasflow-expressions/SKILL.md              |    5 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/editor.json                        |   20 +-
 web/messages/en/executions.json                    |    3 +
 web/messages/id/editor.json                        |   20 +-
 web/messages/id/executions.json                    |    3 +
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   11 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 web/src/lib/api/http.ts                            |   32 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../workflow-editor/workflow-editor.svelte         |   54 +-
 web/src/lib/embed/session.svelte.ts                |    6 +-
 web/src/lib/embed/session.test.ts                  |   47 +-
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   54 +-
 web/src/lib/workflow-editor/chat.ts                |   78 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   71 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   27 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |   70 +
 web/src/lib/workflow-editor/execution.ts           |   62 +-
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   36 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   61 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  181 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 611 files changed, 84890 insertions(+), 1391 deletions(-)
```
