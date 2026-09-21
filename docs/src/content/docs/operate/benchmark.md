---
title: Runtime benchmark vs n8n
description: How the KilasFlow-vs-n8n runtime comparison is produced, what the latest numbers are, and why they are not SLAs.
---

Users migrating from n8n ask one question first: is it faster, slower, or
the same? This page records how that question is answered with a number
instead of prose — and the limits of the number.

## How it runs

The benchmark is on-demand only. It never gates merges and never runs on
every PR: single-machine timings plus third-party-adjacent variance make it
a bad gate and a good investigation tool.

```bash
make bench-test      # the method's own unit tests: no engine, no Docker
make bench-compare   # the benchmark itself
```

`make bench-compare` builds `bin/kilasflow`, boots it against a fresh
temporary database, starts a **throwaway n8n container** pinned by tag and
digest, and runs every row through an ABBA schedule on both engines —
warm-ups discarded, `N=30` timed deliveries per engine per workflow, settled
after every run — then writes raw per-run JSON plus a Markdown summary.

- **Docker is required** for the comparison half. The container is published
  on `127.0.0.1` only, is labelled, has no volume and no privileges, and is
  removed on normal exit and on `SIGINT`/`SIGTERM`. Without Docker (or the
  pinned image) the n8n half records an honest skip and the summary is
  published as **PRELIMINARY** — never as a comparison.
- **No hand-typed credential.** The throwaway owner account is created
  through n8n's own setup with a password generated in memory and used only
  for the login call. It is never written to a file, a ticket, a log, an
  argv or a commit; `scan-secrets.mjs` searches the live values across the
  tree and the temp dirs before anything is committed.
- **An external instance is optional**, not required: `BENCH_N8N=external`
  points the same harness at an n8n the operator already runs
  (`N8N_URL`/`N8N_EMAIL`/`N8N_PASSWORD` in the environment).

| knob | default | meaning |
| ---- | ------- | ------- |
| `BENCH_RUNS` / `BENCH_WARMUP` | `30` / `5` | timed runs per engine per workflow (floor 30, rounded up to a whole block) and discarded warm-ups |
| `BENCH_SMOKE` | unset | `1` = four samples, one warm-up, output under the OS temp dir — a harness check that writes nothing into the repository |
| `BENCH_N8N` | `managed` | `managed` starts the throwaway container; `external` uses `N8N_URL`/`N8N_EMAIL`/`N8N_PASSWORD`; `off` publishes the KilasFlow half as PRELIMINARY |
| `BENCH_SKIP_AGENT` | unset | `1` skips the model row (the harness check uses this) |
| `BENCH_CONTROL` | unset | `aa` runs the A/A negative control on rows 1–2 |
| `BENCH_CONTROL_FILE` | unset | hands the control's measured block to the main run, so the published raw file carries its own control |
| `BENCH_BIND_HOST` | `127.0.0.1` | bench-server bind address; see the non-OrbStack note in `e2e/benchmark/README.md` |
| `N8N_IMAGE` | the pinned tag+digest | override the image under test (its digest is recorded) |

Three method rules keep the number honest:

1. **One stub for both engines.** Every third-party call goes to the same
   loopback server, reached at `127.0.0.1` by KilasFlow and
   `host.docker.internal` by the container, so the comparison measures
   engine overhead and not internet variance. Stub latency is reported
   separately, from the host and from inside the container.
2. **One timing function, two clocks.** The headline is client-observed HTTP
   response latency — request sent to full body received, the same function
   on both engines. The server execution record is the secondary
   cross-check, at 1 ms resolution, and both are published per row.
3. **Pre-registered bounds.** The variance bounds, ratio band, bootstrap seed
   and quiet-load threshold are fixed in `method.mjs` before any n8n number
   exists and are never tuned afterwards. A row that fails a bound is
   published as `inconclusive` with the reason; the machine's load average
   is printed beside every number, and a loaded machine raises a banner. The
   A/A control (KilasFlow against KilasFlow through the identical machinery)
   is published with the comparison: its confidence interval is the
   harness's minimum detectable effect, and a control interval that excludes
   1 would mean the harness itself has an order or position bias.

## Latest results

<!-- bench:table:start -->
| workflow | KilasFlow p50 (ms) | n8n p50 (ms) | ratio n8n/KilasFlow | 95% CI | verdict |
| --- | ---: | ---: | ---: | --- | --- |
| Webhook → Set → Respond | 3.79 | 40.63 | 10.6109 | [9.1189, 12.0013] | n8n slower than KilasFlow |
| IF branch fan-out | 3.99 | 51.9 | 13.5376 | [10.4076, 16.4273] | inconclusive (n8n variance bound failed (robustCv 0.4087 > 0.3 and MAD 14.89ms > 0.5ms)) |
| Paginated loop (100 items, batch 10) | 30.24 | 65.44 | 2.2324 | [1.6076, 2.8952] | inconclusive (n8n variance bound failed (robustCv 0.5487 > 0.3 and MAD 25.005ms > 0.5ms; drift 0.3028 > 0.2)) |
| HTTP → Merge → shaping | 8.37 | 61.43 | 7.4162 | [6.6271, 9.5186] | inconclusive (n8n variance bound failed (robustCv 0.3172 > 0.3 and MAD 13.345ms > 0.5ms; drift 0.2538 > 0.2)) |
| AI-agent tool-loop (local Ollama) | 7079.08 | 7057.12 | 1.0038 | [0.9825, 1.0235] | no meaningful difference |

KilasFlow 580815b-dirty (580815b7ab605d471b2846bcb8e29fd0cb4d0122), n8n 2.33.7, 30 runs per engine after 5 discarded warm-ups, 2026-09-20T12:01:21.004Z.

#### Supplementary row — divergent by design, not part of the identical-work claim

| workflow | KilasFlow p50 (ms) | n8n p50 (ms) | note |
| --- | ---: | ---: | --- |
| Code node (Go/WASM vs JS) | 73.59 | 102.23 | inconclusive (n8n variance bound failed (robustCv 0.8331 > 0.3 and MAD 58.675ms > 0.5ms; drift 0.465 > 0.2)) |
<!-- bench:table:end -->

Two attempts have been run and **both raw files are published**
(`bench-2026-09-20T11-37-55.json`, then `bench-2026-09-20T12-01-21.json`).
The table above is the later attempt, as published, never a cherry-picked
subset. Neither attempt ran on a quiet machine: the 1-minute load average
was in the 30s and rose above 120 during the first attempt, against a
pre-registered quiet threshold of 2. Read the two verdicts above as the only
publishable rows and everything else as "not measured well enough to say" —
the rows that read `inconclusive` failed a pre-registered variance bound,
and the bound was not relaxed to produce a verdict.

## Machine, versions and conditions

- **Machine**: Mac16,13 (Apple M4), 10 CPUs, 24 GiB, macOS 26.5
  (Darwin 25.5.0 arm64), on AC power, no thermal warning recorded.
- **KilasFlow**: build `580815b` at commit
  `580815b7ab605d471b2846bcb8e29fd0cb4d0122`. The `-dirty` suffix in the
  version string comes from `git describe` seeing the harness's own
  uncommitted working files; the tracked product tree matched the recorded
  commit (`dirty: false` in the raw file).
- **n8n**: 2.33.7, image
  `docker.n8n.io/n8nio/n8n:2.33.7@sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30`,
  read from the running instance, `arm64`.
- **Docker**: OrbStack 29.4.0; the container VM has 10 CPUs and 12 GiB and
  shares the host CPUs, so host load is not neutral between the two engines —
  the native process and the container do not degrade equally.
- **Ollama**: 0.34.2, model `gemma4:12b-mlx`
  (digest `ded7a27350032202d9e9b2a6071e8aa89959ab156771b5228f30863c741c4970`).
- **Load average** (1/5/15 min) for the published attempt: `31.95, 51.29,
  66.83` at the start and `36.82, 42.91, 55.32` at the end; the raw file
  records the same figures per workflow.
- **Stub latency** (the one loopback dependency, engine excluded): p50
  `0.62 ms` from the host and `3.17 ms` from inside the container. Hop probe
  on trivial handlers: p50 `0.79 ms` (KilasFlow `/api/v1/health`) against
  `1.19 ms` (n8n `/healthz`), which is the published-port proxy floor under
  every n8n row.
- **A/A control** (KilasFlow against KilasFlow through the same schedule):
  ratio `1.0928`, 95% CI `[0.9973, 1.1737]` — `inconclusive`. The interval
  brackets 1, so the harness shows no order or position bias, but its width
  is the harness's minimum detectable effect on this machine and it is wide
  enough to swallow the 0.95–1.05 band.
- **Agent row**: measured on both engines; one KilasFlow delivery was
  retried because the model answered without issuing the tool call.

## Expected divergences (not drift)

- **Go/WASM Code node vs JavaScript.** KilasFlow compiles a Code node to
  Go/WASM at activation; n8n runs JavaScript in its task runner. The
  `code-node` row is therefore **supplementary** and excluded from the
  identical-work claim.
- **Fan-out scheduling.** n8n schedules the branches of a fan-out
  sequentially under `executionOrder: v1`; KilasFlow's scheduler differs.
- **SSRF defaults.** KilasFlow's guard checks every outbound dial. n8n's SSRF
  protection is off by default in the pinned version; both engines run at
  their defaults and the asymmetry is a stated divergence, not a tuning knob.
- **Per-request tenant work.** KilasFlow resolves a tenant and redacts the
  payload at the webhook boundary on every request; n8n does not.
- **Per-item Respond.** KilasFlow's Respond node loops per incoming item and
  re-encodes the whole array for `allIncomingItems` — quadratic on the
  100-item row — while n8n answers once. Reported here as a follow-up
  candidate; the benchmark does not fix it.
- **Execution records.** Both engines persist a per-run execution record.
- **Substrate.** KilasFlow is a native macOS process on loopback; n8n is a
  Linux container behind OrbStack's published-port proxy. The hop probe
  quantifies that floor, and peak RSS is not like-for-like — a native
  process against summed container processes.
- **Agent row wiring.** The agent row uses the real local Ollama model
  through the same byte-transparent bench gateway on both sides, but over
  different wire protocols (`/v1/chat/completions` vs native `/api/chat`),
  and with no memory node on either side.

## Superseded numbers

The `bench-2026-09-06*.json` raw files are **method v1** and are not
comparable with these results: v1's timed region contained a 25 ms poll
cadence and an API call (its webhook row read p50 30.47 ms against a 1 ms
server record), and two of its rows were dominated by the Go/WASM Code node
it buried inside them. They stay in `e2e/benchmark/` as history.

The first method-v2 attempt (`bench-2026-09-20T11-37-55.json`) is published
too, but two of its fields are unusable: its `loadAverage.before` was sampled
at the same instant as its `after`, and its gateway model/tool durations were
recorded when the response headers arrived rather than when the last byte was
read, which charged streamed model time to the engine. Both are fixed in the
harness; only the later attempt's `SUMMARY.md` and table are published.

## Limits

- Single-machine comparison on a laptop, **not SLAs**. Do not compare across
  machines, and do not quote these as guarantees.
- Every row was measured while the machine was running other work (a load
  average in the 30s, spiking above 120). Rows that failed a pre-registered
  variance bound are published as `inconclusive`; the table is what the
  method allowed, not what a quiet machine would show, and a quiet-machine
  rerun is still owed.
- Stub-backed by design: all third-party calls go to one loopback stub, so
  third-party latency is cancelled out and what is measured is engine
  overhead.
- The primary number is client-observed HTTP response latency; the server
  execution record is the secondary cross-check at 1 ms resolution, and n8n
  persists its record after it responds.
- Percentiles are nearest-rank and the standard deviation is the population
  one, so with `N=30` the p95 is essentially the second-highest sample.
- The 95% confidence interval is a percentile bootstrap over an i.i.d.
  resample of each row; the ABBA schedule tries to make the samples
  exchangeable, and a drifting machine breaks that assumption.
- Excluded, with reasons: third-party-credentialed nodes, queue and Postgres
  modes, throughput and concurrency, cold start, multi-tenant overhead and
  editor latency.
- Peak RSS is sampled after every settled run; it is not like-for-like
  between a native process and a container.

## Reproducing it

From a clean checkout: install Docker, Node and Ollama, `ollama pull
gemma4:12b-mlx`, then run `make bench-test` (fast, no engine) followed by
`make bench-compare` on an otherwise idle machine, on AC power. The run takes
ten to fifteen minutes, most of it the agent row's model calls. It writes
`e2e/benchmark/bench-<timestamp>.json` and `e2e/benchmark/SUMMARY.md`; the
table above is regenerated from the newest raw file with
`node e2e/benchmark/summarise.mjs <raw> --docs docs/src/content/docs/operate/benchmark.md`.
`e2e/benchmark/README.md` documents every knob and the non-OrbStack
arrangement.
