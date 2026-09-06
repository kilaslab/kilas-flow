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
  headers: { Authorization: `Bearer ${process.env.KILASFLOW_API_KEY}` }
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

One thin, typed method per API operation — all 46 under `/api/v1`, grouped
here the way the [API contract](../docs/src/content/docs/reference/api-contract.md)
groups them. Every method takes an `AbortSignal` last, resolves `Promise<void>`
for 204 responses, and surfaces failures as `KilasFlowError` (RFC 9457).

| Group | Methods |
| --- | --- |
| Workflows | `listWorkflows`, `getWorkflow`, `createWorkflow`, `updateWorkflow`, `deleteWorkflow`, `runWorkflow`, `activateWorkflow`, `deactivateWorkflow`, `listWorkflowVersions`, `getWorkflowVersion`, `publishWorkflowVersion`, `restoreWorkflowVersion`, `listWorkflowPublishEvents` |
| Executions | `listExecutions`, `getExecution`, `cancelExecution`, `executionEventsUrl` |
| Credentials | `listCredentialTypes`, `listCredentials`, `createCredential`, `getCredential`, `updateCredential`, `deleteCredential`, `testCredential`, `testCredentialPayload` |
| Auth and keys | `login`, `logout`, `getMe`, `listApiKeys`, `createApiKey`, `revokeApiKey`, `createStreamTicket` |
| Schedules | `listSchedules`, `createSchedule`, `updateSchedule`, `deleteSchedule` |
| Node catalogue | `listNodeTypes`, `nodeIconUrl`, `loadNodePropertyOptions`, `loadNodePropertySchema`, `getExpressionGrammar` |
| Interop | `importWorkflow`, `exportWorkflow` |
| Embed | `createEmbedSession` |
| System | `getHealth`, `getReady` |

Two operations stream rather than answer JSON, so they are URL builders
instead of request methods: `executionEventsUrl` for the live SSE stream
(spend a `createStreamTicket` ticket as `?ticket=` when the caller cannot set
an `Authorization` header) and `nodeIconUrl` for artwork served with an inert
content policy and a long immutable cache lifetime. `importWorkflow` takes the
n8n document as `unknown` inside a typed envelope — it is untrusted input —
while its diagnostics and minted webhook URLs are fully typed.

Authentication is always explicit configuration. `headers` is a plain record
rather than a dedicated `apiKey` field because deployments authenticate
differently — a bearer token, a gateway header, a signed proxy — and inventing
one shape would force the others to work around it.

## Browser: mount the editor and watch a run

```ts
import { mountWorkflowEditor, subscribeExecutionEvents } from '@kilasflow/sdk/browser';

const editor = mountWorkflowEditor({
  container: document.getElementById('editor')!,
  baseUrl: 'https://flows.example',
  session, // fetched from your own backend
  onEvent(event) {
    if (event.type === 'execution-started') {
      subscribeExecutionEvents({
        baseUrl: 'https://flows.example',
        executionId: event.executionId,
        onEvent: (execution) => console.log(execution.type, execution.nodeId)
      });
    }
  }
});

// Removes the iframe, its message listener, and its handshake timer.
editor.unmount();
```

The mount performs the verified handshake: the editor announces itself, and
only then is the token posted — to the editor's exact origin, never `'*'`.
Every message received is checked against `event.origin` and against the frame
it came from before its payload is read.

## Versioning

`SDK_VERSION` (`sdk/src/version.ts`) follows semver for this package's
surface: major on a breaking change, minor on additive surface, patch on
fixes. `API_VERSION` is the API it targets, versioned by its `/api/v1` path,
and moves only when a new `/api/vN` path ships. A host can upgrade one
without the other.

The two numbers in this package MUST agree: `SDK_VERSION` mirrors `version`
in `package.json`, and `make sdk-version-check` (run in CI) fails the build
when they differ. Which server build you are talking to is neither of these —
it is `info.version` in the OpenAPI document (and the binary's `-version`
flag). Pin that, not these two. Full contract:
`docs/src/content/docs/reference/api-contract.md`.

## Licence

Apache-2.0, matching the repository root `LICENSE` — one licence for the
whole project, with an explicit patent grant for hosts embedding this
package.

## Example

`examples/host-page` is a complete, runnable integration: backend mints a
session, page mounts the editor, page receives execution events.
