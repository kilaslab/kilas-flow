---
id: BUG-v6tdjr
title: Nine doc comments describe behaviour the code does not have
status: todo
priority: medium
created: "2026-09-05T18:23:22Z"
updated: "2026-09-05T18:23:22Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Scope

Doc comments in this repository are load-bearing. They are where the *why*
lives, and a reader who trusts one is doing what the house style asks of them.
Nine of them describe behaviour the code beneath does not have.

They were found while writing the architecture documentation (FEAT-5mvech). The
agent writing it trusted the comments over the functions and shipped **twelve**
false statements into the docs as a result — caught by its own review pass
before merge, but only because it ran one. That is the cost these comments
carry: not confusion, but confident documentation of a system that does not
exist.

Two are verified here rather than taken on report:

- `internal/expression/doc.go:74` says `$('Name').item` "reads the paired-item
  lineage the runner tracks". It does not walk a lineage. It requires the
  *named* node to have produced exactly one item, and otherwise fails with
  `node "Many" produced 3 items; use .all(), .first() or .last() to choose one`
  (`internal/expression/expression_test.go:253`). Nothing follows the current
  item's chain. The comment describes a feature, not a limitation.
- `internal/credentials/registry.go:103` says the registry "is assembled at
  composition and read-only afterwards … rather than a package-level map".
  `registry.go:278` is `var defaultRegistry = func() *Registry { … }` — a
  package-level singleton, which is the arrangement the comment argues against.

The remaining seven were reported by the same review and are listed below as
**candidates to verify, not as findings**. The whole point of this ticket is
that a claim about this code has to be checked against the code, and that
applies to the report too.

## Candidates

- `internal/routing/doc.go` and `nodepack.Register` — both claim a routing
  description is checked at registration. The invariant is said to be
  structural rather than asserted.
- `cmd/kilasflow/main.go:115` — claims option loading reaches a service through
  "the same egress policy an HTTP node uses", on the line above a
  `safehttp.DefaultPolicy()` call.
- Repository tenancy: something claims every repository operation takes a
  `TenantScope` and that forgetting one does not compile. `ClaimNext`,
  `ClaimDue`, the webhook `Resolve`, `ClaimDelivery`, `PruneAllVersions` and
  `EnsureTenant` take none, several on the hot path.
- `ClaimNext` described as doing its work "in the same statement" when it is a
  select followed by a compare-and-set update inside one transaction.
- Sub-workflow recursion described as "a stack rather than a depth counter"
  when both exist and the code's own comment says "beside the cycle check
  rather than instead of it".

## Acceptance criteria

- [ ] Every comment listed above is either corrected or the code is changed to
      match it, and the choice is stated in the commit body for each — some of
      these describe the behaviour somebody *intended*, and the right fix may be
      the code.
- [ ] The two verified entries are fixed; the seven candidates are each checked
      against the code and the result recorded, including "this one was
      accurate" where that is the answer.
- [ ] No new comment is added that a test does not or could not support.

## Implementation Plan

Read the function before the comment, not after. That inversion is the entire
lesson here: every one of these was written by somebody who knew what the code
was supposed to do.

Prefer fixing the comment to fixing the code unless the comment describes
something a caller could reasonably depend on — `$('Name').item` walking a real
lineage is a feature worth having, and if it is wanted the ticket for it should
say so rather than a comment implying it already exists.

Reject the idea of a lint rule for this. There is no mechanical check for "this
sentence is true of that function", and adding one that checks something weaker
would create the same problem one level up.

## References

- FEAT-5mvech's `## Review pass` section, which records all twelve documentation
  errors and which comment produced each.
- `internal/expression/doc.go`, `internal/credentials/registry.go` — the two
  verified here.
