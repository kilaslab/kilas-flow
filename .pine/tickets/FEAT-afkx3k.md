---
id: FEAT-afkx3k
title: 'JS Code runtime P7: Code-node compatibility corpus scoreboard and no-Node e2e'
status: doing
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p7
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Prove compatibility against real templates, and prove that no Node.js process exists.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 7* section. Read it before starting.

# Acceptance Criteria
- [ ] `scripts/code-corpus-sync.sh` fetches Code nodes from the top templates into a gitignored directory, pinned by digest in `MANIFEST.json`
- [ ] The scoreboard records the parse, analyser-accept and runtime-error rates plus median/p95 time in `BASELINE.md`; at least 97% of the top-500 JS Code nodes parse and pass analysis; CI fails on regression
- [ ] `make js-diff` (dev-only, needs Node) diffs jsrun against Node with a KilasFlow-authored harness
- [ ] The e2e imports, activates and runs a Code-node workflow (both modes, Luxon, console, `$('Node')`, Buffer, crypto) and asserts that no `node` process exists; it is wired into FEAT-5fhj6p

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 7*.

# Notes

## Plan (2026-09-24)

1. `scripts/code-corpus-sync.sh` (bash + python3, like `scripts/corpus-sync.sh`): with `--update` it ranks the N most-viewed templates (`/api/templates/search?sort=views:desc`, 100 a page) and re-pins; without it, it fetches exactly the template ids `MANIFEST.json` pins and verifies each digest. Each template that carries a JavaScript Code node or a Sort `code` comparator becomes one fixture `internal/jsrun/corpus/fixtures/<id>.json` holding only what a run needs: those nodes (name, type, typeVersion, parameters), the names of every node, the connections and the pinData. `meta`, view counts and the rest of the template are dropped, so a digest moves only when the template's code or data does.
2. `internal/jsrun/corpus`: a doc-only package plus tests. Everything that reads files lives in `_test.go` files, because the guardrail keeps every non-test file under `internal/jsrun/` away from `os`. The scoreboard analyses each body (parse, accept), runs the accepted ones in-process on `Runner.Run` with the shipped `DefaultLimits()`, on the upstream node's pinData or a stub shaped from the fields the body reads, and records per-node outcomes keyed by `<template id>/<node index>` (no user text is committed: no names, no code). `BASELINE.md` + `baseline.json`; the test skips with the sync command when fixtures are absent, fails on any node that moved without a baseline update, and fails if parse+analysis falls below 97%.
3. `make js-diff`: a gated Go test (`-js-diff`) hands the same bodies and inputs to `scripts/js-diff/harness.mjs`, a KilasFlow-authored roots harness on Node's `vm`, and diffs the returned items and errors. Bodies whose output differs between two Node runs (clocks, randomness) are classed nondeterministic, not drift.
4. E2E: one fixture (`e2e/fixtures/epic-code.ts`) imports an n8n workflow with both Code modes, Luxon, console, `$('Node').first()`, Buffer and crypto, activates it, triggers it through its webhook, asserts outputs and console, and samples the process table for the whole run. `e2e/tests/js-code.spec.ts` runs it on its own; proofs 2 and 4 of FEAT-5fhj6p (`e2e/fixtures/epic-proofs.ts`) run it on their live servers.

# Related Files

# Attachments
