# Benchmark summary — 2026-09-06T08:07:31.485Z

> **Preliminary: KilasFlow half only.** The n8n comparison half awaits credentials (`e2e/benchmark/README.md`, "The n8n half") and no n8n number below is real. Do not quote this as a comparison.

KilasFlow 1793d0d-dirty (e326c151afa987cb872cc927109314161bb729e6), Node v24.16.0.
Machine: Mac16,13 (Darwin 25.5.0 arm64), 10 CPUs.
Method: 30 timed runs per workflow after 5 discarded warm-ups, sequential and isolated; primary number is client-observed wall time, secondary is the server execution record (startedAt→finishedAt).
Stub latency (direct, engine excluded): p50 0.18 ms over n=30.

### KilasFlow (client wall-clock ms)

| workflow | n | min (ms) | p50 (ms) | p95 (ms) | max (ms) | mean (ms) | stddev (ms) | peak RSS (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Webhook → Set → Respond | 30 | 28.28 | 30.47 | 32.76 | 33.23 | 30.84 | 1.39 | 51.6 |
| IF branch fan-out | 30 | 26.89 | 28.87 | 30.23 | 30.9 | 28.73 | 0.88 | 53.2 |
| Paginated Split-equivalent loop (100 items, batch 10) | 30 | 60.85 | 63.04 | 64.45 | 64.51 | 62.93 | 1.01 | 145.4 |
| HTTP → Merge → data-shaping | 30 | 29.55 | 55.87 | 57.89 | 58.15 | 54.93 | 4.87 | 219.8 |
| AI-agent tool-loop (local Ollama) | 30 | 6967.19 | 23868.88 | 27476.56 | 29427.83 | 21212.65 | 7022.56 | 91.3 |

### n8n side — not measured here

- **Webhook → Set → Respond**: skipped-no-creds — live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance
- **IF branch fan-out**: skipped-no-creds — live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance
- **Paginated Split-equivalent loop (100 items, batch 10)**: skipped-no-creds — live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance
- **HTTP → Merge → data-shaping**: skipped-no-creds — live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance
- **AI-agent tool-loop (local Ollama)**: skipped-no-creds — live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance

live n8n comparison skipped: N8N_EMAIL/N8N_PASSWORD are not set (N8N_URL is http://localhost:5678); provision with `export N8N_URL="http://localhost:5678" N8N_EMAIL="operator@example.com" N8N_PASSWORD="the-operator-password"` and rerun to execute this half against the reference instance

### Expected divergences (not drift)

- **IF branch fan-out**: Trigger differs by side: KilasFlow times a manual run, n8n times a webhook delivery of the same downstream nodes. Delivery overhead is included on the n8n side only.
- **Paginated Split-equivalent loop (100 items, batch 10)**: Seed language differs (KilasFlow code node vs n8n JS code node) but both emit 100 {n} items; the timed region is the batch loop, not the seed. Trigger differs as in if-fanout.
- **HTTP → Merge → data-shaping**: Both engines call the same loopback stub, so stub latency cancels out; it is still reported separately. Trigger differs as in if-fanout.
- **AI-agent tool-loop (local Ollama)**: Model inventory differs: KilasFlow runs the pinned local Ollama model through the bench gateway; the n8n side needs the same model wired by the operator, otherwise the agent rows are not comparable and are marked as such.

### Limits

- Single-machine numbers, not SLAs. Do not compare across machines.
- Stub-backed: third-party latency is cancelled out by design; what is measured is engine overhead.
- KilasFlow manual workflows time the run API (queue → terminal record); webhook workflows time HTTP delivery → terminal record. The n8n half times webhook deliveries only.
- Peak RSS is sampled after each run via ps(1), not traced; treat it as approximate.

Raw runs: `e2e/benchmark/bench-2026-09-06T08-07-31.json`
