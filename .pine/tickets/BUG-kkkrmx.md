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
- [ ] The error-workflow spec expects the failing node's name (as the runner reports it), not its id.
- [ ] The sticky-note spec asserts the note has no run record and the run's result is unaffected.
- [ ] `playwright test tests/node-coverage.spec.ts` passes on main.
