---
id: BUG-zw16h7
title: 'CI: smoke-docker failed on cleanup of container-written files, and the JS worker spec missed workers named /proc/self/exe'
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:36:34Z"
updated: "2026-10-16T15:36:34Z"
---

# Description

CI on main (run 38924256656, 2026-10-16) failed two things that this fixes; both were already red on main before tonight's push (the 2026-09-29 run failed Smoke and End-to-end too):

1. **Smoke → `make smoke-docker`:** every check passed, then the EXIT trap's `rm -rf "$work_dir"` failed with "Permission denied" on `data/codecache`, which the non-root container wrote into the bind mount, and that failure became the script's exit status.
2. **End-to-end → `js-code.spec.ts` "code runs in worker processes …":** on Linux a JS worker re-executes the server through `/proc/self/exe` (internal/jsworker/limits_linux.go), so its command line is `/proc/self/exe`, never `kilasflow`; the spec's `/kilasflow/` filter found no worker.

Not fixed here, still red in CI (need investigation with the CI environment): `epic-acceptance` proof 3's `assertNoNode` ("lsof found no listener on tcp:<port>") and the skip budget (26 skips against a budget of 24).

# Acceptance Criteria
- [x] smoke-docker's cleanup removes the container-written files from a throwaway root container when the host user cannot, and never fails the run (`sh -n` ok; the script's checks are unchanged).
- [x] The worker spec accepts the server's own binary under either name; `pnpm exec playwright test tests/js-code.spec.ts -g "worker processes"` passes locally.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-16.

- Base: `7f9fd34e` (last commit at or before ticket created 2026-10-16)
- Commits (1):
  - `7f9fd34e` — BUG-zw16h7: smoke-docker's cleanup cannot fail the run on files the container wrote, and the worker spec recognises a worker re-executed through /proc/self/exe
