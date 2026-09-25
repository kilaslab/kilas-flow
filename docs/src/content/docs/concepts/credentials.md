---
title: Credentials
description: How a secret is stored, which half of it is encrypted, where it may be sent, and how a credential type says "put this in a header" as data rather than as Go.
sidebar:
  order: 6
---

A workflow document never contains a secret. A node references a credential by
ID, and the runtime resolves it — which is what makes a workflow export safe to
hand to somebody.

## Storage: the sealed half and the public half

A credential payload is split before it is written. `credentials.Split` walks the
credential *type's* declared fields and routes each one according to whether the
type marked it secret:

```go
func Split(typeID string, fields map[string]string) (secret, public map[string]string)
```

Only the secret half is encrypted. The rest is stored as plaintext JSON in its
own column. That is not laziness — it means a listing can show a username, a
header name or a base URL without the master key being used to satisfy an
ordinary read, which keeps the key on the smallest possible path through the
system.

Keys present in the payload but not declared by the type are silently dropped
here, because validation has already rejected them for any write that reaches
storage. Unknown keys are refused rather than stored, so a typo cannot silently
disable authentication.

The consequence for the API is a hard boundary that is easy to state:

- `List` and `Get` return the **public half only**. They deliberately cannot
  return plaintext. A secret field that holds a value comes back as a non-empty
  redaction placeholder, so an editor can tell "configured" from "empty"; one
  that was never set comes back empty. Which secrets are set is recorded beside
  the public half when the credential is written, so answering does not
  decrypt the payload. A row written before that record existed shows the
  placeholder for every secret until it is next saved.
- `Resolve` is the single path by which a plaintext secret leaves storage, and
  only the runtime calls it.

Editing a credential merges. A field the update leaves out keeps its stored
value, and so does a field sent as the redaction placeholder: the client was
never given the secret, so its silence about one is not a request to erase it.
Changing a name therefore never blanks a password, a JWT private key, or the
refresh token Connect stored. To clear a field, send it as an empty string.

The scope follows the same rule. An update that leaves `allowedDomains` out
keeps the stored scope; only an explicit empty list resets it to the type's
default — unrestricted for a generic HTTP type, the provider's hosts for a
fixed-host type, as at create (see [where a credential may be
sent](#where-a-credential-may-be-sent)). Reading an omitted scope as "reset"
would let any rename widen where the secret may be sent.

Creating a credential refuses the redaction placeholder as a value. There is no
stored value for it to stand for, so storing it would make eight bullet
characters the secret.

## Encryption

AES-256-GCM, with a fresh random nonce per encryption prepended to the
ciphertext — so two identical payloads never produce identical bytes in storage.
Decryption reports failure without distinguishing tampering from a wrong key.

The master key is exactly 32 bytes and there is no key derivation: what you
supply *is* the AES key. It is read from an environment variable whose **name**
is configured by `security.encryption_key_env`, defaulting to
`KILASFLOW_ENCRYPTION_KEY`. Base64, hex and raw bytes are all accepted, so an
operator can paste whichever form their secret manager emits.

The key never comes from the configuration file, and the reason is written into
the code: a config file is routinely committed, copied between environments, and
included in support bundles.

```sh
export KILASFLOW_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

**Credentials are optional at boot.** An installation with no key still starts
and still runs workflows; only credential operations report that storage is
unconfigured. Failing startup instead would make the key mandatory for every
user, including the one evaluating the project for ten minutes. The store refuses
every operation rather than falling back to storing plaintext.

## Where a credential may be sent

Every credential carries `allowedDomains`. What an empty list means depends on
the type:

| Type | An empty list means |
| --- | --- |
| `openAiApi` | `api.openai.com` only |
| `openRouterApi` | `openrouter.ai` only |
| `googleDriveOAuth2Api`, `gmailOAuth2` | the Google API and sign-in hosts |
| `telegramApi` | the host of the credential's own **Base URL** — `api.telegram.org` by default, or your local Bot API server |
| `wahaApi` | the host of the credential's own **Base URL** |
| `httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`, `httpQueryAuth`, `httpCustomAuth`, and any pack type with no default | any host |

A type whose service lives at one address declares it, and an empty list on that
type means that address rather than everywhere. Without it, anyone who could
edit a workflow could point an OpenAI key at a server they run and read it off
the wire. A list you type always **replaces** the default rather than adding to
it: to reach OpenAI through a gateway, list the gateway's host.

The default is resolved **wherever the scope is checked**, not written into
stored rows, so a credential saved before its type had a default is held to it
from the moment the server upgrades. A fixed list is also stored when a
credential is saved with none, the way the Google types always did, so the row
and the form show it. A default derived from a field is never stored: a stored
copy would keep naming the old host after you moved the Base URL. The API reads
every credential back with the scope it enforces, default included, and
`GET /api/v1/credential-types` carries each type's `defaultDomains` or
`defaultDomainsFrom` so an editor can say what leaving the field empty means.

Upgrading changes one behaviour on purpose. An existing OpenAI or OpenRouter
credential that a chat model node sends to a gateway or proxy through its
**Base URL**, with no allowed domains saved, is now refused at that host. Add the
gateway's host to the credential's allowed domains.

What it is compared against is the **host of the outbound request that is about
to carry the secret** — not the workflow, not the credential's own base URL. The
port is stripped, the host is lowercased, a trailing dot is trimmed, and:

- an exact entry matches that host and nothing else;
- a `*.`-prefixed entry matches any subdomain but **never the bare parent
  domain**, so scoping to `*.internal.test` does not silently authorize
  `internal.test` itself.

The check runs everywhere a credential is placed on a request, and in every one
it happens **before the secret touches the request**: the runtime's
`Request.Authenticate`, used by both the hand-written HTTP node and the
declarative routing interpreter; the AI chat model call; edit-time option
loading; the credential test endpoint; Telegram file downloads; and every
trigger lifecycle request — a pack's registration templates and the Telegram
trigger's registration and polling calls. A lifecycle request goes through the
runtime's own `Credential.CheckType` and `Credential.ScopeRequest` rather than a
copy, so it checks the credential's type, its domains against the target host,
and binds the redirect scope below, exactly as a node's request does.

### Redirects

The bound follows the request through redirects. Each hop is checked against the
credential's `allowedDomains` again, and a hop that fails stops the chain with
the last in-scope response rather than failing the call — the node sees the
`30x`. Go drops only `Authorization` and `Cookie` when a redirect changes host,
so without this an `X-Api-Key`, a WAHA key or a custom template's headers would
follow a redirect anywhere. Two more rules apply while a credential is
attached:

- **A credential that names no domains stays on the first host.** "Names no
  domains" is asked of the effective scope: an OpenAI key with an empty list is
  held to its default, `api.openai.com`, across a redirect as on the first
  request. For a generic type with an empty list, unrestricted means "wherever
  the node sends it", not "wherever a server redirects it", so a redirect may only go to the hostname of the first
  request. The port is not compared, as the domain check does not compare it, so
  a service moving between ports on the same host keeps working. A credential
  that names its domains is held to those instead, and a redirect to another
  host it names is followed.
- **No step down from `https` to `http`.** The secret would cross the network in
  the clear on the next hop. This matters even for `Authorization`, which Go
  keeps on a same-host redirect.

The redirect rules apply wherever the secret is placed on the request, not only
on the runtime's `Request.Authenticate` path: the AI chat model and embeddings
calls (which set their own `Authorization: Bearer` header), Telegram downloads,
trigger lifecycle requests and the community-node sidecar all bind the same
scope. Two operator-held secrets get the rule a credential with no domains gets
— the request stays on its first host and never steps down to `http`: the Vault
token on external-secret reads, and the client secret and refresh token a
Google OAuth token exchange or refresh posts.

That single shared implementation is deliberate. The check used to live beside
one node, and the comment on it now says why it moved: with two callers, a second
copy would be a second place for the host-scope test to be forgotten, and neither
caller is allowed its own version. A caller authenticating something that is not
an `*http.Request` goes through the exported `Credential.AllowsHost` rather than
reimplementing the wildcard matching.

This is a **narrowing on top of** the instance-wide egress policy, never a
replacement for it. A credential allowed to reach `example.com` still cannot
reach it if the deployment's outbound policy refuses the resolved address. See
[safety boundaries](/concepts/safety-boundaries/).

### A node carries only the credential types it declares

A node definition declares the credential types it uses — the HTTP Request node
the five generic HTTP types, a chat model node its provider's type. The compiler
refuses a node that attaches any other type, naming the node, the type and what
the node accepts. The runtime resolves whatever a node attaches and checks it
only against the key it was attached under, so without this an HTTP Request node
carrying `{"openAiApi": "…"}` would sign its request with the OpenAI key and send
it to whatever URL the node names. The refusal is at compile time, which gates
activation and every run; a draft still saves, so a document that carries a
stray key can be opened and fixed. A disabled node is exempt, because nothing it
carries is ever applied.

## Authentication placement is data

A credential type declares *how* it authenticates a request, rather than the code
switching on the type ID:

```go
type Authentication struct {
	Placement Placement // header | query | basicAuth | bearer | path
	Name      string    // a {{ field }} template
	Value     string    // a {{ field }} template
	User      string
	Password  string
}
```

The point is that a new credential type needs no Go change. `wahaApi` is
`{header, "X-Api-Key", "{{ apiKey }}"}` and nothing else. And a type that does
not authenticate an HTTP request at all — `postgres`, `mysql`, `sqlite` —
declares no descriptor, so application refuses it by *absence* rather than by
falling through a default branch, which is a better error anyway.

The five placements:

| Placement | Effect |
| --- | --- |
| `header` | sets a named header; the name is itself a field template |
| `query` | sets a named query parameter |
| `basicAuth` | RFC 7617 basic auth from two named fields |
| `bearer` | `Authorization: Bearer …` |
| `path` | substitutes fields into the request path |

`path` exists for Telegram, whose Bot API puts the token in the URL —
`/bot<token>/sendMessage` — which no header or query placement can express. The
request path carries the marker `{credential.accessToken}` (written into the
Telegram pack's JSON, not into Go), and the substitution happens inside the
package that already holds the secret. The alternative was exposing the token to
the expression evaluator through `$credentials`, which carries non-secret fields
only and is meant to keep carrying only those.

The `{{ field }}` templates in a descriptor are expanded from the credential's
**own fields only**, and deliberately not by the
[expression evaluator](/concepts/expressions/): a descriptor is written by
whoever declares the credential type, and the full grammar would let it read
run-time data while signing a request.

## Keeping a secret out of error text

Two credential types put their secret in the URL: `httpQueryAuth` in the query,
and `telegramApi` in the path as `/bot<token>/`. Go's transport error prints the
whole request URL, so a request to a closed port or an unreachable host used to
fail with the secret in its message — and that message became the execution's
error, the error item a "notify on failure" branch sends on, a log line and an
API answer.

Two rules keep it out, and they are independent so that either one alone holds:

- **Every outbound call cuts a transport error's URL to its scheme and host**
  before returning or logging it (`safehttp.RedactError`). The host and port
  stay, because they say which service failed; the path, the query and any
  userinfo are withheld. The HTTP node, the routing interpreter behind every
  declarative pack, edit-time option loading, WASM and JavaScript-sidecar
  packs, the AI model, MCP and embedding calls, Google, Telegram downloads,
  pack-trigger media downloads, Vault, and every trigger lifecycle request —
  registration, removal and Telegram's polling loop — all go through it.
- **A node's error is scrubbed of the secret values of every credential that
  node resolved** before the runner records it. The runner notes each
  credential the node's resolver hands out during an attempt and replaces
  those values with `[redacted]` in the error's text, in the trace row, in the
  execution's own error, and in every error item — per-item outcomes of a
  whole-batch node included. Which values count is the type's declared
  secrets, in the forms a request writes them (as written, trimmed,
  query-escaped and path-escaped, and each string inside an `httpCustomAuth`
  template); a base URL or a header name is left alone, and a value shorter
  than four characters is not replaced. The error keeps its cause, so a
  timeout still reads as a timeout.

## Testing a credential

A type may also declare a test as data — a method, a URL and the status below
which the answer counts as success (default `GET` and 400). The response body is
drained and discarded, because returning it would turn a pass/fail probe into a
general-purpose fetch.

An edit can be tested before it is saved. The form sends the redaction
placeholder for a secret it was never shown, together with `credentialId`, and
the server fills the placeholder from the stored credential. Three rules keep
that from becoming a way to read a stored secret:

- **The target must not move.** A placeholder is filled only while the edit's
  `host`, `port`, `baseUrl` and `url` match the stored values. An edit that
  changes one is refused with a 422 until the secret is typed again or the
  credential is saved: otherwise the stored password would go to whatever host
  the caller just typed.
- **The stored scope applies.** Once a stored secret is in the payload, the
  probe runs under the intersection of the stored credential's effective scope
  (its `allowedDomains`, or its type's default when none were saved) and the
  scope the request sends, so the request can narrow the scope but never widen
  it. Two scopes that share no host are refused rather than read as
  unrestricted. A payload with nothing taken from storage is the caller's own
  and runs under the scope it sends.
- **The credential is checked first.** `credentialId` must name a credential of
  the same type in the caller's tenant before anything uses it. It is also the
  key for the one-test-at-a-time slot, and an unchecked id let a random name
  per request buy a fresh slot per request.

Tests are also capped per tenant: at most four run at once across every
credential and type, and one more is answered `429`. The per-credential slot
answers `409` as before.

The tests that exist are chosen carefully. OpenRouter is probed at `/key` rather
than `/models`, because OpenRouter serves its model catalogue unauthenticated —
so `/models` answers 200 for a key that is expired or revoked, and the test would
pass on a credential that cannot complete anything.

A test answers by its own deadline (`credential.test_timeout`) whatever its probe
does, and only one test of a credential runs at a time. That claim is released
when the test answers, even if the probe underneath is still stuck. It used to be
released only when the probe returned. A SQLite driver that blocked inside its
open therefore left every later test of that credential answering 409 for the
life of the process.

## SQLite files

A `sqlite` credential names a file on the server's own disk, so where that file
may be is an operator decision, not a tenant's. Each tenant gets one directory
under `sql.sqlite_root` (default `./data/sqlite`), created on first use, and the
credential's `path` is read relative to it. `orders.db` for tenant `acme` is
`<sqlite_root>/acme/orders.db`, and `reports/q1.db` works when `reports/` exists
there. The file is created if it does not exist, but missing directories are
not. The following are refused:

- an absolute path;
- a path that leaves the directory through `..`;
- a path through a symbolic link, wherever the link points;
- anything that is not a regular file: a directory, a device, a FIFO or a socket.

Two tenants that both name `orders.db` reach two different files.

An empty `sql.sqlite_root` turns the type off: a test or a node run using a
SQLite credential is refused with a message naming the key. A single-tenant
install whose credentials already name files elsewhere can set
`sql.sqlite_unconfined: true`. That brings back the old reading: absolute, or
relative to the working directory. KilasFlow's own database and non-regular
files stay refused, and the server logs a warning at every boot while it is on.
Do not use it where tenants do not trust each other: on an unconfined install,
every tenant can open every file the server can.

## The built-in catalogue

Thirteen types ship, all declared in one place, and node packs do not add
credential types today:

| ID | For |
| --- | --- |
| `httpBasicAuth` | generic basic auth |
| `httpHeaderAuth` | any fixed-header API — the header name is a field |
| `httpBearerAuth` | generic bearer token |
| `httpQueryAuth` | any fixed-query-parameter API — the parameter name is a field |
| `httpCustomAuth` | n8n's Custom Auth: a JSON template naming several headers and query parameters at once |
| `jwtAuth` | verifying inbound JSON Web Tokens — a passphrase for HS, a PEM public key for RS, PS and ES |
| `postgres` | PostgreSQL connections from a workflow |
| `mysql` | MySQL and MariaDB connections from a workflow |
| `sqlite` | a SQLite file in the tenant's own directory (see [SQLite files](#sqlite-files)) |
| `telegramApi` | Telegram Bot API |
| `wahaApi` | WAHA |
| `openAiApi` | OpenAI |
| `openRouterApi` | OpenRouter |

Three details in that table are load-bearing rather than arbitrary. `wahaApi`'s
`baseUrl` is deliberately **not** marked secret, because a declarative pack reads
it as `{{ $credentials.baseUrl }}` to build every request and `$credentials`
exposes non-secret fields only — marking it secret would leave the pack with no
address to call. `openAiApi`'s field is spelled `apiKey` rather than the generic
`token` because that is what n8n calls it, and an imported workflow names
`openAiApi`. And `jwtAuth` reads its key material by the algorithm's family: the
`secret` field is the shared passphrase an HS token was signed with, `publicKey`
the PEM key an RS, PS or ES token's signature is checked against, and the key
type has to agree with the algorithm — a credential that declares a PEM key while
naming an HMAC algorithm is refused rather than verified against a public key
used as a shared secret. `privateKey` is kept so an n8n-shaped payload stores
unchanged; nothing on this server reads it.

Every ID and every field key is byte-identical to what an earlier version of this
catalogue held, because a stored credential row is keyed by that ID and its
sealed payload by those keys. Changing one makes an existing credential stop
resolving with no way to recover it.

A `Registry` type holds them rather than a bare map, so the catalogue validates
its entries — a type marking an undeclared field as secret is refused, as is a
duplicate ID or an unknown property kind. Unlike the
[node registry](/concepts/node-registry/), though, it is not assembled in the
composition root: it is a package-level singleton built in a variable
initialiser, which panics if the built-in types are invalid, and nothing in
startup registers a credential type. A pack cannot contribute one today.

A type states what an empty `allowedDomains` means for it. `DefaultDomains` is a
fixed list, for a service at one address (`openAiApi`, `openRouterApi`, the
Google types). `DefaultDomainsFrom` names a field whose URL host is the default,
for a type that carries its own address (`telegramApi` and `wahaApi` name
`baseUrl`). `NeverSentOverHTTP` marks a type the host never places on a request
whose URL a node chooses — the database types, SQLite and `jwtAuth` — so it is
never counted as unscoped. A type that sets none of them has no default, and an
empty list means any host.

There is one masking subtlety worth knowing if you author a type. Whether a field
is *secret* — never returned by the API — is a separate flag from whether the
editor *masks* it. Conflating them would make a masked-but-readable field start
coming back as the redaction placeholder, and users would overwrite real values
with it.

## API operations

`GET /api/v1/credential-types` lists the catalogue and each type's fields.
`POST /api/v1/credential-types/{type}/test` tests a payload before it is saved.
`GET`, `POST`, `PUT` and `DELETE` on `/api/v1/credentials` manage stored
credentials, and `POST /api/v1/credentials/{id}/test` tests a stored one.
`POST /api/v1/credentials/{id}/oauth/start` returns a Google authorization URL
for the Connect popup (`window.open`, never an iframe). The browser lands on
`/oauth/callback`, which stores the tokens and posts a message to the opener.
See the [HTTP API reference](/reference/api/).

## Google Connect

The popup's `state` is signed, but a signature only proves the server minted
it, not who is holding it. On its own, a state works for anyone who has the
link. Someone could start Connect on their own credential and send the
authorize URL to a victim, and the victim's consent would store the victim's
Google tokens in the sender's credential. Three things close that:

- **The state is bound to the browser that started it.** The start response
  sets a fresh random nonce as a cookie named `kilasflow_oauth_…`. The cookie is
  `HttpOnly`, `SameSite=Lax`, scoped to the callback path, `Secure` when the
  callback is served over https, and lives as long as the state (ten minutes).
  The state carries the nonce's hash, never the nonce, because the state
  travels in URLs. The callback completes only when the browser presents the
  matching nonce. `Lax` is the strictest mode that works here: the callback is
  a top-level navigation arriving from Google, which `Lax` sends the cookie on
  and `Strict` does not.
- **A state is used once.** The callback records the state before it exchanges
  the code, and a second callback with the same state is refused. The record is
  kept in the database, in the table that already backs `Idempotency-Key`,
  under a key that no request header can spell. A replay that reaches another
  replica is refused there too, and the record expires with the state.
- **The code needs PKCE.** The authorize URL carries an S256
  `code_challenge`, and the exchange sends the matching `code_verifier`. The
  verifier is derived from the browser's nonce under the server key, so nothing
  is stored between start and callback, and a code lifted from a redirect
  cannot be exchanged without the cookie.

The start request and the callback have to reach the same host, or the browser
will not send the cookie back. Behind a proxy that rewrites `Host` (the web dev
server's proxy does this), set `server.public_url` to the address the browser
uses.

An [embed session](/concepts/tenancy-and-embedding/) may read the credential
list — an editor has to offer a picker — and may do nothing else with
credentials. The list it reads holds only the credentials its session was
granted, and they are chosen in the query before the page is cut, so neither the
rows nor the paging cursor name any other credential.

An embed session and a scoped API key may not save or run a workflow that
attaches an **unscoped** credential: one that would be sent over HTTP and has no
allowed domains of its own or from its type. Either caller may edit where a
request goes, so such a credential would, in their hands, be a way to read its
secret. Scope the credential and the refusal goes away. Database, SQLite and JWT
credentials are never unscoped in this sense: they are not placed on a request
whose URL a node chooses. Nor is a credential a Webhook or Form trigger uses to
verify the requests arriving at it, which it never sends, nor one on a disabled
node, which never runs. See
[what a session may actually do](/concepts/tenancy-and-embedding/#credentials-in-a-confined-callers-document).

## Source

`internal/credentials/credentials.go` (`Split`, `Cipher`, `AllowsHost`,
`KeyFromEnvironment`), `internal/credentials/scope.go` (`EffectiveDomains`,
`DefaultDomains`, `Unscoped`), `internal/workflow/compiler.go`
(`undeclaredCredentials`), `internal/credentials/registry.go` (`Placement`,
`Authentication`, `ApplyAuthentication`, `RunTest`),
`internal/credentials/builtin.go` (the built-in types, including Google Drive and Gmail OAuth2),
`internal/repository/credentials.go` (the storage split and `Resolve`),
`internal/engine/authenticate.go` (the domain check on the run path),
`internal/engine/secret_scrub.go` and `internal/credentials/scrub.go` (scrubbing
a node's error of the secrets it resolved), `internal/safehttp/redact.go`
(`RedactError`).
