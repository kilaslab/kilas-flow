# Host page example

A minimal, complete integration: a host backend mints an embed session, the
page mounts the editor, and the page receives execution events.

The split is the point. `server.mjs` holds the API credential and never ships
to a browser; `index.html` holds no credential at all and receives only a
short-lived, workflow-scoped token.

## Run it

```sh
# 1. A KilasFlow deployment with embedding enabled for this origin:
KILASFLOW_EMBED_SIGNING_KEY="$(openssl rand -base64 32)" \
KILASFLOW_EMBED_ALLOWED_ORIGINS="http://localhost:4173" \
  ./bin/kilasflow

# 2. The host backend, which proxies the page and mints sessions:
KILASFLOW_URL=http://127.0.0.1:8080 node server.mjs

# 3. Open http://localhost:4173
```
