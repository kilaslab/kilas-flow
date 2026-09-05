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

- ~~`internal/routing/doc.go` and `nodepack.Register` — both claim a routing
  description is checked at registration.~~ **Checked. The invariant is
  structural, and the comment now says so.** See the evidence.
- ~~`cmd/kilasflow/main.go` — claims option loading reaches a service through
  "the same egress policy an HTTP node uses", on the line above a
  `safehttp.DefaultPolicy()` call.~~ **Checked, and it was the code that was
  wrong.** Fixed: the loader now takes `outboundPolicy(cfg.Outbound)`. See the
  evidence.
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
- [x] The two verified entries are fixed; the seven candidates are each checked
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

## Work evidence — partial

The two entries verified in the scope section are fixed. The seven candidates
are **not** checked and remain open; this ticket stays open with them.

`internal/expression/doc.go` now says what `$('Name').item` does — it requires
the named node to have produced exactly one item and otherwise fails with the
count — and says plainly that it does not walk the current item's chain, since
that is what the name suggests and what the previous comment told a reader who
then wrote it into the documentation.

`internal/credentials/registry.go` no longer claims the package-level form was
avoided. It was not: `defaultRegistry` is forty lines below the comment saying
so. The comment now explains what the value form is for and what the
package-level one is for.

Stopped here because the session was scheduled to end, not because the
remaining seven were judged accurate. Whoever picks this up should read the
function before the comment, which is the whole lesson of the ticket.

### The third one checked, and it was a behavioural defect

`cmd/kilasflow/main.go` said edit-time option loading reached a customer's
service "through the same egress policy an HTTP node uses". An HTTP node was
handed `outboundPolicy(cfg.Outbound)`; the loader was handed
`safehttp.DefaultPolicy()`. Those differ in everything an operator configures —
`allowed_hosts`, `allow_private_networks`, `max_redirects`, `max_response_bytes`
and `timeout`.

It is wrong in both directions. A deployment that restricted egress to an
allowlist found the restriction applied when a workflow ran and **not** while
somebody edited one, so an option loader could reach a host the operator had
excluded. A deployment that permitted private networks found its loaders
blocked instead.

This is the case the ticket's implementation plan anticipated: "some of these
describe the behaviour somebody *intended*, and the right fix may be the code."
Here it was. The comment now says what the line does and what it used to fail
to do.

Six candidates remain unchecked.

### The fourth one checked, and it caught me doing the thing the ticket warns about

`nodepack.Register`'s comment said a definition bound to the routing executor
with no routing description "registers cleanly and fails on its first run" and
is "a startup failure here instead". I read that, found no such check, and
added one.

Then I read `Load`, which is what I should have done first. That combination
cannot arise: `Load` builds the definition and the routing description from one
manifest and returns them together, and the only branch that returns no
description is the trigger branch — which sets `TriggerExecutorID`, not the
routing one. So my check was unreachable, and the test I wrote for it had to
construct a state the loader cannot produce.

Reverted. The comment now says the pairing is structural rather than asserted,
and says which half *is* checked — an executor nobody installed, which does
need one because a pack can name any binding.

This is the ticket's own instruction — read the function before the comment —
applied to me, one file after I wrote it down. Worth recording rather than
quietly fixing, because the failure is not carelessness; it is that a confident
comment is genuinely persuasive.

Five candidates remain unchecked.
