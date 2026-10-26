---
title: Host events
description: "The recommended host → workflow path: publish one event, every active workflow listening for it runs. Generated from the live OpenAPI document."
sidebar:
  order: 6
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

The recommended host → workflow path: publish one event, every active workflow listening for it runs.

## Publish a host event (`publish-event`)

`POST /api/v1/events`

Publishes one event and queues an execution for every active workflow whose event trigger lists the event's type, running each workflow's active revision. This is the recommended host → workflow path: one request reaches every listening workflow, where a webhook route is per workflow and POST /workflows/{id}/run runs the latest revision and ignores activation. The response names what ran. An event nobody listens for is accepted and matches nothing. Send Idempotency-Key to make a retry safe; without one, an eventId in the body is the idempotency key, and deduplicates per workflow either way. 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency.

Parameters:

| Name | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `Idempotency-Key` | header | no | string | 1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means the body's eventId is used, when there is one. |

Request body: `application/json` — `PublishEventInputBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `202` | Accepted | `application/json` — `PublishEventResult` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
