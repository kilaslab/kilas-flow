# Host page example

A minimal, complete integration: a host backend mints an embed session, the
page mounts the editor, and the page receives execution events.

The split is the point. `server.mjs` holds the API credential and never ships
to a browser; `index.html` holds no credential at all and receives only a
short-lived, workflow-scoped token.

## Run it

Nothing is published yet: neither the image nor the SDK package exists
outside the checkout, so build both before starting — `make docker` for the
image, `cd sdk && pnpm install && pnpm build` for the package. The commands
below then read the way they will once a release tag exists.

```sh
# 1. Get this example without the repository (or copy these three files out
#    of it): index.html, server.mjs, package.json.
# 2. A published KilasFlow image with embedding enabled for this origin:
docker run --rm -p 8080:8080 \
  -e KILASFLOW_EMBED_SIGNING_KEY="$(openssl rand -base64 32)" \
  -e KILASFLOW_EMBED_ALLOWED_ORIGINS="http://localhost:4173" \
  kilasflow:latest   # or ghcr.io/kilaslab/kilasflow:v0.1.0 once a tag exists

# 3. The host backend — the SDK, installed from the checkout until the package
#    is on npm (see package.json), never vendored:
npm install
KILASFLOW_URL=http://127.0.0.1:8080 KILASFLOW_API_KEY=kfa1.… npm start

# 4. Open http://localhost:4173
```

Pin both versions in production: the exact image tag (`v0.1.0`, not `latest`)
and the exact SDK version in `package.json`. `KILASFLOW_API_KEY` is the
host's tenant-scoped key; the page never sees it — the backend mints an
embed session and per-execution stream tickets, and the page spends those.
