---
id: BUG-kkkrmx
title: 'e2e node-coverage is red on main: two specs still expect the pre-BUG-c7s5ss node id in errors and a run record for a sticky note'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:02:11Z"
updated: "2026-10-09T12:02:11Z"
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
