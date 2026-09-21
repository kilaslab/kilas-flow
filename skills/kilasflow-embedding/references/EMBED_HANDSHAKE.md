# The embed handshake

A host backend mints a session for one workflow (or one datastore) and one exact
origin, hands the token to its own page, and the page passes it to the editor in
an iframe. This file is the mint, the token and the messages; what the session may
call afterwards is in SESSION_AUTHORITY.md. Sources: internal/embed/embed.go,
internal/api/handlers/embed.go, internal/api/middleware/embed.go,
sdk/src/browser.ts.

## Two credentials, two shapes

| Credential | Prefix | Made by | Spent on |
| --- | --- | --- | --- |
| Embed session | `kfe1` | `create-embed-session` | the API calls the embedded page makes, in the `X-KilasFlow-Embed` header |
| Stream ticket | `kft1` | `create-stream-ticket` | `stream-execution-events`, as the `ticket` query parameter |

The session token is `kfe1.<base64url payload>.<base64url hmac-sha256>`. Its
signature is checked **before** the payload is parsed, so a forged token is
rejected without its contents being interpreted, and expiry is checked last
(`Verify`). The signing key comes from the variable named by
`embed.signing_key_env`, defaults to `KILASFLOW_EMBED_SIGNING_KEY`, and must be
exactly 32 bytes — base64, hex or raw — or the process refuses to boot.

A token is presented in the `X-KilasFlow-Embed` header, or as an
`Authorization: Bearer` value that begins with `kfe1.`, so an API key in that
header is never mistaken for one. A request with no token passes the embed
middleware untouched — the dashboard and operator callers are unaffected. A
token presented on an instance with no embed signing key answers **503** rather
than passing silently.

## Minting a session

`create-embed-session` is a POST to `/embed-sessions` with the caller's own
credential.

| Body field | Required | Meaning |
| --- | --- | --- |
| `workflowId` | one of the two | The one workflow the session may open; the server checks it exists in the caller's tenant before minting. |
| `datastoreId` | one of the two | The one datastore the session may touch; the same ownership check applies. |
| `scopes` | yes | One or more of the five below. |
| `origin` | yes | The exact origin of the page that will frame the editor. |
| `ttlSeconds` | no | Lifetime; capped at 30 minutes. |
| `branding` | no | Validated white-label values, never markup. |

The 201 response carries the token, the embed URL (`/embed/<workflowId>`, empty
for a datastore session), `expiresAt`, the granted scopes, the origin echoed
back, and the branding **after** the deployment defaults were merged into it —
which is why a host forwards this response to its page rather than rebuilding it.

| Answer | When |
| --- | --- |
| 422 | both subjects named, no scope named, an unknown scope, an origin outside `embed.allowed_origins`, invalid branding |
| 404 | the workflow or datastore does not exist in the caller's tenant; another tenant's object reads as unknown, not as forbidden |
| 503 | `embed` is not configured on this instance (no signing key) |
| 401 | the request carried no credential at all |

## Scopes

| Scope | Grants |
| --- | --- |
| `workflow:read` | load the one workflow, list its executions, read the catalogue and the credential picker |
| `workflow:write` | save that workflow's document; implies `workflow:read` |
| `workflow:run` | run that workflow; implies `workflow:read` |
| `datastore:read` | read the one datastore's definition and rows |
| `datastore:write` | write rows in that datastore; implies `datastore:read` |

Implication stays inside one family on purpose: a workflow write that could read
across families would hand a datastore session the workflow. A scope set that
cannot be used against the session's subject is refused at mint, because a token
that passes every check and reaches nothing reads as success to the host that
asked for it (`scopesMatchSubject`). The same vocabulary is what an agent token
carries (`create-api-key` with `scopes`, optionally bound to one `workflowId` and
an `expiresAt`), and one normalisation accepts both.

## Lifetime and origin

- Fifteen minutes by default; `embed.session_ttl` (env
  `KILASFLOW_EMBED_SESSION_TTL`) changes the default, `ttlSeconds` asks per
  session, and **30 minutes is a hard cap no configuration raises**. A configured
  default outside one second to thirty minutes refuses the boot, so a bare
  `session_ttl: 900` (nanoseconds to YAML) is an error rather than a token that is
  dead on arrival. Minting again is the re-authorization point.
- Origin matching is exact `scheme://host[:port]`, lowercased, with no wildcard
  and no suffix match: "trust anything under this domain" is the loophole a
  subdomain takeover walks through.
- At mint the origin must appear in `embed.allowed_origins`, and an empty list
  allows nothing, deliberately: defaulting to everything would publish the editor
  to any site that framed it.
- On every later request carrying the token, a present `Origin` header is checked
  against the session's own origin, so a token copied into another page stops
  working there. A request that sends **no** `Origin` header skips that check.
- Pass the host page's origin — the page that frames the editor — and list every
  domain that frames it. That same allowlist is the `frame-ancestors` of the
  served editor pages (internal/api/routes.go).

## The handshake

```
  host backend                 host page                    editor iframe
  ────────────                 ─────────                    ─────────────
  create-embed-session
    { workflowId, scopes,
      origin, ttlSeconds?, branding? }
        │
        └──▶ token ──────▶ mountWorkflowEditor(options)
                                │   iframe src =
                                │   <origin of baseUrl>/embed/<workflowId>
                                │                            ┌────────────┐
                                │◀─ postMessage              │  loads,    │
                                │   kilasflow:embed-ready    │  announces │
                                │                            └────────────┘
                                │
                                ├─ postMessage kilasflow:embed-session
                                │   { token, workflowId, scopes,
                                │     branding, locale }
                                │   targetOrigin = editor's exact origin
                                │
                                │◀─ kilasflow:ready, :workflow-saved,
                                    :execution-started, :execution-finished
```

- The host waits for the editor to announce itself, and only then posts the
  token — always to an explicit target origin, never `'*'`, which would hand the
  token to whatever document occupies the frame. Every inbound message is checked
  against the editor's origin and its frame before the payload is read
  (sdk/src/browser.ts, `mountWorkflowEditor`).
- The handshake has a deadline (15 seconds by default, `handshakeTimeoutMs`),
  after which the host's `onError` is called and the iframe is left in place;
  tearing it down is the host's decision.
- The frame is sandboxed `allow-scripts allow-same-origin allow-forms` with
  `referrerpolicy origin`: it can run and reach its own origin, and cannot
  navigate the host's page away or open popups.
- A datastore session has no editor URL, so `mountWorkflowEditor` refuses it
  outright rather than mounting a frame this very token would be refused on.
