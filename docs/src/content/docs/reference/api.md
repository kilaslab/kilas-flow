---
title: HTTP API
description: Not yet generated. Where to get an authoritative API reference today.
sidebar:
  order: 1
---

:::caution[This reference has not been generated into this site yet]
:::

## What will be here

The full operation reference, generated into this site from the OpenAPI document
the server produces — not hand-written, and not a checked-in copy of the
specification that could fall behind it.

Alongside it, the two things OpenAPI cannot express: the server-sent event
vocabulary an execution emits, and the webhook surface, whose behaviour is
defined by the trigger node rather than by a route definition.

## Where the authoritative reference is today

**A running server publishes its own.** Open `/docs` on any instance. That page
is rendered from the OpenAPI document the binary generates from the same Go
types that serve the requests, so it describes that instance exactly. It loads
nothing from the internet, so it works air-gapped.

The raw document is at `/api/openapi.json` — also `.yaml`, and
`/api/openapi-3.0.json` and `.yaml` for tools that cannot yet read 3.1.

When this page exists, the two will not compete: a running server's `/docs` is
authoritative for the instance you are talking to, and this site is
authoritative for the current release.

## The shape of it

Thirty-five operations, all under `/api/v1`, in eight groups: workflows,
executions, credentials, schedules, node types, embed sessions, n8n import and
export, and the two system endpoints. Outside that prefix the server also serves
`/docs`, the OpenAPI document, the inbound `/webhook/{route}` surface, and the
editor.

Two behaviours are worth knowing before reading any generated reference, because
they are easy to misread from a signature alone. Running a workflow answers
`202` and does not return results — it enqueues an execution, and you follow it
on the event stream at `GET /api/v1/executions/{id}/events`. And **no operation
requires authentication**; see [Security posture](/operate/security/) for what
that means for how you deploy this.
