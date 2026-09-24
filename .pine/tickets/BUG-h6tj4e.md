---
id: BUG-h6tj4e
title: A script could call Go methods goja_nodejs exposes through reflection (Buffer's API handle, URL.String)
status: done
priority: high
labels:
    - code-node
    - javascript
    - security
parent: EPIC-tjnr1z
created: "2026-09-24T14:56:23Z"
updated: "2026-09-24T15:20:39Z"
---

# Description

Found by the P8 security review (FEAT-vjjs8t). goja_nodejs hands two Go values
to the VM through reflection, and goja exposes their exported methods to any
script:

- `Buffer[Symbol(api)]` is goja_nodejs's `*buffer.Buffer`. A script finds the
  symbol with `Object.getOwnPropertySymbols(Buffer)` and can call its
  `WrapBytes` and `RequiredBufferArgument`. `WrapBytes({ length: 2 ** 27 })`
  converts the array-like into a Go byte slice in one call that no interrupt
  can stop: measured at 6.5 s past a 200 ms time limit, and any length a
  script names. It bypasses every per-call bound `boundAllocations` keeps.
- Every `new URL(...)` is goja_nodejs's `*nodeURL`, whose `String` method is
  an own property of the instance (`new URL('http://a').String()`).

In the server a script runs in a worker, whose kill deadline and address-space
limit end such a call, so this costs a worker, not the server. In process (the
tests, tools) nothing ends it. And it breaks the rule that host bindings are
plain functions and data, never Go structs through reflection.

# Fix

Every VM gets a goja field-name mapper that hides every Go field and method.
goja_nodejs still finds its own values by exporting the objects, which the
mapper does not affect; a script sees each as an opaque handle with nothing on
it. `TestNoGoFieldOrMethodIsReachable` walks everything a script can reach and
fails on any Go value with a member a script can read or call.

# Acceptance Criteria
- [x] No Go field or method is reachable from a script, proved over a walk of everything a script can reach

# Notes

## Progress (2026-09-24)

Fixed in the FEAT-vjjs8t branch (epic/p8-docs): `newVM` sets
`rt.SetFieldNameMapper(noGoMembers{})` (`internal/jsrun/engine.go`).

- RED: `go test ./internal/jsrun -run TestNoGoFieldOrMethodIsReachable` failed
  with `Buffer.[Symbol(api)] is a *buffer.Buffer whose Go members
  [RequiredBufferArgument WrapBytes] a script can reach` and `sample.url is a
  *url.nodeURL whose Go members [String] a script can reach`, in both modes.
- GREEN after the mapper; `TestGoHandlesOfferNothingToCall` pins the
  user-visible side (`typeof handle.WrapBytes`, `typeof url.String` are
  `undefined`, URL and Buffer still work). The whole jsrun suite passes.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `a2cfc058` (last commit at or before ticket created 2026-09-24)
- Commits (1):
  - `9f087778` — BUG-h6tj4e: no Go field or method goja_nodejs keeps behind Buffer or URL is reachable from a script
- Files changed (the ticket's own commits, a2cfc0586c85d374c737925e0f3b2de0dca48425..4e9037c1a941e5cdeddd8dca9452c31db9adc4dd):

```
 .pine/tickets/BUG-h6tj4e.md                              |  59 +++++
 .pine/tickets/FEAT-vjjs8t.md                             | 266 ++++++++++++++++++-
 CHANGELOG.md                                             |  18 ++
 Makefile                                                 |  11 +
 config.example.yaml                                      |   2 +-
 docs/src/content/docs/concepts/safety-boundaries.md      |  46 ++--
 docs/src/content/docs/guides/code-javascript.md          | 345 ++++++++++++++++++++++++
 docs/src/content/docs/guides/community-nodes.md          |   3 +-
 docs/src/content/docs/guides/n8n-migration.md            | 210 ++-------------
 docs/src/content/docs/operate/configuration-reference.md |   2 +-
 docs/src/content/docs/operate/deployment.md              |   4 +-
 docs/src/content/docs/start/what-kilasflow-is.md         |   2 +-
 internal/config/config.go                                |   2 +-
 internal/jsrun/engine.go                                 |  22 ++
 internal/jsrun/jsrun.go                                  |   4 +-
 internal/jsrun/run.go                                    |   3 +
 internal/jsrun/security_test.go                          | 271 +++++++++++++++++++
 internal/jsrun/surface_internal_test.go                  | 488 ++++++++++++++++++++++++++++++++++
 internal/jsrun/testdata/surface.txt                      | 497 +++++++++++++++++++++++++++++++++++
 internal/jsworker/load_test.go                           | 124 +++++++++
 internal/jsworker/security_test.go                       |  51 ++++
 21 files changed, 2209 insertions(+), 221 deletions(-)
```
