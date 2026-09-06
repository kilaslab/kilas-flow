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

No checkout of the KilasFlow repository: the server is a published
container image and the SDK is the published package. Copy these four
files out of the repository (`package.json`, `server.mjs`,
`tenant.html`, this README) into an empty directory and start there.

```sh
# 1. A published KilasFlow image with authentication and embedding enabled
#    for this origin:
docker run --rm -p 8080:8080 \
  -e KILASFLOW_AUTH_ENABLED=true \
  -e KILASFLOW_AUTH_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_BOOTSTRAP_EMAIL=owner@example.com \
  -e KILASFLOW_BOOTSTRAP_PASSWORD="$(openssl rand -base64 24)" \
# 2. One API key per tenant. The dashboard session arrives as a cookie, so
#    keep a jar: log in as the bootstrap owner, then mint the first
#    tenant's key through the API (the token is shown once — note it):
KILASFLOW_URL=http://127.0.0.1:8080
curl -s -c jar.txt -X POST $KILASFLOW_URL/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","password":"<bootstrap password>"}'
curl -s -b jar.txt -X POST $KILASFLOW_URL/api/v1/api-keys \
  -H 'Content-Type: application/json' \
  -d '{"label":"reference host"}'

# Tenant provisioning is an operator action: there is no self-service
# signup endpoint and no user-invitation endpoint. Insert the second
# tenant's row at the store layer (or the equivalent SQL against the
# deployment's database):
#   INSERT INTO tenants (id, name) VALUES ('birch', 'birch');
# then create that tenant's owner and first key the same way — at the
# store layer (`CreateUser` + `CreateAPIKey`), because both calls are
# scoped to the caller's own tenant and cannot reach into a new one.
# The bootstrap tenant already exists as `default`, so its key is the
# one minted above.
npm install
KILASFLOW_URL=http://127.0.0.1:8080 \
  TENANT_A_API_KEY=kfa1.… \
  TENANT_B_API_KEY=kfa1.… \
  npm start

# 4. Open http://localhost:4174 — Acme and Birch side by side, each with
#    its own editor, webhook, and signups list.
```

Pin both versions in production: the exact image tag (`v0.1.0`, not
`latest`) and the exact SDK version in `package.json`.

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
  through `/api/:tenant/fire` and learns the execution id, never the
  header value.
- **Owned reads.** Stream tickets and execution polls re-check that the
  execution belongs to the tenant's workflow before answering.
- **Isolated datastores.** Each tenant provisions `reference-<tenant>`
  with its own key and reads it back filtered; naming another tenant's
  datastore answers 404.

## Datastore note

The datastore path here is host-side REST (`/api/v1/datastores/*`) with
the tenant's key, because the SDK has no datastore methods yet and embed
tokens are default-denied on those endpoints — backend-key-only by
construction. Writing from inside a workflow uses the datastore node
(`kilasflow.datastore`, insert with `dataTableId` and a `columns`
mapping); see the [embedding guide](../../../docs/src/content/docs/guides/embedding.md)
for the exact parameters.
