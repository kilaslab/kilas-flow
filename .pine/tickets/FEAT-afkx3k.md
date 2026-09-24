---
id: FEAT-afkx3k
title: 'JS Code runtime P7: Code-node compatibility corpus scoreboard and no-Node e2e'
status: done
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p7
created: "2026-09-23T01:33:22Z"
updated: "2026-09-24T14:36:11Z"
---

# Description

Prove compatibility against real templates, and prove that no Node.js process exists.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 7* section. Read it before starting.

# Acceptance Criteria
- [x] `scripts/code-corpus-sync.sh` fetches Code nodes from the top templates into a gitignored directory, pinned by digest in `MANIFEST.json`
- [x] The scoreboard records the parse, analyser-accept and runtime-error rates plus median/p95 time in `BASELINE.md`; at least 97% of the top-500 JS Code nodes parse and pass analysis; CI fails on regression
- [x] `make js-diff` (dev-only, needs Node) diffs jsrun against Node with a KilasFlow-authored harness
- [x] The e2e imports, activates and runs a Code-node workflow (both modes, Luxon, console, `$('Node')`, Buffer, crypto) and asserts that no `node` process exists; it is wired into FEAT-5fhj6p

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 7*.

# Notes

## Plan (2026-09-24)

1. `scripts/code-corpus-sync.sh` (bash + python3, like `scripts/corpus-sync.sh`): with `--update` it ranks the N most-viewed templates (`/api/templates/search?sort=views:desc`, 100 a page) and re-pins; without it, it fetches exactly the template ids `MANIFEST.json` pins and verifies each digest. Each template that carries a JavaScript Code node or a Sort `code` comparator becomes one fixture `internal/jsrun/corpus/fixtures/<id>.json` holding only what a run needs: those nodes (name, type, typeVersion, parameters), the names of every node, the connections and the pinData. `meta`, view counts and the rest of the template are dropped, so a digest moves only when the template's code or data does.
2. `internal/jsrun/corpus`: a doc-only package plus tests. Everything that reads files lives in `_test.go` files, because the guardrail keeps every non-test file under `internal/jsrun/` away from `os`. The scoreboard analyses each body (parse, accept), runs the accepted ones in-process on `Runner.Run` with the shipped `DefaultLimits()`, on the upstream node's pinData or a stub shaped from the fields the body reads, and records per-node outcomes keyed by `<template id>/<node index>` (no user text is committed: no names, no code; module names excepted, since the analyser's refusal of an unshipped module quotes it). `BASELINE.md` + `baseline.json`; the test skips with the sync command when fixtures are absent, fails on any node that moved without a baseline update, and fails if parse+analysis falls below 97%.
3. `make js-diff`: a gated Go test (`-js-diff`) hands the same bodies and inputs to `scripts/js-diff/harness.mjs`, a KilasFlow-authored roots harness on Node's `vm`, and diffs the returned items and errors. Bodies whose output differs between two Node runs (clocks, randomness) are classed nondeterministic, not drift.
4. E2E: one fixture (`e2e/fixtures/epic-code.ts`) imports an n8n workflow with both Code modes, Luxon, console, `$('Node').first()`, Buffer and crypto, activates it, triggers it through its webhook, asserts outputs and console, and samples the process table for the whole run. `e2e/tests/js-code.spec.ts` runs it on its own; proofs 2 and 4 of FEAT-5fhj6p (`e2e/fixtures/epic-proofs.ts`) run it on their live servers.

## Progress (2026-09-24)

Done; ready for review.

- **Corpus.** `scripts/code-corpus-sync.sh --update` ranked the 500 most-viewed templates on 2026-09-24: 151 carry JavaScript Code nodes (371 bodies, 51 per-item, 0 Sort code comparators); 7 Python Code nodes are counted and not measured. Plain `scripts/code-corpus-sync.sh` (`make js-corpus`) fetches exactly the pinned ids and verifies every digest; exit 3 means the API could not be reached, exit 1 that a template changed or was withdrawn.
- **Baseline** (`internal/jsrun/corpus/BASELINE.md`, on main 1c51601 with FEAT-x9gq0s merged): 41 bodies have no code in the template (n8n's `jsCode` default is `''`, which fails in n8n too; not measured). Of the 330 measured: parse 329 (99.7%), parse + analysis 328 (**99.4%**, floor 97%), run without error on synthesised input 221 (runtime-error rate 32.6% of accepted, mostly stand-in data: every body ran on a stand-in item because no template pinned data upstream of its code). Median run 0.6–1.3 ms, p95 1.8–3.5 ms depending on machine load (recorded, never compared).
- **What FEAT-x9gq0s unblocked.** Before it merged, 11 bodies were refused by the analyser only for `$getWorkflowStaticData` (9) or `this.helpers` (2), and parse + analysis was 96.1%, below the floor. After it, all 11 are accepted; 7 run clean, 2 fail on stand-in data or on static data another node was meant to have written, and the 2 helper bodies stop at `this.helpers.httpRequest` / `prepareBinaryData`, which the instrument deliberately does not wire (no network, no file store).
- **What FEAT-9we7kw (en-CA/en-GB) will unblock**: 1 body (3363/18) fails only on `date formatting in locale en-CA`; none on en-GB. 5449/15 fails on `id-ID`, which stays a named error. FEAT-9we7kw had not merged when this was written; regenerate with `make js-corpus-baseline` after it does.
- **`make js-diff`** (Node v24.16.0, 332 bodies: 328 accepted corpus bodies, 1 corpus body only V8 parses, 3 controls): same 217, both failed 104 (almost all on stand-in data; the error *messages* differ in wording), failed only under jsrun 3 (en-CA, id-ID, an inline base64 binary), nondeterministic under Node 7 (clocks, randomness), parses only under Node 1 (an HTML-like `<!--` comment), **different output 0**. 2 bodies printed a caught JSON.parse message differently. The control bodies come out the same.
- **Bugs filed** (children of EPIC-tjnr1z): BUG-pdsydm (`$json`/`$binary`/`$itemIndex` in all-items mode read item 0 in n8n; 14 corpus bodies), BUG-kvpx6x (legacy `$items()`; 5 bodies), BUG-djp647 (inline base64 binary returned by code; 1 body), BUG-jwhj6y (built-in error wording is goja's/Go's, not V8's), BUG-3mem9s (HTML-like comments are a syntax error under goja; 1 body).
- **E2E.** `e2e/fixtures/epic-code.ts` imports an n8n workflow (webhook → all-items Code with Luxon and console → per-item Code with `$('Orders').first()`, Buffer, crypto, a timer and console → all-items Code → Respond to Webhook), activates it, delivers to its webhook, asserts the answer, every Code node's output and console, and samples the process table every ~100 ms for the whole run. `js-code.spec.ts` runs it with a whole-table sampler (nothing Node under the server; nothing Node that appeared during the run unless one of the harness's own pre-existing Node processes started it) plus a self-test proving the sampler reports a stray Node process. Epic proofs 2 and 4 run it on their live servers through `host.assertNoNode`, so it holds on the image host as well.

### Decisions

- **Fixture shape.** Only what a run needs is kept (the code nodes, every node's name, connections, pinData), so a digest moves when the code or data does, not when a view count does.
- **Nothing user-authored is committed, module names excepted.** The baseline names a body `<template id>/<node position>` and records outcomes, reason classes and the analyser's own refusal subjects; a test proves an author's identifier never reaches `baseline.json` or `BASELINE.md`. The exception is the module name the analyser's refusal quotes (`requires the module "youtube-transcript"`).
- **Files are read only in `_test.go` files.** The guardrail keeps every non-test file under `internal/jsrun/` away from `os`, and the package is a doc-only package plus tests.
- **The instrument runs in-process** on `Runner.Run` with `DefaultLimits()`, with no helpers wired (no network, no files), and fails on any body that moved, better or worse, like the importer corpus.
- **CI.** Unlike the importer corpus, this one comes from the public template API, so `ci.yml` fetches it on every run and holds the runtime to the baseline; an unreachable API warns and falls back to the control bodies.
- **js-diff harness** is KilasFlow's own (`scripts/js-diff/harness.mjs`): the runtime's wrapper, roots and return rules written from `internal/jsrun`, the vendored Luxon and lodash, each case in a worker thread that is ended at its limit, and each case run twice to tell nondeterminism from drift.

## Fix round 1 (2026-09-24)

- **CI cannot go red for a third party's edit.** `ci.yml` syncs with `--lenient`: a pinned template that changed, lost its code or was withdrawn is a `::warning::`, its fixture is dropped, and `verified.json` (written beside the fixtures by every sync) lists what verified. The scoreboard holds only verified templates to the baseline and reports the rest as "not compared", never "vanished" (`compareBodies`, tested for verified / unverified / absent). `make js-corpus` stays strict (exit 1), and the new weekly `.github/workflows/code-corpus.yml` runs the strict sync and fails loudly so someone re-pins. `-update-baseline` refuses while any pin has drifted.
- **Merge-order rule.** Any change to `internal/jsrun` (or the importer's Code-node mapping) that moves a corpus outcome regenerates the baseline in the same change: `make js-corpus && make js-corpus-baseline`. Recorded in `.pine/memory/code-node.md`, with a second bullet that `jsdiff_test.go`'s os/exec and file I/O under `internal/jsrun/` are test-only.
- **Sampler.** A Node process that appeared during the run is reported only when its first already-running ancestor is the server or pid 1 (an orphan, as a daemonised child would be); anything started by another pre-existing process (the harness, another agent) is left alone. The self-test proves both halves. The sampling loop's errors are caught and rethrown after the delivery.
- **Stand-in stub.** A field read through an array method (`.map(`, `.filter(`, `.length`, …) is `[{}]`; one read through is `{}`. Rebaselined (on main 08ef90d): ran 221 → 224, runtime-error rate 32.6% → 31.7% (107 → 104); js-diff same 217 → 220, both failed 104 → 101, different output still 0.
- **Wording.** "No user content" is now "module names excepted" in `doc.go`, `BASELINE.md` and here.

# Related Files

- `scripts/code-corpus-sync.sh`, `internal/jsrun/corpus/` (`MANIFEST.json`, `BASELINE.md`, `baseline.json`, `doc.go`, `*_test.go`, `testdata/control/`)
- `scripts/js-diff/harness.mjs`, `Makefile` (`js-corpus`, `js-corpus-baseline`, `js-corpus-check`, `js-diff`), `.github/workflows/ci.yml`, `.github/workflows/code-corpus.yml`
- `e2e/fixtures/epic-code.ts`, `e2e/fixtures/epic-proofs.ts`, `e2e/tests/js-code.spec.ts`

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (6):
  - `b0f58868` — merge: FEAT-afkx3k the Code-node compatibility corpus, make js-diff, and the no-Node e2e
  - `0e380645` — FEAT-afkx3k: a template drifted upstream warns in CI and fails only the weekly strict check, the stand-in reads lists as lists, and the no-Node sampler blames only the server's own or orphaned processes
  - `922dd859` — FEAT-afkx3k: the ticket moves to testing with the corpus numbers, the js-diff summary and the decisions
  - `1cae9230` — FEAT-afkx3k: an imported workflow's Code nodes run end to end with no Node.js process sampled at any point, in js-code and in epic proofs 2 and 4
  - `8f8aee76` — FEAT-afkx3k: the corpus baseline is recorded and held in CI, and make js-diff diffs jsrun against Node with a KilasFlow-authored harness
  - `517ae56f` — FEAT-afkx3k: the Code-node corpus is fetched from the top templates, pinned by digest and scored against the runtime
- Files changed (the ticket's own commits, 08ef90d62b12c973722a807b1128ea8ba669056e..b0f588689289ce58f7ef99b3b71719b5403b206e):

```
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .pine/memory/code-node.md                          |    2 +
 .pine/tickets/BUG-3mem9s.md                        |   39 +
 .pine/tickets/BUG-djp647.md                        |   40 +
 .pine/tickets/BUG-jwhj6y.md                        |   34 +
 .pine/tickets/BUG-kvpx6x.md                        |   38 +
 .pine/tickets/BUG-pdsydm.md                        |   37 +
 .pine/tickets/FEAT-afkx3k.md                       |   50 +-
 Makefile                                           |   24 +
 e2e/fixtures/epic-code.ts                          |  271 ++++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/tests/js-code.spec.ts                          |   76 +-
 internal/jsrun/corpus/BASELINE.md                  |  413 +++++
 internal/jsrun/corpus/MANIFEST.json                |  769 +++++++++
 internal/jsrun/corpus/baseline.json                | 3485 ++++++++++++++++++++++++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 +++++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  308 ++++
 internal/jsrun/corpus/scoreboard_test.go           |  704 ++++++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 ++
 scripts/code-corpus-sync.sh                        |  312 ++++
 scripts/js-diff/harness.mjs                        |  298 ++++
 23 files changed, 7520 insertions(+), 43 deletions(-)
```
