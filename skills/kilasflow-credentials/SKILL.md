---
name: kilasflow-credentials
description: Use when a workflow needs an API key, token, password or a database connection, when a stored credential must be listed, inspected, tested or replaced, or when a new agent key must be minted. Triggers on "credential", "secret", "token", "auth", "api key".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow credential list
  - kilasflow credential get
  - kilasflow credential test
  - kilasflow workflow get
  - kilasflow api
kilasflow_operations:
  - list-credentials
  - get-credential
  - test-credential
  - list-credential-types
  - test-credential-payload
  - create-credential
  - update-credential
  - delete-credential
  - list-api-keys
  - create-api-key
  - revoke-api-key
  - get-workflow
kilasflow_nodes:
  - kilasflow.httpRequest
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet'
  - 'No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch'
---

## Non-negotiables

1. A credential is referenced by id: it never appears in a workflow document, a CLI argument or chat. A node names one by its credential *type* — `credentials: {"<credentialType>": "<credentialId>"}` — the document stores that id and nothing else (internal/workflow/document.go, `Node.Credentials`), and a credential entry that is an empty string is refused as a missing reference.
2. A Read returns the public half only. `kilasflow credential get <credentialId>` (`get-credential`) and `kilasflow credential list` (`list-credentials`) never return a secret: secret fields come back as the redaction placeholder, and a stored secret is never returned after it is written, not even to the caller that wrote it (internal/api/handlers/credentials.go, `credentialResource`; internal/credentials/credentials.go, `Split`).
3. Deleting or replacing a credential is destructive, and no verb guards it today (see Not shipped yet). Confirm with the user, and read every workflow that names the id first.
4. A write is not a check. `kilasflow credential test <credentialId>` (`test-credential`) runs the type's own probe; a 2xx from a create or an update runs nothing (internal/api/handlers/credentials.go, `Test`).

## Strong defaults

- Find before you create. `kilasflow credential list` (`list-credentials`, with `--limit` and `--cursor`) is names, types and stamps only — a credential that already exists and works is a credential nobody has to store again (internal/cli/verbs_credential.go).
- Read one with `kilasflow credential get <credentialId>` (`get-credential`); the response carries the non-secret fields as stored, `allowedDomains`, and the placeholder for what is secret.
- The catalogue is served, not guessed. `kilasflow api list-credential-types` (`list-credential-types`) returns every type and each type's editor fields, so a field name or option is read from the server rather than recalled (internal/api/handlers/credentials.go, `ListTypes`; internal/credentials/builtin.go).
- Test before saving and after saving. `kilasflow credential test <credentialId>` (`test-credential`) probes a stored one without sending the secret back; `kilasflow api test-credential-payload --path type=<credentialType> --body @payload.json` (`test-credential-payload`) probes one that has not been saved, and a redacted field plus `credentialId` in that body resolves the stored secret so an edit is tested against what is actually held (internal/api/handlers/credentials.go, `TestPayload` and `mergeStoredSecrets`). A type with no probe answers `untestable`, which is not a failure.
- Writes are one call each through the escape hatch: `kilasflow api create-credential --body @cred.json` (`create-credential`), `kilasflow api update-credential --path id=<credentialId> --body @cred.json` (`update-credential`, where the type is immutable after creation, and a field left at the placeholder keeps its stored value), and `kilasflow api delete-credential --path id=<credentialId>` (`delete-credential`, answered 204). The value is supplied once, by whoever owns it, and read from their own tooling or environment rather than echoed into a transcript.
- Scope where it may be sent. `allowedDomains` holds the hosts a credential may reach; an exact entry matches one host, and a `*.` entry matches subdomains but never the bare parent (internal/credentials/credentials.go, `AllowsHost`; docs/src/content/docs/concepts/credentials.md). An empty list means the type's default: `api.openai.com` for `openAiApi`, `openrouter.ai` for `openRouterApi`, Google's hosts for the Google types, and the host of the credential's own `baseUrl` for `telegramApi` and `wahaApi`; only the generic HTTP types are unrestricted when empty (internal/credentials/scope.go). A typed list replaces the default. An agent token may not save or run a workflow that attaches an unscoped credential to a node that calls out, so scope a generic HTTP credential before a narrowed token builds with it. This narrows the instance egress policy, never replaces it.
- Read the reference before editing a workflow. `kilasflow workflow get <workflowId>` (`get-workflow`) shows `nodes[].credentials`, and the compiler requires a non-empty reference for every credential a node type declares — `config.required` with the path `/nodes/N/credentials/<type>` when it is missing (internal/workflow/compiler.go). `kilasflow.httpRequest` is the shape to have in mind: it declares `httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`, `httpQueryAuth` and `httpCustomAuth` (nodes/http.go).
- The agent keys are the tenant's own way in. `kilasflow api create-api-key --body @key.json` (`create-api-key`) mints a key and returns it in full exactly once; `scopes` narrows it to `workflow:read`, `workflow:write`, `workflow:run`, `datastore:read` and `datastore:write`, `workflowId` binds it to one workflow and `expiresAt` retires it; omit `scopes` for the tenant-wide key (internal/api/handlers/auth.go; internal/embed/embed.go). `kilasflow api list-api-keys` (`list-api-keys`) never includes a secret, and `kilasflow api revoke-api-key --path id=<keyId>` (`revoke-api-key`) stops a key immediately and permanently. A key minted with scopes is an agent token, and it cannot mint another key: that refusal is `403` with the code `scope_denied` (internal/api/handlers/auth.go, `CreateKey`).
- The operator key is registered at boot, not minted: it is read from the environment variable named by `auth.operator_key_env` (default `KILASFLOW_AUTH_OPERATOR_KEY`), registered in the operator tenant, and it is the credential that may provision customers (cmd/kilasflow/main.go; internal/config/config.go). It never comes from the configuration file.
- The CLI's own credential is the one it stores for itself: `kilasflow auth login` reads the token from stdin when the flag's value is a dash, and refuses a token written on the command line at all, because it would land in shell history and the process listing (internal/cli/verbs_auth.go). That is an identity for the CLI, not something a workflow carries.
- Credentials are optional at boot: an installation with no encryption key still starts and runs workflows, and only credential operations report that storage is unconfigured (docs/src/content/docs/concepts/credentials.md).

## Decision tree

```
what are you doing?
|
+-- a workflow needs to call an API or a database
|     -> kilasflow credential list, find an id, attach it to the node
|        in the document as credentials: {"<type>": "<credentialId>"}
|        then kilasflow workflow get <workflowId> to confirm it was saved
|
+-- is this stored credential still good?
|     -> kilasflow credential test <credentialId>
|        untestable means the type has no probe, not that it failed
|
+-- check a credential before storing it
|     -> kilasflow api test-credential-payload --path type=<credentialType> \
|          --body @payload.json
|
+-- store a new one
|     -> whoever owns the value supplies it once, into a body file
|        kilasflow api create-credential --body @cred.json
|        then kilasflow credential get <credentialId> and credential test it
|
+-- change one
|     -> kilasflow credential get <credentialId> first
|        send only the fields that change; leave the rest at the placeholder
|        kilasflow api update-credential --path id=<credentialId> --body @cred.json
|
+-- remove one
|     -> which workflows name it? kilasflow workflow get <workflowId>
|        ask the user, then kilasflow api delete-credential --path id=<credentialId>
|
+-- which fields does a type have, and where does it go in a request?
|     -> references/CREDENTIAL_TYPES.md
|
+-- an agent needs its own way in
      -> kilasflow api create-api-key --body @key.json with scopes and a workflow
         binding, then hand the key over once and record its id to revoke it later
```

## Not shipped yet

- No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet — every one of them is served, so reach it through kilasflow api with --path and --body, and treat the missing verb as the missing guard: confirm with the user before a write that cannot be undone, then read the resource back.
- No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch — mint, list and revoke keys through kilasflow api with --body and --path, and note that the token comes back once from the mint and is never returned again, so it has to be stored by whoever asked for it.

## Anti-patterns

- "I'll put the value in the node's parameters" → the secret is then in the document, in every revision of it, and in every export; a credential is referenced by id → create or find the credential, attach its id in `credentials`, and leave the parameters to carry data.
- "Let me read the secret back to check it" → `get-credential` and `list-credentials` return the public half and a placeholder, and no surface returns a stored secret except the runtime's own resolution → prove it with `credential test`, or with a probe of the payload.
- "The credential test passed, so the run will" → the probe exercises the credential, not the node's parameters, the URL or the workflow's egress policy → pass the id to the node and run the workflow to a terminal status.
- "The name is the id" → a credential row is keyed by an id, and the document stores that id → take it from `credential list`, never from a name.
- "I'll scope it to the API host with a wildcard" → `*.example.com` does not match `example.com` itself → name both entries when both hosts must be reachable, and check what the node actually calls.
- "The credential type is wrong, so I'll update it" → a type is immutable after creation and an update that names a different one is refused outright → store a new credential of the type you need and point the node at it (internal/repository/credentials.go, `Update`).
- "I'll delete the credential to revoke access" → the document keeps naming an id that no longer resolves, so the workflow fails at authentication → revoke the key at the provider or rotate the credential, and only delete it when nothing names it.
- "An agent key is just a key" → a key minted with `scopes` is an agent token, it cannot mint another, and it is bound to the workflow you named → mint the narrowest key for the task and record its id, because revoking it is the only way back.
- "The expired key will still work for this run" → an expired key stops authenticating, and expiry is a value the minter chose → set `expiresAt` when the task is bounded, and check `list-api-keys` before blaming the workflow.

## Reference files

| File | Read when |
| --- | --- |
| CREDENTIAL_TYPES.md | you need a credential type's id, its fields, which of them are secret, where its authentication is placed in a request, or whether it has a probe |
