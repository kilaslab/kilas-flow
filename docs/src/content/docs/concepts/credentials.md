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
  return plaintext, and the secret fields come back as a non-empty redaction
  placeholder so an editor can tell "configured" from "empty".
- `Resolve` is the single path by which a plaintext secret leaves storage, and
  only the runtime calls it.

Editing a credential merges: a secret field left at the redaction placeholder
keeps its stored value, so changing a name never silently blanks a password the
client was never given.

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

The check runs in four places, and in every one it happens **before the secret
touches the request**: the runtime's `Request.Authenticate`, used by both the
hand-written HTTP node and the declarative routing interpreter; the AI chat model
call; edit-time option loading; and the credential test endpoint.

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

## Testing a credential

A type may also declare a test as data — a method, a URL and the status below
which the answer counts as success (default `GET` and 400). The response body is
drained and discarded, because returning it would turn a pass/fail probe into a
general-purpose fetch.

The tests that exist are chosen carefully. OpenRouter is probed at `/key` rather
than `/models`, because OpenRouter serves its model catalogue unauthenticated —
so `/models` answers 200 for a key that is expired or revoked, and the test would
pass on a credential that cannot complete anything.

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
| `sqlite` | a SQLite file path |
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
verify the requests arriving at it, which it never sends. See
[what a session may actually do](/concepts/tenancy-and-embedding/#credentials-in-a-confined-callers-document).

## Source

`internal/credentials/credentials.go` (`Split`, `Cipher`, `AllowsHost`,
`KeyFromEnvironment`), `internal/credentials/scope.go` (`EffectiveDomains`,
`DefaultDomains`, `Unscoped`), `internal/workflow/compiler.go`
(`undeclaredCredentials`), `internal/credentials/registry.go` (`Placement`,
`Authentication`, `ApplyAuthentication`, `RunTest`),
`internal/credentials/builtin.go` (the built-in types, including Google Drive and Gmail OAuth2),
`internal/repository/credentials.go` (the storage split and `Resolve`),
`internal/engine/authenticate.go` (the domain check on the run path).
