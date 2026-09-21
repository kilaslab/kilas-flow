---
id: FEAT-8mymac
title: Benchmark KilasFlow runtime against n8n and publish comparison
status: done
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
updated: "2026-09-21T00:43:55Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `db79cff1` (last commit at or before ticket created 2026-09-06)
- Commits (4):
  - `6ef623bd` — FEAT-8mymac: measure KilasFlow against n8n and publish the comparison
  - `b3c9f3f9` — chore(pine): start the board — the open tickets move to doing before the parallel wave
  - `fd7dda24` — merge: reproducible runtime benchmark, KilasFlow half live (FEAT-8mymac)
  - `f7deaa38` — chore(pine): record live-n8n tickets filed mid-session, secrets redacted
- Files changed (base → working tree):

```
 .agents/skills/pine/SKILL.md                       |    2 +-
 .editorconfig                                      |   33 +
 .env.example                                       |    2 +-
 .github/ISSUE_TEMPLATE/bug_report.yml              |   89 +
 .github/ISSUE_TEMPLATE/config.yml                  |   11 +
 .github/ISSUE_TEMPLATE/feature_request.yml         |   57 +
 .github/PULL_REQUEST_TEMPLATE.md                   |   25 +
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/dependabot.yml                             |   78 +
 .github/workflows/ci.yml                           |  101 +-
 .github/workflows/release.yml                      |  123 +-
 .gitignore                                         |   13 +-
 .pine/MEMORY.md                                    |    4 +
 .pine/memory/e2e.md                                |    9 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++
 .pine/tickets/BUG-277a2m.md                        |  506 ++
 .pine/tickets/BUG-341sxn.md                        |  623 ++
 .pine/tickets/BUG-4053h6.md                        |  998 ++++
 .pine/tickets/BUG-57n76x.md                        |  576 ++
 .pine/tickets/BUG-5gws7n.md                        |   97 +
 .pine/tickets/BUG-66es9z.md                        |  248 +
 .pine/tickets/BUG-6as5y7.md                        |  690 +++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++
 .pine/tickets/BUG-6jvcs5.md                        |  710 +++
 .pine/tickets/BUG-8dmp5y.md                        |  660 +++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++
 .pine/tickets/BUG-8sb0jw.md                        |  715 +++
 .pine/tickets/BUG-8t94wn.md                        |  628 ++
 .pine/tickets/BUG-9853ay.md                        |  556 ++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  815 +++
 .pine/tickets/BUG-c241hm.md                        |  607 ++
 .pine/tickets/BUG-cq4yk3.md                        |  818 +++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++
 .pine/tickets/BUG-esb9sh.md                        |  590 ++
 .pine/tickets/BUG-f9frth.md                        |  870 +++
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fv5fer.md                        |  657 +++
 .pine/tickets/BUG-fvdz46.md                        |  400 ++
 .pine/tickets/BUG-gaavr5.md                        |  813 +++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++
 .pine/tickets/BUG-hm76dq.md                        |  569 ++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  548 ++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++
 .pine/tickets/BUG-npfz43.md                        |  487 ++
 .pine/tickets/BUG-p3t7yq.md                        |  212 +
 .pine/tickets/BUG-pwckhd.md                        |  512 ++
 .pine/tickets/BUG-qmgz2f.md                        |  652 +++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++
 .pine/tickets/BUG-rjd6fm.md                        |  721 +++
 .pine/tickets/BUG-rpkjpy.md                        |  984 ++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++
 .pine/tickets/BUG-tcqkad.md                        |  660 +++
 .pine/tickets/BUG-th16c1.md                        |  218 +
 .pine/tickets/BUG-txc9xg.md                        |  520 ++
 .pine/tickets/BUG-vzzkg3.md                        |  598 ++
 .pine/tickets/BUG-w8h3km.md                        |  123 +
 .pine/tickets/BUG-wdypd2.md                        |  680 +++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++
 .pine/tickets/BUG-xmr673.md                        |  737 +++
 .pine/tickets/BUG-y57cz4.md                        |  640 +++
 .pine/tickets/BUG-ysvmaa.md                        |  773 +++
 .pine/tickets/BUG-ze1nn8.md                        |  564 ++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++
 .pine/tickets/EPIC-3844bz.md                       |   29 +
 .pine/tickets/EPIC-87t47t.md                       |   50 +
 .pine/tickets/EPIC-8n8aq8.md                       |   38 +
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-cfe7ny.md                       |   61 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-qx56ay.md                       |   36 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-0895qc.md                       |  761 +++
 .pine/tickets/FEAT-15k49d.md                       | 1369 +++++
 .pine/tickets/FEAT-1axhdn.md                       | 2023 ++++++-
 .pine/tickets/FEAT-1c70nt.md                       |  120 +-
 .pine/tickets/FEAT-1jqjtd.md                       |  131 +
 .pine/tickets/FEAT-2mth85.md                       |   57 +
 .pine/tickets/FEAT-3taswf.md                       | 1199 +++-
 .pine/tickets/FEAT-41m8dj.md                       |   55 +
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 +
 .pine/tickets/FEAT-56nep4.md                       |  625 ++
 .pine/tickets/FEAT-5fhj6p.md                       |   79 +-
 .pine/tickets/FEAT-76p02z.md                       |   44 +
 .pine/tickets/FEAT-77rveq.md                       |   73 +
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-7fs90q.md                       |   61 +
 .pine/tickets/FEAT-8apb8n.md                       |   37 +
 .pine/tickets/FEAT-8mymac.md                       |  221 +
 .pine/tickets/FEAT-9555xz.md                       | 1073 +++-
 .pine/tickets/FEAT-9pe65j.md                       |   37 +
 .pine/tickets/FEAT-a5fhjw.md                       |  401 ++
 .pine/tickets/FEAT-adyeh0.md                       |   53 +
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++
 .pine/tickets/FEAT-c32499.md                       |   39 +
 .pine/tickets/FEAT-c71wy8.md                       |   46 +
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cpdp8y.md                       |  858 ++-
 .pine/tickets/FEAT-cwmw90.md                       |  530 ++
 .pine/tickets/FEAT-ds4e0m.md                       |   54 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  936 +++
 .pine/tickets/FEAT-g07pj8.md                       |   67 +
 .pine/tickets/FEAT-hj8pyx.md                       |  750 +++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++
 .pine/tickets/FEAT-jvembs.md                       |  813 +++
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mbs0qq.md                       |   61 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +
 .pine/tickets/FEAT-nc6z9r.md                       |  950 +++-
 .pine/tickets/FEAT-nqpvf6.md                       |  611 ++
 .pine/tickets/FEAT-p77zr3.md                       |   67 +
 .pine/tickets/FEAT-qdedm0.md                       | 1025 ++++
 .pine/tickets/FEAT-v3gk2x.md                       |   35 +
 .pine/tickets/FEAT-vxbkhg.md                       |   42 +
 .pine/tickets/FEAT-vzp0kb.md                       |   37 +
 .pine/tickets/FEAT-wdnc03.md                       |  120 +
 .pine/tickets/FEAT-x35nqx.md                       |   58 +
 .pine/tickets/FEAT-x5km1z.md                       |  590 ++
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 AGENTS.md                                          |    2 +-
 CHANGELOG.md                                       |  169 +
 CODE_OF_CONDUCT.md                                 |  174 +
 CONTRIBUTING.md                                    |  145 +
 Dockerfile                                         |   15 +-
 Makefile                                           |  189 +-
 README.md                                          |   78 +-
 SECURITY.md                                        |  101 +
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++
 cmd/kilasflow/idempotency_test.go                  |  225 +
 cmd/kilasflow/main.go                              |  534 +-
 cmd/kilasflow/main_test.go                         |    6 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 cmd/kilasflow/role_test.go                         |   70 +
 cmd/kilasflow/secrets_boot_test.go                 |    4 +-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +
 cmd/nodepackgen/authorcmd.go                       |    2 +-
 cmd/nodepackgen/generate.go                        |    8 +-
 cmd/nodepackgen/generate_test.go                   |   12 +-
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |  123 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    8 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 .../content/docs/concepts/datastore-concurrency.md |  179 +
 docs/src/content/docs/concepts/execution-model.md  |  211 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   75 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |  169 +-
 docs/src/content/docs/guides/community-nodes.md    |  133 +-
 docs/src/content/docs/guides/embedding.md          |   97 +-
 docs/src/content/docs/guides/idempotency.md        |  197 +
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   16 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +
 docs/src/content/docs/index.mdx                    |    2 +-
 docs/src/content/docs/operate/benchmark.md         |  214 +
 .../docs/operate/configuration-reference.md        |  191 +-
 docs/src/content/docs/operate/deployment.md        |   81 +-
 docs/src/content/docs/operate/security.md          |   69 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 +
 docs/src/content/docs/operate/upgrades.md          |   78 +-
 docs/src/content/docs/reference/api-contract.md    |   45 +-
 docs/src/content/docs/reference/api.md             |    8 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    9 +-
 docs/src/content/docs/reference/api/datastores.md  |  417 ++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |   18 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    7 +-
 docs/src/content/docs/reference/api/tenants.md     |  221 +
 docs/src/content/docs/reference/api/workflows.md   |   58 +-
 docs/src/content/docs/reference/cli.md             |  628 ++
 .../content/docs/reference/expression-grammar.md   |  369 +-
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   58 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   92 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 ++
 e2e/benchmark/README.md                            |  189 +
 e2e/benchmark/SUMMARY.md                           |   93 +
 e2e/benchmark/bench-2026-09-06T07-47-10.json       |  216 +
 e2e/benchmark/bench-2026-09-06T08-07-31.json       |  831 +++
 e2e/benchmark/bench-2026-09-20T11-37-55.json       | 5978 ++++++++++++++++++++
 e2e/benchmark/bench-2026-09-20T12-01-21.json       | 5976 +++++++++++++++++++
 e2e/benchmark/lib.mjs                              |  494 ++
 e2e/benchmark/method.mjs                           |  393 ++
 e2e/benchmark/method.test.mjs                      |  366 ++
 e2e/benchmark/n8n-container.mjs                    |  458 ++
 e2e/benchmark/n8n-container.test.mjs               |  131 +
 e2e/benchmark/n8n.mjs                              |  304 +
 e2e/benchmark/run.mjs                              |  790 +++
 e2e/benchmark/scan-secrets.mjs                     |  196 +
 e2e/benchmark/scan-secrets.test.mjs                |   70 +
 e2e/benchmark/summarise.mjs                        |  391 ++
 e2e/benchmark/summarise.test.mjs                   |  303 +
 e2e/benchmark/workflows.mjs                        |  466 ++
 e2e/benchmark/workflows.test.mjs                   |  223 +
 e2e/fixtures/datastore.ts                          |  546 ++
 e2e/fixtures/epic-external.ts                      |  290 +
 e2e/fixtures/epic-telegram.ts                      |  442 ++
 e2e/fixtures/error-form-nodes.ts                   |  218 +
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import-exports/ai-agent.json  |   60 +
 .../library-import-exports/data-shaping.json       |   49 +
 .../library-import-exports/flow-control.json       |   67 +
 e2e/fixtures/library-import-exports/http-stub.json |   46 +
 .../library-import-exports/scheduled-tick.json     |   38 +
 .../library-import-exports/unsupported-node.json   |   37 +
 .../library-import-exports/webhook-echo.json       |   50 +
 e2e/fixtures/library-import.ts                     |  181 +
 e2e/fixtures/live-backend.ts                       |  263 +
 e2e/fixtures/n8n-live.ts                           |  284 +
 e2e/fixtures/pack-convert-driver.go                |    2 +-
 e2e/helpers/stub.ts                                |   11 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/dashboard-lists.spec.ts                  |  187 +
 e2e/tests/datastore-pg.spec.ts                     |  257 +
 e2e/tests/datastore.spec.ts                        |  376 ++
 e2e/tests/epic-acceptance.spec.ts                  |  769 +++
 e2e/tests/i18n.spec.ts                             |   62 +
 e2e/tests/library-import.spec.ts                   |  363 ++
 e2e/tests/live-backend-api.spec.ts                 |  386 ++
 e2e/tests/live-backend-datastore.spec.ts           |  282 +
 e2e/tests/live-backend-http-auth.spec.ts           |   94 +
 e2e/tests/live-backend-queue.spec.ts               |  213 +
 e2e/tests/live-backend-webhook.spec.ts             |  401 ++
 e2e/tests/n8n-compare.spec.ts                      | 1073 ++++
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |   11 +-
 go.mod                                             |    4 +-
 go.sum                                             |    4 +
 internal/ai/agent.go                               |  104 +-
 internal/ai/agent_output_test.go                   |    2 +-
 internal/ai/ai.go                                  |   47 +-
 internal/ai/ai_test.go                             |  134 +-
 internal/ai/fromai_test.go                         |    2 +-
 internal/ai/maf/runtime.go                         |    2 +-
 internal/ai/maf/runtime_test.go                    |    2 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  146 +-
 internal/ai/openai_test.go                         |  147 +-
 internal/ai/outputschema.go                        |   16 +
 internal/api/auth_test.go                          |   28 +-
 internal/api/cors_test.go                          |  136 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |  114 +-
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_csv_test.go                |    2 +-
 internal/api/datastores_test.go                    |  480 +-
 internal/api/embed_confinement_test.go             |  352 ++
 internal/api/embed_datastore_test.go               |  258 +
 internal/api/embed_defaults_test.go                |  150 +
 internal/api/embed_test.go                         |   35 +-
 internal/api/events_test.go                        |   97 +-
 internal/api/handlers/admin.go                     |  678 +++
 internal/api/handlers/admin_admin_test.go          |  834 +++
 internal/api/handlers/auth.go                      |  237 +-
 internal/api/handlers/auth_test.go                 |  422 ++
 internal/api/handlers/credentials.go               |   73 +-
 internal/api/handlers/datastores.go                |  411 +-
 internal/api/handlers/datastores_csv.go            |   86 +-
 internal/api/handlers/datastores_csv_test.go       |  147 +
 internal/api/handlers/embed.go                     |   82 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  332 +-
 internal/api/handlers/idempotency.go               |  115 +
 internal/api/handlers/interop.go                   |  148 +-
 internal/api/handlers/nodes.go                     |  134 +-
 internal/api/handlers/problem.go                   |  129 +
 internal/api/handlers/resume.go                    |    8 +-
 internal/api/handlers/resume_test.go               |   12 +-
 internal/api/handlers/schedules.go                 |   56 +-
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/tenants.go                   |    6 +-
 internal/api/handlers/workflows.go                 |  351 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    8 +-
 internal/api/idempotency_test.go                   | 1004 ++++
 internal/api/import_diagnostics_test.go            |  142 +
 internal/api/list_pagination_test.go               |  221 +
 internal/api/middleware/auth.go                    |   86 +-
 internal/api/middleware/auth_test.go               |  436 ++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  147 +
 internal/api/middleware/cors_test.go               |  235 +
 internal/api/middleware/embed.go                   |   65 +-
 internal/api/middleware/embed_test.go              |  122 +
 internal/api/middleware/loginlimit.go              |  216 +
 internal/api/middleware/loginlimit_test.go         |  171 +
 internal/api/middleware/sessioncache.go            |  156 +
 internal/api/node_types_test.go                    |  109 +-
 internal/api/node_visibility_test.go               |  544 ++
 internal/api/openapi_security_test.go              |  196 +
 internal/api/ready_fleet_test.go                   |  358 ++
 internal/api/routes.go                             |   65 +-
 internal/api/server.go                             |  176 +-
 internal/api/server_test.go                        |    4 +-
 internal/api/tenant_delete_test.go                 |  386 ++
 internal/api/workflow_history_test.go              |    2 +-
 internal/api/workflows_test.go                     |  187 +-
 internal/auth/auth_test.go                         |   44 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |  134 +-
 internal/binary/binary_test.go                     |  219 +-
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 ++
 internal/cli/cli_test.go                           |  248 +
 internal/cli/client.go                             |  397 ++
 internal/cli/client_test.go                        |  320 ++
 internal/cli/command.go                            |  139 +
 internal/cli/command_test.go                       |  302 +
 internal/cli/config.go                             |  258 +
 internal/cli/config_test.go                        |  749 +++
 internal/cli/context.go                            |  318 ++
 internal/cli/context_test.go                       |  236 +
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   84 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 +
 internal/cli/openapi.go                            |  202 +
 internal/cli/openapi_contract_test.go              |  411 ++
 internal/cli/output.go                             |  116 +
 internal/cli/output_test.go                        |  246 +
 internal/cli/sse.go                                |  151 +
 internal/cli/sse_test.go                           |  149 +
 internal/cli/verbs_api.go                          |  315 ++
 internal/cli/verbs_api_test.go                     |  820 +++
 internal/cli/verbs_auth.go                         |  289 +
 internal/cli/verbs_credential.go                   |  146 +
 internal/cli/verbs_credential_test.go              |  119 +
 internal/cli/verbs_datastore.go                    |  209 +
 internal/cli/verbs_datastore_test.go               |  215 +
 internal/cli/verbs_exec.go                         |  413 ++
 internal/cli/verbs_exec_test.go                    |  436 ++
 internal/cli/verbs_node.go                         |  292 +
 internal/cli/verbs_node_test.go                    |  196 +
 internal/cli/verbs_pack.go                         |  143 +
 internal/cli/verbs_pack_test.go                    |  195 +
 internal/cli/verbs_run.go                          |  257 +
 internal/cli/verbs_run_test.go                     |  314 +
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +
 internal/cli/verbs_system_test.go                  |  182 +
 internal/cli/verbs_tenant.go                       |   86 +
 internal/cli/verbs_tenant_test.go                  |  116 +
 internal/cli/verbs_workflow.go                     |  592 ++
 internal/cli/verbs_workflow_test.go                |  425 ++
 internal/conditions/conditions.go                  |  455 +-
 internal/conditions/conditions_test.go             |  197 +-
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  323 ++
 internal/config/config.go                          |  489 +-
 internal/config/config_test.go                     |  104 +-
 internal/config/embed_branding_test.go             |  192 +
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 +
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   70 +-
 internal/credentials/credentials_test.go           |   62 +-
 internal/credentials/external.go                   |    4 +-
 internal/credentials/external_test.go              |    6 +-
 internal/credentials/redirect_test.go              |  141 +
 internal/credentials/registry.go                   |   83 +-
 internal/credentials/vault.go                      |    2 +-
 internal/database/database.go                      |    2 +-
 internal/database/database_test.go                 |    2 +-
 internal/database/migrate.go                       |   94 +-
 internal/database/migrate_test.go                  |  140 +-
 internal/database/prefix_test.go                   |    4 +-
 internal/database/tenant_columns_test.go           |  309 +
 internal/database/webhook_route_backfill_test.go   |  491 ++
 internal/datastore/catalogue.go                    |  164 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/column_tenant_test.go           |  152 +
 internal/datastore/concurrency.go                  |  224 +-
 internal/datastore/concurrency_test.go             |  579 +-
 internal/datastore/config_bind_test.go             |    2 +-
 internal/datastore/doc.go                          |   13 +-
 internal/datastore/engine.go                       |   31 +-
 internal/datastore/engine_test.go                  |   70 +-
 internal/datastore/fleet.go                        |  361 +-
 internal/datastore/fleet_engine_test.go            |  711 +++
 internal/datastore/idents_test.go                  |    2 +-
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  245 +-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |  124 +-
 internal/datastore/trace_test.go                   |    2 +-
 internal/datastore/upsert_id.go                    |  247 +
 internal/datastore/upsert_id_test.go               |  498 ++
 internal/datetime/datetime_test.go                 |    2 +-
 internal/embed/confinement.go                      |  156 +
 internal/embed/confinement_test.go                 |  142 +
 internal/embed/embed.go                            |  249 +-
 internal/embed/embed_branding_test.go              |  164 +
 internal/embed/embed_lifetime_test.go              |  146 +
 internal/embed/embed_test.go                       |  135 +-
 internal/engine/approval.go                        |   19 +-
 internal/engine/approval_test.go                   |    4 +-
 internal/engine/authenticate.go                    |   51 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   32 +-
 internal/engine/datastore_concurrency_test.go      |  393 ++
 internal/engine/error_workflow_test.go             |  230 +
 internal/engine/export_test.go                     |   20 +
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 ++
 internal/engine/live_progress_test.go              |  187 +
 internal/engine/loopstate_test.go                  |  183 +
 internal/engine/multiproc.go                       |  285 +
 internal/engine/multiprocess_test.go               |  682 ++-
 internal/engine/runner.go                          | 1736 ++++--
 internal/engine/runner_test.go                     | 1489 ++++-
 internal/engine/service.go                         |  832 ++-
 internal/engine/service_test.go                    |  407 +-
 internal/engine/subworkflow_test.go                |   24 +-
 internal/engine/tenant_visibility_test.go          |  379 ++
 internal/engine/trace.go                           |    2 +-
 internal/engine/trace_persist_test.go              |  159 +
 internal/engine/trace_test.go                      |   28 +-
 internal/engine/wait_service.go                    |  225 +-
 internal/engine/wait_service_test.go               |  578 +-
 internal/engine/worker_test.go                     |   24 +-
 internal/events/events.go                          |    2 +-
 internal/events/events_test.go                     |    2 +-
 internal/execution/records.go                      |   17 +-
 internal/execution/redact_datastore_test.go        |    2 +-
 internal/execution/redact_test.go                  |    2 +-
 internal/expression/doc.go                         |  107 +-
 internal/expression/evaluator.go                   |  938 +++
 internal/expression/expression.go                  |  591 +-
 internal/expression/expression_test.go             |   58 +-
 internal/expression/functions.go                   |  219 -
 internal/expression/globals.go                     |  565 ++
 internal/expression/luxon.go                       |  320 ++
 internal/expression/methods.go                     | 1217 ++++
 internal/expression/parity_test.go                 | 1041 ++++
 internal/expression/parser.go                      |  824 +++
 internal/expression/roots.go                       |  365 +-
 internal/guardrails/compile_scope_test.go          |  440 ++
 internal/guardrails/licence_boundary_test.go       |    4 +-
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 +
 internal/idempotency/idempotency.go                |  432 ++
 internal/idempotency/idempotency_test.go           | 1120 ++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 +
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   40 +-
 internal/interop/n8n/gowa.go                       |  190 +
 internal/interop/n8n/gowa_test.go                  |   89 +
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++
 internal/interop/n8n/n8n.go                        |  486 +-
 internal/interop/n8n/n8n_test.go                   |  258 +-
 internal/interop/n8n/parameters.go                 | 2802 ++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |    8 +-
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++
 internal/loadoptions/datastores.go                 |    2 +-
 internal/loadoptions/datastores_test.go            |    6 +-
 internal/loadoptions/loadoptions.go                |   17 +-
 internal/loadoptions/loadoptions_test.go           |   10 +-
 internal/loadoptions/redirect_test.go              |  117 +
 internal/loadoptions/schema.go                     |    2 +-
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   28 +-
 internal/loadoptions/workflows.go                  |    2 +-
 internal/node/registry.go                          |   35 +-
 internal/node/registry_bench_test.go               |  112 +
 internal/node/registry_test.go                     |    6 +-
 internal/node/visibility.go                        |  347 ++
 internal/node/visibility_test.go                   |  796 +++
 internal/nodepack/author.go                        |    6 +-
 internal/nodepack/author_test.go                   |   47 +-
 internal/nodepack/convert.go                       |    8 +-
 internal/nodepack/convert_test.go                  |   10 +-
 internal/nodepack/loaddir.go                       |    8 +-
 internal/nodepack/loaddir_test.go                  |   16 +-
 internal/nodepack/nodepack.go                      |   30 +-
 internal/nodepack/startcase_test.go                |    2 +-
 internal/nodepack/trigger.go                       |   18 +-
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   25 +-
 internal/nodepack/visibility_test.go               |  262 +
 internal/property/locator_test.go                  |    4 +-
 internal/property/mapper_test.go                   |    2 +-
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   36 +-
 internal/repository/auth.go                        |  392 +-
 internal/repository/auth_admin_test.go             |  477 ++
 internal/repository/auth_test.go                   |    8 +-
 internal/repository/claim_lease_test.go            |  266 +
 internal/repository/claim_wake_test.go             |   27 +-
 internal/repository/credentials.go                 |  107 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/execution_retention.go         |    2 +-
 internal/repository/execution_retention_test.go    |   15 +-
 internal/repository/executions.go                  |  550 +-
 internal/repository/idempotency.go                 |  360 ++
 internal/repository/idempotency_test.go            |  615 ++
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 +
 internal/repository/models.go                      |   82 +-
 internal/repository/models_test.go                 |  151 +-
 internal/repository/postgres_execution_test.go     |   30 +-
 internal/repository/prefix_test.go                 |   16 +-
 internal/repository/schedule_list_test.go          |  188 +
 internal/repository/schedules.go                   |  114 +-
 internal/repository/subworkflow_activation_test.go |  234 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   58 +-
 internal/repository/tenant_rows.go                 |  283 +
 internal/repository/tenant_rows_test.go            |  420 ++
 internal/repository/waits.go                       |   20 +-
 internal/repository/waits_test.go                  |   12 +-
 internal/repository/webhooks.go                    |  280 +-
 internal/repository/webhooks_delivery_test.go      |  147 +
 internal/repository/webhooks_test.go               |  464 ++
 internal/repository/workflow_history.go            |   16 +-
 internal/repository/workflow_history_test.go       |   10 +-
 internal/repository/workflow_list_test.go          |  196 +
 internal/repository/workflows.go                   |  257 +-
 internal/routing/executor.go                       |   12 +-
 internal/routing/request.go                        |    8 +-
 internal/routing/response.go                       |    4 +-
 internal/routing/routing.go                        |    2 +-
 internal/routing/routing_test.go                   |   10 +-
 internal/runcode/runcode_test.go                   |    2 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   99 +-
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   56 +-
 internal/scheduler/item.go                         |    2 +-
 internal/scheduler/rule_test.go                    |    2 +-
 internal/scheduler/scheduler.go                    |    2 +-
 internal/scheduler/scheduler_test.go               |   81 +-
 internal/sqlbuild/sqlbuild.go                      |    2 +-
 internal/sqlbuild/sqlbuild_test.go                 |   15 +-
 internal/sqlguard/attack_test.go                   |    2 +-
 internal/sqlguard/sqlguard_test.go                 |    2 +-
 internal/sqlnode/guard_test.go                     |    4 +-
 internal/sqlnode/internal_test.go                  |    4 +-
 internal/sqlnode/policy_test.go                    |    4 +-
 internal/sqlnode/sqlnode.go                        |    6 +-
 internal/sqlnode/sqlnode_test.go                   |    2 +-
 internal/tenantpurge/completeness_test.go          |  368 ++
 internal/tenantpurge/doc.go                        |  120 +
 internal/tenantpurge/docs_test.go                  |  115 +
 internal/tenantpurge/harness_test.go               |  614 ++
 internal/tenantpurge/purge.go                      |  412 ++
 internal/tenantpurge/purge_test.go                 |  507 ++
 internal/web/dist/index.html                       |    1 -
 internal/web/embed.go                              |  459 +-
 internal/web/embed_test.go                         |  335 +-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    4 +-
 internal/webhook/form.go                           |  262 +
 internal/webhook/form_test.go                      |  169 +
 internal/webhook/jwt.go                            |  144 +
 internal/webhook/jwt_test.go                       |  212 +
 internal/webhook/lifecycle.go                      |    6 +-
 internal/webhook/lifecycle_test.go                 |   10 +-
 internal/webhook/request_lifecycle.go              |  431 +-
 internal/webhook/request_lifecycle_test.go         |  411 ++
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++
 internal/webhook/route_label_test.go               |  172 +
 internal/webhook/shape.go                          |  328 +-
 internal/webhook/shape_test.go                     |  180 +-
 internal/webhook/webhook.go                        |  920 ++-
 internal/webhook/webhook_test.go                   |  991 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |  105 +-
 internal/workflow/compiler_test.go                 |    2 +-
 internal/workflow/compiler_visibility_test.go      |  280 +
 internal/workflow/document.go                      |    4 +
 internal/workflow/document_test.go                 |    8 +-
 internal/workflow/typeversion_test.go              |    2 +-
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/ai.go                                        |  555 +-
 nodes/ai_mcp_test.go                               |   10 +-
 nodes/ai_ollama_test.go                            |   16 +-
 nodes/ai_test.go                                   |  890 ++-
 nodes/ai_tools_test.go                             |    8 +-
 nodes/annotation.go                                |    6 +-
 nodes/apostrophe_live_test.go                      |    4 +-
 nodes/assignments.go                               |   53 +-
 nodes/bindings_test.go                             |   10 +-
 nodes/code.go                                      |    8 +-
 nodes/code_test.go                                 |   10 +-
 nodes/conditions.go                                |    4 +-
 nodes/core.go                                      |   14 +-
 nodes/database.go                                  |   10 +-
 nodes/database_test.go                             |   18 +-
 nodes/datastore.go                                 |  140 +-
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  425 +-
 nodes/datastore_tool_test.go                       |   12 +-
 nodes/datetime.go                                  |   90 +-
 nodes/datetime_test.go                             |  169 +-
 nodes/embedscope.go                                |  217 +
 nodes/embedscope_test.go                           |  288 +
 nodes/error_workflow.go                            |  227 +
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   54 +-
 nodes/executors_test.go                            |   78 +-
 nodes/flow.go                                      |   10 +-
 nodes/flow_test.go                                 |   10 +-
 nodes/http.go                                      |  375 +-
 nodes/http_test.go                                 |  347 +-
 nodes/jscode.go                                    |    6 +-
 nodes/jscode_test.go                               |    6 +-
 nodes/loop.go                                      |  166 +-
 nodes/mysql_v2.go                                  |   10 +-
 nodes/mysql_v2_test.go                             |   10 +-
 nodes/pgvector.go                                  |    8 +-
 nodes/pgvector_test.go                             |   54 +-
 nodes/postgres_v2.go                               |   16 +-
 nodes/postgres_v2_test.go                          |   10 +-
 nodes/presentation_test.go                         |   39 +-
 nodes/routing.go                                   |    6 +-
 nodes/sql_options.go                               |    4 +-
 nodes/sql_options_live_test.go                     |   62 +-
 nodes/sql_options_test.go                          |    6 +-
 nodes/sqlite_attach_test.go                        |    8 +-
 nodes/subworkflow.go                               |  168 +-
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram.go                                  |    6 +-
 nodes/telegram_download.go                         |    9 +-
 nodes/telegram_lifecycle.go                        |   13 +-
 nodes/telegram_test.go                             |   18 +-
 nodes/transform.go                                 |   38 +-
 nodes/transform_test.go                            |   14 +-
 nodes/unsupported.go                               |   22 +-
 nodes/wait.go                                      |  227 +-
 nodes/webhook.go                                   |  490 +-
 packs/gowa/GAPS.md                                 |   42 +
 packs/gowa/README.md                               |    7 +
 packs/telegram/telegram.go                         |    8 +-
 packs/telegram/telegram_test.go                    |   20 +-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   93 +-
 packs/waha/waha_test.go                            |  411 +-
 packs/waha/webhook-lifecycle.json                  |   14 +
 pkg/sdk/example/echo/main.go                       |    2 +-
 pkg/sdk/sdk_test.go                                |    2 +-
 pkg/sdk/wasm_exec_test.go                          |    2 +-
 scripts/check-coordinates.sh                       |   87 +
 scripts/config-reference.go                        |    2 +-
 scripts/config-reference_test.go                   |    2 +-
 scripts/generate-api-reference.mjs                 |   47 +-
 scripts/smoke-cli.sh                               |  228 +
 scripts/smoke-dev.sh                               |   27 +
 scripts/smoke-postgres.sh                          |   22 +
 sdk/CHANGELOG.md                                   |   33 +-
 sdk/LICENSE                                        |  202 +
 sdk/README.md                                      |  175 +-
 sdk/RELEASING.md                                   |  188 +
 sdk/examples/host-page/README.md                   |   61 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   37 +-
 sdk/examples/reference-host/tenant.html            |    3 +
 sdk/package.json                                   |   17 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++
 sdk/scripts/check-package.mjs                      |  315 ++
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +
 sdk/scripts/release.mjs                            |  149 +
 sdk/src/browser.ts                                 |   40 +-
 sdk/src/generated/models.ts                        | 1123 +++-
 sdk/src/http.ts                                    |   59 +-
 sdk/src/server.ts                                  |  703 ++-
 sdk/test/browser.test.ts                           |   43 +
 sdk/test/datastore-live.test.mjs                   |  100 +
 sdk/test/operation-coverage.test.mjs               |   61 +-
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 +
 sdk/test/release.test.mjs                          |  390 ++
 sdk/test/server.test.ts                            |  480 +-
 web/messages/en/auth.json                          |   36 +
 web/messages/en/canvas.json                        |   52 +
 web/messages/en/common.json                        |   27 +
 web/messages/en/credentials.json                   |   40 +
 web/messages/en/datastores.json                    |  176 +
 web/messages/en/editor.json                        |  134 +
 web/messages/en/embed.json                         |   16 +
 web/messages/en/executions.json                    |   94 +
 web/messages/en/home.json                          |   20 +
 web/messages/en/nav.json                           |   10 +
 web/messages/en/properties.json                    |  118 +
 web/messages/en/schedules.json                     |   33 +
 web/messages/en/settings.json                      |   46 +
 web/messages/en/versions.json                      |   89 +
 web/messages/en/workflows.json                     |  223 +
 web/messages/id/auth.json                          |   36 +
 web/messages/id/canvas.json                        |   52 +
 web/messages/id/common.json                        |   27 +
 web/messages/id/credentials.json                   |   40 +
 web/messages/id/datastores.json                    |  209 +
 web/messages/id/editor.json                        |  164 +
 web/messages/id/embed.json                         |   16 +
 web/messages/id/executions.json                    |   94 +
 web/messages/id/home.json                          |   20 +
 web/messages/id/nav.json                           |   10 +
 web/messages/id/properties.json                    |  123 +
 web/messages/id/schedules.json                     |   33 +
 web/messages/id/settings.json                      |   46 +
 web/messages/id/versions.json                      |   89 +
 web/messages/id/workflows.json                     |  222 +
 web/package.json                                   |    8 +-
 web/pnpm-lock.yaml                                 |  219 +
 web/project.inlang/settings.json                   |   25 +
 web/src/app.css                                    |   33 +-
 web/src/lib/api/generated/admin/admin.ts           | 1051 ++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 .../api/generated/datastore-rows/datastore-rows.ts |  110 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../api/generated/models/deleteRowsInputBody.ts    |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionNodeRunResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   24 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 .../api/generated/models/updateRowsInputBody.ts    |    2 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  129 +-
 web/src/lib/api/http.ts                            |   20 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   63 +-
 .../lib/components/dashboard/list-states.svelte    |   11 +-
 .../components/dashboard/locale-switcher.svelte    |   31 +
 .../lib/components/ui/dialog/dialog-content.svelte |    3 +-
 .../lib/components/ui/dialog/dialog-footer.svelte  |    3 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |   14 +-
 .../workflow-editor/activation-notices.svelte      |   15 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   82 +
 .../components/workflow-editor/canvas-node.svelte  |  395 +-
 .../workflow-editor/editor-controls.svelte         |   46 +
 .../workflow-editor/execution-canvas-node.svelte   |   58 +-
 .../workflow-editor/execution-canvas.svelte        |   30 +-
 .../components/workflow-editor/node-picker.svelte  |  155 +-
 .../workflow-editor/properties-panel.svelte        |  184 +-
 .../workflow-editor/property-field.svelte          |  703 ++-
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  163 +-
 .../workflow-editor/workflow-editor.svelte         |  898 ++-
 web/src/lib/dashboard/cursor-page.test.ts          |  368 +-
 web/src/lib/dashboard/cursor-page.ts               |  141 +
 web/src/lib/dashboard/execution-list.test.ts       |  248 +
 web/src/lib/dashboard/execution-list.ts            |  200 +
 web/src/lib/dashboard/nav-sections.test.ts         |   55 +-
 web/src/lib/dashboard/nav-sections.ts              |   59 +
 web/src/lib/dashboard/workflow-list.test.ts        |  119 +
 web/src/lib/dashboard/workflow-list.ts             |  136 +
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   39 +-
 web/src/lib/datastore/transfer.ts                  |   15 +-
 web/src/lib/embed/embed-editor.svelte              |  200 +-
 web/src/lib/embed/session.svelte.ts                |  113 +-
 web/src/lib/embed/session.test.ts                  |   86 +-
 web/src/lib/i18n/catalog.test.ts                   |   75 +
 web/src/lib/i18n/copy.test.ts                      |  399 ++
 web/src/lib/i18n/locale.svelte.ts                  |  107 +
 web/src/lib/i18n/locale.test.ts                    |   90 +
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/activation.ts          |    3 +-
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 +
 web/src/lib/workflow-editor/clipboard.ts           |  293 +
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  172 +-
 web/src/lib/workflow-editor/document.ts            |  309 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   64 +-
 web/src/lib/workflow-editor/execution.ts           |   58 +-
 .../lib/workflow-editor/expression-assist.test.ts  |  103 +
 web/src/lib/workflow-editor/expression-assist.ts   |  141 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   50 +
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 10556 -> 10731 bytes
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   93 +
 web/src/lib/workflow-editor/layout.test.ts         |  275 +
 web/src/lib/workflow-editor/layout.ts              |  319 ++
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   45 +-
 web/src/lib/workflow-editor/node-visual.ts         |   72 +-
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  126 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  158 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 .../lib/workflow-editor/version-history.test.ts    |   65 +-
 web/src/lib/workflow-editor/version-history.ts     |   82 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |  121 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  478 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  423 +-
 .../app/workflows/[id]/export-dialog.svelte        |   33 +-
 .../app/workflows/diagnostics-section.svelte       |   60 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   67 +-
 .../app/workflows/import-report-drawer.svelte      |   61 +
 .../(dashboard)/app/workflows/import-report.svelte |  132 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  252 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |  117 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  315 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  357 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  279 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  187 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  328 +-
 web/src/routes/+layout.svelte                      |   11 +
 web/src/routes/+page.svelte                        |   42 +-
 web/src/routes/approve/[token]/+page.svelte        |   51 +-
 web/src/routes/embed/[id]/+page.svelte             |   23 +-
 web/src/routes/login/+page.svelte                  |   76 +
 web/vite.config.ts                                 |   28 +-
 945 files changed, 178936 insertions(+), 7371 deletions(-)
```
