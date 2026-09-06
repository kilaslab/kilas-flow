---
title: Embed
description: Mint a session that confines an embedded editor to one workflow. Generated from the live OpenAPI document.
sidebar:
  order: 8
---

<!-- Generated from the live OpenAPI document by scripts/generate-api-reference.mjs — do not edit by hand. -->

> **Generated reference — KilasFlow `0.1.0-dev`.** Produced from the OpenAPI document a real binary serves at `/api/openapi.json`, not hand-written. [Which reference to trust](/reference/api/).

Mint a session that confines an embedded editor to one workflow.

## Create an embed session (`create-embed-session`)

`POST /api/v1/embed-sessions`

Mints a short-lived, workflow-scoped token for one host origin. The host passes it to the iframe over postMessage.

Request body: `application/json` — `EmbedSessionBody` (required)

Responses:

| Status | Description | Body |
| --- | --- | --- |
| `201` | Created | `application/json` — `EmbedSessionResource` |
| `default` | Error | `application/problem+json` |

Embed: Deny — listing workflows, minting sessions, schedules, credential writes, and credential-type endpoints do not belong to an embedded editor.
