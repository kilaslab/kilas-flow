---
title: Tenancy and the embed boundary
description: How a tenant is decided for a request, and the signed short-lived token that confines an embedded editor to one workflow on one origin.
sidebar:
  order: 7
---

KilasFlow is built to be embedded in somebody else's product, and the two ideas
on this page are what make that safe: a tenant scope that every stored row is
partitioned by, and a token that *narrows* what a request may do rather than
granting it anything.

## Tenant scope

Every tenant-facing repository operation takes a `TenantScope` as its first
argument. That is the signature rather than a convention, so a caller cannot
forget the tenant by accident.

```go
type TenantScope struct {
	ID string
}
```

It is not a universal property of the repository layer, and it would be
misleading to say so. Operations that are not acting *for* a tenant do not take
one: `ClaimNext` takes a worker ID because it claims whatever execution is next
regardless of who owns it, and then carries that row's tenant forward;
`ClaimDue` does the same for schedules; webhook routing resolves a binding from
the opaque route and *derives* the tenant from it; logging in and authenticating
an API key cannot be scoped because they are what decide the tenant. Each of
those is a deliberate entry point, not an oversight — but "unscoped repository
call" is a real category, so review one carefully rather than trusting the
signature to have caught it.

For everything a request can reach, scoping is enforced by a `WHERE` clause
rather than by a check in a handler.
The consequence you will see repeated through the code is that a resource
belonging to another tenant does not *fail a permission check*, it **does not
exist**: a sub-workflow call naming a workflow ID from another tenant is refused
as "not found", not as "forbidden". The executor never reaches into storage
itself — credentials, binaries and sub-workflow calls all go through interfaces
the runtime scopes first — which keeps tenant enforcement in one place instead of
in every node.

The one documented exception is that logging in and authenticating an API key
cannot be tenant-scoped, because those are the operations that *decide* the
tenant.

### Where the tenant comes from

A resolver runs on every request, with this precedence:

1. an **embed session**, if one is on the request;
2. the authenticated **principal**, if authentication is on and succeeded;
3. a **fallback**.

An embed session wins over an API key because it is the narrower authority of
the two.

The fallback is where a deployment's posture shows. With authentication
**disabled** — which is the default — the fallback is the tenant literally named
`default`, so an unauthenticated request gets it and a standalone installation
works with no setup. With authentication **enabled** the fallback is the empty
string, which is a tenant that owns nothing, so an unauthenticated request that
somehow reached a handler finds an empty world rather than the shared one.

## Authentication

Authentication landed recently and is **opt-in**. `auth.enabled` defaults to
`false`, and with it off nothing under `/api/v1` requires a credential — reaching
the port is equivalent to being an administrator. The server logs that fact on
every boot rather than leaving it to be discovered.

The default is off for a specific reason rather than out of laziness: turning
authentication on for an existing installation that has no accounts and no keys
would answer every request with `401` and lock its operator out. For the same
reason, enabling it with no signing key configured **refuses to start**, because
the alternative is a running server nobody can reach.

When it is on, three credential forms are accepted, tried in this order:

| Form | Shape | Used by |
| --- | --- | --- |
| Stream ticket | `kft1` prefix, single-use, 30-second ceiling | `GET /executions/{id}/events`, where a browser cannot set a header |
| API key | `kfa1_<prefix>_<secret>`, presented as a bearer token | machine callers, host backends |
| Session cookie | `kfs1` prefix, `__Host-` cookie, 12-hour default | the dashboard |

Four operations stay open because they must: `/health`, `/ready`,
`/auth/login` and `/auth/logout`. API key secrets are stored as SHA-256 hashes
and compared in constant time; passwords use PBKDF2-SHA256 at 600,000 iterations
against a decoy hash, so a login for a user that does not exist costs the same as
one that does.

The authentication middleware is scoped to the `/api/v1` prefix, because the same
mux also carries the public [webhook surface](/concepts/webhooks/) and the SPA's
static assets, and neither can present a credential.

A request carrying an embed token bypasses the API-key requirement by design —
the token is itself the credential, and it grants strictly less.

## The embed boundary

The host's backend mints a session, hands the token to its own frontend, and the
frontend passes it to the editor in an iframe. The token is the only thing that
crosses, and it is minted server-side so the host's authorization decision — *may
this user edit this workflow?* — is made where the host's data lives. KilasFlow
cannot make that decision for the host and does not try to.

### The token

```
kfe1.<base64url(payload)>.<base64url(hmac-sha256)>
```

Three parts, always. The prefix exists so the format can change without an older
token being reinterpreted under new rules. The signing key comes from the
environment variable named by `embed.signing_key_env`, defaulting to
`KILASFLOW_EMBED_SIGNING_KEY`, and must be **exactly 32 bytes** — accepted as
base64, hex or raw, and rejected at boot at any other length. It shares no key
with the authentication package; a key that signed both would let a forged value
of one kind be presented as the other.

Verification order is load-bearing and stated as such in the code: **the
signature is checked before the payload is parsed**, so a forged token is
rejected without its contents ever being interpreted. Only after a constant-time
signature comparison passes does the payload get base64-decoded and unmarshalled,
and only then is expiry checked.

The payload names one tenant, exactly one workflow, a set of scopes, one exact
origin, and its issue and expiry times.

### Scopes and the implication rule

Three scopes exist: `workflow:read`, `workflow:write` and `workflow:run`. A
session must carry at least one — a session with none could do nothing, so an
empty request is a mistake rather than a read-only default — and an unrecognised
scope is refused at mint time rather than ignored.

The only implication runs downward toward read: **`workflow:write` implies
`workflow:read`, and so does `workflow:run`.** A session that can save must be
able to load, and one that can run must be able to see what it ran. Nothing
implies write, and nothing implies run.

### Lifetime

Fifteen minutes by default, thirty minutes maximum. A request for longer is
clamped rather than refused.

An embed token travels through a host page and sits in a browser, so it is
deliberately short: a leaked one is only useful for minutes. A host that needs a
longer editing session mints another token, which is cheap and is also the point
at which the host re-checks that the user is still allowed.

### Origin matching is exact

The comparison is a string equality of normalized origins — scheme plus
lowercased host and port. There is no wildcard and no suffix match, and the
reason is written into the code: an embed token is minted for one host page, and
"trust anything under this domain" is precisely the loophole a subdomain takeover
walks through.

Two separate checks use it. At mint time the requested origin must appear in the
deployment's `embed.allowed_origins` list, and an **empty allowlist means nothing
is allowed** — defaulting to "everything" would silently publish the editor to
any site that framed it. Then on every subsequent request the request's `Origin`
header is re-checked against the session's own origin, so a token copied into
another page stops working there.

One caveat worth knowing: a request that sends **no** `Origin` header skips the
per-request check. The header is not something a browser omits for a cross-origin
request, but a non-browser client can.

### The handshake

```
  host backend                   host page                     editor iframe
  ────────────                   ─────────                     ─────────────
  POST /api/v1/embed-sessions
    { workflowId, scopes,
      origin, ttlSeconds? }
    (tenant comes from the
     caller, not the body)
        │
        └──▶ kfe1.… token ──▶ mountWorkflowEditor()
                                  │  iframe at <origin of
                                  │  baseUrl>/embed/<workflowId>
                                  │                          ┌──────────────┐
                                  │◀── postMessage           │  loads,      │
                                  │    'kilasflow:embed-ready'│  announces  │
                                  │                          └──────────────┘
                                  │
                                  ├── postMessage
                                  │   { type: 'kilasflow:embed-session',
                                  │     token }
                                  │   targetOrigin = editor's exact origin
                                  │
                                  │◀── 'ready' | 'workflow-saved'
                                  │    'execution-started' | 'execution-finished'
```

Every message is posted to an explicit target origin, never `'*'` — `'*'` would
hand the token to whatever happens to be in the frame. Every message received is
checked against `event.origin` before it is read. A handshake that does not
complete within fifteen seconds calls the caller's `onError`; the iframe is left
in place, and tearing it down is the host's decision.

### What a session may actually do

The middleware answers one question — *is this request inside this session's
authority?* — and it asks it about **what the request targets**, not about which
handler will run.

| Request | Result |
| --- | --- |
| anything under `/node-types` | needs `workflow:read` |
| `GET /credentials` | needs `workflow:read` — the editor has to offer a picker |
| `GET /workflows/{that one}` | needs `workflow:read` |
| `POST /workflows/{that one}/run` | needs `workflow:run` |
| any other write to `/workflows/{that one}` | needs `workflow:write` |
| `/workflows/{a different one}` | refused: scoped to a different workflow |
| activate, deactivate, or delete a workflow | refused outright, at any scope |
| `POST /workflows/import` | refused outright |
| `GET /executions?workflowId={that one}` | needs `workflow:read`; a different `workflowId`, or none, is refused |
| anything under `/executions/{id}` | needs `workflow:read` — see the note below |
| **anything else** | **refused** |

The `/executions/{id}` row is the one place the middleware does not settle the
question. It cannot: whether that execution belongs to the session's workflow is
a fact in the database, not in the URL. So the middleware checks the scope and
the handler checks ownership — `ownsExecution`, and an inline equivalent in the
event stream. Both `GET /executions/{id}` and its event stream reach a handler
before an embed session is told no.

That last row is the important one, and it is a genuine default arm rather than a
list of exclusions:

```go
default:
	// Listing every workflow, minting another session, managing schedules
	// or credentials: none of that belongs to an embedded editor.
	return false, "An embed session cannot use this endpoint."
```

A route added tomorrow is denied to embed sessions until somebody deliberately
permits it. That is the correct direction for a security boundary, and it is the
opposite of what an enumerate-the-forbidden approach would give.

The token is read from an `X-KilasFlow-Embed` header, or from an
`Authorization: Bearer` value **only if** it starts with `kfe1.` — so an API key
in that header is never mistaken for an embed token.

When no token is present the middleware is inert. When a token is present on an
instance with no embed signing key configured, the answer is `503` saying
embedding is not configured, rather than a silent pass — embedding is opt-in, and
with no key the endpoints say so clearly and the middleware refuses every token,
rather than the editor silently being frameable.

## Multi-tenancy today: what is and is not true

**The plumbing is complete.** Every row carries a tenant, every repository call
takes a scope, the resolver has a real precedence order, and both authentication
and embed sessions supply real tenant identifiers.

**A tenant is a row, but a thin one.** The `tenants` table holds an id, a name
and timestamps, and `users` and `api_keys` reference it with an
`ON DELETE RESTRICT` foreign key so an identity cannot outlive its tenant. There
is no per-tenant configuration and no per-tenant quota.

**Nothing creates a tenant through the API.** No operation exists for it. A
tenant comes into existence in one of three ways: the identity migration seeded
`default` plus every distinct tenant already present in `workflows` and
`credentials`; `auth.bootstrap_tenant` is ensured on every boot; or an operator
inserts a row. Workflow and credential rows, by contrast, will happily carry any
tenant string, because those tables predate the `tenants` table and do not
reference it.

**With authentication off, there is effectively one tenant**, named `default`,
because that is what the fallback resolves to. A deployment that wants real
isolation must turn authentication on and put its users in tenants.

**Tenant isolation is a `WHERE` clause, not a separate database.** Every tenant's
rows share the same tables in the same database. That is a deliberate and
ordinary design, but it means the isolation is only as good as the scoping — so
a repository method is never given a way to run unscoped, and there is no
"list everything" call for a handler to reach for by mistake.

## API operations

`POST /api/v1/embed-sessions` mints a session. `POST /api/v1/auth/login`,
`POST /api/v1/auth/logout` and `GET /api/v1/auth/me` cover the dashboard session;
`GET`, `POST` and `DELETE` on `/api/v1/api-keys` manage machine credentials; and
`POST /api/v1/stream-tickets` mints the single-use ticket a browser needs for the
event stream. See the [HTTP API reference](/reference/api/), and the
[embedding guide](/guides/embedding/) for a worked integration.

## Source

`internal/embed/embed.go` (the token format, `Session`, `Scope`, `Allows`,
`MatchesOrigin`, the lifetime constants), `internal/api/middleware/embed.go`
(`permits` and its default-deny arm), `internal/auth/` (`Principal`, sessions,
tickets, API keys), `internal/api/middleware/auth.go` (the gate and the public
operations), `internal/api/handlers/tenants.go` (the resolver's precedence),
`internal/repository/workflows.go` (`TenantScope`), `sdk/src/browser.ts`
(`mountWorkflowEditor` and the handshake).
