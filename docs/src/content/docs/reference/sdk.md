---
title: Host SDK
description: The @kilasflow/sdk API reference, generated from the SDK's own source — every export of the server and browser entry points, with the JSDoc that documents them.
sidebar:
  order: 6
---

<!-- Generated from sdk/src and sdk/package.json by scripts/generate-sdk-reference.mjs — do not edit by hand. -->

> **Generated reference — `@kilasflow/sdk` `0.1.0`.** Produced from the SDK's committed source (JSDoc included), not hand-written. To regenerate, run `make generate-sdk-reference`. The guide is [Using the host SDK](/guides/sdk/).

The SDK is ESM-only, with no runtime dependencies, and is not yet published to npm — the [install instructions](/guides/sdk/#install-from-a-checkout-until-the-first-release) cover the pre-release path.

## Entry points

The `exports` map in `sdk/package.json` is the authority; both current subpaths, as this page was generated:

| Import | Module |
| --- | --- |
| `@kilasflow/sdk` | `./dist/index.js` |
| `@kilasflow/sdk/server` | `./dist/server.js` |
| `@kilasflow/sdk/browser` | `./dist/browser.js` |

## Server entry point (`@kilasflow/sdk/server`)

The backend client. It carries the host's API credentials, so it must never be bundled into a browser. Construct once per credential: `new KilasFlowClient({ baseUrl, apiKey })`.


### Exports

#### `EmbedScope`

```ts
export type EmbedScope = | 'workflow:read' | 'workflow:write' | 'workflow:run' | 'workflow:publish' | 'workflow:activate' | 'datastore:read' | 'datastore:write'
```

Scopes an embed session may carry.

#### `EmbedSessionRequest`

```ts
export interface EmbedSessionRequest
```

#### `ExecutionListQuery`

```ts
export interface ExecutionListQuery
```

#### `WorkflowVersionListQuery`

```ts
export interface WorkflowVersionListQuery
```

#### `DatastoreColumnType`

```ts
export type DatastoreColumnType = 'string' | 'number' | 'boolean' | 'date'
```

A datastore column's declared type, matching n8n's four wire values.

#### `DatastoreColumn`

```ts
export interface DatastoreColumn
```

One column of a datastore create or add-column call.

#### `DatastoreRow`

```ts
export type DatastoreRow = Record<string, unknown>
```

One datastore row as the API returns it: the system `id`, `createdAt` and `updatedAt` beside one entry per user column.

#### `DatastoreFilterOperator`

```ts
export type DatastoreFilterOperator = | 'eq' | 'neq' | 'like' | 'ilike' | 'gt' | 'gte' | 'lt' | 'lte' | 'isEmpty' | 'isNotEmpty'
```

Closed operator set for row filters, matching the server's ten supported conditions.

#### `DatastoreFilterCondition`

```ts
export type DatastoreFilterCondition = | { columnName: string; condition: 'isEmpty' | 'isNotEmpty' } | { columnName: string; condition: Exclude<DatastoreFilterOperator, 'isEmpty' | 'isNotEmpty'>; value: unknown }
```

One filter predicate. `isEmpty` and `isNotEmpty` take no value — they test for the absence of one — so the union withholds the slot rather than leaving a caller to guess what to put in it.

#### `datastoreFilter`

```ts
export function datastoreFilter(type: 'and' | 'or', conditions: DatastoreFilterCondition[]): Filter
```

Builds the wire filter `{type, filters: [{columnName, condition, value}]}` verbatim — the server is the authority on the shape, so the builder produces it rather than inventing a friendlier dialect.

#### `CursorPage`

```ts
export interface CursorPage<T>
```

One page of a cursor-paged listing: items plus the cursor for the next.

#### `paginateCursor`

```ts
export async function* paginateCursor<T>( fetchPage: (cursor?: string) => Promise<CursorPage<T>> ): AsyncGenerator<T, void, void>
```

Pages a cursor listing to exhaustion, yielding items across every page.

#### `MAX_IDEMPOTENCY_KEY_LENGTH`

```ts
export const MAX_IDEMPOTENCY_KEY_LENGTH = 255
```

The longest key the `Idempotency-Key` header accepts: 255 characters, the column width the server stores it in.

#### `IdempotentWriteOptions`

```ts
export interface IdempotentWriteOptions
```

Options for the writes that can be made idempotent: a manual run and the two datastore row writes.

#### `RunWorkflowOptions`

```ts
export interface RunWorkflowOptions extends IdempotentWriteOptions
```

Options for a manual run: the idempotency key every retryable write takes, and the revision to run.

#### `TenantClientPoolOptions`

```ts
export interface TenantClientPoolOptions
```

Shared configuration for every tenant client a host builds.

#### `tenantClientFactory`

```ts
export function tenantClientFactory(shared: TenantClientPoolOptions): (tenantApiKey: string) => KilasFlowClient
```

Builds one fixed-credential client per tenant.


### Classes

#### `KilasFlowClient`

```ts
export class KilasFlowClient
```

The host-facing client.


### Methods

#### `constructor`

```ts
constructor(options: TransportOptions)
```


### Workflows

#### `listWorkflows`

```ts
listWorkflows(signal?: AbortSignal): Promise<WorkflowSummary[]>
```

#### `getWorkflow`

```ts
getWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource>
```

#### `createWorkflow`

```ts
createWorkflow(document: WorkflowDocumentInput, signal?: AbortSignal): Promise<WorkflowResource>
```

#### `updateWorkflow`

```ts
updateWorkflow(workflowId: string, document: WorkflowDocumentInput, signal?: AbortSignal): Promise<WorkflowResource>
```

#### `deleteWorkflow`

```ts
deleteWorkflow(workflowId: string, signal?: AbortSignal): Promise<void>
```

#### `getWorkflowVersion`

```ts
getWorkflowVersion(workflowId: string, versionId: string, signal?: AbortSignal): Promise<WorkflowVersionResource>
```

Reads one immutable revision, such as the one an execution pinned.

#### `listWorkflowVersions`

```ts
listWorkflowVersions( workflowId: string, query: WorkflowVersionListQuery = {}, signal?: AbortSignal ): Promise<WorkflowVersionListResource>
```

Lists one workflow's revisions, newest first.

#### `publishWorkflowVersion`

```ts
publishWorkflowVersion( workflowId: string, versionId: string, reason?: string, signal?: AbortSignal ): Promise<WorkflowResource>
```

Publishes a revision so production traffic runs it.

#### `restoreWorkflowVersion`

```ts
restoreWorkflowVersion( workflowId: string, versionId: string, reason?: string, signal?: AbortSignal ): Promise<WorkflowResource>
```

Restores a revision's document into a new draft.

#### `listWorkflowPublishEvents`

```ts
listWorkflowPublishEvents(workflowId: string, signal?: AbortSignal): Promise<WorkflowPublishEventResource[]>
```

Reads a workflow's publish audit trail: what was published, unpublished, or restored.

#### `getWorkflowDiagnostics`

```ts
getWorkflowDiagnostics( workflowId: string, options: { versionId?: string } = {}, signal?: AbortSignal ): Promise<WorkflowDiagnosticsResource>
```

Reads the import report stored with a revision: what the n8n translation could not carry faithfully, per node and per field.

#### `listWorkflowWebhooks`

```ts
listWorkflowWebhooks(workflowId: string, signal?: AbortSignal): Promise<WebhookRouteResource[]>
```

Lists every webhook trigger's public address: the URL a sender is configured with, minted on the first read (or the first import) and reused forever, so it is known before activation and unchanged by it.

#### `activateWorkflow`

```ts
activateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource>
```

#### `deactivateWorkflow`

```ts
deactivateWorkflow(workflowId: string, signal?: AbortSignal): Promise<WorkflowResource>
```

#### `runWorkflow`

```ts
async runWorkflow( workflowId: string, input?: unknown, options?: AbortSignal | RunWorkflowOptions ): Promise<{ id: string; status: string }>
```

Queues a manual run and returns immediately with the execution record.

#### `validateWorkflowDocument`

```ts
validateWorkflowDocument(document: WorkflowDocumentInput, signal?: AbortSignal): Promise<ValidateWorkflowResource>
```

Compiles a document without saving it.

#### `duplicateWorkflow`

```ts
duplicateWorkflow( workflowId: string, options: { name?: string } = {}, signal?: AbortSignal ): Promise<WorkflowResource>
```

Copies a workflow's newest revision into a new workflow under the same tenant, named `<name> (copy)` unless one is given.


### Executions

#### `listExecutions`

```ts
listExecutions(query: ExecutionListQuery = {}, signal?: AbortSignal): Promise<ExecutionListResource>
```

#### `getExecution`

```ts
getExecution(executionId: string, signal?: AbortSignal): Promise<ExecutionResource>
```

#### `cancelExecution`

```ts
cancelExecution(executionId: string, signal?: AbortSignal): Promise<{ id: string; status: string }>
```

#### `retryExecution`

```ts
retryExecution(executionId: string, signal?: AbortSignal): Promise<ExecutionResource>
```

Queues a finished execution again, carrying its workflow, its revision and its input — the same queue path a manual run takes, with the revision named instead of resolved.

#### `evalExpression`

```ts
evalExpression( executionId: string, expression: string, options: { nodeId?: string } = {}, signal?: AbortSignal ): Promise<EvalExpressionResource>
```

Evaluates one expression against a finished execution's stored node outputs, exactly as the runtime would have evaluated it during the run.

#### `executionEventsUrl`

```ts
executionEventsUrl(executionId: string): string
```

Absolute URL of an execution's live event stream.


### Credentials

#### `listCredentialTypes`

```ts
listCredentialTypes(signal?: AbortSignal): Promise<CredentialTypeResource[]>
```

Lists the credential types a host can render a form for.

#### `listCredentials`

```ts
listCredentials(signal?: AbortSignal): Promise<CredentialResource[]>
```

#### `createCredential`

```ts
createCredential(body: CredentialBody, signal?: AbortSignal): Promise<CredentialResource>
```

#### `getCredential`

```ts
getCredential(credentialId: string, signal?: AbortSignal): Promise<CredentialResource>
```

#### `updateCredential`

```ts
updateCredential(credentialId: string, body: CredentialBody, signal?: AbortSignal): Promise<CredentialResource>
```

#### `deleteCredential`

```ts
deleteCredential(credentialId: string, signal?: AbortSignal): Promise<void>
```

#### `testCredential`

```ts
testCredential(credentialId: string, signal?: AbortSignal): Promise<TestCredentialResource>
```

Probes a stored credential and reports whether it actually works.

#### `testCredentialPayload`

```ts
testCredentialPayload(type: string, body: TestPayloadBody, signal?: AbortSignal): Promise<TestCredentialResource>
```

Probes an unsaved credential payload, e.g. to validate a form before storing it.


### Authentication and keys

#### `login`

```ts
login(credentials: LoginInputBody, signal?: AbortSignal): Promise<PrincipalResource>
```

Signs in with email and password.

#### `logout`

```ts
logout(signal?: AbortSignal): Promise<void>
```

#### `getMe`

```ts
getMe(signal?: AbortSignal): Promise<PrincipalResource>
```

Describes the tenant and identity this request authenticated as.

#### `listApiKeys`

```ts
listApiKeys(signal?: AbortSignal): Promise<ListAPIKeysOutputBody>
```

Lists this tenant's keys.

#### `createApiKey`

```ts
createApiKey(label: string, signal?: AbortSignal): Promise<CreatedAPIKeyResource>
```

Mints a key and returns it in full exactly once.

#### `revokeApiKey`

```ts
revokeApiKey(keyId: string, signal?: AbortSignal): Promise<APIKeyResource>
```

Revokes a key immediately and permanently, keeping the row for audit.

#### `createStreamTicket`

```ts
createStreamTicket(executionId: string, signal?: AbortSignal): Promise<StreamTicketResource>
```

Mints a single-use ticket for one execution's event stream.


### Tenants (operator surface)

#### `listTenants`

```ts
listTenants(signal?: AbortSignal): Promise<ListTenantsOutputBody>
```

Lists every tenant in the deployment, oldest first, with its account count.

#### `createTenant`

```ts
createTenant(input: CreateTenantInputBody, signal?: AbortSignal): Promise<TenantResource>
```

Adds a tenant.

#### `getTenant`

```ts
getTenant(tenantId: string, signal?: AbortSignal): Promise<TenantResource>
```

Reads one tenant, such as the resource a create answered with.

#### `deleteTenant`

```ts
deleteTenant(tenantId: string, signal?: AbortSignal): Promise<TenantDeletionResource>
```

Deletes a tenant and everything it owns — executions and their payload files, workflows and versions, credentials, schedules, webhook deliveries, datastores and their physical tables, accounts, keys, and finally the tenant row — and answers with what was removed, per table.

#### `createTenantApiKey`

```ts
createTenantApiKey( tenantId: string, input: CreateTenantAPIKeyInputBody, signal?: AbortSignal ): Promise<CreatedAPIKeyResource>
```

Mints a key on another tenant's behalf and returns it in full exactly once.

#### `listTenantUsers`

```ts
listTenantUsers(tenantId: string, signal?: AbortSignal): Promise<ListTenantUsersOutputBody>
```

Lists one tenant's accounts.

#### `createTenantUser`

```ts
createTenantUser(tenantId: string, input: CreateTenantUserInputBody, signal?: AbortSignal): Promise<UserResource>
```

Creates an account that can sign in immediately with the password it was given.

#### `disableTenantUser`

```ts
disableTenantUser(tenantId: string, userId: string, signal?: AbortSignal): Promise<UserResource>
```

Stops an account signing in from the next request onwards.

#### `enableTenantUser`

```ts
enableTenantUser(tenantId: string, userId: string, signal?: AbortSignal): Promise<UserResource>
```

Clears the offboarding marker, so the account signs in with the password it already had.

#### `setTenantUserPassword`

```ts
setTenantUserPassword( tenantId: string, userId: string, input: SetUserPasswordInputBody, signal?: AbortSignal ): Promise<UserResource>
```

Sets a new password: the operator's reset.


### Schedules

#### `listSchedules`

```ts
listSchedules(signal?: AbortSignal): Promise<ScheduleResource[]>
```

#### `createSchedule`

```ts
createSchedule(schedule: ScheduleBody, signal?: AbortSignal): Promise<ScheduleResource>
```

#### `updateSchedule`

```ts
updateSchedule(scheduleId: string, schedule: ScheduleBody, signal?: AbortSignal): Promise<ScheduleResource>
```

#### `deleteSchedule`

```ts
deleteSchedule(scheduleId: string, signal?: AbortSignal): Promise<void>
```


### Node catalogue

#### `listNodeTypes`

```ts
listNodeTypes(signal?: AbortSignal): Promise<Definition[]>
```

Lists the registered node types.

#### `nodeIconUrl`

```ts
nodeIconUrl(nodeType: string, options: { version?: string; theme?: GetNodeIconTheme } = {}): string
```

Absolute URL of a node's artwork, for an `<img src>`.

#### `loadNodePropertyOptions`

```ts
loadNodePropertyOptions( nodeType: string, input: LoadOptionsInputBody, signal?: AbortSignal ): Promise<LoadOptionsResource>
```

Resolves a node's dynamic property options against its current configuration.

#### `loadNodePropertySchema`

```ts
loadNodePropertySchema( nodeType: string, input: LoadOptionsInputBody, signal?: AbortSignal ): Promise<LoadSchemaResource>
```

Resolves a resource mapper's columns.

#### `getExpressionGrammar`

```ts
getExpressionGrammar(signal?: AbortSignal): Promise<ExpressionGrammar>
```

Reads the expression surface the editor validates against, served rather than duplicated.


### Interop

#### `importWorkflow`

```ts
importWorkflow(document: ImportWorkflowInputBody, signal?: AbortSignal): Promise<ImportedWorkflowResource>
```

Imports an n8n workflow as a KilasFlow draft.

#### `convertWorkflowFragment`

```ts
convertWorkflowFragment(input: ConvertFragmentInputBody, signal?: AbortSignal): Promise<ConvertedFragmentResource>
```

Translates n8n JSON — nodes copied from an n8n canvas, or a whole export — into KilasFlow nodes and connections, with the same translator `importWorkflow` uses, and saves nothing.

#### `exportWorkflow`

```ts
exportWorkflow(workflowId: string, signal?: AbortSignal): Promise<ExportedWorkflowResource>
```

Exports a workflow as n8n-compatible JSON plus what the format could not carry.


### System

#### `getHealth`

```ts
getHealth(signal?: AbortSignal): Promise<HealthOutputBody>
```

Liveness: the process is up.

#### `getReady`

```ts
getReady(signal?: AbortSignal): Promise<ReadyOutputBody>
```

Readiness: the instance can serve requests, including database reachability.


### Datastores

#### `getDatastore`

```ts
getDatastore(datastoreId: string, signal?: AbortSignal): Promise<DatastoreResource>
```

Reads one datastore with its live columns in definition order.

#### `listDatastoreRows`

```ts
listDatastoreRows( datastoreId: string, query: ListDatastoreRowsParams = {}, signal?: AbortSignal ): Promise<RowListOutputBody>
```

Lists one datastore's rows in id order with the cursor for the next page.

#### `getDatastoreRow`

```ts
getDatastoreRow(datastoreId: string, rowId: number, signal?: AbortSignal): Promise<GetDatastoreRow200>
```

Returns one row by id.


### Datastore management

#### `listDatastores`

```ts
listDatastores(signal?: AbortSignal): Promise<DatastoreListOutputBody>
```

Lists every datastore in the workspace.

#### `createDatastore`

```ts
createDatastore(input: { name: string; columns?: DatastoreColumn[] }, signal?: AbortSignal): Promise<DatastoreResource>
```

Creates a datastore; columns may arrive with it or be added afterwards through {@link addDatastoreColumn}.

#### `renameDatastore`

```ts
renameDatastore(datastoreId: string, name: string, signal?: AbortSignal): Promise<DatastoreResource>
```

Renames a datastore; columns change through the column methods.

#### `deleteDatastore`

```ts
deleteDatastore(datastoreId: string, signal?: AbortSignal): Promise<void>
```

Deletes a datastore and every row it holds.

#### `clearDatastore`

```ts
clearDatastore(datastoreId: string, signal?: AbortSignal): Promise<ClearedDatastoreOutputBody>
```

Deletes every row and keeps the schema.

#### `addDatastoreColumn`

```ts
addDatastoreColumn(datastoreId: string, column: DatastoreColumn, signal?: AbortSignal): Promise<DatastoreColumnResource>
```

Appends one user column to a datastore.

#### `renameDatastoreColumn`

```ts
renameDatastoreColumn( datastoreId: string, columnName: string, newName: string, signal?: AbortSignal ): Promise<DatastoreColumnResource>
```

Renames one user column.

#### `deleteDatastoreColumn`

```ts
deleteDatastoreColumn(datastoreId: string, columnName: string, signal?: AbortSignal): Promise<void>
```

Removes one user column.

#### `updateDatastoreRows`

```ts
async updateDatastoreRows( datastoreId: string, filter: Filter, values: UpdateRowsInputBody['values'], signal?: AbortSignal ): Promise<UpdateRowsOutputBody>
```

Sets columns on every row matching the filter and reads the touched rows back with the match count.

#### `deleteDatastoreRows`

```ts
async deleteDatastoreRows(datastoreId: string, filter: Filter, signal?: AbortSignal): Promise<DeleteRowsOutputBody>
```

Removes every row matching the filter and reads the removed rows back with the count.

#### `upsertDatastoreRow`

```ts
async upsertDatastoreRow( datastoreId: string, filter: Filter, values: UpsertRowInputBody['values'], options?: AbortSignal | IdempotentWriteOptions ): Promise<UpsertRowOutputBody>
```

Updates every row matching the filter, or inserts one row from the values when nothing matches.

#### `incrementDatastoreRows`

```ts
async incrementDatastoreRows( datastoreId: string, filter: Filter, column: string, amount = 1, signal?: AbortSignal ): Promise<IncrementRowsOutputBody>
```

Adds to a number column on every row matching the filter, in one statement, and resolves the rows as that statement left them.

#### `exportDatastoreRows`

```ts
exportDatastoreRows( datastoreId: string, options: { includeSystemColumns?: boolean } = {}, signal?: AbortSignal ): Promise<string>
```

Streams the datastore's rows as RFC 4180 CSV in id order — one header row plus one record per row — and resolves the file as text.

#### `importDatastoreRows`

```ts
importDatastoreRows(datastoreId: string, csv: string, signal?: AbortSignal): Promise<CSVImportReport>
```

Imports rows from a CSV file with a header row naming user columns.

#### `insertDatastoreRow`

```ts
async insertDatastoreRow( datastoreId: string, values: InsertRowInputBody['values'], options?: AbortSignal | IdempotentWriteOptions ): Promise<InsertDatastoreRow201>
```

Writes one row and reads it back.


### Embedding

#### `createEmbedSession`

```ts
async createEmbedSession(request: EmbedSessionRequest, signal?: AbortSignal): Promise<EmbedSessionResource>
```

Mints a short-lived session for one host origin, scoped to one workflow or one datastore.


## Browser entry point (`@kilasflow/sdk/browser`)

The page half: mount the embedded editor, drive it, and watch a run. Nothing here needs or accepts a privileged API key.


### Exports

#### `EmbedSessionHandle`

```ts
export interface EmbedSessionHandle
```

What `createEmbedSession` returned, as the browser receives it.

#### `EMBED_PROTOCOL_VERSION`

```ts
export const EMBED_PROTOCOL_VERSION = 2
```

The postMessage protocol this SDK speaks with the embedded editor.

#### `MountOptions`

```ts
export interface MountOptions
```

#### `EditorEvent`

```ts
export type EditorEvent = | { type: 'ready'; workflowId: string; /** The frame's protocol version; 1 when the frame did not say. */ protocol: number } | { type: 'workflow-saved'; workflowId: string; revision: number } /** A revision was pinned as the one production traffic runs. */ | { type: 'workflow-published'; workflowId: string; versionId: string; revision: number } | { type: 'execution-started'; workflowId: string; executionId: string } | { type: 'execution-finished'; workflowId: string; executionId: string; status: string } /** The canvas gained or lost unsaved edits (`dirty` is the new state). */ | { type: 'dirty-changed'; workflowId: string; dirty: boolean } /** * The session token is sixty seconds from expiring (or already past it). * Mint a fresh session and hand it over with `setSession` — the editor is * not reloaded and the user's work survives. */ | { type: 'session-expiring'; workflowId: string; expiresAt: string } /** * The server refused the token outright. Nothing this frame can do will * recover it: mint a new session and `setSession` it, or unmount. */ | { type: 'unauthorized'; workflowId: string }
```

Messages the embedded editor sends to its host.

#### `CommandFailure`

```ts
export type CommandFailure = { ok: false; code: string; message?: string; dirty?: boolean }
```

A command the host sent was refused; `code` names the refusal.

#### `SaveResult`

```ts
export type SaveResult = { ok: true; saved: boolean; dirty: boolean; revision?: number } | CommandFailure
```

#### `RunResult`

```ts
export type RunResult = { ok: true; executionId: string | null; status?: string } | CommandFailure
```

#### `FocusResult`

```ts
export type FocusResult = { ok: true } | CommandFailure
```

#### `MountedEditor`

```ts
export interface MountedEditor
```

#### `mountWorkflowEditor`

```ts
export function mountWorkflowEditor(options: MountOptions): MountedEditor
```

Mounts the embedded editor in an iframe and performs the session handshake.

#### `ExecutionEvent`

```ts
export interface ExecutionEvent
```

One standardized execution event, as the live feed sends it.

#### `ExecutionEventOptions`

```ts
export interface ExecutionEventOptions
```

#### `subscribeExecutionEvents`

```ts
export function subscribeExecutionEvents(options: ExecutionEventOptions): () => void
```

Subscribes to an execution's live events.


## Shared (`@kilasflow/sdk`)

What both halves use. The root entry point re-exports the server and browser modules plus the generated model types, so a non-browser-restricted bundle imports `@kilasflow/sdk` once.


### Exports

#### `ProblemDetail`

```ts
export interface ProblemDetail
```

RFC 9457 problem document, as the API returns it.

#### `TransportOptions`

```ts
export interface TransportOptions
```


### Classes

#### `KilasFlowError`

```ts
export class KilasFlowError
```

Every non-success response becomes one of these.

#### `Transport`

```ts
export class Transport
```


### Methods

#### `constructor`

```ts
constructor(status: number, message: string, problem?: ProblemDetail, retryAfterSeconds?: number)
```

#### `constructor`

```ts
constructor(options: TransportOptions)
```

#### `url`

```ts
url(path: string, query?: Record<string, string | number | boolean | string[] | undefined>): string
```

Builds an absolute API URL with encoded query parameters.

