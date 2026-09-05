---
title: Security posture
description: Not yet written, but the parts an operator must know before deploying are stated here now.
sidebar:
  order: 3
---

:::caution[This page has not been written yet]
The full treatment is still to come. The facts below are not placeholders,
though — they are the ones that change how you would deploy this, so they are
stated now rather than held back until the page is finished.
:::

## The API is not authenticated

Nothing under `/api/v1` requires a credential. Listing workflows, creating them,
running them, reading executions and managing credentials are all open to
anything that can reach the port.

The embed session token is the only token mechanism, and it works in the
opposite direction to the one people usually expect: it **restricts** a request
to a single workflow and a set of scopes. A request without a token is not
rejected — it is passed through with full access.

So KilasFlow must sit behind something that authenticates, on a network that
does not expose it directly. Treat reaching the port as equivalent to being an
administrator, because it is.

## What is defended today

These are implemented, and they are worth knowing about because they shape how
the parts that *are* exposed behave.

**Credentials are encrypted at rest** with AES-256-GCM. The key is read from the
environment and never from the configuration file; without it, credential
storage is disabled rather than silently falling back to something weaker.

**Outbound requests refuse internal infrastructure by default.** The HTTP client
workflows use will not reach private networks unless that is turned on, which is
what stops a workflow being used to probe the network it runs in. Redirects,
response size and timeout are all bounded.

**Webhook routes are unguessable rather than authenticated.** The route segment
carries 16 bytes of entropy, because this endpoint is very often called by a
third party that cannot hold a credential. Every request that does not resolve
to an active binding gets the same `404` with the same body, so the endpoint
cannot be used to enumerate which workflows exist. Individual trigger types can
verify a delivery on top of that — a Telegram secret header, an HMAC over the
raw body for WAHA — and a failed check is a `401` with no run recorded.

**The bundled API reference makes no external requests.** The `/docs` page is
served with a strict Content-Security-Policy and its JavaScript is vendored into
the binary, so it works air-gapped and an embedding customer's traffic never
reaches a third party.

## What is modelled but not enforced

Every stored row carries a tenant identifier and every repository call takes a
tenant scope. Nothing resolves a tenant from a request, so there is one tenant,
named `default`. Do not rely on tenant separation for isolation between
customers today.
