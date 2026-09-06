---
title: HTTP API
description: The full operation reference, generated from the OpenAPI document a real server produces.
sidebar:
  order: 1
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json` (`KilasFlow API`, OpenAPI `3.1.0`), not hand-written. To regenerate, run `make generate-api-reference`.

## Which reference to trust

A running server’s `/docs` page is authoritative for the instance you are talking to. It is rendered from the OpenAPI document the binary generates from the same Go types that serve the requests, and it loads nothing from the internet, so it works air-gapped. This site is authoritative for the current release.

When the two disagree, compare versions: every page here states the server version it was generated from, and the binary reports its own in the document’s `info.version` and in its `-version` flag. The raw document is at `/api/openapi.json` — also `.yaml`, and `/api/openapi-3.0.json` and `.yaml` for tools that cannot yet read 3.1.

## Operations

46 operations, all under `/api/v1`, in 9 groups. Paths, HTTP methods, and operation ids are stable — see [API contract and stability](/reference/api-contract/).

| Group | Operations | Contents |
| --- | --- | --- |
| [Workflows](/reference/api/workflows/) | 13 | Create, read, update, run, activate, and version workflows. |
| [Executions](/reference/api/executions/) | 4 | List, inspect, cancel, and follow executions on the event stream. |
| [Credentials](/reference/api/credentials/) | 8 | Credential types and stored credentials. Reads never return values. |
| [Authentication and keys](/reference/api/auth/) | 7 | Sessions, API keys, and stream tickets. |
| [Schedules](/reference/api/schedules/) | 4 | Cron-style triggers owned by a workflow. |
| [Node types](/reference/api/nodes/) | 5 | The node catalogue, icons, load-options, load-schema, and the expression grammar. |
| [Interop](/reference/api/interop/) | 2 | Import and export workflows across formats. |
| [Embed](/reference/api/embed/) | 1 | Mint a session that confines an embedded editor to one workflow. |
| [System](/reference/api/system/) | 2 | Health and readiness. |

Two behaviours are worth knowing before reading any operation page, because they are easy to misread from a signature alone. Running a workflow answers `202` and does not return results — it enqueues an execution, and you follow it on the event stream at `GET /api/v1/executions/{id}/events`. And **no operation requires authentication**; see [Security posture](/operate/security/) for what that means for how you deploy this.

## Beyond the operation pages

- [Errors](/reference/api/errors/) — every non-success response is an RFC 9457 problem document, and workflow compile failures carry a structured `WorkflowValidationIssue`.
- [Events](/reference/api/events/) — the nine server-sent event types and the `Last-Event-ID` resume behaviour, which an OpenAPI document describes the endpoint of but not the vocabulary carried over it.
- [Webhooks](/reference/api/webhooks/) — the inbound `/webhook/{route}` surface, whose behaviour is defined by the trigger node rather than by a route definition.
- [API contract and stability](/reference/api-contract/) — what `/api/v1` promises, what it explicitly does not, and how to tell whether an upgrade will break you.
