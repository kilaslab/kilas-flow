---
title: Embedding and multi-tenancy
description: Not yet written. What exists today for embedding the editor in a host application.
---

:::caution[This page has not been written yet]
:::

## What will be here

A worked guide to mounting the editor inside a host application: minting a
session for one of your users, mounting the iframe, receiving execution events,
and doing all of that for more than one tenant at once. It will be written
against a reference host application rather than in the abstract.

## What exists today

The mechanism is implemented and tested, so the summary below is accurate even
though the guide is not written.

The host's backend calls `POST /api/v1/embed-sessions` and gets back an opaque
signed token. The token names one tenant, exactly one workflow, a set of scopes
(`workflow:read`, `workflow:write`, `workflow:run`), and one exact origin — no
wildcards. It expires; the default lifetime is fifteen minutes and the ceiling
is thirty. Signing uses a key from `KILASFLOW_EMBED_SIGNING_KEY`, and with no
key set, embedding is off and every token is refused.

The host's frontend passes that token to `mountWorkflowEditor` from
`@kilasflow/sdk/browser`, which creates the iframe, performs an origin-checked
handshake and posts the token to the editor's exact origin.

:::caution[Read this before you design around it]
The embed token **narrows** authority; it never grants it. A request that
arrives with no token at all is passed through with full access, because the API
has no authentication of its own. Embedding is therefore a way to confine a
browser session to one workflow, not a way to secure the server. Anything
reachable by your users must be in front of your own authentication.

Relatedly, tenancy is present in the data model — every row is tenant-scoped —
but nothing resolves a tenant from a request yet, so a running server has one
tenant called `default`. A guide to multi-tenant embedding cannot be written
honestly until that is true.
:::

## What to read in the meantime

`sdk/README.md` documents the SDK's surface, and `sdk/examples/host-page/` is a
small runnable host application. The Go side is `internal/embed`, whose doc
comment explains the token format and why each field is in it.
