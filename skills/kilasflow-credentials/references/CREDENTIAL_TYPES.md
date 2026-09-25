# Credential types

The catalogue is server-defined data (`internal/credentials/builtin.go`), served
by `list-credential-types`, and it is the authority on a type's id and fields.
This file is the reading of it: what each type holds, which fields are secret,
where the type places its authentication in a request, and whether it can be
probed.

## What a type declares

A credential type is one `credentials.Type` value:
`ID`, `DisplayName`, `Description`, `Properties`, `Secrets`, an optional
`Authenticate` descriptor and an optional `Test` (`internal/credentials/registry.go`).

- **Properties** are described in the same language a node's parameters use, so
  a credential gets conditional visibility and masking for free. A required
  field is refused before anything is stored.
- **Secrets** names the fields the API never returns after they are stored.
  This flag is deliberately separate from a property's password masking: a
  masked-but-readable field must keep coming back, or a caller overwrites a real
  value with the placeholder.
- **Authentication placement is data**, not a switch in Go: five placements
  exist — `header`, `query`, `basicAuth`, `bearer` and `path` — plus `custom`,
  which merges a JSON template's `headers` and `qs` objects into the request.
  The `{{ field }}` templates in a descriptor are expanded from the credential's
  own fields only, and never by the expression evaluator.
- **A type that does not sign an HTTP request declares no descriptor at all**:
  `jwtAuth` verifies callers arriving at this server, and the database types open
  a connection rather than building a request. Refusing by absence is the better
  error.
- **A type may declare a probe**: a method (default `GET`), a URL template that
  may name the credential's own fields, and a status below which the answer
  counts as success (default 400). The response body is drained and discarded, so
  a pass/fail probe never becomes a general-purpose fetch.

## The catalogue

| ID | For | Fields | Authentication |
| --- | --- | --- | --- |
| `httpBasicAuth` | generic basic auth | `user`, `password` (secret) | basic auth from those two fields |
| `httpHeaderAuth` | any fixed-header API | `name`, `value` (secret) | one header, whose name is itself a field |
| `httpBearerAuth` | a bearer token | `token` (secret) | bearer placement from that field |
| `httpQueryAuth` | any fixed-query-parameter API | `name`, `value` (secret) | one query parameter, name from a field |
| `httpCustomAuth` | n8n's Custom Auth | `json` (secret) | a JSON template merged into the request's headers and query parameters |
| `jwtAuth` | verifying inbound JSON Web Tokens | `keyType`, `secret` (secret), `publicKey`, `privateKey` (secret), `algorithm` | none: it verifies callers, it does not sign outbound requests |
| `postgres` | PostgreSQL | `host`, `port`, `database`, `user`, `password` (secret), `sslMode` | none: a database connection |
| `mysql` | MySQL and MariaDB | `host`, `port`, `database`, `user`, `password` (secret), `tls` | none: a database connection |
| `sqlite` | a SQLite file on the server | `path` | none: a file, not a host |
| `telegramApi` | Telegram Bot API | `accessToken` (secret), `baseUrl` | path placement: the Bot API puts the token in the request path |
| `wahaApi` | a self-hosted WAHA instance | `baseUrl`, `apiKey` (secret) | one fixed header whose name the type declares |
| `openAiApi` | OpenAI | `apiKey` (secret) | bearer placement |
| `openRouterApi` | OpenRouter | `apiKey` (secret) | bearer placement |

Thirteen types ship, and every id and field key is byte-identical to what the
catalogue has always held: a stored row is keyed by the type id and its sealed
payload by the field keys, so renaming either makes an existing credential stop
resolving with no way to recover it. A node pack cannot add a credential type
today; the registry is a package-level singleton built at init.

## The three that are load-bearing

- **`jwtAuth` reads its key material by the algorithm's family.** `secret` is the
  shared passphrase an HS token was signed with; `publicKey` is the PEM key an
  RS, PS or ES token's signature is checked against; the key type has to agree
  with the algorithm, or the credential is refused rather than verified against
  a public key used as a shared secret. `privateKey` is kept so an n8n-shaped
  payload stores unchanged — nothing on this server reads it.
- **`wahaApi`'s `baseUrl` is deliberately not secret.** A declarative pack reads
  it to build every request, and a non-secret field is the only kind a pack may
  read; marking it secret would leave the pack with no address to call.
- **`openAiApi`'s field is `apiKey`, not a generic `token`**, because that is
  what n8n names and an imported workflow names `openAiApi`. `openRouterApi` and
  `openAiApi` are separate types for the same reason, and each carries its own
  probe.

## Which types can be tested

A probe exists where a harmless authenticated call proves the credential:
`openAiApi` and `openRouterApi` declare one, and the three database types are
tested by connecting (`internal/api/handlers/credentials.go`, `probe`, which
routes a database type to the SQL driver's own handshake under the same guard
the executors receive).

Everything else answers `untestable`, which is a different answer from a probe
that ran and failed — a client showing a cross for both would be lying about
one. OpenRouter is probed at its key endpoint rather than its model catalogue,
because that catalogue is served unauthenticated and a 200 there would pass an
expired key.

A stored credential's probe runs under the credential's own `allowedDomains`
and the instance egress policy, and one test at a time per credential: a probe
that bypassed either would be a way to point the server at a host the credential
was never scoped to. The verdict carries no secret and no remote body.

## Reading a type from a workflow

A node's `credentials` map is keyed by the credential *type*, and its value is
the credential id:

```
credentials: {"httpBearerAuth": "<credentialId>"}
```

The compiler resolves each credential the node type declares — `httpBasicAuth`,
`httpHeaderAuth`, `httpBearerAuth`, `httpQueryAuth` and `httpCustomAuth` for
`kilasflow.httpRequest` — and reports `config.required` at
`/nodes/N/credentials/<type>` when the entry is missing or empty. A credential
attached under a type the node does not declare — `openAiApi` on an HTTP
Request node — is refused as `config.invalid` at the same path, naming the types
the node accepts (`internal/workflow/compiler.go`, `undeclaredCredentials`); a
draft still saves. Nothing in the document is the value: the runtime resolves the payload at the moment it
authenticates, after the host scope check and before the secret touches the
request (`internal/repository/credentials.go`, `Resolve`;
`internal/engine/authenticate.go`).

## Adding a type

Not this bundle's job, and not a pack's today: a type is a Go value in
`internal/credentials/builtin.go`, and a new one needs an id, its fields, which
of them are secret, a placement if it signs HTTP requests, and optionally a
probe. A type that marks a field secret without declaring it, duplicates an id,
or uses an unknown property kind is refused when the registry is built.
