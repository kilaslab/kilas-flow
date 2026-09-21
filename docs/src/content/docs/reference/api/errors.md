---
title: Errors
description: How every error in this API is expressed — RFC 9457 problems and structured workflow validation issues.
sidebar:
  order: 10
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Every non-success response is an RFC 9457 problem document (`application/problem+json`). The shape is stable; the human-readable strings inside it are not — never match on `detail` text. Operations declare their failures as the `default` response, so the table below applies to the whole API; an operation that declares one status of its own (currently `get-ready`, whose `503` carries the fleet spread when a migration is outstanding, as its page describes) serves the same problem fields plus the ones that page names.

Problem fields, as served:

| Field | Type | Description |
| --- | --- | --- |
| `$schema` | string | A URL to the JSON Schema for this object. |
| `detail` | string | A human-readable explanation specific to this occurrence of the problem. |
| `errors` | array or null | Optional list of individual error details |
| `instance` | string | A URI reference that identifies the specific occurrence of the problem. |
| `status` | integer | HTTP status code |
| `title` | string | A short, human-readable summary of the problem type. This value should not change between occurrences of the error. |
| `type` | string | A URI reference to human-readable documentation for the error. |

Each entry of `errors` carries:

| Field | Type | Description |
| --- | --- | --- |
| `location` | string | Where the error occurred, e.g. 'body.items[3].tags' or 'path.thing-id' |
| `message` | string | Error message text |
| `value` |  | The value at the given location |

## Workflow validation issues

Workflow compile failure is structured: a `422` whose error details carry a `WorkflowValidationIssue` value with `code`, `nodeId`, and `connectionId`, so a client can map failures back to graph elements without parsing a message. Draft validation failure is likewise a `422` with a `body`-located detail. The `code` vocabulary grows additively; unknown codes must be rendered generically, keyed off `nodeId` when present. See `WorkflowValidationIssue` in `internal/api/handlers/workflows.go` for the source of this contract.

## Idempotency conflicts

The operations that accept an `Idempotency-Key` request header — `run-workflow`, `insert-datastore-row` and `upsert-datastore-row` — answer a reuse with a `409` whose error detail carries a typed `value.code`:

| `value.code` | Meaning | What to do |
| --- | --- | --- |
| `idempotency_key_reused` | The key was first used for a different request or resource. | Send a new key for the new request. |
| `idempotency_key_in_flight` | The first request with this key is still running. | Retry after the seconds named in the `Retry-After` response header; the key frees when that request finishes or its two-minute lease expires. |

A retry that matches the first request is answered from the recorded outcome with the `Idempotent-Replayed: true` response header and repeats no side effect; the header is absent on a first response. Above a 1 MiB recorded outcome the replay carries the durable identity instead of the full body: an inserted row replays its `Location` from the row id, and an upsert replays its `inserted` and `matched` counts with an empty `rows` list. Keys are per tenant and expire after `idempotency.retention`.
