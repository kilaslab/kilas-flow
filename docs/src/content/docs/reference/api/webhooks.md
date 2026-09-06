---
title: Webhooks
description: The inbound /webhook/{route} surface — routing, authentication, and delivery answers.
sidebar:
  order: 12
---

<!-- Hand-written companion to the generated reference. The OpenAPI document cannot express this surface — its route is minted per workflow, not declared — so this page is curated, not generated. -->

Inbound automation arrives at `/webhook/{route}`, outside the `/api/v1` prefix and therefore outside the [API contract](/reference/api-contract/). The route segment is minted opaquely per tenant, workflow, and trigger node when the workflow activates — it is a capability, not a name: anyone who holds it can deliver.

## Routing answers

| Request | Answer |
| --- | --- |
| Empty path (`/webhook/` with nothing after it) | `404` — no webhook path was given. |
| Unknown route, inactive or deleted workflow, or wrong method | `404` — no active workflow is bound to this webhook. All three answer identically on purpose, so the endpoint never confirms which workflows exist. |
| Routing unavailable (no bindings or no runner) | `503` — webhook routing is not available. |

Any HTTP method may deliver; the binding resolves the method together with the route, so a `POST` binding does not answer a `GET`.

## Authentication and verification

How a delivery is authenticated depends on the trigger node type, not on the route. A trigger kind may declare its own shape and verification; without a registered kind every trigger keeps the envelope shape. A request that fails verification never becomes an execution:

| Check | Answer |
| --- | --- |
| Missing or wrong credentials | `401`, with `WWW-Authenticate: Basic realm="webhook"` for basic-protected triggers. |
| Failed signature verification | `401`. |
| Body over the size limit | `413`. |

## Filtered, not failed

A trigger may also restrict which deliveries it accepts — for example, only certain event kinds from a sender. A delivery that arrives correctly but is restricted away was received properly and deliberately not acted on, so the sender is told `200` with `{"filtered": true, "reason": ...}` and stops retrying. That answer is different from an error on purpose: the delivery was fine, there was simply nothing to do.

See `Handler.ServeHTTP` in `internal/webhook/webhook.go` for the source of this contract, and the [event stream](/reference/api/events/) for what happens after a delivery becomes an execution.
