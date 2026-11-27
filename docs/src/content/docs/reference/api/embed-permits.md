---
title: Embed permit table
description: "Which operations an embed session may call, per scope, and the exact refusal for the rest. Exported from the Go rules that enforce it."
sidebar:
  order: 14
---

<!-- Generated from scripts/embed-permits/embed-permits.json by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

An [embed session](/guides/embed-sessions/) carries one or more scopes and is checked on every request, before the handler runs. This table is not written from the rules: `go run ./scripts/embed-permits` asks the code that enforces them (`EmbedPermit` in `internal/api/middleware`) about every operation in the OpenAPI document, once per scope, and a drift check fails CI when the answer changes.

Each column is a session holding that one scope, on a path that names the session’s own workflow or datastore. Scopes combine by union, so a session holding two scopes may call whatever either column allows. `workflow:write`, `workflow:run`, `workflow:publish` and `workflow:activate` each carry `workflow:read`, and `datastore:write` carries `datastore:read`; no other scope implies another. A workflow session cannot hold a datastore scope or the reverse.

What this table cannot show is what a handler adds after the gate lets a request through: an execution of another workflow answers `404`, another datastore’s id answers `404`, and a save, publish or run is refused with a `403` naming the node when the document reaches outside the session’s confinement. Those are on each operation’s page and in [Embed sessions](/guides/embed-sessions/).

## Allowed operations

| Operation | `workflow:read` | `workflow:write` | `workflow:run` | `workflow:publish` | `workflow:activate` | `datastore:read` | `datastore:write` |
| --- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| [`POST /api/v1/workflows/validate`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/workflows/{id}`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/workflows/{id}/webhooks`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`PUT /api/v1/workflows/{id}`](/reference/api/workflows/) | — | Allow | — | — | — | — | — |
| [`POST /api/v1/workflows/{id}/run`](/reference/api/workflows/) | — | — | Allow | — | — | — | — |
| [`POST /api/v1/workflows/{id}/activate`](/reference/api/workflows/) | — | — | — | — | Allow | — | — |
| [`POST /api/v1/workflows/{id}/deactivate`](/reference/api/workflows/) | — | — | — | — | Allow | — | — |
| [`GET /api/v1/workflows/{id}/versions`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/workflows/{id}/versions/{versionId}`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`POST /api/v1/workflows/{id}/versions/{versionId}/publish`](/reference/api/workflows/) | — | — | — | Allow | — | — | — |
| [`POST /api/v1/workflows/{id}/versions/{versionId}/restore`](/reference/api/workflows/) | — | Allow | — | — | — | — | — |
| [`GET /api/v1/workflows/{id}/publish-events`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/workflows/{id}/diagnostics`](/reference/api/workflows/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/executions`](/reference/api/executions/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/executions/{id}`](/reference/api/executions/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`POST /api/v1/executions/{id}/cancel`](/reference/api/executions/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`POST /api/v1/executions/{id}/retry`](/reference/api/executions/) | — | — | Allow | — | — | — | — |
| [`POST /api/v1/executions/{id}/eval`](/reference/api/executions/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/executions/{id}/events`](/reference/api/executions/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/credential-types`](/reference/api/credentials/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/credentials`](/reference/api/credentials/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/node-types`](/reference/api/nodes/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/node-types/{type}/icon`](/reference/api/nodes/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`POST /api/v1/node-types/{type}/load-options`](/reference/api/nodes/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`POST /api/v1/node-types/{type}/load-schema`](/reference/api/nodes/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/datastores/{id}`](/reference/api/datastores/) | — | — | — | — | — | Allow | Allow |
| [`POST /api/v1/datastores/{id}/rows`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`GET /api/v1/datastores/{id}/rows/{rowId}`](/reference/api/datastores/) | — | — | — | — | — | Allow | Allow |
| [`GET /api/v1/datastores/{id}/rows`](/reference/api/datastores/) | — | — | — | — | — | Allow | Allow |
| [`PUT /api/v1/datastores/{id}/rows`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`POST /api/v1/datastores/{id}/rows/upsert`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`POST /api/v1/datastores/{id}/rows/increment`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`DELETE /api/v1/datastores/{id}/rows`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`POST /api/v1/datastores/{id}/rows/import`](/reference/api/datastores/) | — | — | — | — | — | — | Allow |
| [`GET /api/v1/datastores/{id}/rows/export`](/reference/api/datastores/) | — | — | — | — | — | Allow | Allow |
| [`POST /api/v1/workflows/convert`](/reference/api/interop/) | Allow | Allow | Allow | Allow | Allow | — | — |
| [`GET /api/v1/workflows/{id}/export`](/reference/api/interop/) | Allow | Allow | Allow | Allow | Allow | — | — |

## Refused operations

Every other operation is refused to every embed scope, with the sentence below. The status is `403` unless noted.

| Operation | Refusal |
| --- | --- |
| [`POST /api/v1/workflows`](/reference/api/workflows/) | 403 “This embed session cannot list workflows.” |
| [`GET /api/v1/workflows`](/reference/api/workflows/) | 403 “This embed session cannot list workflows.” |
| [`DELETE /api/v1/workflows/{id}`](/reference/api/workflows/) | 403 “This embed session cannot delete a workflow.” |
| [`POST /api/v1/workflows/{id}/duplicate`](/reference/api/workflows/) | 403 “This embed session cannot duplicate workflows.” |
| [`POST /api/v1/credential-types/{type}/test`](/reference/api/credentials/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/credentials`](/reference/api/credentials/) | 403 “This embed session cannot manage credentials.” |
| [`GET /api/v1/credentials/{id}`](/reference/api/credentials/) | 403 “This embed session cannot use this endpoint.” |
| [`PUT /api/v1/credentials/{id}`](/reference/api/credentials/) | 403 “This embed session cannot manage credentials.” |
| [`DELETE /api/v1/credentials/{id}`](/reference/api/credentials/) | 403 “This embed session cannot manage credentials.” |
| [`POST /api/v1/credentials/{id}/test`](/reference/api/credentials/) | 403 “This embed session cannot manage credentials.” |
| [`POST /api/v1/credentials/{id}/oauth/start`](/reference/api/credentials/) | 403 “This embed session cannot manage credentials.” |
| [`POST /api/v1/auth/login`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/auth/logout`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/auth/me`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/api-keys`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/api-keys`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`DELETE /api/v1/api-keys/{id}`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/stream-tickets`](/reference/api/auth/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/schedules`](/reference/api/schedules/) | 403 “This embed session cannot manage schedules.” |
| [`POST /api/v1/schedules`](/reference/api/schedules/) | 403 “This embed session cannot manage schedules.” |
| [`PUT /api/v1/schedules/{id}`](/reference/api/schedules/) | 403 “This embed session cannot manage schedules.” |
| [`DELETE /api/v1/schedules/{id}`](/reference/api/schedules/) | 403 “This embed session cannot manage schedules.” |
| [`GET /api/v1/variables`](/reference/api/variables/) | 403 “This embed session cannot manage variables.” |
| [`POST /api/v1/variables`](/reference/api/variables/) | 403 “This embed session cannot manage variables.” |
| [`PUT /api/v1/variables/{id}`](/reference/api/variables/) | 403 “This embed session cannot manage variables.” |
| [`DELETE /api/v1/variables/{id}`](/reference/api/variables/) | 403 “This embed session cannot manage variables.” |
| [`POST /api/v1/events`](/reference/api/host-events/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/expression-grammar`](/reference/api/nodes/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/datastores`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`POST /api/v1/datastores`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`PUT /api/v1/datastores/{id}`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`DELETE /api/v1/datastores/{id}`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`POST /api/v1/datastores/{id}/columns`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`PUT /api/v1/datastores/{id}/columns/{name}`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`DELETE /api/v1/datastores/{id}/columns/{name}`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`POST /api/v1/datastores/{id}/clear`](/reference/api/datastores/) | 403 “This embed session cannot manage datastores.” |
| [`GET /api/v1/tenants`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/tenants/{id}`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`PATCH /api/v1/tenants/{id}`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`DELETE /api/v1/tenants/{id}`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/tenants/{id}/users`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants/{id}/users`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants/{id}/users/{userId}/disable`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants/{id}/users/{userId}/enable`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants/{id}/users/{userId}/password`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/tenants/{id}/api-keys`](/reference/api/tenants/) | 403 “This embed session cannot use this endpoint.” |
| [`POST /api/v1/workflows/import`](/reference/api/interop/) | 403 “This embed session cannot import workflows.” |
| [`POST /api/v1/embed-sessions`](/reference/api/embed/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/health`](/reference/api/system/) | 403 “This embed session cannot use this endpoint.” |
| [`GET /api/v1/ready`](/reference/api/system/) | 403 “This embed session cannot use this endpoint.” |
