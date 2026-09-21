# Benchmark: KilasFlow runtime vs n8n (FEAT-8mymac)

On-demand, single-machine comparison. It does **not** gate merges and does
**not** run on every PR — run it when you need a number.

## Quick start

```bash
make bench-test      # the method's own unit tests (no engine, no Docker)
make bench-compare   # the benchmark itself
```

`make bench-compare` boots a real `bin/kilasflow` against a fresh temp
database, starts the managed throwaway n8n container, runs every benchmark
row through the ABBA schedule on both engines, and writes:

- `e2e/benchmark/bench-<timestamp>.json` — raw per-run numbers
- `e2e/benchmark/SUMMARY.md` — Markdown tables (latest run)

A smoke run writes **nothing into the repository**:

```bash
BENCH_SMOKE=1 node e2e/benchmark/run.mjs                     # both engines
BENCH_SMOKE=1 BENCH_N8N=off BENCH_SKIP_AGENT=1 node e2e/benchmark/run.mjs
```

It uses four samples per engine, one discarded warm-up, and an output
directory under the OS temp dir. A run fails loudly if
`webhook-set-respond` client p50 reaches 25 ms — the poll cadence method v1
timed — because that means the artefact is back inside the timed region.
Investigate it; never relax the ceiling.

A second operator needs only Docker, Node, Go (to build `bin/kilasflow`),
Ollama and the model: no credential is configured by hand. If Docker or the
pinned image is unavailable the n8n half records an honest skip and the
summary stays PRELIMINARY.

## Method (version 2)

Method version 1 is superseded and its raw files (`bench-2026-09-06*.json`)
are **not comparable** with version 2. What changed and why:

- **One timing function.** v1 timed `deliver → poll for a new execution →
  wait for terminal`, so every sample carried a 25 ms sleep and an API call
  inside the timed region; its webhook row read p50 30.47 ms against a 1 ms
  server record. v2 times only the request and the reading of the full
  response body (`deliverTimed` in `method.mjs`), on both engines.
- **The same work.** Rows 1–4 are one hand-authored n8n workflow JSON each,
  run by n8n and imported into KilasFlow from that same object, so the graph
  cannot drift between the two sides. v1 kept two hand-written copies and its
  HTTP→Merge fixture was not the same work at all.
- **Full-body equivalence.** Every run, warm-ups included, is checked against
  the row's declared body, not just an item count and one spot value.
- **ABBA and settling.** Blocks of five, `A B B A …`, and every run is
  settled (untimed) before the next so a post-response tail cannot overlap the
  next timed request.
- **Pre-registered bounds.** `method.mjs` fixes the variance bounds
  (`robustCv ≤ 0.30` or `MAD ≤ 0.5 ms`, median-based `drift ≤ 0.20`), the
  ratio band (`0.95–1.05`), the bootstrap seed and the quiet-load threshold
  before any n8n number exists. A row that fails a bound is published as
  `inconclusive` with the reason; a bound is never relaxed after seeing data.

`BENCH_CONTROL=aa` runs KilasFlow against KilasFlow through the identical
machinery as an A/A negative control: if the harness has an order or position
bias, that is where it shows.

## Latest run

The published comparison has been run **twice**, and both raw files are kept:

| attempt | raw file | outcome |
| ------- | -------- | ------- |
| 1 | `bench-2026-09-20T11-37-55.json` | one verdict (webhook row), the rest inconclusive or "no meaningful difference" |
| 2 | `bench-2026-09-20T12-01-21.json` | the file `SUMMARY.md` and the docs table are generated from |

Attempts are never cherry-picked: the summary always comes from the newest
raw file. Neither attempt ran on a quiet machine — the 1-minute load average
sat in the 30s and reached 123 during the first attempt, against the method's
pre-registered quiet threshold of 2 — so any row that failed a pre-registered
variance bound is published as `inconclusive`, and the summary carries a
not-quiet banner. A quiet-machine run is still owed.

The first attempt also predates two harness fixes, so two of its fields are
not usable: its `environment.loadAverage.before` was sampled at the same
instant as its `after`, and its gateway-observed model and tool durations
were recorded when the response headers arrived rather than when the last
byte was read (Ollama streams, so that charged model time to the engine).

## Rows

| key | work | notes |
| --- | ---- | ----- |
| `webhook-set-respond` | Webhook → Set → Respond | identical n8n JSON on both engines |
| `if-fanout` | IF branch fan-out | identical n8n JSON on both engines |
| `paginated-loop` | 100 items through a batch-10 loop | seeded from the request payload; no Code node |
| `http-merge-shape` | 2× HTTP → Merge append → shaping | one loopback stub for both engines |
| `agent-tool-loop` | AI-agent tool-loop against the local Ollama model | native on the KilasFlow side; see below |
| `code-node` | Code node | **supplementary**: Go/WASM vs JS, divergent by design |

The Code row is excluded from the identical-work claim and reported in its own
table. The agent row is not identical in bytes: n8n's Ollama chat model cannot
be imported without loss, so KilasFlow runs a native document with the same
wiring, and the wire protocol differs (`/v1/chat/completions` versus native
`/api/chat`).

## Environment knobs

| variable | default | meaning |
| -------- | ------- | ------- |
| `BENCH_RUNS` | `30` | timed runs per engine per workflow (floor 30, rounded up to a whole number of blocks) |
| `BENCH_WARMUP` | `5` | discarded warm-up runs (JIT/GC/cold-cache) |
| `BENCH_SMOKE` | unset | `1` = 4 runs, block 2, 1 warm-up, output under the OS temp dir |
| `BENCH_N8N` | `managed` | `managed` starts the throwaway container; `external` uses `N8N_URL`/`N8N_EMAIL`/`N8N_PASSWORD`; `off` publishes the KilasFlow half as **preliminary** |
| `BENCH_SKIP_AGENT` | unset | `1` skips the model row |
| `BENCH_CONTROL` | unset | `aa` runs the A/A negative control on rows 1–2 (forces `BENCH_N8N=off`) |
| `BENCH_CONTROL_FILE` | unset | the control run writes its measured block here and the main run reads it, so the published raw file carries its own negative control |
| `BENCH_OUT_DIR` | repo (temp for smoke) | where the raw file is written |
| `BENCH_BIND_HOST` | `127.0.0.1` | bench-server bind address; see the non-OrbStack note below |
| `N8N_IMAGE` | the pinned tag+digest | override the image under test (record its digest too) |
| `N8N_URL` / `N8N_EMAIL` / `N8N_PASSWORD` | unset | external mode only; the password is a secret and comes from the environment, never a file |
| `N8N_STUB_ORIGIN` | derived | external mode only: how the operator's n8n reaches this host's bench server |
| `KILASFLOW_TEST_OLLAMA_BASE_URL` | `http://127.0.0.1:11434/v1` | model endpoint |
| `KILASFLOW_TEST_OLLAMA_MODEL` | `gemma4:12b-mlx` | pinned model tag |

The agent row needs `ollama serve` plus `ollama pull gemma4:12b-mlx` (or a
portable tag via `KILASFLOW_TEST_OLLAMA_MODEL`). Without the model the row
records `skipped-no-model` naming the exact pull command.

## The n8n half

The comparison half runs against a **managed throwaway n8n**: `run.mjs` starts
`docker.n8n.io/n8nio/n8n:2.33.7@sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30`
(a single tag-plus-digest ref — both the version and the bytes are pinned),
published on `127.0.0.1` with an ephemeral port, labelled `kilasflow.bench`
and the owning pid, with no volume and no privileges, and removed on normal
exit and on `SIGINT`/`SIGTERM`.

- **No hand-typed credential.** The owner account is created through n8n's own
  `POST /rest/owner/setup` with an ephemeral password generated in memory and
  used only for `POST /rest/login`. It is never written to a file, a ticket, a
  log, an argv or a commit. The n8n encryption key is passed to `docker run` as
  a value-less `-e N8N_ENCRYPTION_KEY` so its value never appears in an argv,
  and `recordedContainerEnv` drops it from the raw file.
- **A stranded container is swept first**, but only one that carries this
  ticket's label *and* whose recorded owner pid is dead — never a concurrent
  run, never another ticket's `kf-pg-*` scratch Postgres.
- **The client** (`n8n.mjs`, `N8nClient`) talks to n8n's own UI API for owner
  setup, login, workflow create/activate/archive/delete, paginated execution
  listing and credential creation; the API shapes were verified against the
  running 2.33.7 instance, not assumed.
- **One-command opt-out.** `BENCH_N8N=off` records `skipped-off` and the report
  says **PRELIMINARY** in plain words; a missing Docker or image is
  `skipped-no-docker`/`skipped-no-image` with the exact reason.
- **`scan-secrets.mjs`** searches the live secret values across every tracked
  and untracked file, `e2e/benchmark/`, the harness temp dirs and any directory
  given on the command line (the playwright-cli scratch dir), and fails naming
  the file only.

The agent row wires n8n's own `lmChatOllama` chat model plus a
`toolHttpRequest` tool, with no memory node, to the same local Ollama through
the same byte-transparent bench gateway KilasFlow uses, so per-run model and
tool calls are countable on both engines. If it cannot be wired honestly the
row is recorded as `needs-operator-model` with the observed reason.

## Non-OrbStack hosts

n8n in a container reaches the bench server at `host.docker.internal`, which
`startBenchServer` publishes through the container's host-gateway mapping. A
host that does not provide that name needs `BENCH_BIND_HOST` set to an address
the container can reach — **and that exposes an unauthenticated proxy to the
local Ollama on that interface**, so bind it to a private network only.

## Files

- `method.mjs` — the pre-registered method: schedule, bounds, bootstrap,
  equivalence, secret scan, the one timing function
- `method.test.mjs` / `workflows.test.mjs` / `summarise.test.mjs` /
  `n8n-container.test.mjs` / `scan-secrets.test.mjs` — `make bench-test`
- `workflows.mjs` — the fixed rows (shared n8n JSON + native documents) and
  the stub bodies the expectations are built from
- `run.mjs` — the harness: engine adapters, the schedule, settling, raw output
- `lib.mjs` — server boot, bench loopback server, stats, environment capture
- `summarise.mjs` — raw JSON → `SUMMARY.md` (also standalone, and the docs
  table between the `bench:table` markers)
- `n8n.mjs` — the n8n half: `N8nClient` over n8n's UI API and the engine adapter
- `n8n-container.mjs` — the managed throwaway container: pinned image, argv,
  readiness, stale sweep, container-side probes
- `scan-secrets.mjs` — proves no live secret reached a file
- `bench-*.json` — raw runs; `SUMMARY.md` — latest tables
