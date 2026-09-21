---
title: Idempotent requests
description: Make a retried run or datastore write safe with an Idempotency-Key, and know exactly what the replay promises and what it does not.
---

A retry is a second side effect. A backend that times out waiting for
`POST /workflows/{id}/run` cannot tell "the request never arrived" from "the
execution was queued and the answer was lost", so it sends the request again —
and the workflow runs twice. The same shape appears on a row write: the retry
inserts a second row and nothing distinguishes it from the first.

Three operations accept an `Idempotency-Key` request header so a retry can
carry a fact instead of a guess. The key is claimed before the work runs and
completed with the outcome afterwards, and the record lives in the database
rather than in the process, so a retry may land on any replica and outlives a
restart.

## Sending the header

```bash
curl -sS -X POST "https://your-host/api/v1/workflows/$WORKFLOW_ID/run" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 7a1c9f0e-2f5b-4c3d-9e10-4b2f8d6a1c33" \
  -d '{"input": {"rows": 3}}'
```

The header is declared on [`run-workflow`](/reference/api/workflows/) and on
`insert-datastore-row` and `upsert-datastore-row`
([datastores](/reference/api/datastores/)), and it is optional everywhere: a
request without it behaves exactly as it did before this feature existed. A
header that is present must be 1-255 printable ASCII characters with no spaces;
anything else is a `422`. The same key may be sent as `Idempotency-Key` on any
of the three, but a key belongs to the request it was first used for (see
[What counts as the same request](#what-counts-as-the-same-request)).

An empty `Idempotency-Key` header is treated as absent, because the server
cannot tell an empty header from no header. A client that computes its key must
therefore never let one come out empty — an empty key is not a safe retry, it
is no retry protection at all.

In the host SDK the key is an option on the same three methods:

```ts
// One key per logical operation, persisted BEFORE the first attempt. A key
// minted per attempt would protect nothing.
const key = crypto.randomUUID();
await storeOperationKey(key);

const execution = await kilasflow.runWorkflow(workflowId, { rows: 3 }, { idempotencyKey: key });

// The retry carries the SAME key and returns the first outcome.
const again = await kilasflow.runWorkflow(workflowId, { rows: 3 }, { idempotencyKey: key });

await kilasflow.insertDatastoreRow(storeId, { email: 'ada@example.com' }, { idempotencyKey: insertKey });
await kilasflow.upsertDatastoreRow(storeId, filter, { tier: 'pro' }, { idempotencyKey: upsertKey });
```

The SDK generates no keys of its own — generating one per call is exactly the
mistake the header exists to prevent — and it does not surface the
`Idempotent-Replayed` marker, because a host that persisted its key before the
first attempt never needs to ask whether a response was a replay.

## What a replay returns

A retry that matches the first request is answered from the recorded outcome:

- the **same status code and the same body** as the first response,
- `Idempotent-Replayed: true`, which is absent on a first response,
- **no side effect**: no second execution is queued, no second row is written,
  and no worker is woken,
- the same `Location` on an inserted row.

Two details are worth knowing before you depend on byte-identical replays:

- A **run replay does not repeat the input echo**. The recorded outcome drops
  the redacted `input` the first response carried, so a caller's payload does
  not outlive `execution.retention` in a second table. Everything else about the
  execution record is the same.
- A recorded outcome is capped at 1 MiB. A larger one is replayed in its
  compact form: an inserted row replays its id and `Location` rebuilt from it,
  and an upsert replays its `inserted` and `matched` counts with an empty `rows`
  list. The key is never silently freed just because a response was large.

## What counts as the same request

A key is remembered together with a hash of the operation, the target id, and
the canonical request body. Canonical means JSON key order and insignificant
whitespace do not change the hash, so a client that re-serialises its body
before retrying still matches. The hash is salted per tenant, so it is not a way
to probe another tenant's requests.

Two requests are **the same** when operation, target and body all match, and
only then is the retry replayed. Send the same key with a different body, or to
a different workflow or datastore, and the server refuses rather than returning
the earlier result:

| Status | `errors[0].value.code` | What it means | What to do |
| --- | --- | --- | --- |
| `409` | `idempotency_key_reused` | This key was first used for a different request or resource. | Send a new key for the new request. Reusing the key would answer the wrong question. |
| `409` | `idempotency_key_in_flight` | The first request with this key is still running. | Wait the seconds named in `Retry-After`, then retry with the same key. |
| `422` | — | The key is not 1-255 printable ASCII characters. | Fix the key. |
| `503` | — | This instance has no idempotency store. | Retry against a healthy instance; the request was refused rather than run unprotected. |

Both conflicts are RFC 9457 problem documents located at
`header.Idempotency-Key`, with a typed `value.code` so a client branches on the
code rather than the message ([Errors](/reference/api/errors/)).

An in-flight duplicate is answered, not waited on: the server returns the `409`
immediately with `Retry-After` and holds no goroutine or connection on a guess
about how long the first request will take. `Retry-After` names whole seconds
and never more than five, because it is the two-minute lease that frees the key,
not the header — a longer wait would only push a client past the retry it is
allowed to make. In the SDK the same number arrives as
`error.retryAfterSeconds`, so the documented retry is a call rather than a
header parse:

```ts
try {
  return await kilasflow.runWorkflow(workflowId, input, { idempotencyKey: key });
} catch (error) {
  const failure = error as KilasFlowError;
  const issue = failure.problem?.errors?.[0]?.value as { code?: string } | undefined;
  if (issue?.code === 'idempotency_key_in_flight') {
    // The first request is still running: wait what it asked for and retry
    // the SAME key. A new key here would defeat the point.
    await new Promise((resolve) => setTimeout(resolve, (failure.retryAfterSeconds ?? 1) * 1000));
    return kilasflow.runWorkflow(workflowId, input, { idempotencyKey: key });
  }
  throw error;
}
```

## Scope

Keys are **per tenant**, not per credential or per session, and a tenant's
credentials share them: a host backend using its API key and an embed session it
minted deduplicate against each other. That is deliberate — a tenant is one
correctness domain, and two paths to the same write should not both land — but
it means a constant key is not a licence to relax: because every credential
shares the keyspace, a single constant key would make unrelated writes collide,
which is why the key must be unique per logical operation.

Nothing crosses a tenant boundary. Another tenant sending the first tenant's key
sees no replay, no conflict and no hint that the key exists; it gets its own
fresh execution or row, which is also why a key is not a way to observe another
tenant's work.

Upsert is idempotent in outcome here, not only in status: a replayed upsert
still reports `inserted: true` from the first attempt, where a keyless repeat
would find the row it just wrote and report `inserted: false, matched: 1`. A
host that needs to know which attempt inserted the row gets the honest answer
from the recorded outcome.

## Retention

`idempotency.retention` sets how long a completed key is remembered. The default
is `24h`, bounded to `1m`..`720h`
([configuration reference](/operate/configuration-reference/)). An **expired key
behaves as an unseen key at once** — the next request carrying it runs again —
and the sweep that deletes expired rows every ten minutes is housekeeping, not
the boundary. Long-lived keys are also the wrong shape for a retried run: the
retry is a matter of minutes, not days.

If you set `execution.retention` (off by default, which keeps every execution),
keep it at or above `idempotency.retention`: a replayed run answers with an
execution id, and an id whose execution has been pruned no longer resolves.

Only the retention is configurable. The two-minute in-flight lease, the 1 MiB
recorded-outcome cap, and the ten-minute sweep interval are fixed constants, and
the table is covered by the tenant purge like every other tenant-owned table.

## What is not guaranteed

Read this list before designing around idempotency. Each item is a real
behaviour of the current implementation, not a caveat in passing.

- **A failed request releases the key.** Only a successful (`2xx`) outcome is
  recorded; a `4xx` or `5xx` frees the key so the same key can be retried after
  the caller fixes whatever failed.
- **A crash between the side effect and the recorded outcome** leaves the key in
  flight for up to its two-minute lease. A retry after that lease runs the work
  again. A run is therefore not crash-atomic.
- **A first request that outlives the lease** — slower than two minutes — can be
  duplicated by a retry. The lease assumes a request that has not answered in
  two minutes is dead; a request that is merely slow is indistinguishable from
  one that is dead.
- **A datastore insert that commits but fails on read-back** answers `500`,
  releases the key, and a retry inserts a second row. The row exists; the key
  does not remember it.
- **A worker lease reclaim can still re-run nodes of one execution.** Idempotency
  protects the request, not the run; see
  [Execution model](/concepts/execution-model/).
- **Webhook trigger deliveries are deduplicated separately**, by the sender's
  delivery id, not by `Idempotency-Key`.
- **The other mutating routes are out of scope**: datastore CSV import,
  schedules, and everything else. A retry there is still a second request.
