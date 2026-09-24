---
id: FEAT-mammrz
title: 'JS Code runtime P6: Sort node code comparator on the JS runtime'
status: doing
priority: low
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p6
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Sort's `code` comparator is the other JavaScript escape hatch. It runs on the same runtime.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 6* section. Read it before starting.

# Acceptance Criteria
- [ ] `sortToKilas` carries `type: code`; the executor compiles the comparator once per node run and sorts under the same limits
- [ ] The test asserting that both escape hatches refuse in the same words now asserts that Python and unsupported constructs still share one sentence

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 6*.

# Notes

## Plan (2026-09-24)

1. jsrun: a third run mode, `ModeComparator` ("sortComparator", not an n8n Code-node mode). Its wrapper is `(function (items, $input) { return function (a, b) {` + body + `}; })`, one prelude line as before, so user lines map the same way. `checkShape` accepts that shape (a plain, non-async inner function); the analyser runs on it unchanged, plus one comparator-only fact: the lines of the comparator's own `return` statements (`Analysis.Returns`).
2. jsrun: `Execute` runs a comparator job as one charged VM entry: the runtime sorts the input's indexes with the captured `Array.prototype.sort` (goja's is `sort.Stable`, so ties keep their order), calling the comparator on the items; a result that is not a number (or is NaN) stops the sort with `ErrInvalidReturn`, naming what came back, the two items, and the return's line when there is exactly one. The output is the order as JSON. `Finish` checks it is a permutation of the input (a worker is trusted only as far as its code could go) and returns it as `Result.Order`. No jsworker change: a worker runs whatever `Execute` runs.
3. nodes: the Sort node gains `type: code` and `code` (n8n's own names), shown when the type is code; its executor runs the comparator on the deployment's runtime (the same engine and limits as the Code node), reorders the Go items by the order (binaries and lineage untouched), and refuses with `JavaScriptDisabled` when the runtime is off. Validation analyses the comparator. The node becomes WholeBatch, since a comparator split into one-item calls would sort nothing.
4. interop/n8n: `sortToKilas` carries `type: code` and `code` raw (never read as an expression), and flags only what the analyser refuses (the one sentence) or what does not parse; `sortToN8N` writes them back byte for byte.
5. Tests: jsrun (numbers, strings, stable ties, `items`, throws with line, non-number, NaN, unsupported, time limit, a lying worker's order), jsworker (a comparator job in a worker), nodes (executor, errors, disabled runtime, validation), importer (round trip, refusal, parity).

## What n8n's Sort code mode does (observed 2026-09-24)

Black-box, on the official n8n 2.33.7 Docker image: 25 one-node workflows (a Code node seeding five items, one with a file, then a Sort of type `code`), each run with `n8n execute`. Recorded in my own words:

- The parameter is `code` beside `type: "code"`. The body runs as the inside of a comparator given to the list's own sort, so `a` and `b` are whole items (`json`, `pairedItem`, and `binary` when the item has one), and `items` is the whole list. None of the Code node's `$` globals (`$input`, `$json`, `$`, `$node`, `$workflow`, `$execution`), `require` or `DateTime` exist there; `console` does.
- It is a plain function: `await` in it is a SyntaxError. A throw fails the node with the message; the line n8n reports is always 1 (its wrapper is one line).
- Before running, n8n refuses a body in which the word `return` does not appear at all ("doesn't return"). A body with the word only in a comment passes that check and returns undefined.
- What the comparator returns goes through JavaScript's number conversion: a boolean, a numeric string, null and undefined all sort by the number they convert to (undefined, `'x'`, `{}` and NaN all count as a tie); a BigInt fails with a conversion TypeError.
- Ties keep their input order (V8's sort is stable). Strings compared with `<` order by code unit (`Zebra` before `apple`).
- The output items are the input items moved: files and pairedItem kept. A change the comparator makes to `a.json` is kept on the output item.

## Decisions (2026-09-24)

- **Roots.** The comparator's wrapper takes `items` and `$input`, the all-items roots, and the runtime installs the rest of the Code node's globals as for any job. That is a superset of what n8n offers, which no n8n comparator can notice. `a` and `b` are the items as a Code node sees them (`json`, and `binary` metadata); n8n's `pairedItem` on them is not offered.
- **A non-number is a named error, NaN included**, as the brief says, although n8n converts. A comparator that is not consistent (a boolean one is the common case) orders items in whatever way the engine's sort algorithm happens to visit them, and goja's (Go's `sort.Stable`) is not V8's, so converting would sort the same workflow differently with nothing to say so. The error names what came back, the two items, and the line when the comparator has exactly one `return` of its own (found by the analyser, `Analysis.Returns`); with several, which one answered cannot be told.
- **Mutations are not kept**, as the brief says: the runtime returns the order and Go moves the node's own items, which keeps files and lineage without the worker ever handling them.
- **No return at all** is a validation error (the analyser counts the comparator's own returns), n8n's up-front check done structurally; the importer flags only refusals and parse errors, as it does for a Code node.
- **The Sort node is WholeBatch now.** Split into one-item calls under continue-on-fail, any sort would order nothing; the comparator made that impossible to leave as it was.
- **A Code node cannot select the comparator mode**: validation already refuses an unknown mode, and the Code executor now refuses one too.
- **No jsworker change.** A worker runs whatever `jsrun.Runner.Execute` runs; the comparator is a mode of the job, and `Job.Finish` checks the order in the server.

# Related Files

# Attachments
