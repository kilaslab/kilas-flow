# Benchmark: KilasFlow runtime vs n8n (FEAT-8mymac)

On-demand, single-machine comparison. It does **not** gate merges and does
**not** run on every PR — run it when you need a number.

## Quick start

```bash
make bench-compare
```

That boots a real `bin/kilasflow` against a fresh temp database, runs every
benchmark workflow 30 times sequentially (5 warm-ups discarded), and writes:

- `e2e/benchmark/bench-<timestamp>.json` — raw per-run numbers
- `e2e/benchmark/SUMMARY.md` — Markdown table (latest run)

## What is measured

Five fixed workflows, identical logical work on both engines. All
third-party calls go to the same loopback bench server, so the comparison
measures engine overhead, not internet variance; stub latency is reported
separately in the raw file.

| key | work |
| --- | ---- |
| `webhook-set-respond` | Webhook → Set → Respond, timed over HTTP delivery |
| `if-fanout` | IF branch fan-out |
| `paginated-loop` | 100 items through a batch-10 loop (Split-in-Batches equivalent) |
| `http-merge-shape` | 2× HTTP → Merge append → Code shaping |
| `agent-tool-loop` | AI-agent tool-loop against the local Ollama model |

Per workflow the report records wall-clock per run with
`min / p50 / p95 / max` + `mean / stddev`, the server execution record
(`startedAt`→`finishedAt`) as a secondary number, and sampled peak RSS of
the server process — plus machine spec, engine versions, and date.

## Environment knobs

| variable | default | meaning |
| -------- | ------- | ------- |
| `BENCH_RUNS` | `30` | timed runs per workflow (floor: 30, per ticket contract) |
| `BENCH_WARMUP` | `5` | discarded warm-up runs (JIT/GC/cold-cache) |
| `BENCH_SKIP_AGENT` | unset | set to `1` to skip the model run (needs Ollama below) |
| `KILASFLOW_TEST_OLLAMA_BASE_URL` | `http://127.0.0.1:11434/v1` | model endpoint |
| `KILASFLOW_TEST_OLLAMA_MODEL` | `gemma4:12b-mlx` | pinned model tag |
| `N8N_URL` | `http://localhost:5678` | reference n8n (loopback default, not a secret) |
| `N8N_EMAIL` / `N8N_PASSWORD` | unset | n8n login, env only — never in files |

The agent workflow needs `ollama serve` plus `ollama pull gemma4:12b-mlx`
(or set `KILASFLOW_TEST_OLLAMA_MODEL` to a portable tag on non-Apple-Silicon).
Without the model that case records a skip naming the exact pull command.

## The n8n half (env-gated)

Without `N8N_EMAIL`/`N8N_PASSWORD` the harness records an honest skip with
the exact exports that provision it, and the KilasFlow half still runs. The
published report then says plainly that the comparison half awaits creds —
KilasFlow numbers ship as **preliminary**, never as a comparison.

With credentials, the same command signs into the reference instance,
imports the checked-in n8n JSON fixtures from `workflows.mjs`, activates
each workflow, times webhook deliveries, then deactivates and deletes them:

```bash
export N8N_URL="http://localhost:5678"
export N8N_EMAIL="operator@example.com"
export N8N_PASSWORD="the-operator-password"
make bench-compare
```

Secrets never touch files: they travel env → login request only, and the
report records `skipped-no-creds` rather than values when absent.

## Files

- `run.mjs` — the timing harness (setup in code, never eyeballed)
- `workflows.mjs` — the fixed set: native KilasFlow docs + equivalent n8n JSON
- `n8n.mjs` — env gate, skip reason, credentialed n8n runner
- `lib.mjs` — server boot, bench loopback server, stats, env capture
- `summarise.mjs` — raw JSON → `SUMMARY.md` (also standalone)
- `bench-*.json` — raw runs; `SUMMARY.md` — latest table
