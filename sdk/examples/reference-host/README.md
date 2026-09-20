# Reference host application

A complete multi-tenant integration: one host process serves **two tenants**
(Acme and Birch), each with its own KilasFlow API key, workflow, credential,
webhook, and datastore. Each tenant page embeds the workflow editor for an
end user, fires a signup webhook, observes the execution, and reads the
tenant's datastore back.

The split is the point. `server.mjs` holds the API credentials and never
ships to a browser; `tenant.html` holds no credential at all and receives
only a short-lived, workflow-scoped token for its own tenant.

## Run it

Nothing is published yet: `ghcr.io/kilaslab/kilasflow` holds no image and
`@kilasflow/sdk` is not on npm, so both come from the checkout for now —
`make docker` for the image, and `pnpm install && pnpm build` in `sdk/`
for the package this directory depends on by path. The commands below are
the published shape, with the local build noted where it differs.

```sh
# 1. Build the image from the checkout and give it the registry name. The tag
#    below is the exact version `make docker` stamps — its VERSION default,
#    `git describe --tags --always --dirty` — because what it builds is
#    `kilasflow:<that>`, not the published name.
make docker
docker tag "kilasflow:$(git describe --tags --always --dirty)" ghcr.io/kilaslab/kilasflow:v0.1.0

# 2. KilasFlow itself: authentication and embedding for this origin, credential
#    storage (the host stores a header credential per tenant — without the
#    encryption key the server runs but refuses to store one), and a named
#    volume, because the state file below assumes the installation kept its
#    database across restarts.
export KILASFLOW_OPERATOR_KEY="$(printf 'kfa1_%s_%s' \
  "$(openssl rand -hex 6)" "$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')")"
docker run --rm -p 8080:8080 \
  -v kilasflow-reference-data:/app/data \
  -e KILASFLOW_AUTH_ENABLED=true \
  -e KILASFLOW_AUTH_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_AUTH_OPERATOR_KEY="$KILASFLOW_OPERATOR_KEY" \
  -e KILASFLOW_AUTH_BOOTSTRAP_EMAIL=owner@example.com \
  -e KILASFLOW_BOOTSTRAP_PASSWORD=choose-a-first-password \
  -e KILASFLOW_ENCRYPTION_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_EMBED_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_EMBED_ALLOWED_ORIGINS=http://localhost:4174 \
  ghcr.io/kilaslab/kilasflow:v0.1.0

# 3. Provision both tenants through the operator surface. The operator key is
#    not a tenant key: it is scoped to the `operator` tenant and it is the only
#    credential that may create tenants, users or another tenant's key. It is
#    registered at boot from `KILASFLOW_AUTH_OPERATOR_KEY` (the variable
#    `auth.operator_key_env` names), and it has to be shaped like every other
#    key — `kfa1_`, a hex prefix, then the secret — or the server refuses to
#    start. Every minted token is shown once, as kfa1_<prefix>_<secret> — note
#    each down.
KILASFLOW_URL=http://127.0.0.1:8080
op() { curl -sS -H "Authorization: Bearer $KILASFLOW_OPERATOR_KEY" \
  -H 'Content-Type: application/json' "$@"; }

op -X POST $KILASFLOW_URL/api/v1/tenants -d '{"id":"acme","name":"Acme"}'
op -X POST $KILASFLOW_URL/api/v1/tenants -d '{"id":"birch","name":"Birch"}'

# A first user per tenant — the whole of onboarding, no SQL and no store call.
op -X POST $KILASFLOW_URL/api/v1/tenants/acme/users \
  -d '{"email":"owner@acme.example","name":"Acme Owner","password":"choose-one"}'
op -X POST $KILASFLOW_URL/api/v1/tenants/birch/users \
  -d '{"email":"owner@birch.example","name":"Birch Owner","password":"choose-one"}'

# One API key per tenant, minted for that tenant by the operator. The bootstrap
# tenant `default` also exists; this example does not use it.
op -X POST $KILASFLOW_URL/api/v1/tenants/acme/api-keys -d '{"label":"reference host"}'
op -X POST $KILASFLOW_URL/api/v1/tenants/birch/api-keys -d '{"label":"reference host"}'

# 4. Point the host at those two keys and start it. The SDK is not on npm yet:
#    `cd ../../ && pnpm install && pnpm build` first, then `pnpm install` here
#    (package.json depends on it by path).
npm install
KILASFLOW_URL=http://127.0.0.1:8080 \
  TENANT_A_API_KEY=kfa1_<acme prefix>_<acme secret> \
  TENANT_B_API_KEY=kfa1_<birch prefix>_<birch secret> \
  npm start

# 5. Open http://localhost:4174 — Acme and Birch side by side, each with
#    its own editor, webhook, and signups list.
```

Pin both versions in production once both are published: the exact image
tag (`v0.1.0`, not `latest`) and the exact SDK version in `package.json`.
Until then `package.json` names the SDK by path (`file:../..`), which is
what makes this directory runnable from the checkout — switch it back to a
version range on the day the package is on npm.

On first boot the server provisions each tenant idempotently — import a
webhook workflow (the import response is the only call that reports the
minted webhook address), store a header credential, attach it to the
trigger, activate, and ensure a datastore — and records the ids in
`.reference-state.json` (gitignored local state, not a migration). A
later boot reuses the state after verifying the workflow and credential
still exist.

## What each tenant demonstrates

- **Own key, own client.** `tenantClientFactory` builds one
  fixed-credential client per tenant. There is no shared client and no
  key swap, so one customer's request cannot be sent as another's.
- **Authorized minting.** `/api/:tenant/embed-session` looks the slug up
  in the host's own registry first. An unknown slug answers 404 before
  any token exists — minting is the host's authorization decision, and
  the server cannot make it for you.
- **Verified origin.** The session origin is a server-side constant
  (`HOST_ORIGIN`), never taken from request headers.
- **Authenticated webhook.** The trigger requires the stored header
  credential; a delivery without it answers 401 and never becomes an
  execution. The secret lives in the backend state file — the page fires
  through `/api/:tenant/fire` and never sees the header value. The run id is
  *not* in the delivery acknowledgement: the trigger is in n8n's `onReceived`
  mode, whose answer is n8n's own `{"message":"Workflow was started"}`, so a
  host finds its run through the tenant's own key — the newest execution of
  that workflow — and then owns the id. `server.mjs` still reads
  `receipt.executionId` from the old `{executionId, status}` acknowledgement and
  needs that one lookup before it works against a current server.
- **Owned reads.** Stream tickets and execution polls re-check that the
  execution belongs to the tenant's workflow before answering.
- **Isolated datastores.** Each tenant provisions `reference-<tenant>`
  with its own key and reads it back filtered; naming another tenant's
  datastore answers 404.

## Datastore note

The datastore path here is host-side REST (`/api/v1/datastores/*`) with
the tenant's key. The SDK has datastore methods now — `insertDatastoreRow`,
`listDatastoreRows`, `getDatastoreRow`, `updateDatastoreRows`,
`deleteDatastoreRows`, `upsertDatastoreRow`, the CSV export/import pair, and the
column operations — so a host written today can use those instead; this sample
predates them and the REST call is the same request.

Embed sessions can reach data too, and only within one datastore: a session
minted for a `datastoreId` with `datastore:read` or `datastore:write` may read
the table definition and read or write its **rows**. Everything that reshapes the
table — listing datastores, clearing it, adding, renaming or dropping a column,
and the CSV import — stays with the backend key, and a workflow-scoped session
never reaches any of it. Writing from inside a workflow uses the datastore node
(`kilasflow.datastore`, insert with `dataTableId` and a `columns`
mapping); see the [embedding guide](../../../docs/src/content/docs/guides/embedding.md)
for the exact parameters and the scope table.
