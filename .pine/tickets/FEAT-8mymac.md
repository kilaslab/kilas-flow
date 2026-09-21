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

- [x] A fixed benchmark set (minimum: webhook→Set→respond; IF-branch fan-out; paginated loop over Split-in-Batches equivalent; HTTP→Merge→Code/Set data-shaping; one AI-agent tool-loop run against the local Ollama stub) exists as identical logical workflows on both engines.
- [x] Each workflow is executed N≥30 times per engine on the same machine, sequentially and isolated (no parallel cross-engine runs); report records wall-clock per run with `min / p50 / p95 / max` + mean/stddev, plus peak RSS of the server process per engine.
- [x] All third-party calls go to the same local stub for both engines, so the comparison measures engine overhead, not internet variance; stub latency is reported separately.
- [x] Results publish as `docs/` or `e2e/benchmark/` JSON (raw runs) + Markdown table (summary) including: workflow, engine versions (KilasFlow commit + n8n version from the local instance), machine spec, date, and any expected-divergence notes (e.g. features n8n runs that KilasFlow capsules).
- [x] The benchmark is runnable on demand via a single command (`make bench-compare` or equivalent) and documented so a second operator reproduces it; it does NOT gate merges (third-party/local variance) and does NOT run on every PR.
- [x] The report states its limits explicitly: what was stubbed, which workflows were excluded and why, and that numbers are single-machine comparisons, not SLAs.

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

## Notes (Benchmark, 2026-09-06) — SUPERSEDED by the Implementation notes below

> These numbers are method v1: the timed region contained a 25 ms poll cadence
> and two of the rows were dominated by Go/WASM Code nodes. The raw files stay
> in the tree as history and are **not comparable** with method v2.

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

## Implementation notes

### Stage 1 (2026-09-20) — measurement method v2, literally identical fixtures, full-body equivalence

Harness only; no product code, config, API, migration, web or dependency change. No acceptance criterion is ticked by this stage alone — it builds and proves the method the remaining stages run and publish.

**What changed**

- `method.mjs` (new, pure): the pre-registered constants (`BLOCK_SIZE=5`, `BOUNDS={robustCv:0.30, absNoiseMs:0.5, drift:0.20}`, `RATIO_BAND=[0.95,1.05]`, `BOOTSTRAP={resamples:2000, seed:20260920}`, `QUIET_LOAD=2.0`), `deliverTimed` as the ONLY timing function (brackets the request and the full response body; no polling, no API call, no parsing inside), `buildSchedule` (ABBA, `runs` samples per engine in blocks, start engine flipping per workflow), `robustSpread`/`verdict` (MAD-based `robustCv`, drift on half-medians), `bootstrapRatio`, `compareLabel`, `pairRecords` (sorted by `startedAt`, one record per attempt, a failed record tolerated only for an agent retry), `canonicalize`/`assertEquivalent` (full canonicalised-body equality), `assertNoSecrets`, `gatewayOverhead`.
- `method.test.mjs`, `workflows.test.mjs`, `summarise.test.mjs` (new, 38 tests) — and `make bench-test` to run them.
- `lib.mjs`: `benchPlan()` replaces `runCount()`/`warmupCount()` (floors at 30, rounds up to a whole block; `BENCH_SMOKE=1` = 4/2/1 with a temp output dir); `startBenchServer(ollamaBaseURL, {bindHost})` exposes `port`/`originFor(host)` and proxies Ollama's native `/api/*` paths byte-transparently beside `/v1/*`, recording `durMs` per hit; `classifyHit`/`hitsSince`; `executionRecords` (paginated); `environment()` now records chip, macOS version, load average, dirty flag (excluding `e2e/benchmark/` and `.pine/`), power source, thermal state, Docker facts and the Ollama version/model digest; `startKilasFlow().close()` removes its temp data dir. `measureWorkflow`/`manualTrigger` (the poll-based timing) are deleted.
- `workflows.mjs`: rows 1–4 are ONE hand-authored n8n JSON each, executed by n8n and imported into KilasFlow from that same object (identity, not a copy), with `settings.executionOrder: 'v1'`, a `webhookId` on the Webhook node, `Split Out(body.items)` seeded from the request payload (no Code node), Split In Batches v3 `done`=output 0 / `loop`=output 1, an explicit If operator `type`, `includeOtherFields: true` where input fields must survive, and the stub base read from the delivery payload so no host is hardcoded. Row 5 (agent) keeps a native KilasFlow document beside the n8n JSON; row 6 (Code) is supplementary and divergent by design. Every row is webhook-triggered on both engines (KilasFlow answers a `responseNode` delivery synchronously), so one timing function covers all of them.
- `run.mjs`: engine adapter (`prepare`/`deliver`/`settle`/`records`/`rss`/`teardown`), warm-ups alternating and discarded, ABBA schedule, `settle()` untimed after EVERY run, RSS after every settled run, per-run rows, `BENCH_N8N` (defaults `off`), `BENCH_SKIP_AGENT`, `BENCH_CONTROL=aa`, `BENCH_OUT_DIR`, SIGINT/SIGTERM cleanup and `caffeinate` on darwin. It no longer imports `n8n.mjs` (that runner timed a different clock and is rewritten in stage 2).
- `summarise.mjs`: import-safe (CLI behind an is-main guard), per-engine tables with server p50 and peak RSS, comparison table with ratio/95% CI/verdict, the supplementary Code table, the A/A control line, divergences, exclusions and limits; `writeSummary` refuses to touch `SUMMARY.md` or the docs page for a smoke run and runs `assertNoSecrets` before every write; `renderTable`/`rewriteDocsTable` write the docs block between the `bench:table` markers idempotently.
- `README.md` rewritten for method v2 (v1 numbers declared not comparable); `Makefile` gained `bench-test` and the stale creds comment above `bench-compare` was refreshed.

**Commands and outcomes**

- `node --test e2e/benchmark/*.test.mjs` first ran RED against the pre-change tree (`Cannot find module './method.mjs'`; `does not provide an export named 'RUN_SUFFIX'` from the old `workflows.mjs`), then `make bench-test` → **38 tests, 38 pass, 0 fail**.
- `make build` → `bin/kilasflow` at `1520a85-dirty`.
- `BENCH_SMOKE=1 BENCH_N8N=off BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs` → all four shared rows plus the Code row measured; **every run passed full-body equivalence**, per-run stub-hit counts equalled `expect.stubCalls`, and every shared row imported with zero blocking issues. Client p50 (server-record p50): webhook-set-respond **4.97 ms (3 ms)**, if-fanout 5.17 (5), paginated-loop 30.63 (58), http-merge-shape 8.30 (10), code-node 62.85 (62). Nothing was written into the repository — the raw file went to `$TMPDIR/kilasflow-bench-smoke-<pid>/`.
- `BENCH_SMOKE=1 BENCH_CONTROL=aa BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs` → control ratio 0.918, 95% CI [0.54, 1.16], `inconclusive` (rows 1–2, four samples each). Informational only: the machine's 1-minute load average was ~22–24 during both smoke runs (the parallel wave), so the not-quiet banner fired and the four-sample control cannot resolve anything. The published control is stage 3.
- `go test -race ./internal/guardrails/...` → **ok** (run after staging the new files, because that scan reads `git ls-files`).
- `git status --porcelain` → only `Makefile` and `e2e/benchmark/{method,method.test,workflows,workflows.test,lib,run,summarise,summarise.test}.mjs` (plus this ticket file). `SUMMARY.md` and the 2026-09-06 raw files are untouched; `n8n.mjs` is untouched.

**Findings**

- The poll artefact is confirmed and gone: v1's webhook row read client p50 30.47 ms against a 1 ms server record because the timed region contained a 25 ms sleep and a `listExecutionIds` call; v2 reads **4.97 ms against 3 ms** under a load average of 21.8. The smoke run fails loudly if that row ever exceeds 25 ms again.
- Code nodes did dominate v1's loop and HTTP→Merge rows server-side (42 ms and 29 ms against 1 ms). The v2 shared rows are Code-free and the isolated Code row reads 62.85 ms client / 62 ms server, which is now visible instead of buried.
- **Importer finding (recorded, not fixed — product code is out of scope):** a shared fixture must carry `webhookId` (n8n's `/webhook/<path>` depends on it) and `settings.executionOrder: 'v1'` (or n8n runs the legacy order), and KilasFlow's importer reports each as a `dropped` issue, so `unsupported` is not empty for rows 1–4 as the plan assumed. `run.mjs` therefore asserts zero blocking issues and that the only losses are exactly those two fields; any other loss aborts the run rather than publishing a comparison of two different graphs.
- Both engines reach the same bench server at different addresses (KilasFlow is a host process, n8n a container), so the stub origin travels in the delivery payload; the workflow JSON itself stays literally identical. That hop difference is a stated divergence, together with KilasFlow's SSRF re-check, its tenant resolution and payload redaction, and its per-item Respond loop (quadratic on the 100-item row — a follow-up candidate, not fixed here).

**Not done in this stage (next stages, unchanged from the plan):** the managed throwaway n8n container, the REST client and the validated fixtures (stage 2); the timed N≥30 run, the A/A control on a quiet machine, publishing the raw JSON/`SUMMARY.md` and the docs table (stage 3). `SUMMARY.md` and `docs/src/content/docs/operate/benchmark.md` still carry v1 text and numbers; they are regenerated in stage 3.

### Stage 2 (2026-09-20) — managed throwaway n8n, REST client, validated fixtures, Ollama agent wiring

Harness only; again no product code, config, API, migration, web or dependency change. Stage 2 makes the n8n half real: one command now stands up a pinned throwaway n8n, owns it, drives the same rows on both engines and removes it. Ticked criterion 1 (the fixed set runs identically on both engines) and criterion 3 (one stub for both engines; stub latency reported separately).

**Secrets tooling first**

- `scan-secrets.mjs` + `scan-secrets.test.mjs` (new): reads `N8N_EMAIL`/`N8N_PASSWORD`/`N8N_ENCRYPTION_KEY`/`N8N_SESSION_COOKIE` from the environment only and scans `git ls-files -co --exclude-standard` (tracked **and** untracked-not-ignored, so committed stage-1 files stay covered), `e2e/benchmark/`, `os.tmpdir()/kilasflow-bench-*` and every directory given on the command line. It exits non-zero naming the FILE only — never the value. Written test-first: the two tests were RED (`Cannot find module './scan-secrets.mjs'`) before the implementation.

**Managed container (`n8n-container.mjs` + `n8n-container.test.mjs`, new)**

- Pinned by tag **and** digest: `docker.n8n.io/n8nio/n8n:2.33.7@sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30`. The 2.33.7 tag's digest equals the cached image's digest (verified with `docker image inspect .RepoDigests`), so one ref pins version and bytes; `ensureImage` pulls only when absent.
- `docker run -d --name kf-bench-n8n-<8hex> --label kilasflow.bench=FEAT-8mymac --label kilasflow.bench.pid=<pid> --add-host=host.docker.internal:host-gateway -p 127.0.0.1::5678 -e … <image>`: loopback-only, ephemeral port, no volume, not privileged, diagnostics/version-check/templates/personalization flags off. Readiness polls `/healthz/readiness`, then asserts `/rest/settings` `userManagement.showSetupOnFirstLoad` is `true`.
- **Secrets:** the owner password is generated in memory, used only for `POST /rest/owner/setup` + `POST /rest/login`, and never written anywhere. The n8n encryption key is passed as a **value-less** `-e N8N_ENCRYPTION_KEY` so docker copies it from the client env and the value never appears in the argv; `recordedContainerEnv` drops it (and any `*PASSWORD*`/`*KEY*`) from what the raw file records.
- **Sweep:** only a container carrying this ticket's label AND whose recorded owner pid is dead is removed — never a concurrent run, never a `kf-pg-*` scratch Postgres. `close()` is idempotent and wired to `finally` + `SIGINT`/`SIGTERM` inside `startN8nContainer`, not at import (the import-safety test pins that). Container-side probes: RSS summed over the node processes (`docker exec … node -e`, probe pid excluded), `VmHWM` at the end, and stub latency from inside the container.

**REST client + adapter (`n8n.mjs`, rewritten)**

- `N8nClient` over n8n's UI API with a cookie jar: `ownerSetup`, `login`, `createWorkflow`, `activate` (asserts `active: true`; retries once with a random `push-ref` header only if the server asks), `deactivate`, `archive`, `deleteWorkflow` (n8n refuses to delete an unarchived workflow — found live), paginated `listExecutions` (`data.results`, keyed by `lastId`), `createCredential`. Errors surface n8n's own error body, never the request body.
- API shapes were verified against the running 2.33.7 instance, not assumed: `/rest/settings` carries `versionCli` only when authenticated; execution records carry `startedAt`/`stoppedAt`; archive-before-delete; `/types/nodes.json` (authenticated) carries the node schemas.

**Fixtures validated live (the stage's main finding)**

- Two hand-authored fixture bugs the stage-1 tests could not see, caught on the real instance and fixed: **the Set node was silently a pass-through** because the `assignments` parameter is only read at Set `typeVersion ≥ 3.4` (3.0–3.2 read `fields`), so `Webhook → Set → Respond` returned the raw webhook envelope and the loop/HTTP rows dropped their shaped fields; and the If node was pinned to `typeVersion 2`. Set is now `3.4` (with `mode: 'manual'`) and If `2.2`. After the fix every row's n8n delivery equals `expect.body` exactly.
- `run.mjs` gains one **untimed probe delivery per engine per row before any warm-up**, which both validates the hand-authored JSON and, on rows 1–4, deep-equals the two engines' bodies (canonicalised) — the cross-engine equality the plan asked for. Probe and warm-up attempts are recorded so `pairRecords` still matches 1:1 with execution records.
- **Agent row measured on both engines:** `lmChatOllama` (model `gemma4:12b-mlx`, temperature 0) + `toolHttpRequest` `get_weather` + no memory node, with n8n's Ollama `baseUrl` pointed at the **bench gateway** (`host.docker.internal:<benchport>`) so both engines' model and tool calls go through the same byte-transparent proxy and are counted per run.

**run.mjs wiring**

- Engines `['kilasflow','n8n']` by default; `BENCH_N8N` ∈ `managed` (default) / `external` / `off`, and `BENCH_CONTROL=aa` forces `off`. Docker/unavailable image/owner-setup failure each record an honest `skipped-*` with the reason and leave the summary PRELIMINARY. External mode keeps the operator's own instance and lets `N8N_STUB_ORIGIN` override how it reaches this host's bench server.
- Per-engine context view fixes the one origin that differs: KilasFlow reaches the stub at `127.0.0.1:<benchport>`, n8n at `host.docker.internal:<benchport>`; the delivery payload carries the engine-correct `stubBase`, so the workflow JSON stays literally identical. The raw file now carries `n8n {version, instanceVersion, image, digest, architecture, containerEnv, container{hwmKib}, results}`, `stubLatency`, `containerStubLatency` and `hopProbe` (n8n `/healthz` vs KilasFlow `/api/v1/health`, N=30).
- `SIGINT` now marks a `shuttingDown` flag so a delivery that fails because its engine was torn down cannot race the signal's own exit code.

**Browser discovery (playwright-cli)**

- Skill read; session `-s=bench-n8n` run from `/tmp/kf-bench-browser` (outside the tree, so `.playwright-cli/` snapshots never enter the repo), secrets exported only in that one shell call. The **first-run “Set up owner account” screen was opened and snapshotted** (page text: `Set up owner account / Email* / First Name* / Last Name* / Password* / 8+ characters, at least 1 number and 1 capital letter / Next`; inputs `email`, `firstName`, `lastName`, `password`).
- **Finding:** `playwright-cli run-code` cannot see `process.env` here (`ReferenceError: process is not defined`), and the plan forbids typing the password in the browser when it cannot (run-code prints generated `fill` code, which would echo it). So, per the plan's documented fallback, owner setup and sign-in are done by REST and the browser is limited to unauthenticated first-run inspection. The authenticated UI checks (About version, credential form, agent editor Test/Execute) are therefore not done in the browser; the REST path — owner setup, login, credential creation, agent workflow activation and one executed agent run (`/rest/settings` `versionCli` **2.33.7**, workflow active, execution `success`) — is the reproducible setup. Session closed with `close-all` and `delete-data`; no browser artefact left in the tree.

**Commands and outcomes (load average ~40–46 throughout; the parallel wave)**

- `node --test e2e/benchmark/*.test.mjs` first RED for both new files, then `make bench-test` → **48 tests, 48 pass, 0 fail** (was 39).
- `make build` → `bin/kilasflow` at `08e7ecf-dirty`.
- `BENCH_SMOKE=1 BENCH_N8N=off BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs` → the refactor still measures the KilasFlow half identically (webhook p50 5.78 ms, server 4 ms; the 25 ms ceiling holds).
- `BENCH_SMOKE=1 BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs` → **exit 0**, rows 1–4 + Code measured on **both engines**, full-body equivalence and cross-engine body equality passing, per-run stub-hit counts equal on both; n8n from the instance `2.33.7` (digest recorded), container gone afterwards. Under load ~46 every row reads `inconclusive` on a variance bound (stage 3 publishes on a quiet machine; bounds were not relaxed).
- `BENCH_SMOKE=1 node e2e/benchmark/run.mjs` (agent included) → **exit 0**; agent-tool-loop measured on both engines (KilasFlow p50 8031 ms, n8n 8283 ms, ratio 1.01, `inconclusive` — CI straddles the band, model-bound as predicted), tool/model call-count and `contains '19'` assertions passing.
- SIGINT mid-run: one container live during the run, `docker ps -a --filter label=kilasflow.bench=FEAT-8mymac -q` **empty after**, node exit 130.
- `node e2e/benchmark/scan-secrets.mjs /tmp/kf-bench-browser` with the live `N8N_*` exported in the same shell as the smoke → **1594 files scanned, no secret material found, exit 0**.
- `go build ./...` ok, `go vet ./...` ok, `go test -race ./internal/guardrails/...` **ok** (after `git add`, because that scan reads `git ls-files`), `gofmt -l` on changed Go files empty (no Go changed), `grep -rn bench .github/workflows` finds nothing.

**Published comparison preview (not the stage-3 artifact, load ~40, N=4, inconclusive by design)**

- Under a load average of ~40 the four-shared-row ratio ran **~7–13× n8n slower** and the Code row ~0.5–2×; all `inconclusive` on a variance bound. Hop probe (trivial handlers, N=30): n8n `/healthz` p50 **0.74 ms** vs KilasFlow `/api/v1/health` p50 **0.38 ms** — the OrbStack published-port proxy floor. Container-side stub latency p50 ~3 ms vs ~0.7 ms from the host. None of these is a published number; stage 3 produces them on a quiet machine.

**Not done in this stage (stage 3):** the timed N≥30 comparison, the A/A control on a quiet machine, publishing the raw JSON/`SUMMARY.md` and the docs table, and closing the ticket. `docs/src/content/docs/operate/benchmark.md` still carries v1 text (including a now-stale sentence that the n8n side runs only with `N8N_EMAIL`/`N8N_PASSWORD` from the environment); stage 3 rewrites it from the generated table. `SUMMARY.md` is untouched. No container, image byte or credential was left behind.

### Stage 3 (2026-09-20) — the timed comparison, the A/A control, publication, docs and evidence

Harness, docs and ticket only; still no product code, config, API, migration, web or dependency change. Ticked criteria 2, 4, 5 and 6 (1 and 3 were ticked in stage 2).

**Two attempts were run and both raw files are published** (never a cherry-picked subset; `SUMMARY.md` and the docs table come from the later one, as published):

| attempt | raw file | condition (1-minute load average) | verdicts |
| --- | --- | --- | --- |
| 1 | `bench-2026-09-20T11-37-55.json` | 17–21 across rows 1–4, then 137 during the agent row | webhook-set-respond **6.66×** n8n slower (95% CI [6.00, 7.65]); agent **no meaningful difference**; the other four inconclusive on a pre-registered bound |
| 2 | `bench-2026-09-20T12-01-21.json` | 31.95 → 36.82 overall; 30–38 per row | webhook-set-respond **10.61×** n8n slower (95% CI [9.12, 12.00]); agent **no meaningful difference** (1.0038, CI [0.9825, 1.0235]); the other four inconclusive on n8n's variance bound |

**The condition is the finding.** The wave kept this 10-CPU laptop at a 1-minute load average between 17 and 145 for the whole window, against the method's pre-registered quiet threshold of 2.0, so both summaries carry the **not-quiet banner** and no bound was relaxed to turn an inconclusive row into a verdict. KilasFlow runs as a native process while n8n runs inside the OrbStack VM, so the two do not degrade equally under host load: in attempt 2 KilasFlow's own rows mostly passed their bounds while n8n's failed on four rows, which is a load artefact rather than an engine finding. Only the webhook row and the agent row therefore carry verdicts in the published tables; the other rows say "not measured well enough to say". **A quiet-machine run is still owed**, and the docs page, `SUMMARY.md` and the README say so.

**What stage 3 changed (test first where it is code)**

- `method.mjs` `controlFromFile` + `run.mjs` `BENCH_CONTROL_FILE`: the A/A control is measured in its own short run and its measured block is handed to the main run, so the raw file that carries the comparison also carries the harness's own negative control (the summary prints `ratio (95% CI) — label` beside the rows). A truncated, numberless or unlabelled control file is refused with a reason rather than attached.
- `lib.mjs`: a proxied hit's `durMs` is now recorded at the **last byte**, not when the response headers arrive. Found in the attempt-1 data: Ollama streams, so the agent row's gateway-observed model time read ~1.0 s inside an 8.7 s response and ~7 s of model time was charged to the engine as overhead. After the fix the same column reads **28.37 ms (KilasFlow) vs 194.13 ms (n8n)** — engine overhead instead of model time. Test: *a streamed proxied hit records durMs to the last byte, not the first* (a fake upstream that writes headers, waits 300 ms, then ends; it failed at 7 ms before the fix).
- `run.mjs`: the raw file's `environment.loadAverage.before` is sampled at the **start** of the run. It was sampled after the last row, so `before` and `after` were the same instant; attempt 1's raw shows `[123.99, 119.54, 84.63]` twice for that reason and its per-row load fields are the usable record.
- `summarise.mjs`: the gateway-overhead line names the engine (`KilasFlow 28.37 ms; n8n 194.13 ms`) and the divergence list is a set (a shared row's divergence was printed twice). Test: *the gateway-attributed overhead line names the engine, and a shared divergence is listed once*.
- `summarise.test.mjs`: *docs-table-matches-latest-raw* regenerates the docs table from the newest non-smoke method-v2 raw file and compares it with the block between the docs markers, so the page cannot drift from the JSON.
- `docs/src/content/docs/operate/benchmark.md`: rewritten for method v2 — the stale v1 bullets ("low tens of milliseconds", "first-run sandbox compilation", "sub-second" loop) are gone, the creds-gated n8n section is replaced by the managed throwaway container and the `BENCH_*`/`N8N_*` knobs, the three method rules now match the method, and machine spec, engine versions, divergences, exclusions, limits, the reproduction recipe and a "superseded" note are on the page. Not a BUG-vzzkg3 page.
- `e2e/benchmark/README.md`: an attempt table with the condition for each, plus the `BENCH_CONTROL_FILE` knob.
- `summarise.mjs`: the published summary now also prints the Docker runtime and container-VM spec, the n8n container's environment-variable names (secret and key variables dropped, so no credential can reach the file), the loopback stub's latency measured from inside the container, and the hop probe on each engine's trivial handler. Test: *the summariser prints the Docker spec, the container env keys, the container stub latency and the hop probe*, including the KilasFlow-only case where none of those facts exist and the section must simply not appear.

**Commands and outcomes**

- `make bench-test` → **53 tests, 53 pass, 0 fail** (stage 1's method tests, stage 2's, and four new ones here), each new test seen failing first for the right reason.
- Attempt 1: `BENCH_CONTROL=aa … node e2e/benchmark/run.mjs` then `make bench-compare`, exit 0 both; raw `bench-2026-09-20T11-37-55.json`, n8n 2.33.7 from the instance (digest `sha256:3989d9b8…`), stub p50 0.63 ms, hop probe n8n 1.06 ms vs KilasFlow 0.41 ms.
- Attempt 2 (after the two harness fixes, in the same script): control exit 0, main exit 0, raw `bench-2026-09-20T12-01-21.json`, container `kf-bench-n8n-d61e64bf` removed; hop probe n8n 1.19 ms vs KilasFlow 0.79 ms; container-side stub latency p50 3.17 ms vs 0.62 ms from the host; `VmHWM` 1.14 GiB; agent row measured on both engines with one KilasFlow retry (the model answered without issuing the tool call — counted as `retriedRuns=1`, tolerated by `pairRecords`).
- A/A control, attempt 2: ratio **1.0928** (95% CI [0.9973, 1.1737]) — inconclusive. The interval brackets 1, so the harness shows no order or position bias, but on this loaded machine its half-width is the harness's minimum detectable effect and it is wide enough to swallow the whole 0.95–1.05 band. Attempt 1's control: 1.0531 (CI [0.9565, 1.3051]).
- `node e2e/benchmark/summarise.mjs e2e/benchmark/bench-2026-09-20T12-01-21.json --docs docs/src/content/docs/operate/benchmark.md` → regenerated `SUMMARY.md` and the docs table; `docs-table-matches-latest-raw` green.
- `node e2e/benchmark/scan-secrets.mjs /tmp/kf-bench-browser` with the live credentials exported in the same shell as the runs → **1612 files scanned, no secret material found, exit 0** (the values were generated inside that shell and never echoed; the scan searches for those exact live values).
- `docker ps -a --filter label=kilasflow.bench=FEAT-8mymac -q` → **empty** after both runs, and empty after a SIGINT mid-run in stage 2. No other container was touched.
- `grep -rn bench .github/workflows` → nothing (never a PR gate). `go build ./...` ok, `go vet ./...` ok, `gofmt -l` on changed Go files empty (no Go changed), `go test -race ./internal/guardrails/...` **ok** (it scans the new tracked `*.mjs`, the Makefile and the YAML).
- `cd docs && pnpm install --frozen-lockfile && cd .. && make docs-build` → 43 pages built, all internal links valid.
- Reproducibility proof, README only: `git worktree add --detach /tmp/kf-mymmac-smoke <this commit>` (clean, `git status --porcelain` empty), then `BENCH_SMOKE=1 make bench-compare` → **exit 0**: the managed container came up, both engines were measured on every row, the smoke raw went to the OS temp dir (`summary not written`), and the container was gone afterwards. This proves the documented steps on this machine; it does not prove a second machine, and non-OrbStack hosts still need the documented `BENCH_BIND_HOST` arrangement.

**Learnings for the orchestrator (not written to Pine memory)**

- The gateway's per-hit duration must be taken at the last byte: Ollama streams, so header-time measurement silently attributes the entire token stream to the engine.
- Under host CPU load the containerised engine degrades far more than the native one, so a loaded machine biases the comparison in KilasFlow's favour — a comparison run under load must say so, and its inconclusive labels are the only honest output.
- `environment()`-style "before" fields that are captured after the workload are worse than useless: they claim a stable machine that was never observed.
- The A/A control's confidence-interval half-width is the harness's minimum detectable effect; publishing it beside the comparison is what makes the ratio band readable.
