---
title: Metrics
description: The opt-in Prometheus endpoint, every series it serves, and how to bound the tenant label.
sidebar:
  order: 3.5
---

KilasFlow can expose a Prometheus endpoint with the series an operator needs
to size a deployment and spot a struggling one: how much work is running, how
deep the queue is, what is failing and for whom. It is **off by default** and
served on **its own listener, never on the public API** — the series carry
tenant labels and queue depths, which are the operator's to share, not the
API's.

```yaml
metrics:
  enabled: true
  host: 127.0.0.1   # the default; bind an internal interface for a scraper on another host
  port: 9090
  tenant_label: off # off | on | top
  top_tenants: 10   # only read when tenant_label is top
```

Or from the environment: `KILASFLOW_METRICS_ENABLED=true`,
`KILASFLOW_METRICS_HOST`, `KILASFLOW_METRICS_PORT`,
`KILASFLOW_METRICS_TENANT_LABEL`, `KILASFLOW_METRICS_TOP_TENANTS`. The
[configuration reference](/operate/configuration-reference/) lists every key.

The endpoint is the Prometheus text exposition format, served at `/` on the
listener, and runs in **every process role** — including a `--role worker`
process, deliberately: the workers are where executions actually run, and an
endpoint that only existed on the API process would report a split
deployment's least interesting half. Scrape each process; the gauges are
per-process (see below) and the counters and histograms sum cleanly across
them.

## Every series

| Series | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `kilasflow_executions_started_total` | counter | `trigger` | Executions that began running. |
| `kilasflow_executions_finished_total` | counter | `trigger`, `status` | Executions that reached a terminal state (`succeeded`, `failed`, `cancelled`). |
| `kilasflow_execution_claim_seconds` | histogram | `trigger` | How long an execution sat queued before a worker claimed it. |
| `kilasflow_node_run_seconds` | histogram | `node_type` | How long one node ran, by node type — the slow step in a workflow shows up here, not in the execution total. |
| `kilasflow_webhook_requests_total` | counter | `outcome` | Inbound webhook requests: `accepted` (queued, deduplicated, or served a hosted page), `filtered` (the trigger's own filter or the ignore-bots option — a 200 on purpose), `refused` (turned away: unroutable, unauthenticated, oversized, or a queue failure). |
| `kilasflow_execution_queue_depth` | gauge | — | Executions waiting for a worker, across every tenant. Read from the database at scrape time; omitted from a scrape whose query failed rather than rendered as zero. |
| `kilasflow_executions_running` | gauge | — | Executions **this process** is running right now. Per-process: sum across processes for a deployment total. |
| `kilasflow_execution_worker_capacity` | gauge | — | The `execution.max_concurrent` worker pool size this process was started with. The denominator of the running gauge. |

Every series name starts with `kilasflow_`. New series and label values are
added in minor releases; a scraper must ignore names it does not recognise.

## The tenant label

The executions and webhook series can carry a `tenant` label, which is a
**cardinality decision only the operator can make**: a deployment with three
tenants pays nothing for it, and one with fifty thousand pays on every
series, every scrape. Three modes:

- `off` (the default) — no tenant label at all.
- `on` — a label per tenant, unbounded. Use it when the tenant count is
  small and known.
- `top` — a label for the `top_tenants` busiest tenants by observation
  count, and every other tenant folded into `tenant="other"`. Use it when
  the count is large: the output is bounded however many tenants the
  deployment serves, and the fold happens at scrape time, so the busy
  tenants keep their label as traffic shifts.

An unroutable webhook request is counted with an empty `tenant` value: it
belongs to no tenant, and the series says so rather than guessing.

## Example alerts

Queue backing up while workers sit at their ceiling — add workers or raise
`execution.max_concurrent`:

```promql
# The queue has been deeper than 50 for five minutes and the workers are the reason.
kilasflow_execution_queue_depth > 50
  and on() (sum by (instance) (kilasflow_executions_running) / sum by (instance) (kilasflow_execution_worker_capacity) > 0.9)
```

Failing executions, per trigger:

```promql
# More than 5% of runs have failed for ten minutes.
sum by (trigger) (rate(kilasflow_executions_finished_total{status="failed"}[10m]))
  / sum by (trigger) (rate(kilasflow_executions_finished_total[10m])) > 0.05
```

A webhook endpoint turning its senders away:

```promql
# Refusals are the majority of what the surface answers.
sum by (instance) (rate(kilasflow_webhook_requests_total{outcome="refused"}[5m]))
  / sum by (instance) (rate(kilasflow_webhook_requests_total[5m])) > 0.5
```

Claim latency — work waiting too long for a worker:

```promql
# Half of claims wait longer than five seconds.
histogram_quantile(0.5, sum by (le) (rate(kilasflow_execution_claim_seconds_bucket[10m]))) > 5
```

## Security posture

The listener is the boundary. Keep it on loopback for a scraper on the same
host, or bind an internal interface and firewall it — a series with
`tenant_label: on` is a directory of who is busiest, and the queue depth is
capacity information an operator may not want public. There is no
authentication on the endpoint by design: adding one would couple a scraper's
credentials to the operator's secrets store, and the network layer is where
an internal endpoint belongs.
