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

## Types

`src/generated/models.ts` is generated from the server's own OpenAPI document
by `pnpm generate:types`, and `pnpm generate:types:check` fails if it has
drifted. No endpoint shape is defined twice.

## Versioning

`SDK_VERSION` follows semver for this package's surface. `API_VERSION` is the
API it targets, versioned by its `/api/v1` path. A host can upgrade one without
the other.

## Example

`examples/host-page` is a complete, runnable integration: backend mints a
session, page mounts the editor, page receives execution events.
