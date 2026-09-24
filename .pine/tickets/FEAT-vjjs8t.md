---
id: FEAT-vjjs8t
title: 'JS Code runtime P8: docs page, security review, load test'
status: doing
priority: medium
labels:
    - code-node
    - javascript
deps:
    - FEAT-afkx3k
parent: EPIC-tjnr1z
phase: p8
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Documentation and hardening before the runtime is declared shipped.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 8* section. Read it before starting.

# Acceptance Criteria
- [ ] A docs page "Code (JavaScript)" covers what runs, the differences from n8n, the configuration keys and the limits
- [ ] A security review covers prototype pollution confined to one execution, host bindings exposing plain functions and data only, and an enumeration test of the globals
- [ ] p99 latency under concurrent Code-node load is measured and recorded

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 8*.

# Notes

## Plan (2026-09-24)

1. **Security review, test first.** A new `internal/jsrun/security_test.go`
   (and one pool test in `internal/jsworker`) backs each claim:
   - prototype pollution (every built-in prototype, the global object, the
     Symbol registry, a library's module object, Luxon's Settings) is gone in
     the next execution, in process and in a worker process reused by another
     tenant's job; within one execution it cannot make a result name a file it
     was not given;
   - a walk of everything a script can reach (globals, prototypes, accessors,
     symbol-keyed properties, and sample instances of every host-made object)
     finds no Go value exposed through reflection, and the surface the runtime
     adds over bare goja matches a reviewed list in
     `internal/jsrun/testdata/surface.txt`, so an unreviewed property fails;
   - eval / new Function / Function.prototype.constructor / generator and
     async constructors reach only the same global object; no `process`,
     `module`, `global`-style Node handles; `Error.prepareStackTrace` is never
     called and stacks are strings; Proxy-wrapped arguments to host functions
     are treated as data.
   - The worker protocol's hostility tests already exist
     (`internal/jsworker/jsworker_test.go` `lyingWorker`): referenced, not
     duplicated.
2. **Load test.** `BenchmarkCodeNodeUnderLoad` in `internal/jsworker`, through
   the real worker pool (the test binary is its own worker), for a trivial body
   and a 1000-item transform at several concurrency levels, reporting
   p50/p95/p99 and executions per second; `make js-load` runs it. Results and
   machine details (with load average) recorded here.
3. **Docs.** A new page `guides/code-javascript.md`, "Code (JavaScript)", that
   holds what the migration guide's Code-node section held, plus the
   configuration keys and limits; the migration guide, safety boundaries and
   other pages link to it instead of repeating it.
4. **CHANGELOG** `[Unreleased]` entry for the JavaScript Code node.

# Related Files

# Attachments
