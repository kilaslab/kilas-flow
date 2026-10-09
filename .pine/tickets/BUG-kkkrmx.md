---
id: BUG-kkkrmx
title: 'e2e node-coverage is red on main: two specs still expect the pre-BUG-c7s5ss node id in errors and a run record for a sticky note'
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:02:11Z"
updated: "2026-10-09T12:22:03Z"
---

# Description

Two e2e specs in `e2e/tests/node-coverage.spec.ts` are red on main because their expectations predate BUG-c7s5ss (commit dd0f9fb4). That commit made a run's error name the failing node by its **name** rather than its id, and stopped listing a sticky note among a run's nodes. Both are intended behaviour, so the specs are stale, not the runner.

1. "error workflow pair": `e2e/fixtures/error-form-nodes.ts` (~line 125) expects `execute node "stop": …` (the node id), while `internal/engine/runner.go` (~1897) formats `execute node %q` with `node.Name`.
2. "a sticky note saves and runs without affecting the result": `node-coverage.spec.ts` (~line 684) requires `nodeRun(record, 'note').status` to be `succeeded`, but a sticky note no longer gets a run record.

Found by the Track F coder and confirmed by its reviewer in the 2026-10-09 parallel wave.

# Steps to Reproduce

1. `cd e2e && pnpm install --frozen-lockfile && pnpm exec playwright test tests/node-coverage.spec.ts`

# Expected

Both specs pass and assert the current behaviour.

# Actual

Both fail on main.

# Acceptance Criteria
- [x] The error-workflow spec expects the failing node's name (as the runner reports it), not its id. Evidence: `e2e/fixtures/error-form-nodes.ts` now expects `execute node "Stop": <message>`; before the change the spec failed with `Expected "execute node \"stop\"…" Received "execute node \"Stop\"…"`.
- [x] The sticky-note spec asserts the note has no run record and the run's result is unaffected. Evidence: `node-coverage.spec.ts` "a sticky note saves and runs without affecting the result" asserts no `nodeRuns` entry for `note` (and `runToSuccess` plus the `out` item assertion keep the result checks); before the change it failed with `node note has a recorded run`.
- [x] `playwright test tests/node-coverage.spec.ts` passes on main. Evidence: `cd e2e && pnpm exec playwright test tests/node-coverage.spec.ts --workers=3` before: 2 failed / 16 passed; after: 18 passed (53.3s). Run on this branch, which is main plus the two e2e edits.

## Plan (2026-10-09)

Both fixes are expectation-only; the runner is not touched.

1. `e2e/fixtures/error-form-nodes.ts` (~line 125): the error-workflow pair expects `execute node "stop": …` (the node id); `internal/engine/runner.go` formats `execute node %q` with `node.Name`. Read the fixture's node definition to learn the failing node's name and expect that.
2. `e2e/tests/node-coverage.spec.ts`, "a sticky note saves and runs without affecting the result": replace the `nodeRun(record, 'note').status === 'succeeded'` assertion with one that the note has no run record, keeping the assertions that the run succeeds and the other nodes' output is unchanged. Edits are confined to that one spec (other tracks add coverage entries to the same file).

Proof: run `cd e2e && pnpm exec playwright test tests/node-coverage.spec.ts` before (to see both specs fail for the stated reasons) and after (all specs pass). Any other failing spec in that file is reported, not changed.

## Progress (2026-10-09)

- Reproduced first: the spec run failed exactly the two specs named above and passed the other 16, for the stated reasons (the error message carries the node name `Stop`, and no run record exists for `note`).
- Fixed both expectations; the runner is untouched. The error-workflow expectation lives in `e2e/fixtures/error-form-nodes.ts`, which `n8n-compare.spec.ts` also calls, so its "error workflow pair" test is fixed too (it passed after the change).
- Deviation, same root cause: `e2e/tests/n8n-compare.spec.ts` ("a loop iterates, notes stay inert, …", ~line 553) carried the same stale sticky-note assertion (`nodeRun(stickyRecord, 'note').status === 'succeeded'`) and was red without any live n8n, i.e. in the ordinary e2e run. Changed that one assertion the same way, outside the `node-coverage.spec.ts` file other tracks are editing. `playwright test tests/n8n-compare.spec.ts`: 15 passed, 5 skipped (the live-n8n gate, each carrying its reason).
- Environment note for a fresh worktree: the suite's global setup runs `make build-all`, which needs `pnpm install --frozen-lockfile` in `web/` as well as in `e2e/` (the vendor-docs step fails with "standalone.js not found" otherwise).

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `f5714b55` (last commit at or before ticket created 2026-10-09)
- Commits (5):
  - `6501dc9d` — merge: make sdk-test and the node-coverage e2e specs are green again (BUG-vbw239, BUG-kkkrmx)
  - `7e0c3c36` — chore(pine): BUG-kkkrmx records the e2e worktree prerequisite and the run-presentation facts the specs assert
  - `5afdb7f9` — BUG-kkkrmx: the e2e specs expect the failing node's name in a run's error and no run record for a sticky note, so node-coverage is green again
  - `c7c81350` — BUG-vbw239: the SDK covers convert-workflow-fragment with convertWorkflowFragment and excludes start-credential-oauth for its browser-bound sign-in, so make sdk-test is green
  - `0c363fe3` — chore(pine): BUG-kkkrmx records two e2e node-coverage specs left stale by BUG-c7s5ss
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |   36 +-
 .pine/memory/docs.md                               |    4 +-
 .pine/memory/e2e.md                                |    1 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-6gkd12.md                        | 1045 +++++++++++++++++-
 .pine/tickets/BUG-9pmv8y.md                        | 1074 ++++++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++++++++++-
 .pine/tickets/BUG-kkkrmx.md                        |   51 +
 .pine/tickets/BUG-pc284d.md                        |   60 ++
 .pine/tickets/BUG-vbw239.md                        |  197 ++++
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-e898nz.md                       |  115 +-
 .pine/tickets/FEAT-gcq50s.md                       |   11 +-
 .pine/tickets/FEAT-v8md2e.md                       |   66 ++
 .pine/tickets/FEAT-zm3wh2.md                       | 1126 +++++++++++++++++++-
 CHANGELOG.md                                       |   30 +
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |  132 +--
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  227 ++++
 docs/src/content/docs/concepts/credentials.md      |   10 +-
 docs/src/content/docs/concepts/node-registry.md    |   20 +-
 docs/src/content/docs/guides/n8n-migration.md      |   23 +-
 docs/src/content/docs/reference/cli.md             |  193 ++--
 docs/src/content/docs/start/what-kilasflow-is.md   |   15 +-
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/node-coverage.spec.ts                    |   13 +-
 internal/ai/agent.go                               |    4 +-
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 ++
 internal/api/workflow_tags_test.go                 |   11 +
 internal/cli/docs_examples_test.go                 |   70 ++
 internal/cli/exit.go                               |  136 +++
 internal/cli/exit_reference_test.go                |  286 +++++
 internal/docexamples/docexamples.go                |  318 ++++++
 internal/docexamples/docexamples_test.go           |  209 ++++
 internal/interop/n8n/n8n.go                        |    6 +-
 internal/interop/n8n/n8n_test.go                   |    2 +
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/tags.go                       |   46 +-
 internal/interop/n8n/tags_test.go                  |   59 +
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 internal/interop/n8n/vectorstore.go                |  153 +++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++++
 internal/shippedcatalogue/shippedcatalogue.go      |  162 +++
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 +++
 internal/workflow/tags.go                          |   30 +-
 nodes/ai.go                                        |    2 +
 nodes/core.go                                      |    1 +
 nodes/executors.go                                 |    1 +
 nodes/glyphs_test.go                               |    1 +
 nodes/openai_credential_url_test.go                |   18 +-
 nodes/pgvector.go                                  |   30 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/rag.go                                       |   13 +
 nodes/vectorstore_retrieve.go                      |   84 ++
 nodes/vectorstore_retrieve_test.go                 |  250 +++++
 nodes/vectorstore_tool.go                          |  325 ++++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++++
 nodes/vectorstore_tool_test.go                     |  565 ++++++++++
 scripts/cli-examples/main.go                       |  583 ++++++++++
 scripts/cli-examples/main_test.go                  |  187 ++++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 ++
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  275 +++++
 scripts/cli-examples/server.go                     |  183 ++++
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/docs-facts/main.go                         |  177 +++
 scripts/docs-facts/main_test.go                    |  278 +++++
 scripts/internal/splice/splice.go                  |  149 +++
 scripts/internal/splice/splice_test.go             |  112 ++
 scripts/skills-command-reference/main.go           |  112 +-
 sdk/CHANGELOG.md                                   |    6 +
 sdk/README.md                                      |   20 +-
 sdk/src/server.ts                                  |   17 +
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 web/src/lib/dashboard/workflow-list.test.ts        |   47 +
 web/src/lib/dashboard/workflow-list.ts             |   25 +-
 web/src/lib/workflow-editor/document.ts            |   16 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   30 +-
 90 files changed, 11411 insertions(+), 426 deletions(-)
```
