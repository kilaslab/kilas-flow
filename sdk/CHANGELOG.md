# Changelog

Every entry states whether it is **additive** (new surface, existing callers
unaffected), a **fix** (behavior brought in line with what was documented), or
**breaking** (an existing caller must change). The package stays on `0.x`
until the server surface it targets is considered stable; see `README.md`.

## Unreleased

- **Additive**: workflow tags. `Document` and `WorkflowDocumentInput` take an
  optional `tags` list (at most 20, each 1 to 24 characters, unique ignoring
  case; a save without `tags` clears them, so a client that rewrites a document
  must send back the `tags` it read), and every `WorkflowSummary` carries
  `tags`, an array that is empty when the workflow has none. The list endpoint
  takes a `tag` filter. A client that builds a `WorkflowSummary` literal in
  TypeScript must add `tags: []`.
- **Additive**: `convertWorkflowFragment`, covering `POST /api/v1/workflows/convert`.
  It translates n8n JSON — nodes copied from an n8n canvas, or a whole export —
  into KilasFlow nodes and connections with the translator `importWorkflow`
  uses, answers them with the import report, and saves nothing. It needs only
  read authority, so a host can preview what a customer's paste would become
  before writing it with `createWorkflow` or `updateWorkflow`.
- **Additive**: `deleteTenant`, covering `DELETE /api/v1/tenants/{id}`. It
  deletes a customer and everything it owns — executions and their payload
  files, workflows and versions, credentials, schedules, webhook deliveries,
  datastores and their physical tables, accounts, keys, and the tenant row —
  and answers with the counts it removed, per table. It needs the operator
  credential, it is irreversible, and it is idempotent: send it again until it
  reports zeros.
- **Additive**: `locale` on `MountOptions` — the language tag the embedded
  editor renders its own chrome in, posted with the session in the handshake.
  The editor validates it against the catalog list it ships and ignores a tag
  it does not carry, so an unknown locale leaves the editor in its base locale
  rather than failing the handshake.

## 0.1.0

First published release.

- **Additive**: the full operation surface — workflows and versions (including
  diagnostics), executions, credentials, API keys, schedules, node catalogue,
  interop, datastores, tenants and accounts (the operator surface), embed
  sessions, system — across `@kilasflow/sdk`, `@kilasflow/sdk/server` and
  `@kilasflow/sdk/browser`.
- **Additive**: `apiKey` on `TransportOptions`, a convenience sending
  `Authorization: Bearer <key>` beside the existing `headers` escape hatch,
  which remains supported and wins on conflict.
- **Additive**: `tenantClientFactory` for one fixed-credential client per
  tenant, and a single-use stream-ticket handshake in
  `subscribeExecutionEvents` with mint-per-connect reconnect and resume.
- **Additive**: `idempotencyKey` on `runWorkflow`, `insertDatastoreRow` and
  `upsertDatastoreRow` — one key per logical operation, sent as
  `Idempotency-Key` so a retry is answered with the first outcome instead of a
  second side effect. Those three methods take an `IdempotentWriteOptions`
  object or, as before, a bare `AbortSignal`. `KilasFlowError` gains
  `retryAfterSeconds`, the in-flight 409's retry hint.
- **Fix**: the manifest licence is Apache-2.0, matching the repository root
  `LICENSE`.
- **Additive**: `incrementDatastoreRows`, the atomic counter write. It adds an
  amount to a number column on every row matching the filter in one statement —
  atomic per row on SQLite and PostgreSQL alike — and resolves the rows as that
  statement left them, where a read-then-`updateDatastoreRows` loses a write to
  a concurrent caller.
