# Benchmark summary — 2026-09-20T12:01:21.004Z

> **The machine was not quiet.** 1-minute load average reached 36.82 (the pre-registered banner threshold is 2). Rows that failed a variance bound read inconclusive; the bounds were not relaxed.

KilasFlow 580815b-dirty (580815b7ab605d471b2846bcb8e29fd0cb4d0122), Node v24.16.0.
Machine: Apple M4 (Mac16,13, macOS 26.5, Darwin 25.5.0 arm64), 10 CPUs, 24 GiB.
n8n: 2.33.7 (image docker.n8n.io/n8nio/n8n:2.33.7@sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30, digest sha256:3989d9b8ebb77b4ee8f604519eb73e44f4384bfaa689526e0104eed79a237d30).
Power: Now drawing from 'AC Power'; thermal: Note: No thermal warning level has been recorded.
Load average (1m/5m/15m): before [31.95, 51.29, 66.83], after [36.82, 42.91, 55.32].
Ollama: 0.34.2, model gemma4:12b-mlx (digest ded7a27350032202d9e9b2a6071e8aa89959ab156771b5228f30863c741c4970).
Docker: OrbStack 29.4.0, container VM 10 CPUs / 12 GiB (shares the host CPUs).
n8n container env keys (secret and key variables dropped): N8N_DIAGNOSTICS_ENABLED, N8N_PERSONALIZATION_ENABLED, N8N_HIRING_BANNER_ENABLED, N8N_SECURE_COOKIE, GENERIC_TIMEZONE, N8N_VERSION_NOTIFICATIONS_ENABLED, N8N_TEMPLATES_ENABLED, N8N_LOG_LEVEL, NODE_VERSION, NPM_CONFIG_UPDATE_NOTIFIER, PATH, NODE_PATH, NODE_ENV, N8N_RELEASE_TYPE, SHELL.
Stub latency from inside the container: p50 3.17 ms over n=30.
Hop probe (trivial handlers): n8n /healthz p50 1.19 ms vs KilasFlow /api/v1/health p50 0.79 ms over n=30.
Method: 30 timed runs per engine per workflow after 5 discarded warm-ups, blocks of 5, ABBA order, seed 20260920.
Primary metric: client-observed HTTP response latency, request sent to full body received (same function on both engines).
Secondary metric: server execution record startedAt to finishedAt, 1 ms resolution, fetched after the run.
Stub latency (direct, engine excluded): p50 0.62 ms over n=30.

### KilasFlow (client-observed HTTP response latency, ms)

| workflow | n | min (ms) | p50 (ms) | p95 (ms) | max (ms) | mean (ms) | stddev (ms) | server p50 (ms) | peak RSS (MiB) | verdict |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Webhook → Set → Respond | 30 | 2.93 | 3.79 | 8.58 | 27.52 | 5.01 | 4.3 | 3 | 54.9 | ok |
| IF branch fan-out | 30 | 3.38 | 3.99 | 7.87 | 15.45 | 4.82 | 2.28 | 4 | 56.9 | ok |
| Paginated loop (100 items, batch 10) | 30 | 25.71 | 30.24 | 35.04 | 54.79 | 31.43 | 4.93 | 53 | 68.7 | ok |
| HTTP → Merge → shaping | 30 | 6.61 | 8.37 | 12.86 | 52.36 | 9.89 | 8 | 10 | 69 | ok |
| AI-agent tool-loop (local Ollama) | 30 | 6380.04 | 7079.08 | 7475.38 | 7803.13 | 7091.29 | 252.04 | 7081 | 55.1 | ok |
| Code node (Go/WASM vs JS) | 30 | 58.5 | 73.59 | 147.15 | 155.41 | 81.61 | 24.03 | 70 | 165.6 | ok |

### n8n (client-observed HTTP response latency, ms)

| workflow | n | min (ms) | p50 (ms) | p95 (ms) | max (ms) | mean (ms) | stddev (ms) | server p50 (ms) | peak RSS (MiB) | verdict |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Webhook → Set → Respond | 30 | 28 | 40.63 | 193.88 | 344.95 | 65.84 | 63.92 | 15 | 1076.1 | ok |
| IF branch fan-out | 30 | 29.93 | 51.9 | 144.59 | 205.45 | 68.6 | 41.07 | 21 | 1120.7 | inconclusive (robustCv 0.4087 > 0.3 and MAD 14.89ms > 0.5ms) |
| Paginated loop (100 items, batch 10) | 30 | 30.57 | 65.44 | 204.04 | 236.18 | 83.04 | 52.62 | 24 | 953.3 | inconclusive (robustCv 0.5487 > 0.3 and MAD 25.005ms > 0.5ms; drift 0.3028 > 0.2) |
| HTTP → Merge → shaping | 30 | 35.32 | 61.43 | 177.17 | 188.54 | 82.64 | 41.82 | 28 | 960.5 | inconclusive (robustCv 0.3172 > 0.3 and MAD 13.345ms > 0.5ms; drift 0.2538 > 0.2) |
| AI-agent tool-loop (local Ollama) | 30 | 6723.01 | 7057.12 | 9070.43 | 9872.81 | 7370.87 | 736.1 | 6997 | 609.7 | ok |
| Code node (Go/WASM vs JS) | 30 | 32.9 | 102.23 | 266.16 | 365.87 | 120.03 | 78.3 | 21 | 491.4 | inconclusive (robustCv 0.8331 > 0.3 and MAD 58.675ms > 0.5ms; drift 0.465 > 0.2) |

### Comparison (ratio = n8n p50 / KilasFlow p50; >1 means n8n took longer)

| workflow | KilasFlow p50 (ms) | n8n p50 (ms) | ratio n8n/KilasFlow | 95% CI | verdict |
| --- | ---: | ---: | ---: | --- | --- |
| Webhook → Set → Respond | 3.79 | 40.63 | 10.6109 | [9.1189, 12.0013] | n8n slower than KilasFlow |
| IF branch fan-out | 3.99 | 51.9 | 13.5376 | [10.4076, 16.4273] | inconclusive (n8n variance bound failed (robustCv 0.4087 > 0.3 and MAD 14.89ms > 0.5ms)) |
| Paginated loop (100 items, batch 10) | 30.24 | 65.44 | 2.2324 | [1.6076, 2.8952] | inconclusive (n8n variance bound failed (robustCv 0.5487 > 0.3 and MAD 25.005ms > 0.5ms; drift 0.3028 > 0.2)) |
| HTTP → Merge → shaping | 8.37 | 61.43 | 7.4162 | [6.6271, 9.5186] | inconclusive (n8n variance bound failed (robustCv 0.3172 > 0.3 and MAD 13.345ms > 0.5ms; drift 0.2538 > 0.2)) |
| AI-agent tool-loop (local Ollama) | 7079.08 | 7057.12 | 1.0038 | [0.9825, 1.0235] | no meaningful difference |

Gateway-attributed engine overhead (agent row, response clock minus gateway-observed model and tool time; supplementary and not variance-gated): KilasFlow 28.37 ms; n8n 194.13 ms.

### A/A negative control (KilasFlow vs KilasFlow through the same machinery)

ratio 1.0928 (95% CI [0.9973, 1.1737]) — inconclusive (CI [0.9973, 1.1737] straddles the 0.95–1.05 band edge). A CI that excludes 1 would mean the harness itself has an order or position bias.

### Supplementary row — divergent by design

The Code row is **not** identical work (KilasFlow runs compiled Go/WASM, n8n runs JavaScript) and is excluded from the headline five.

| workflow | KilasFlow p50 (ms) | n8n p50 (ms) | note |
| --- | ---: | ---: | --- |
| Code node (Go/WASM vs JS) | 73.59 | 102.23 | inconclusive (n8n variance bound failed (robustCv 0.8331 > 0.3 and MAD 58.675ms > 0.5ms; drift 0.465 > 0.2)) |

### Expected divergences (not drift)

- **Paginated loop (100 items, batch 10)**: KilasFlow answers a Respond with allIncomingItems by re-encoding the whole array once per incoming item (nodes/webhook.go), which is quadratic on this row; n8n answers once. Real engine behaviour, reported as a follow-up, not fixed here.
- **HTTP → Merge → shaping**: Both engines call the same loopback stub through the same bench server; the address differs because KilasFlow is a host process and n8n a container, so the payload carries it. KilasFlow re-checks the stub address against its SSRF policy on every dial while n8n 2.33.7 has its SSRF protection off by default; both engines stay at their defaults.
- **AI-agent tool-loop (local Ollama)**: Not identical in bytes: n8n’s Ollama chat model cannot be imported without loss (the importer forces http://localhost:11434/v1 and reports the credential it cannot carry), so KilasFlow runs a native document with the same wiring. The model is the same local Ollama on both sides but the wire protocol differs (/v1/chat/completions versus native /api/chat). No memory node on either side: a constant session key would accumulate context across every run. This row is expected to be model-bound and usually inconclusive under the uniform variance rule.
- **Code node (Go/WASM vs JS)**: Divergent by design and excluded from the identical-work claim: KilasFlow runs a compiled Go/WASM Code node while n8n runs JavaScript in its task runner. The row exists to isolate the Code cost that v1 buried inside two headline rows (29–42 ms server-side against 1 ms for the Code-free rows).

### Exclusions (and why)

- **Third-party-credentialed nodes**: an engine cannot be timed on a node whose upstream service this machine has no credential for
- **Queue mode and Postgres mode**: both engines run in their default single-process configuration; SQLite for KilasFlow
- **Throughput and concurrency**: every run is settled before the next, so this measures latency, never requests per second
- **Cold start**: warm-ups are discarded by design
- **Multi-tenant overhead**: one tenant, one workflow at a time
- **Editor latency**: browser-only micro-interactions are a different measurement

### Limits

- Single-machine comparison on a laptop, **not SLAs**. Do not compare across machines or quote these as guarantees.
- Stub-backed by design: all third-party calls go to one loopback stub, so third-party latency is cancelled out and what is measured is engine overhead. The stub is reached at 127.0.0.1 by KilasFlow and host.docker.internal by n8n; that hop difference is a stated divergence.
- The primary number is client-observed HTTP response latency, measured by one shared function on both engines (request sent to full body received). The server execution record is the secondary cross-check at 1 ms resolution.
- Percentiles are nearest-rank and the standard deviation is the population one: with N=30 the p95 is essentially the second-highest sample.
- The 95% CI is a percentile bootstrap over an i.i.d. resample of each row, which assumes the samples are exchangeable; the ABBA schedule tries to make that true and a drifting machine breaks it.
- Peak RSS is sampled after each settled run and is not like-for-like: KilasFlow is a native macOS process while n8n is a set of Linux processes inside a container VM.
- Laptop thermals and background load are recorded per run (load average, power source, thermal state); a throttled or loaded machine can fail a variance bound and the row then reads inconclusive.
- Excluded from this comparison, with reasons above: third-party-credentialed nodes, queue and Postgres modes, throughput and concurrency, cold start, multi-tenant overhead and editor latency.

Raw runs: `e2e/benchmark/bench-2026-09-20T12-01-21.json`
