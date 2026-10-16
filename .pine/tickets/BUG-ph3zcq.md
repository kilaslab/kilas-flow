---
id: BUG-ph3zcq
title: 'CI e2e: epic proof 3 cannot find the server with lsof, and the serial skips after it break the skip budget'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:39:09Z"
updated: "2026-10-16T15:39:09Z"
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
- [ ] The no-Node check resolves the server process in CI's topology (e.g. `docker top`/`docker inspect` when the server is a container, or `ss -ltnp`/`/proc/net/tcp` inode matching), with the reason recorded.
- [ ] CI's End-to-end job passes the proof and the skip budget on main.
