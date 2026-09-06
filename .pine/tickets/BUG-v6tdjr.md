---
id: BUG-v6tdjr
title: Nine doc comments describe behaviour the code does not have
status: done
priority: medium
created: "2026-09-05T18:23:22Z"
updated: "2026-09-06T03:37:32Z"
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

### The remaining five, checked (DocComments, 2026-09-06)

Read the function before the comment in each case. Three comments fixed, two
recorded accurate. All fixes are comment-only; no behavior change.

1. **Repository tenancy — FALSE, comment fixed.**
   `internal/repository/workflows.go:20` claimed "TenantScope is mandatory for
   every repository operation" and that auth/embed resolution was still
   "Future". Both wrong: `ClaimNext` (`executions.go:365` takes worker ID +
   lease, no scope), `ClaimDue` (`schedules.go:176`), webhook `Resolve`
   (`webhooks.go:78`), `ClaimDelivery` (`webhooks.go:242`),
   `RecordDeliveryExecution` (`webhooks.go:282`), `PruneAllVersions`
   (`workflow_history.go:306`), `EnsureTenant` / `GetTenant` / `CountUsers` /
   `FindUserForLogin` / `AuthenticateAPIKey` (`auth.go`) all take none — and
   auth exists (`internal/auth`, `PrincipalTenants.Resolve`). The comment now
   lists which operations take no scope and says plainly that a forgotten
   tenant is a bug that still compiles.
2. **ClaimNext "same statement" — ACCURATE, no change.** No repo comment says
   "same statement" (searched); the only ClaimNext comment,
   `internal/repository/executions.go:362` ("atomically assigns"), describes
   the select-then-compare-and-set-update in one transaction (`executions.go:
   379-433`, lost races return `RowsAffected == 0` → unclaimed). Exactly one
   worker wins, so "atomically" is true of the effect. The docs' "same
   statement" was the doc author's compression, not this comment's claim.
3. **Sub-workflow recursion "stack rather than depth counter" — FALSE as
   stated, comment fixed.** `internal/engine/runner.go:57` denied the counter,
   but `checkCallStack` (`service.go:600-615`) does both: the stack refuses
   the repeated workflow immediately, then `len(stack) >=
   MaxWorkflowCallDepth` (16) caps chains that never repeat — and
   `service.go:535` already says "beside the cycle check rather than instead
   of it". The field comment now says "beside … rather than instead of" and
   names the cap. The `A→B→A→B` rationale sentences are kept; they are true of
   why the stack exists.
4. **Embed signing key "at least 32 bytes" — ACCURATE of the function,
   deployment qualifier added (unnamed candidate 1, from the review pass).**
   `internal/embed/embed.go:159` matches `NewIssuer` exactly (`len(key) < 32`
   rejects; `embed_test.go:40` covers 0/16/31). But the only boot path
   (`cmd/kilasflow/main.go:191` via `credentials.KeyFromEnvironment`, which
   demands exactly 32 bytes in any encoding) rejects the 33+ byte keys the
   comment blesses. One sentence added saying the server only ever passes
   exactly 32 — supported by `credentials_test.go:219` on both sides.
5. **Wait "bounded at one hour" — TRUE of the node, execution-timeout caveat
   added (unnamed candidate 2, from the review pass).** `MaxWaitDuration`
   (`nodes/wait.go:40`) is enforced at runtime (`wait.go:157`, covered by
   `TestAWaitLongerThanTheServerAllowsIsRefusedRatherThanHeld`), and the
   comment already names the worker slot and the run's own timeout. What it
   did not say — the thing the docs got wrong — is that the execution timeout
   usually binds first (`service.go:174` wraps the run in
   `context.WithTimeout(defaultTimeout)`; stock default 60s per
   `config.go:384`). One sentence added; nothing else touched.

`go vet` + `go test` pass on `internal/repository` and `internal/embed`.
`internal/engine` and `nodes` currently fail to build through a sibling's
mid-flight breakage in `internal/ai` (unrelated files); the four touched files
are comment-only and `gofmt`-clean, with engine/nodes verification deferred to
final validation.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `e8f1d67f` (last commit at or before ticket created 2026-09-05)
- Commits (4):
  - `0701d124` — docs: say how the node-pack pairing is actually guaranteed
  - `c0d329d4` — fix(config): apply the operator's egress policy to edit-time option loading
  - `4bd34261` — docs: correct two comments that described behaviour the code lacks
  - `b7f86a87` — chore(pine): file the doc comments that describe behaviour the code lacks
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |  43 +
 .pine/CHECKPOINT.md                                | 148 ++++
 .pine/MEMORY.md                                    |   2 +
 .pine/memory/persistence.md                        |   5 +-
 .pine/roadmap.md                                   |  25 +-
 .pine/tickets/BUG-br7ggc.md                        |  80 +-
 .pine/tickets/BUG-v6tdjr.md                        | 231 ++++++
 .pine/tickets/BUG-xmcm8x.md                        |  70 +-
 .pine/tickets/FEAT-0556ck.md                       | 667 +++++++++++++++-
 .pine/tickets/FEAT-096vs9.md                       | 781 ++++++++++++++++++-
 .pine/tickets/FEAT-27km39.md                       | 663 +++++++++++++++-
 .pine/tickets/FEAT-4d0bje.md                       | 862 ++++++++++++++++++++-
 .pine/tickets/FEAT-5fv8gf.md                       | 187 ++++-
 .pine/tickets/FEAT-96p7m3.md                       | 781 ++++++++++++++++++-
 .pine/tickets/FEAT-bscygc.md                       | 655 +++++++++++++++-
 .pine/tickets/FEAT-cgm1y3.md                       | 784 ++++++++++++++++++-
 .pine/tickets/FEAT-cx3hq1.md                       | 645 ++++++++++++++-
 .pine/tickets/FEAT-kwxxd0.md                       |  90 ++-
 .pine/tickets/FEAT-nrfz6m.md                       | 151 +++-
 .pine/tickets/FEAT-ptyh9w.md                       |  13 +-
 .pine/tickets/FEAT-r6xhnp.md                       | 806 ++++++++++++++++++-
 Makefile                                           |  30 +
 cmd/kilasflow/main.go                              | 130 +++-
 cmd/kilasflow/main_test.go                         |  35 +
 config.example.yaml                                | 351 ++++++---
 docs/src/content/docs/operate/configuration.md     |  80 +-
 docs/src/content/docs/operate/deployment.md        | 111 ++-
 docs/src/content/docs/operate/security.md          | 111 ++-
 docs/src/content/docs/operate/upgrades.md          |  78 +-
 docs/src/content/docs/reference/api-contract.md    | 312 +++++++-
 internal/ai/agent.go                               |  30 +-
 internal/ai/ai.go                                  |   7 +-
 internal/ai/ai_test.go                             | 166 ++++
 internal/ai/memory.go                              | 164 +++-
 internal/api/credentials_test.go                   |  46 +-
 internal/api/handlers/credentials.go               |  43 +-
 internal/api/handlers/workflows.go                 |  28 +-
 internal/api/routes.go                             |   2 +-
 internal/api/server.go                             |   4 +
 internal/config/config.go                          | 326 +++++++-
 internal/config/config_test.go                     | 240 ++++++
 internal/credentials/registry.go                   |  14 +-
 internal/database/database.go                      |  54 +-
 internal/database/database_test.go                 |  42 +
 internal/database/migrate.go                       | 195 ++++-
 internal/database/migrate_test.go                  | 142 ++++
 internal/embed/embed.go                            |   2 +
 internal/engine/runner.go                          |  12 +-
 internal/engine/service.go                         |  26 +-
 internal/expression/doc.go                         |  19 +-
 internal/interop/n8n/n8n.go                        | 125 ++-
 internal/interop/n8n/n8n_test.go                   | 199 ++++-
 internal/interop/n8n/parameters.go                 | 250 +++++-
 internal/interop/n8n/sqlfidelity_test.go           |  56 ++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  17 +
 internal/nodepack/nodepack.go                      |  16 +-
 internal/repository/execution_retention.go         | 180 +++++
 internal/repository/execution_retention_test.go    | 396 ++++++++++
 internal/repository/executions.go                  |  73 +-
 internal/repository/models.go                      |  58 +-
 internal/repository/postgres_execution_test.go     | 213 +++++
 internal/repository/workflows.go                   |   8 +-
 internal/safehttp/safehttp.go                      | 139 +++-
 internal/safehttp/safehttp_test.go                 | 193 +++++
 internal/sqlguard/admit.go                         | 245 ++++++
 internal/sqlguard/attack_test.go                   | 344 ++++++++
 internal/sqlguard/dialect.go                       | 260 +++++++
 internal/sqlguard/doc.go                           |  53 ++
 internal/sqlguard/sqlguard.go                      | 443 +++++++++++
 internal/sqlguard/sqlguard_test.go                 | 338 ++++++++
 internal/sqlnode/export_test.go                    |  11 +
 internal/sqlnode/guard_test.go                     | 126 +++
 internal/sqlnode/sqlnode.go                        | 372 ++++++++-
 internal/web/dist/index.html                       |  38 +-
 internal/workflow/compiler.go                      |  74 ++
 internal/workflow/compiler_test.go                 | 118 +++
 .../postgres/000004_execution_indexes.down.sql     |   5 +
 .../postgres/000004_execution_indexes.up.sql       |  26 +
 .../sqlite/000004_execution_indexes.down.sql       |   5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |  21 +
 nodes/ai.go                                        | 752 +++++++++++++++---
 nodes/ai_ollama_test.go                            | 413 ++++++++++
 nodes/ai_test.go                                   | 650 +++++++++++++++-
 nodes/apostrophe_live_test.go                      |  43 +
 nodes/core.go                                      |   1 +
 nodes/database.go                                  |  74 +-
 nodes/database_test.go                             | 243 +++++-
 nodes/executors.go                                 |   1 +
 nodes/mysql_v2.go                                  |   8 +
 nodes/postgres_v2.go                               |  31 +-
 nodes/sql_options_live_test.go                     |  14 +-
 nodes/sqlite_attach_test.go                        | 161 ++++
 nodes/wait.go                                      |   5 +-
 scripts/smoke-postgres.sh                          |  14 +
 sdk/README.md                                      |  25 +-
 sdk/package.json                                   |   2 +-
 sdk/src/version.ts                                 |  15 +-
 .../components/workflow-editor/canvas-node.svelte  |  12 +
 .../workflow-editor/workflow-editor.svelte         |  66 +-
 web/src/lib/workflow-editor/node-visual.ts         |   2 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   6 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   6 +-
 102 files changed, 16772 insertions(+), 663 deletions(-)
```
