---
title: System
description: "Health and readiness. Generated from the live OpenAPI document."
sidebar:
  order: 11
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Health and readiness.

## Liveness probe (`get-health`)

`GET /api/v1/health`

Reports that the process is up. Does not check dependencies; use /ready for that.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `HealthOutputBody` |
| `default` | Error | `application/problem+json` |

Fields of the `HealthOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `authEnabled` | yes | `boolean` | Whether the API requires a credential. When false every call is served without one, and API keys are only enforced once authentication is enabled |
| `status` | yes | `string` | Always "ok" when the process is serving traffic |
| `version` | yes | `string` | KilasFlow version |

Response (skeleton — every field, placeholder values):

```json
{
  "authEnabled": true,
  "status": "ok",
  "version": "0.1.0"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/health" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.

## Readiness probe (`get-ready`)

`GET /api/v1/ready`

Reports whether the instance can serve requests. Verifies that the database is reachable and that no datastore is waiting on a schema migration; the body carries the datastore schema-version spread, counts only. Returns 503 when either check fails: a migration outstanding carries the same datastores block in the problem document, while an unreachable database — a catalogue that cannot be read — carries none.

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `200` | OK | `application/json` — `ReadyOutputBody` |
| `503` | Not ready: the database is unreachable or a datastore migration is outstanding. The body is a problem document; a migration outstanding carries the same `datastores` block the 200 body carries, while an unreachable database carries none. | `application/problem+json` |
| `default` | Error | `application/problem+json` |

Fields of the `ReadyOutputBody` body:

| Field | Required | Type | Description |
| --- | --- | --- | --- |
| `database` | yes | `string` | Database reachability |
| `datastores` | no | `ReadyDatastores` | Datastore schema-version spread; absent when the instance has no datastore store |
| `datastores.ahead` | yes | `integer` | Datastores above the served version: a newer build migrated them and this build refuses them |
| `datastores.behind` | yes | `integer` | Datastores below the served version: a migration is outstanding |
| `datastores.schemaVersion` | yes | `integer` | The datastore schema version this build serves |
| `datastores.spread` | yes | `object` | Datastores per schema version, keyed by the version number |
| `error` | no | `string` | Why the instance is not ready, when it is not |
| `status` | yes | `string` | Overall readiness |

Response (skeleton — every field, placeholder values):

```json
{
  "database": "ok",
  "datastores": {
    "ahead": 0,
    "behind": 0,
    "schemaVersion": 1,
    "spread": {}
  },
  "error": "string",
  "status": "ok"
}
```

```sh
curl -X GET "$KILASFLOW_HOST/api/v1/ready" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
```

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
