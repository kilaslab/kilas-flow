---
id: FEAT-8mymac
title: Benchmark KilasFlow runtime against n8n and publish comparison
status: todo
priority: high
labels:
    - e2e
    - testing
    - benchmark
    - perf
deps:
    - FEAT-cx3hq1
    - FEAT-1jqjtd
    - FEAT-wdnc03
parent: EPIC-m42s3g
phase: p11
created: "2026-09-06T06:41:11Z"
updated: "2026-09-06T06:41:11Z"
---

## Scope

No ticket measures how KilasFlow's runtime compares to n8n's on the same work. Users migrating from n8n will ask exactly this ("is it faster, slower, or the same?"), and today the answer is prose. This ticket produces a number: execute identical workflows on KilasFlow and on the live local n8n, measure, and publish a comparison + benchmark report.

Live n8n fixture (provided by requester, local only):
- URL: `http://localhost:5678/signin?redirect=%252F`
- Email: operator's n8n login / Password: **[REDACTED by Main 2026-09-06 — was committed here in plaintext; rotate it, export N8N_EMAIL/N8N_PASSWORD, never write values into tickets]**
- Secret handling as in siblings: env (`N8N_EMAIL` / `N8N_PASSWORD`), never hardcoded.

Tooling constraint: the implementer MUST read `skill://playwright-cli` first; browser-driven setup (n8n signin, workflow import/activate on both sides) goes through `playwright-cli`. The timing harness itself is a script (not eyeballed in the browser) so numbers are reproducible; browser work is setup, measurement is code.

## Acceptance criteria

- [ ] A fixed benchmark set (minimum: webhook→Set→respond; IF-branch fan-out; paginated loop over Split-in-Batches equivalent; HTTP→Merge→Code/Set data-shaping; one AI-agent tool-loop run against the local Ollama stub) exists as identical logical workflows on both engines.
- [ ] Each workflow is executed N≥30 times per engine on the same machine, sequentially and isolated (no parallel cross-engine runs); report records wall-clock per run with `min / p50 / p95 / max` + mean/stddev, plus peak RSS of the server process per engine.
- [ ] All third-party calls go to the same local stub for both engines, so the comparison measures engine overhead, not internet variance; stub latency is reported separately.
- [ ] Results publish as `docs/` or `e2e/benchmark/` JSON (raw runs) + Markdown table (summary) including: workflow, engine versions (KilasFlow commit + n8n version from the local instance), machine spec, date, and any expected-divergence notes (e.g. features n8n runs that KilasFlow capsules).
- [ ] The benchmark is runnable on demand via a single command (`make bench-compare` or equivalent) and documented so a second operator reproduces it; it does NOT gate merges (third-party/local variance) and does NOT run on every PR.
- [ ] The report states its limits explicitly: what was stubbed, which workflows were excluded and why, and that numbers are single-machine comparisons, not SLAs.

## Implementation Plan

1. Depends on the two sibling tickets: the node-execution matrix (identical-workflow construction) and the library-import flow (realistic fixtures). Sequence this ticket after both; reuse their workflows as the benchmark set rather than inventing a third set.
2. Methodology: warm-up runs discarded (JIT/GC/cold-cache), then N timed runs alternating engines in blocks (AABB, not all-A-then-all-B) to cancel drift; assert run-to-run variance bounds before publishing, otherwise mark the result `inconclusive`, not green.
3. Timing source: server-side execution record (`startedAt`/`finishedAt`) as primary, client-observed wall time as secondary; never `Date.now()` in the browser as the primary number. Cross-check the two.
4. Keep it boring: a small runner script + checked-in workflow JSON pair (KilasFlow canonical + n8n export), not a framework. Raw NDJSON per run, summariser emits the Markdown table.
5. Licence hygiene: no n8n source vendored, no n8n bytes in the image; benchmark fixtures referencing library workflows stay gitignored per FEAT-yyjfjq.

Out of scope: fixing whatever performance gap is found (follow-up tickets reference the report); browser-only micro-interactions (editor latency is not this ticket).

## References

- `skill://playwright-cli` — REQUIRED reading; browser setup steps driven through it.
- Sibling tickets in this batch: node-execution matrix + library-import suite (benchmark set source).
- `.pine/tickets/FEAT-cx3hq1.md` — `e2e/` harness patterns (per-instance boot, no fixed sleeps).
- `.pine/tickets/FEAT-5fhj6p.md` — capstone suite; this report feeds its "versions under test" discipline.
- `.pine/tickets/FEAT-yyjfjq.md` — licence boundary for fixtures.
- Live n8n: `http://localhost:5678/signin?redirect=%252F` (creds via env, see Scope).
