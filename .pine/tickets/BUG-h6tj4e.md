---
id: BUG-h6tj4e
title: A script could call Go methods goja_nodejs exposes through reflection (Buffer's API handle, URL.String)
status: testing
priority: high
labels:
    - code-node
    - javascript
    - security
parent: EPIC-tjnr1z
created: "2026-09-24T14:56:23Z"
updated: "2026-09-24T14:56:23Z"
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
