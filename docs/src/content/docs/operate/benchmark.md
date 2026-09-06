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
make bench-compare
```

The harness (`e2e/benchmark/run.mjs`) boots a real `bin/kilasflow`
against a fresh temporary database, executes a fixed set of five workflows
30 times each — sequentially and isolated, after discarded warm-ups — and
writes raw per-run JSON plus a Markdown summary table. The five workflows
cover webhook→Set→respond, IF fan-out, a paginated batch loop, HTTP→Merge→
data-shaping, and one AI-agent tool-loop against the local Ollama model.

Three methodology rules keep the number honest:

1. **Same stub.** All third-party calls go to the same loopback server for
   both engines, so the comparison measures engine overhead, not internet
   variance. Stub latency is reported separately.
2. **Two clocks.** Client-observed wall time is primary; the server
   execution record (`startedAt`→`finishedAt`) is secondary and
   cross-checked, never eyeballed in a browser.
3. **No fake half.** The n8n side runs only with `N8N_EMAIL`/`N8N_PASSWORD`
   from the environment. Without them the report says plainly that the
   comparison half awaits creds and publishes the KilasFlow half as
   preliminary — never as a comparison.

## Latest results

See `e2e/benchmark/SUMMARY.md` in the repository for the latest table with
full numbers, environment (machine spec, KilasFlow commit, n8n version,
date), and per-workflow notes. The headline from the last run:

- Webhook→Set→respond and IF fan-out complete in the low tens of
  milliseconds client-observed on Apple Silicon.
- The HTTP→Merge→shaping workflow is dominated by the two stub round-trips
  plus first-run sandbox compilation; warm-ups absorb the compile cost.
- The 100-item batch-10 loop is sub-second after warm-up.
- The agent tool-loop is model-bound (tens of seconds per run against the
  local 12B model) — engine overhead is noise next to inference.

## Limits

- Single-machine comparisons, not SLAs. Do not compare numbers across
  machines or quote them as guarantees.
- Stub-backed by design: third-party latency is cancelled out, so what is
  measured is engine overhead.
- KilasFlow manual workflows time the run API (queue → terminal record);
  webhook workflows time HTTP delivery → terminal record. The n8n half
  times webhook deliveries only — the trigger asymmetry is noted per row.
- Peak RSS is sampled after each run via `ps(1)`, not traced; approximate.
- Reproduce it with `make bench-compare`; the README in `e2e/benchmark/`
  documents every environment knob.
