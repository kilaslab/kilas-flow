---
name: kilasflow-embedding
description: Use when a host SaaS integrates KilasFlow — minting embed sessions for the iframe editor, mapping customers onto tenants, provisioning tenant users and keys, white-labelling the editor, or deleting a tenant. Triggers on "embed", "tenant", "iframe", "API key", "branding", "multi-tenant".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow tenant list
  - kilasflow tenant get
  - kilasflow tenant users
  - kilasflow auth whoami
  - kilasflow context
  - kilasflow api
kilasflow_operations:
  - create-tenant
  - list-tenants
  - get-tenant
  - list-tenant-users
  - create-tenant-user
  - set-tenant-user-password
  - enable-tenant-user
  - disable-tenant-user
  - create-api-key
  - list-api-keys
  - revoke-api-key
  - create-tenant-api-key
  - create-embed-session
  - create-stream-ticket
  - stream-execution-events
  - delete-tenant
  - get-me
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No embed verb: create-embed-session and create-stream-ticket are served and reached through the escape hatch'
  - 'No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch'
  - 'No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet'
  - 'No Go library: embedding is the embed session handshake, the iframe editor and the tenant-realm reads, never an import'
---

## Non-negotiables

1. The tenant comes from the credential, never from the request. `create-embed-session` binds the session to the tenant of the caller that minted it, and the resolver prefers an embed session over an API key because it is the narrower authority of the two (internal/api/handlers/embed.go; internal/embed/embed.go; docs/src/content/docs/concepts/tenancy-and-embedding.md). Minting with the wrong customer's client is a cross-tenant grant that no browser-side check can undo.
2. The origin is a server-side constant, never a value reflected from a request header. Matching is exact `scheme://host[:port]` with no wildcard and no suffix match, the mint origin must appear in `embed.allowed_origins` — an empty list allows nothing — and the request's own `Origin` is re-checked on every request afterwards (internal/embed/embed.go, `MatchesOrigin`, `OriginAllowed`; internal/config/config.go, `Embed`).
3. Authentication is what makes a tenant real. With `auth.enabled` false, which is the default, every unauthenticated caller resolves to the single tenant named `default`; one key per customer therefore requires an enabled auth with a 32-byte signing key, and a deployment that skips it has partitioning conventions instead of isolation (docs/src/content/docs/concepts/tenancy-and-embedding.md).
4. Deletion is irreversible and operator-only: `delete-tenant` removes everything the tenant owns — including its datastores' physical tables and its payload files — with no export first, no soft delete and no undo. Confirm with the user before it, and never reach for it as a cleanup step.

## Strong defaults

- Provision a customer with operator operations, in this order: the tenant row (`create-tenant`, answered with `201` and a `Location`, and a duplicate id is `409`), its first account (`create-tenant-user`), then a key for the host's backend (`create-tenant-api-key`, which mints on another tenant's behalf). Tenant rows are operator-managed: there is no self-service signup (internal/api/handlers/admin.go; docs/src/content/docs/guides/embedding.md).
- One key per customer, held by your backend and never by a page. `create-api-key` mints a key for the calling tenant and returns the whole credential exactly once — the server keeps only a hash — `list-api-keys` reads one page without any secret, and `revoke-api-key` stops one immediately and permanently (internal/api/handlers/auth.go).
- Account lifecycle is four operator operations on one tenant's accounts: `create-tenant-user` makes an account that can sign in immediately, `disable-tenant-user` stops it signing in from the next request while keeping the row so the workflows it authored still have a name, `enable-tenant-user` clears that marker so it signs in again with its existing password, and `set-tenant-user-password` is the operator's reset — the old password stops working at once (internal/api/handlers/admin.go).
- A key can be narrowed instead of tenant-wide: the same `scopes` vocabulary an embed session uses, an optional `workflowId` binding, and an optional `expiresAt`. A workflow binding without scopes is refused, because a tenant-wide key is not narrowed (internal/api/handlers/auth.go; internal/embed/embed.go, `NormalizeScopes`).
- Verify which tenant a configured credential acts as with `kilasflow auth whoami`, which is the `get-me` operation and prints `tenantId`, `kind`, `label`, `keyId` and `userId` (internal/cli/verbs_auth.go). Begin a session with `kilasflow context`, one read-only call covering the server, the identity, workflows, datastores and node types (internal/cli/context.go).
- The operator's view of the deployment is three read-only verbs, each requiring an operator credential: `kilasflow tenant list` (`list-tenants`), `kilasflow tenant get <id>` (`get-tenant`) and `kilasflow tenant users <id>` (`list-tenant-users`, whose projection carries no password hash). A customer's own key gets the refusal as exit 3 with `error.code = "scope_denied"`, never a quietly filtered listing (internal/cli/verbs_tenant.go; internal/api/handlers/admin.go).
- Mint the editor session with the escape hatch: `kilasflow api create-embed-session --body @session.json` (`--path name=value`, `--body @file.json` and `--list` are the verb's own flags). The body names exactly one subject — `workflowId` or `datastoreId`, never both — plus `scopes`, the exact `origin`, and optionally `ttlSeconds` and `branding`. The response carries the token, where to load the iframe, its expiry, the granted scopes, the origin and the merged branding; a datastore session has an empty `embedUrl` because it has no editor to open (internal/api/handlers/embed.go; internal/cli/verbs_api.go).
- The scope vocabulary is five values: `workflow:read`, `workflow:write`, `workflow:run`, `datastore:read`, `datastore:write`. A session must carry at least one, and implication runs downward only — write implies read, run implies read, datastore:write implies datastore:read. Nothing implies write or run, and a scope outside the vocabulary is refused at mint rather than ignored (internal/embed/embed.go, `Allows`, `scopesMatchSubject`).
- Sessions are minutes, not hours: fifteen by default (`embed.session_ttl`, env `KILASFLOW_EMBED_SESSION_TTL`), thirty maximum, `ttlSeconds` per request. A configured default outside one second to thirty minutes refuses the boot (internal/embed/embed.go, `MaxLifetime`, `CheckDefaultLifetime`; internal/config/embed_validate.go).
- Watch a run through a ticket, not a credential: mint `create-stream-ticket` after checking the execution belongs to your tenant (the server checks inside the caller's tenant too), then spend the single-use ticket as the `ticket` query parameter on `stream-execution-events` (`GET /executions/{id}/events`). A ticket names one execution and lives at most 30 seconds, because it travels in a query string and therefore lands in proxy logs (internal/api/handlers/auth.go; internal/auth/session.go, `MaxTicketTTL`; internal/api/middleware/auth.go).
- White-label with values, never markup: `name`, `logoUrl` (an absolute https URL), `accent`, `hideRun`, `hideSave`. The deployment's `branding.name` and `branding.logo` are defaults that the session's own values win over field by field, the merged set comes back in the mint response, and hiding a control never stops an action because the server enforces the scopes (internal/embed/embed.go, `Branding`, `WithDefaults`; docs/src/content/docs/guides/embedding.md).
- The host shares one database rather than supplying storage: every tenant's rows live in the same tables of the same database, and isolation is a tenant scope carried by the repository call's signature rather than a separate store or an interface a host implements (docs/src/content/docs/concepts/tenancy-and-embedding.md).
- Deleting a customer is one operation, and it is safe to repeat: `delete-tenant` through the escape hatch with an operator credential. A repeat reports `tenantRemoved: false` with every count at zero, which is what confirms the deletion finished (internal/api/handlers/admin.go; docs/src/content/docs/operate/tenant-deletion.md).
- Take the operator credential from the environment or a file, never as a literal argument: the CLI reads `KILASFLOW_TOKEN` or the file named by `--token-file <path>`, and `kilasflow auth login` reads the credential from stdin when the token flag's value is a bare dash, so it never lands in shell history or a process listing (internal/cli/config.go, `resolveToken`; internal/cli/verbs_auth.go).

## Decision tree

```
what are you doing?
|
+-- a customer signs up
|     -> create-tenant, then create-tenant-user, then create-tenant-api-key
|        all three are operator operations through kilasflow api
|
+-- the end user opens the editor
|     -> create-embed-session for ONE workflow and ONE exact origin
|        hand the token to the page, not to the frame until it announces itself
|
+-- the end user must watch a run
|     -> create-stream-ticket, then spend it on stream-execution-events
|        single-use, seconds-lived, minted per connect
|
+-- the embedded surface must touch rows
|     -> a datastore session: datastore:read / datastore:write, one datastore
|
+-- the iframe never appears
|     -> no embed signing key configured, or the page's origin is not in
|        embed.allowed_origins (exact match; an empty list allows nothing)
|
+-- the editor loads but every call is refused
|     -> wrong scopes, or the session was minted with another customer's key
|
+-- which customer am I acting as?
|     -> kilasflow auth whoami, then kilasflow tenant get <id>
|
+-- a customer leaves
      -> confirm, then delete-tenant; repeat until the counts are zero
```

## Not shipped yet

- No embed verb: create-embed-session and create-stream-ticket are served and reached through the escape hatch — mint both with `kilasflow api`, passing the body with `--body @file.json` (or `--body -` from stdin) and reading the token, the embed URL and the expiry out of the envelope.
- No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch — `kilasflow api create-api-key`, `kilasflow api list-api-keys` and `kilasflow api revoke-api-key --path id=<keyId>` are the calls; nothing in the verb tree mints or revokes a key. The one key operation under a tenant is create-tenant-api-key, an operator action on another tenant's behalf, and it is reached the same way.
- No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet — every one of them is an operation the server serves, so reach it with `kilasflow api <operation-id>` and `--body @file.json`, and treat the guarded writes as user decisions rather than agent housekeeping.
- No Go library: embedding is the embed session handshake, the iframe editor and the tenant-realm reads, never an import — a host integration talks to the HTTP API with its own client (the TypeScript SDK under `sdk/`, or the reference host under `sdk/examples/reference-host`), and nothing in this repository is a Go package a host application links against.

## Anti-patterns

- "One shared tenant is simpler" → any key in that tenant lists every workflow, resolves every credential and reads every row → one tenant per customer; that is the model the isolation tests prove (docs/src/content/docs/guides/embedding.md).
- "I'll reflect the request's `Origin` header into the mint" → whoever controls that header receives your customer's token → keep the mint origin and the iframe target as backend constants, and list every framing domain in `embed.allowed_origins`.
- "Fifteen minutes is the ceiling" → the default is 15 but the hard cap is 30, and `ttlSeconds` is honoured up to it → mint again for a longer session; it is cheap and it is the re-authorization point.
- "The token can activate the workflow" → activation, deactivation, deletion and import are refused to an embed session at any scope, and importing creates a workflow outside its single-workflow authority → those are backend-key actions (internal/api/middleware/embed.go).
- "The page can hold the key" → the browser sends an embed token, which grants strictly less and expires → mint server-side and pass only the token over postMessage.
- "The iframe is blank, so the session is bad" → the usual causes are a missing embed signing key (503) or an origin that is not exactly in the allowlist → check the two configuration keys before the session.
- "The tenant is gone, so its sessions are too" → embed sessions are stateless and keep their scopes until they expire, and can keep writing rows keyed to the deleted tenant's id until then → repeat the deletion after they lapse (docs/src/content/docs/operate/tenant-deletion.md).
- "The deletion returned 200, so it finished" → steps can be reached again by a worker or a live session → send it again and require `tenantRemoved: false` with every count zero.
- "I'll implement the storage" → there is no interface to implement: every tenant shares one database and isolation is a tenant scope on every repository call → use the tenant-scoped HTTP surface.
- "`list-api-keys` is an operator operation" → it lists the calling tenant's own keys, while `create-tenant-api-key` is the operator one → do not look for a customer's keys behind an operator verb.

## Reference files

| File | Read when |
| --- | --- |
| EMBED_HANDSHAKE.md | you are minting a session, wiring the iframe handshake, or debugging one that never completes, and need the token, scope, origin and message contract |
| SESSION_AUTHORITY.md | you need to know exactly what a live session may call, why a save was refused, or how the live execution stream is authorized |
