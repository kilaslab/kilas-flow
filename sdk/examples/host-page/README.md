# Host page example

A minimal, complete integration: a host backend mints an embed session, the
page mounts the editor, and the page receives execution events. **No checkout
of this repository is needed** — the image comes from a registry and the
package from npm.

The split is the point. `server.mjs` holds the API credential and never ships
to a browser; `index.html` holds no credential at all and receives only a
short-lived, workflow-scoped token.

## Run it

```sh
# 1. KilasFlow itself. Authentication is on for a reason: with it off, the
#    example's /api/stream-ticket route answers "authentication is not
#    configured on this instance". The operator key is shaped like every other
#    key — kfa1_, a hex prefix, then the secret — or the server refuses it.
export KILASFLOW_OPERATOR_KEY="$(printf 'kfa1_%s_%s' \
  "$(openssl rand -hex 6)" "$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')")"
docker run --rm -p 8080:8080 \
  -e KILASFLOW_AUTH_ENABLED=true \
  -e KILASFLOW_AUTH_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_AUTH_OPERATOR_KEY="$KILASFLOW_OPERATOR_KEY" \
  -e KILASFLOW_AUTH_BOOTSTRAP_EMAIL=owner@example.com \
  -e KILASFLOW_BOOTSTRAP_PASSWORD=choose-a-first-password \
  -e KILASFLOW_EMBED_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_EMBED_ALLOWED_ORIGINS=http://localhost:4173 \
  ghcr.io/kilaslab/kilasflow:v0.1.0

# 2. Mint this host's own key on the default tenant. The operator key is the
#    only credential that may mint one; the response's `token` field is the
#    kfa1_<prefix>_<secret> value for KILASFLOW_API_KEY below. It is shown
#    once — note it down.
curl -X POST \
  -H "Authorization: Bearer $KILASFLOW_OPERATOR_KEY" \
  -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/api/v1/tenants/default/api-keys \
  -d '{"label":"host-page"}'

# 3. Copy index.html, server.mjs and package.json out of this directory into
#    an empty one, then install the SDK from npm and start the host backend:
npm install
KILASFLOW_URL=http://127.0.0.1:8080 KILASFLOW_API_KEY=kfa1_<prefix>_<secret> npm start

# 4. Open http://localhost:4173
```

Pin both versions in production: the exact image tag (`v0.1.0`, not `latest`)
and the exact SDK version in `package.json` — this example pins `0.1.0`.
`KILASFLOW_API_KEY` is the host's tenant-scoped key; the page never sees it —
the backend mints an embed session and per-execution stream tickets, and the
page spends those.

The example mints an embed session for anyone who can reach its port. In a real
host, deciding who may open the editor is an authorization decision the host
makes, not one the server can make for it — see the `examples/reference-host`
README.

<!-- Delete this block after the first release is published; sdk/RELEASING.md
     lists it among the statements that flip the moment 0.1.0 is on npm. -->
> **Before the first release is published.** Neither the image nor the package
> exists yet, so build both from a checkout of this repository: `make docker`
> instead of the `docker run` above, and `npm install --no-save <path to the
> packed tarball>` instead of the plain `npm install`, which would 404 on the
> unpublished `0.1.0` pin (`cd sdk && pnpm install && npm pack
> --pack-destination <dir>` makes the tarball).
