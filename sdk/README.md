# @kilasflow/sdk

Host integration SDK for KilasFlow: workflow management, embedded editor
sessions, and typed execution events.

The SDK is a convenience layer over the documented REST API — never an
alternate source of state or authorization. It holds no cache, makes no
decision the server would not make, and grants no permission the API does not.

## Two entry points

They are separate on purpose, and nothing imports across the boundary.

| Import | Runs on | Needs a credential |
| --- | --- | --- |
| `@kilasflow/sdk/server` | your backend | yes — the host's API key |
| `@kilasflow/sdk/browser` | the page | no |

A browser bundle built from `/browser` cannot carry a server credential even by
mistake, because there is no import path from it to the server client.

## Backend: manage workflows and mint a session

```ts
import { KilasFlowClient } from '@kilasflow/sdk/server';

const kilasflow = new KilasFlowClient({
  baseUrl: 'https://flows.example',
  apiKey: process.env.KILASFLOW_API_KEY // a `kfa1_…` key, tenant-scoped
});

const workflow = await kilasflow.createWorkflow({
  schemaVersion: 1, name: 'Onboarding', nodes: [], connections: [], settings: {}
});

const session = await kilasflow.createEmbedSession({
  workflowId: workflow.id,
  scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
  origin: 'https://app.acme.example', // exact; no wildcards
  branding: { name: 'Acme Flows', accent: '#0ea5e9' }
});
```

## Operation surface

One thin, typed method per API operation — all 76 under `/api/v1`, grouped
here the way the [API contract](https://github.com/kilaslab/kilas-flow/blob/main/docs/src/content/docs/reference/api-contract.md)
groups them. Every method takes an `AbortSignal` last, resolves `Promise<void>`
for 204 responses, and surfaces failures as `KilasFlowError` (RFC 9457) — except
the three writes that can be retried safely, which take an
`IdempotentWriteOptions` object instead ([Retrying safely](#retrying-safely)).

| Group | Methods |
| --- | --- |
| Workflows | `listWorkflows`, `getWorkflow`, `listWorkflowWebhooks`, `createWorkflow`, `updateWorkflow`, `deleteWorkflow`, `runWorkflow`, `activateWorkflow`, `deactivateWorkflow`, `listWorkflowVersions`, `getWorkflowVersion`, `publishWorkflowVersion`, `restoreWorkflowVersion`, `listWorkflowPublishEvents`, `getWorkflowDiagnostics` |
| Executions | `listExecutions`, `getExecution`, `cancelExecution`, `executionEventsUrl`, `iterateExecutions` |
| Credentials | `listCredentialTypes`, `listCredentials`, `createCredential`, `getCredential`, `updateCredential`, `deleteCredential`, `testCredential`, `testCredentialPayload` |
| Auth and keys | `login`, `logout`, `getMe`, `listApiKeys`, `createApiKey`, `revokeApiKey`, `createStreamTicket` |
| Schedules | `listSchedules`, `createSchedule`, `updateSchedule`, `deleteSchedule` |
| Node catalogue | `listNodeTypes`, `nodeIconUrl`, `loadNodePropertyOptions`, `loadNodePropertySchema`, `getExpressionGrammar` |
| Interop | `importWorkflow`, `exportWorkflow` |
| Datastores | `listDatastores`, `createDatastore`, `getDatastore`, `renameDatastore`, `deleteDatastore`, `clearDatastore`, `addDatastoreColumn`, `renameDatastoreColumn`, `deleteDatastoreColumn`, `listDatastoreRows`, `getDatastoreRow`, `insertDatastoreRow`, `updateDatastoreRows`, `deleteDatastoreRows`, `upsertDatastoreRow`, `incrementDatastoreRows`, `iterateDatastoreRows`, `exportDatastoreRows`, `importDatastoreRows`, `datastoreFilter`, `paginateCursor` |
| Tenants and accounts | `listTenants`, `createTenant`, `getTenant`, `deleteTenant`, `listTenantUsers`, `createTenantUser`, `disableTenantUser`, `enableTenantUser`, `setTenantUserPassword`, `createTenantApiKey` |
| Embed | `createEmbedSession` |
| System | `getHealth`, `getReady` |

The tenant group is the operator surface: every method in it needs an API key
scoped to the operator tenant and reaches across customers, so a tenant's own
key — and every embed session — is refused by the server. A host that provisions
its customers from its own control plane uses these; a host that provisions them
out of band, the way `examples/reference-host` does, never calls them.

Two operations stream rather than answer JSON, so they are URL builders
instead of request methods: `executionEventsUrl` for the live SSE stream
(spend a `createStreamTicket` ticket as `?ticket=` when the caller cannot set
an `Authorization` header) and `nodeIconUrl` for artwork served with an inert
content policy and a long immutable cache lifetime. `importWorkflow` takes the
n8n document as `unknown` inside a typed envelope — it is untrusted input —
while its diagnostics and minted webhook URLs are fully typed.

### Retrying safely

`runWorkflow`, `insertDatastoreRow` and `upsertDatastoreRow` take an
`IdempotentWriteOptions` object where the signal goes, so a retry after a
timeout repeats no side effect:

```ts
// One key per logical operation, persisted *before* the first attempt. A key
// generated per attempt would protect nothing.
const key = crypto.randomUUID();
await storeOperationKey(key);

const execution = await kilasflow.runWorkflow(workflow.id, { rows: 3 }, { idempotencyKey: key });

// The retry carries the SAME key, so it returns the first execution — the same
// record and id — instead of queueing a second run.
const again = await kilasflow.runWorkflow(workflow.id, { rows: 3 }, { idempotencyKey: key });
```

A key is unique per logical operation, never a constant: keys are shared by
every credential of the tenant, so one constant would make unrelated writes
collide. The same key with a different request is refused with a 409 whose
`problem.errors[0].value.code` is `idempotency_key_reused`. A 409 with
`idempotency_key_in_flight` means the first request is still running: wait
`error.retryAfterSeconds` and retry with the same key. The SDK generates no
keys and does not surface `Idempotent-Replayed` — it returns the body, and a
host that persisted its key before the first attempt needs no marker to be
correct. The whole contract is in
[Idempotent requests](../docs/src/content/docs/guides/idempotency.md).

## Datastores: embed reads versus backend management

Two halves, one API. The embed half — `getDatastore`, `listDatastoreRows`,
`getDatastoreRow`, `insertDatastoreRow` — is what a datastore-scoped session
may reach: one datastore, no schema work, refused outright for anything else.
Everything below is the backend half and sends the host's own tenant API key;
an embed token cannot reach it by design.

```ts
// Provisioning from the backend: create, shape, fill.
const store = await kilasflow.createDatastore({
  name: 'Customers',
  columns: [{ name: 'email', type: 'string' }]
});
await kilasflow.addDatastoreColumn(store.id, { name: 'tier', type: 'string' });
await kilasflow.importDatastoreRows(store.id, 'email,tier\nada@example.com,pro\n');
```

Column types are closed — `string`, `number`, `boolean`, `date` — and the
server normalises on read (numbers arrive as numbers, booleans as booleans,
dates as strings), so a host never branches on the driver. There is no
retype: a column's type is fixed at creation; rename or drop and re-add
instead.

### Filters are built, not written

The operator set is closed — `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`,
`lte`, `isEmpty`, `isNotEmpty` — and an unrecognised operator is a server-side
refusal, so the builder holds the set and a misspelling fails to compile.
`isEmpty` and `isNotEmpty` take no value; the builder withholds the slot.

```ts
import { datastoreFilter } from '@kilasflow/sdk/server';

const filter = datastoreFilter('and', [
  { columnName: 'tier', condition: 'eq', value: 'pro' },
  { columnName: 'email', condition: 'isNotEmpty' }
]);

await kilasflow.updateDatastoreRows(store.id, filter, { tier: 'vip' });
```

A filtered write cannot be expressed without a filter: the signature requires
one, and an empty filter is refused client-side — never sent — because on the
server it is a 422 that removes nothing, and must never read as "every row".
The refusal carries no row values, so a rejected write cannot leak its values
into logs. The server exposes no dry-run parameter on these endpoints; the
typed `matched`/`deleted`/`inserted` counts with the affected rows are the
whole answer.

### Paging without hand-rolled loops

`iterateDatastoreRows` pages to exhaustion on `nextCursor`, yielding one row
at a time; `iterateExecutions` does the same for executions through the shared
`paginateCursor` helper, so the two cursor surfaces cannot drift. Rows are
typed honestly: a datastore's columns are known at runtime, so hosts that
declare a schema pass it as `TRow` and hosts that do not take the permissive
default.

```ts
interface Customer { id: number; email: string; tier: string; [key: string]: unknown }

for await (const row of kilasflow.iterateDatastoreRows<Customer>(store.id, { limit: 200 })) {
  console.log(row.email, row.tier);
}
```

### CSV transfer

`exportDatastoreRows` resolves the file as text (`Accept: text/csv`,
`includeSystemColumns` adds `id`, `createdAt`, `updatedAt` around the user
columns); `importDatastoreRows` posts the file raw rather than
base64-in-JSON and resolves the per-line report — the server validates every
record before writing any row, so a file with a failed row imports nothing.

A workflow SQL node can never read a datastore: the Datastore node and this
API are the only paths.

## Credentials

`apiKey` is a convenience layered on `headers`, never a replacement for it.
It sends the key as `Authorization: Bearer <key>`; when `headers` already
carries an `Authorization` entry, that explicit value wins, so gateway
headers, signed proxies and other shapes keep working exactly as before.
`headers` is a supported credential path, not a transitional one.

### One client per tenant

A host holds one API key per tenant — the server issues tenant-scoped keys
and offers no impersonation, so an operator key used "on behalf of" a tenant
is not a model anything supports. Build one client per key with
`tenantClientFactory`, which takes the shared base URL and options once and
returns a function from tenant key to client:

```ts
import { tenantClientFactory } from '@kilasflow/sdk/server';

const forTenant = tenantClientFactory({ baseUrl: 'https://flows.example' });

// Per request, per tenant — never a shared client whose key is swapped.
const acme = forTenant(tenantKeyFor('acme'));
await acme.listWorkflows();
```

The rule is stated and tested: a client's credential is fixed for its
lifetime. There is no setter and no refresh callback, and the constructor
copies the headers it is given, so a key cannot be reassigned by a concurrent
request handler. Build a client per request, or cache one client per tenant
id; never mutate.

### Rotation

Mint the new key, build a new client with it, direct new work at the new
client. In-flight requests on the old client finish or fail on their own — a
request authorized when it started and revoked before it finished fails with
a 401 the caller already handles. There is deliberately no
credential-refresh callback: one invites a host to hold a single long-lived
client across a rotation, which is the shared-mutable-client bug wearing a
different hat.

### Telling 401 from 403

Every failure surfaces as `KilasFlowError` with a numeric `status` — no
message text to parse. `401` is "your key is wrong"; `403` is "your key is
fine and this is not yours". A host retries, re-authenticates, or pages on
the number, not the prose.

Nothing in this package reads the process environment or any ambient source.
The example above passes `process.env` explicitly because the host chose to;
the SDK never looks one up itself, and a caller that supplies no credential
gets an unauthenticated request rather than a surprise.

## Browser: mount the editor and watch a run

```ts
import { mountWorkflowEditor, subscribeExecutionEvents } from '@kilasflow/sdk/browser';

const editor = mountWorkflowEditor({
  container: document.getElementById('editor')!,
  baseUrl: 'https://flows.example',
  session, // fetched from your own backend
  locale: 'id', // the language the editor's own chrome speaks
  onEvent(event) {
    if (event.type === 'execution-started') {
      // The backend minted a ticket for this execution (see below); the
      // page spends it without ever seeing the API key.
      subscribeExecutionEvents({
        baseUrl: 'https://flows.example',
        executionId: event.executionId,
        onEvent: (execution) => console.log(execution.type, execution.nodeId),
        ticket: () => fetchTicket(event.executionId)
      });
    }
  }

// Removes the iframe, its message listener, and its handshake timer.
editor.unmount();
```

The mount performs the verified handshake: the editor announces itself, and
only then is the token posted — to the editor's exact origin, never `'*'`.
Every message received is checked against `event.origin` and against the frame
it came from before its payload is read. `locale` rides along in that same
message: the editor checks it against the catalogs it ships and ignores one it
does not carry.

### Watching a run on an authenticated server

`EventSource` cannot send an `Authorization` header, so the backend exchanges
its credential for a single-use ticket and the page spends it as `?ticket=`:

```ts
// Backend: mint adjacent to the subscribe, per execution.
const ticket = await kilasflow.createStreamTicket(executionId);
// => { ticket: '…', executionId, expiresAt }

// Page: hand the SDK a minter, not a string. A ticket is single-use and
// lives for seconds, so the SDK calls it before every connect — first and
// every reconnect — and resumes after the last event received. A fixed
// string is spent once and only suits a stream that never reconnects.
async function fetchTicket(executionId: string): Promise<string> {
  const response = await fetch(`/api/stream-ticket?executionId=${executionId}`);
  return (await response.json()).ticket as string;
}
```

## Install

```sh
npm install @kilasflow/sdk
```

Pin the exact version in production. Releases are cut from `sdk-vX.Y.Z` tags
with npm provenance attested (`npm audit signatures` verifies it), and every
entry in [CHANGELOG.md](./CHANGELOG.md) states whether it is additive, a fix,
or breaking.

### Requirements

ESM only — there is no CommonJS build — and three subpaths: `.`, `./server`
and `./browser`. The package is type-checked in CI under TypeScript 5.9 with
`moduleResolution` `bundler`, `node16` and `nodenext`; the `node10` setting
cannot see the subpaths' `exports` entries. At runtime it needs a global
`fetch`: Node 18 or later, or a current browser (CI runs Node 24).

What the type checker needs depends on the entry point:

- `@kilasflow/sdk/server` on its own references `Headers` and `RequestInit` in
  `generated/models.d.ts`, so it needs fetch types — either `lib DOM` or
  `@types/node`. Without either, `tsc` reports TS2304 on those two; with
  `@types/node` it is clean.
- The root entry and `@kilasflow/sdk/browser` additionally reference
  `HTMLElement`, `HTMLIFrameElement` and `Event` in `browser.d.ts`, so they
  need `lib DOM` even when `@types/node` is present — a root import with
  `@types/node` still fails with two TS2304.

A Node-only project should import from `@kilasflow/sdk/server`, not the root.
`skipLibCheck: true` silences all of the above; `make sdk-package-check` runs
that variant as well.

### Unreleased changes

`main` may be ahead of the latest release. To use what `main` has, build the
package from a checkout and install the tarball it produces:

```sh
cd sdk && pnpm install && npm pack --pack-destination <dir>
npm install <dir>/kilasflow-sdk-X.Y.Z.tgz
```

## Versioning

`SDK_VERSION` (`sdk/src/version.ts`) follows semver for this package's
surface: major on a breaking change, minor on additive surface, patch on
fixes. `API_VERSION` is the API it targets, versioned by its `/api/v1` path,
and moves only when a new `/api/vN` path ships. A host can upgrade one
without the other.

The package stays on `0.x` until the server surface it targets is considered
stable: semver's pre-1.0 allowance is the honest description of an SDK whose
authentication story only just landed. No `1.0.0` is promised before then.

The two numbers in this package MUST agree: `SDK_VERSION` mirrors `version`
in `package.json`, and `make sdk-version-check` (run in CI, mirrored by
`test/version.test.mjs` under `pnpm test`) fails the build when they differ.
Which server build you are talking to is neither of these — it is
`info.version` in the OpenAPI document (and the binary's `-version` flag).
Pin that, not these two. Full contract:
`docs/src/content/docs/reference/api-contract.md`.

## Licence

Apache-2.0, matching the repository root `LICENSE` — one licence for the
whole project, with an explicit patent grant for hosts embedding this
package.

## Example

`examples/host-page` is a complete, runnable integration: backend mints a
session, page mounts the editor, page receives execution events. Its README
runs it against a pulled container image and the published package, with no
checkout of this repository.
