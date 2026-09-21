---
title: Tenant deletion
description: 'What DELETE /api/v1/tenants/{id} removes, in which order, and what it cannot reach.'
sidebar:
  order: 6
---

`DELETE /api/v1/tenants/{id}` removes one customer and everything kilasflow holds
for it: executions and the payload files they wrote, workflows and their
published versions, credentials and secret bindings, schedules, leased poll
cursors, webhook deliveries, routes and bindings, datastores together with the
physical tables behind them, vector rows, accounts and API keys, and finally
the tenant row.

It is **irreversible**. There is no export-before-delete, no soft delete, and no
window in which the rows are still there: the only way to recover is a database
and filesystem backup taken before the call. It also runs on the operator's
authority alone — an API key scoped to the operator tenant. Any other principal,
including a customer's own valid key, gets `403` and cannot even learn from the
answer whether the tenant exists.

The deletion is assembled from one ordered list of steps in
`internal/tenantpurge`. That order is not a preference: every position below is
forced by a foreign key, by an in-flight trigger, or by the fact that a
filesystem has no transaction to join.

## The steps, in order

| Step | Tables it empties | Why it is here |
| --- | --- | --- |
| `lock-out` | `api_keys`, `users` (an update, nothing is deleted) | The tenant cannot write while its own rows are going. A deletion that fails leaves it locked out, which is the safe direction. |
| `stop-triggers` | — | The coordinator on this process asks every remote service to forget the tenant's registrations, and its hooks resolve the tenant's credentials, so those credentials have to still exist. |
| `triggers` | `poll_cursors`, `schedules`, `webhook_deliveries`, `webhook_routes`, `webhook_bindings` | Intake is closed. A webhook arriving after this would queue an execution that the definitions step could then not delete, because `workflow_versions` refuses to lose its executions. Leased Gmail and Drive poll cursors go with the other intake rows so a replica cannot emit into a tenant that is being deleted. |
| `binaries` | — | A filesystem has no transaction to join, so it is a step of its own, and the payload directory goes before the rows that name its payloads. |
| `runs` | `execution_node_runs`, `execution_waits`, `executions`, `idempotency_keys` | Waits are deleted before executions: `execution_waits.execution_id` is `ON DELETE RESTRICT`, so the other order fails the whole transaction for any tenant with a suspended run — the ordinary case for a deletion request. |
| `definitions` | `secret_bindings`, `credentials`, `workflow_versions`, `workflow_publish_events`, `workflows` | Versions before workflows (also `RESTRICT`). Workflows are hard-deleted, not soft-deleted. |
| `sessions` | — | The in-process agent conversation memory of this process. Best effort by construction: it is not in a database. |
| `datastores` | `datastore_columns`, `datastores` | The catalogue rows and the physical tables they name, together, so no table is left with no row describing it and no row is left describing a table that is gone. |
| `vectors` | `vector_documents_384`, `vector_documents_768`, `vector_documents_1024`, `vector_documents_1536`, `vector_collections` | Each one is guarded, because a SQLite install and a PostgreSQL install without the `vector` extension have none of them. |
| `identity` | `api_keys`, `users`, `tenants` | Forced by both foreign keys. `users` and `api_keys` reference `tenants(id)` `ON DELETE RESTRICT`, so an identity cannot outlive its tenant and the tenant row goes last. It is also the record that the deletion finished. |

## Transactional behaviour

One transaction per step, not one for the whole deletion. The tenant row is
deleted in the last of them.

- On **PostgreSQL**, DDL is transactional, so a step that drops a datastore's
  physical table is atomic with the catalogue rows it deleted. `DROP TABLE`
  takes `ACCESS EXCLUSIVE` locks, which is one of the reasons the deletion is
  not one giant transaction: a long one would hold those locks across every
  other tenant's reads for the whole run.
- On **SQLite**, DDL is transactional too, and there is a single connection.
  A single deletion-wide transaction would block the entire application for its
  duration, so per-step transactions are the difference between a slow deletion
  and a stalled server.
- The `datastores` step reads the tenant's catalogue and drops exactly the
  tables that read returned, in one transaction. A datastore created while the
  step runs is therefore left whole — row, columns and physical table — and is
  the next call's work, rather than half-removed. On SQLite a writer that
  commits from another connection during the step can make that step fail
  instead of silently dropping a table it never read; the deletion is safe to
  repeat, so send it again.
- **Binaries and in-memory state cannot join a transaction at all.** A payload
  directory and an agent's conversation buffer are not rows, so no ordering of
  commits makes them atomic with the database. That is why the deletion is
  safe to repeat rather than transactional: a crash between two steps leaves a
  state the next call finishes.

## Retry semantics

Repeating the request converges. Every step only deletes what is still there, so
a second call removes whatever the first did not reach and reports zeros for
everything that is already gone.

- A deletion that fails answers `500` with a problem document naming the step it
  stopped in and saying the request can be repeated. Steps after the failure are
  not attempted.
- The tenant stays **locked out** from the first step onwards: its keys are
  revoked and its accounts are disabled before anything is deleted, so nothing
  it holds can write while its rows are going. A failed deletion therefore
  leaves the tenant unusable, and repeating the request is the way out.
- The deletion is **detached from the request context**. There is no server
  write timeout to run out, but the SDK's default request timeout is 30 seconds
  and proxies cut connections; a cancelled request would abort the in-flight
  `DELETE` of a tenant's biggest table, and every retry would abort in the same
  place. So a timeout on the client is not a stopped deletion — send the request
  again until it reports zeros.

## Request and response

```bash
curl -X DELETE https://flows.example/api/v1/tenants/acme \
  -H "Authorization: Bearer $KILASFLOW_AUTH_OPERATOR_KEY"
```

`KILASFLOW_AUTH_OPERATOR_KEY` is the default of `auth.operator_key_env` (see the
[configuration reference](/operate/configuration-reference/#authoperator_key_env)),
so a host that bootstraps the operator key writes it wherever that variable
points. With authentication disabled there is no principal at all — the
operation refuses every caller, exactly like any other operator route.

```json
{
  "tenantId": "acme",
  "tenantRemoved": true,
  "removed": {
    "api_keys": 2,
    "credentials": 3,
    "datastore_columns": 4,
    "datastores": 1,
    "execution_node_runs": 12,
    "execution_waits": 1,
    "executions": 6,
    "idempotency_keys": 2,
    "poll_cursors": 1,
    "schedules": 1,
    "secret_bindings": 1,
    "tenants": 1,
    "users": 3,
    "vector_collections": 0,
    "vector_documents_1024": 0,
    "vector_documents_1536": 0,
    "vector_documents_384": 0,
    "vector_documents_768": 0,
    "webhook_bindings": 1,
    "webhook_deliveries": 9,
    "webhook_routes": 1,
    "workflow_publish_events": 2,
    "workflow_versions": 4,
    "workflows": 2
  },
  "datastoreTables": 1,
  "binaries": { "executions": 6, "files": 11, "bytes": 2473194 }
}
```

`removed` names **every** table the deletion covers, including the ones this
call found empty — that zero is how you tell "there was nothing left" from "this
deployment does not cover that table". The four `vector_documents_*` keys are
the document tables of each embedding width; they answer `0` even where the
table does not exist (a SQLite install, or a PostgreSQL install without the
`vector` extension), because the key set is fixed and only the counts move.
`datastoreTables` counts the physical tables that were dropped and `binaries`
the payload files and bytes deleted from disk; neither is a row in a covered
table. An unknown id answers `200` with zero counts rather than `404`,
deliberately: a deletion means "remove everything keyed to this id", which also
has to clean up rows an earlier partial deletion or a stale embed session left
behind.

**How to verify a deletion finished:** send it again and read the answer.
`tenantRemoved: false` with every count at zero is the confirmation — the tenant
row and everything keyed to it are gone. Any non-zero count means rows were
still being created, and you repeat until they stop.

## Binary payloads and the table prefix

Payload files live under `<binary.root>/<tenant>/<execution>/<id>`. The
`binaries` step removes the tenant's directory, so `binary.root` has to be
storage this API process can actually reach and write. If it is a local
directory that is not shared, a payload written by a different host is not
removed by the host serving the request — see the caveats below.

Datastore tables carry the instance's `database.table_prefix`, so what the
deletion drops are the physical tables this deployment created, and physical
table names are never returned in the response (they embed internal
surrogates).

## What it does not remove

State the deletion cannot reach, and which therefore needs an operator's own
process:

- **Backups, WAL archives and replicas.** A backup taken before the deletion
  still holds the tenant.
- **Logs.** Request and purge log lines survive, and they name the tenant.
- **Embed sessions already issued.** They are stateless and keep their scopes
  until they expire (15 minutes by default, 30 at most) and can keep writing
  rows under the deleted tenant's id until then. Repeat the delete afterwards
  and expect zeros.
- **Secrets in an external secret manager.** The deletion removes the binding
  rows; the remote secret itself is untouched.
- **Registrations that `stop-triggers` could not remove.** Unregistering from
  somebody else's service is best effort and failures are logged, never fatal.
- **Triggers owned by another replica.** A Telegram poller running in a
  different API process than the one serving the request is not stopped by it.
- **Agent conversation memory held by other processes.** The in-process store
  is per process.
- **Payloads on another host's local disk** when `binary.root` is not shared
  storage.
- **A worker still executing a run**, which can recreate a payload directory
  after the `binaries` step until its next database write fails. Repeat until
  the counts are zero.

None of these make the deletion incomplete in the sense the endpoint promises —
the database and the configured payload root are cleaned — but a host that has
to answer a regulator should handle them beside it.
