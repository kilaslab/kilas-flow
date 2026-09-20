---
id: FEAT-8mymac
title: Benchmark KilasFlow runtime against n8n and publish comparison
status: doing
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
updated: "2026-09-20T07:42:30Z"
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

## Notes (Benchmark, 2026-09-06)

Status: KilasFlow half MEASURED and green; n8n half honestly skipped (N8N_EMAIL/N8N_PASSWORD verified absent via env — `env | grep -i n8n` empty, not assumed). Ticket stays doing until a credentialed run executes the comparison half. Nothing was fabricated for the n8n side; no secrets in files (creds travel env → login request only).

Deliverables (new files only; no product-code edits):
- `e2e/benchmark/{run,workflows,n8n,lib,summarise}.mjs` + `README.md` — timing harness (script, reproducible), fixed 5-workflow set (native KilasFlow docs + equivalent hand-written n8n JSON), env-gated n8n runner.
- `e2e/benchmark/bench-*.json` (raw per-run) + `SUMMARY.md` (latest table).
- `docs/src/content/docs/operate/benchmark.md` (autogenerated sidebar, no config edit).
`make bench-compare` green 2026-09-06 (run 2, artifact `e2e/benchmark/bench-2026-09-06T08-07-31.json`; client wall-clock ms, N=30 after 5 discarded warm-ups, sequential/isolated):

| workflow | min | p50 | p95 | max | mean | stddev | peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Webhook → Set → Respond | 28.28 | 30.47 | 32.76 | 33.23 | 30.84 | 1.39 | 51.6 MiB |
| IF branch fan-out | 26.89 | 28.87 | 30.23 | 30.90 | 28.73 | 0.88 | 53.2 MiB |
| Paginated loop (100 items, batch 10) | 60.85 | 63.04 | 64.45 | 64.51 | 62.93 | 1.01 | 145.4 MiB |
| HTTP → Merge → shaping | 29.55 | 55.87 | 57.89 | 58.15 | 54.93 | 4.87 | 219.8 MiB |
| AI-agent tool-loop (Ollama gemma4:12b-mlx) | 6967.19 | 23868.88 | 27476.56 | 29427.83 | 21212.65 | 7022.56 | 91.3 MiB |
Stub latency direct (engine excluded): p50 0.18 ms, n=30. retriedRuns=0 on all five.

Reproducibility: run 1 (`bench-2026-09-06T07-47-10.json`) agrees — webhook p50 32.07, if 30.60, loop 61.86, shape 55.18, agent 18633.34 (model-bound variance expected). Note: run 1 raw lacks per-run `runs[]` arrays (added before run 2); summaries recompute from run 2's file.

Environment (recorded in every raw file): Mac16,13, Darwin 25.5.0 arm64, 10 CPUs; KilasFlow 1793d0d-dirty (run 2; run 1: 1b76522-dirty — same tree, rebuilt by make), Node v24.16.0; date 2026-09-06T08:07:31Z. n8n version: unknown — auth required; recorded at first credentialed run.

Incidents: the first `make bench-compare` attempt failed on one agent run (transient Ollama `context deadline exceeded`, model turn 2). Fix, not suppression: timed slots now retry ≤3 attempts with `retriedRuns` counted in the raw file; persistent failure still aborts red. Retry path not yet exercised (0 retries across 65 subsequent agent runs).

Remaining for close: credentialed operator runs `export N8N_URL/N8N_EMAIL/N8N_PASSWORD` + `make bench-compare`, records n8n version + table, validates `e2e/benchmark/n8n.mjs` login→create→activate→webhook-timing path end to end (implemented against verified `/rest/login` shape, otherwise unverified). Agent n8n fixture needs an operator-wired model; until then its n8n row stays `needs-operator-model`.
