---
id: BUG-ph3zcq
title: 'CI e2e: epic proof 3 cannot find the server with lsof, and the serial skips after it break the skip budget'
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:39:09Z"
updated: "2026-11-26T08:41:26Z"
---

# Description

CI's End-to-end job on main is still red after 2026-10-16's fixes, on one root cause:

- `epic-acceptance.spec.ts` › proof 3 fails in `assertNoNodeOk` with `lsof found no listener on tcp:<port>` (e2e/fixtures/epic-external.ts `assertNoNodeBesideServer`, which runs `lsof -ti tcp:<port> -sTCP:LISTEN` as the runner user).
- Because the describe is serial, the proofs after it are skipped, which pushes the run to 26 skips against the 24 budget, so the `Skip budget` step fails too.

Locally proof 3 skips (no PostgreSQL), so it was never seen outside CI. Most likely cause (unverified): in CI the proof's server listens behind Docker's port proxy or under another user, so an unprivileged `lsof` cannot see the listener. The 2026-09-29 CI run on main was already red in this job.

# Steps to Reproduce

1. Push to main; open the CI run's End-to-end job.

# Expected

Proof 3 resolves the server's pid (or the container's process table) and judges the no-Node.js criterion; the skip count stays within budget.

# Actual

`lsof found no listener on tcp:<port>`; 2 failed (this and, before BUG-zw16h7, js-code), 26 skipped.

# Acceptance Criteria
- [x] The no-Node check resolves the server process in CI's topology (e.g. `docker top`/`docker inspect` when the server is a container, or `ss -ltnp`/`/proc/net/tcp` inode matching), with the reason recorded.
- [x] CI's End-to-end job passes the proof and the skip budget on main.

## Progress (2026-10-16)

The binary host now hands the no-Node check the server's own pid (`PgServer.pid`, `EpicServer.pid`, `assertNoNodeBesideServer(port, scope, serverPid)`), and the check judges that process subtree directly; lsof stays only as the fallback when no pid is known (the image host keeps its container path). Local evidence: `KILASFLOW_E2E_PREBUILT=1 pnpm exec playwright test tests/epic-acceptance.spec.ts --workers=1` → 5 passed, 2 skipped (no PostgreSQL / no local model here). Criterion 2 waits on the CI run for this push; leave this ticket open until that run's End-to-end job is green.


## Verified on main (2026-11-26)

CI run 45781343712 on main @ c59e13a9 (push, after PR #57), job "End-to-end (Playwright)": every step succeeded. All three proof 3 cases (`epic-acceptance.spec.ts:98`, `:103`, `:108`, the last being "the datastore path holds on postgres") ran and passed; none of them is on the skip list. The no-Node check judges the server by its own pid (`e2e/fixtures/epic-hermetic.ts:82` passes `server.pid`). The run had 162 passed and 22 skipped with 0 failed, and the `Skip budget` step reported `22/24 skipped, every one of them explained` (local model, WAHA corpus or live n8n unavailable). PR #57's own E2E job was green as well. The fix itself is 193b9118.

## Work Evidence

Recorded from commits by `pine close --evidence --commit` on 2026-11-26.

- Commits (1):
  - `193b9118` — BUG-ph3zcq: the epic no-Node check judges the binary host's server by its own pid instead of finding the listener with lsof
- Files changed:

```
193b9118 BUG-ph3zcq: the epic no-Node check judges the binary host's server by its own pid instead of finding the listener with lsof

 .pine/tickets/BUG-ph3zcq.md   | 7 ++++++-
 e2e/fixtures/datastore.ts     | 3 +++
 e2e/fixtures/epic-external.ts | 8 +++++++-
 e2e/fixtures/epic-hermetic.ts | 3 ++-
 e2e/fixtures/epic-proofs.ts   | 4 ++++
 5 files changed, 22 insertions(+), 3 deletions(-)
```
