---
title: JavaScript sidecar
description: How to run programmatic n8n community nodes in an opt-in Node sidecar, and exactly what that costs and does not protect.
sidebar:
  order: 6
---

A small number of community packages on npm are not declarative. They carry a
real `execute()` in JavaScript — a chat agent that talks to its provider, a tool
that shapes a payload — and no generated node pack can replicate them. The
JavaScript sidecar is the opt-in path that runs them.

It is off by default, it is the most expensive thing in the deployment, and it
buys the least coverage. Across the 100 most-viewed n8n.io templates (2,377 node
instances) only 11 were third-party `n8n-nodes-*` packages: **0.46%**. If you
run a stock install, do not turn this on. WAHA, Telegram and the OpenAPI
declarative packs cover the demand that was actually measured.

## What the deployment becomes

Today's runtime image is `gcr.io/distroless/static-debian12:nonroot` with a
single `CGO_ENABLED=0` Go binary and no shell. A sidecar deployment adds a Node
runtime and an npm dependency tree:

- Node 24 LTS in the image (`node:24-slim` is a working base), the `kilasflow`
  binary beside it, and the operator's packages installed into a prefix such as
  `/opt/sidecar` (`npm install --prefix /opt/sidecar <package>`).
- A writable data volume: the sidecar extracts its runner and creates one
  unix-socket directory per process under `sidecar.runtime_dir`.
- **Every process role that boots with `sidecar.enabled` must have Node and the
  packages.** The node catalogue is read from the packages at boot, so an `api`
  process with no Node starts with an empty community catalogue and reports
  those nodes unavailable.

A recipe built from this shape is [below](#a-docker-recipe); it is built and
booted by hand, not in CI.

## What the operator installs and accepts

| You install | Why |
| --- | --- |
| Node 24 LTS | the runtime. KilasFlow ships no Node. |
| The package (`npm install --prefix /opt/sidecar <pkg>`) | KilasFlow ships no community package and no dependency tree. |
| The package's peer dependencies | a package whose compiled code requires `n8n-workflow` at run time needs **you** to install it, under n8n's own terms. KilasFlow ships no stand-in. |

A package whose node file cannot load — a missing peer, for example — is a
*per-file* failure: that node is excluded with a named warning and the rest of
the package still loads. A broken manifest, a missing package directory, or a
network attempt while loading refuses the boot, naming the package.

KilasFlow itself ships exactly one thing here: a single clean-room runner script
(`sidecar/runner/runner.cjs`), embedded in the Go binary and extracted into the
runtime directory. It uses Node built-ins only, has no dependency of its own,
and contains no n8n code, types or bytes.

## Configuration

Every key is in the [configuration reference](/operate/configuration-reference/#sidecar).
The short version: `sidecar.enabled`, `sidecar.node_path` (empty means `node`
from PATH), `sidecar.packages_dir` (an npm `--prefix` directory, or one
directory per flat package), `sidecar.packages` (the allowlist — transitive
dependencies and unlisted packages are never loaded), and the limits
(`timeout`, `spawn_timeout`, `idle_timeout`, `max_heap_mb`, `max_rss_mb`,
`max_processes`, `max_output_bytes`).

## How the nodes appear

A community node registers as `sidecar.<slug(package)>.<slug(nodeName)>` with
`"source": "sidecar"` in the catalogue, beside `builtin` and `pack`, and is
filed under the **Community** category. The n8n importer does **not** map
`n8n-nodes-x.y` community types onto these, so an imported n8n workflow that
uses one does not run until its nodes are re-selected.

The package's credential classes are registered too. **Every field of a
community credential is encrypted and write-only** — the API never returns its
value, not even for a field such as a base URL, because this build cannot tell a
secret field from a non-secret one by inspection and withholding all of them is
the failure that leaks nothing.

**Always set `allowedDomains` on a credential a community node uses.** The
package author, not the workflow author, chooses the URLs the node requests, so
an unscoped credential lets the package reach any host the deployment's egress
policy allows.

## Trust model and limits

| Bound | How |
| --- | --- |
| Process | one process per tenant; all allowlisted packages share it |
| Environment | empty except `LANG` and `TZ`; the credential master key never crosses |
| Filesystem | Node's permission model with one read grant, the packages directory |
| Network | a JS guard that refuses direct sockets, DNS, datagram and `fetch`; HTTP only through a host-proxied call |
| Egress | the same `internal/safehttp` policy a native node uses, narrowed by every held credential's `allowedDomains` |
| Wall clock | `sidecar.timeout` per run, `sidecar.spawn_timeout` per cold start |
| Memory | `sidecar.max_heap_mb` (V8 heap) plus a host RSS watchdog (`sidecar.max_rss_mb`) |
| Output | `sidecar.max_output_bytes` decoded payload, plus a frame bound |
| Processes | `sidecar.max_processes` tenant processes at once |

### What is *not* a boundary

The JS guard and Node's permission model are **seat belts, not a sandbox against
malicious code**. Stated plainly:

- A package can still signal same-uid processes — the host and other tenants'
  sidecars. `--permission` does not stop `process.kill` (verified). The runner
  refuses it, but that is a guard, not a kernel boundary.
- All allowlisted packages share **one process per tenant**, so one package can
  read another package's credentials for that tenant.
- Decrypted credentials linger in a warm process until `sidecar.idle_timeout`.
- A package that logs its own credentials writes them to the host log.
- A host `SIGKILL` can leave a busy-looping orphan Node process until the
  container restarts.

For a hard boundary, use `sidecar.wrapper` to run the child under a separate uid
or namespace (`setpriv`, a sandbox launcher), or the container and network
policy. KilasFlow does not verify what the wrapper isolates — that is the
deployment's boundary, not this build's guarantee.

Also remember: the catalogue is a **boot snapshot**. Changing packages on disk
needs a restart, and a run for a `(node, version)` the process no longer has
fails with a named message saying so.

## Supported and not supported

Runs: one main input and at least one main output, no triggers or webhooks, no
polling, no binary data (an input or output item carrying an attachment fails the
run), no `resourceLocator`/`resourceMapper`/`filter` properties, no icons, no
subtitles, no versioned-wrapper nodes, no `httpRequestWithAuthentication` and no
credential `authenticate` blocks. Dynamic `loadOptions` lists become free-text
fields, so a required agent or provider ID is typed by hand. Unsupported
`httpRequest` options throw rather than being ignored. Host calls are served one
at a time, one node run runs at a time per tenant, and a node with `onError`
other than `stop` runs once per item.

## Failure codes

| Code | Operator action |
| --- | --- |
| `no-sidecar` | the node ran where the sidecar is disabled; enable it or remove the node |
| `spawn-failed` | the child never dialled back; read the sidecar log lines above the error and check Node and the packages |
| `tenant-required` | a composition fault: a community node ran without a tenant |
| `sidecar-crash` | the process died; the diagnostic names the exit status or signal |
| `sidecar-timeout` | the run passed `sidecar.timeout`; raise it or fix the package |
| `sidecar-memory-limit` | the heap or RSS bound was reached; raise it or fix the package |
| `output-too-large` | the result exceeded `sidecar.max_output_bytes` |
| `frame-too-large` | one protocol line exceeded the frame bound |
| `host-call-denied` | the package called a host function it may not |
| `network-refused` | the package reached the network directly, which fails the run even if it swallowed the error |
| `process-refused` | the package tried to signal another process |
| `protocol-violation` | the child sent a frame the host never asked for |
| `sidecar-cancelled` | the run was cancelled |
| `sidecar-busy` | no process slot was free within `sidecar.spawn_timeout`; raise `sidecar.max_processes` |

## A Docker recipe

The recipe below was built and booted by hand once against the fixture package;
it is **not** built in CI and no KilasFlow image ships a sidecar. It shows the
shape: `node:24-slim`, the cross-compiled binary, the operator's packages under
`/opt/sidecar/node_modules`, a writable data directory, and a non-root user.

```dockerfile
# Build the binary in the repository checkout:
#   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ctx/kilasflow ./cmd/kilasflow
FROM node:24-slim

# The operator's packages, in the layout npm produces.
COPY my-community-package /opt/sidecar/node_modules/my-community-package

COPY kilasflow /usr/local/bin/kilasflow

RUN mkdir -p /var/lib/kilasflow && chown node:node /var/lib/kilasflow

USER node

ENV KILASFLOW_SIDECAR_ENABLED=true \
    KILASFLOW_SIDECAR_NODE_PATH=/usr/local/bin/node \
    KILASFLOW_SIDECAR_PACKAGES_DIR=/opt/sidecar \
    KILASFLOW_SIDECAR_PACKAGES=my-community-package \
    KILASFLOW_SIDECAR_RUNTIME_DIR=/var/lib/kilasflow/sidecar \
    KILASFLOW_DATABASE_DSN=/var/lib/kilasflow/kilasflow.db \
    KILASFLOW_SERVER_HOST=0.0.0.0

EXPOSE 8080
ENTRYPOINT ["kilasflow"]
```

## What declining it loses

Only these nodes: a stock install runs exactly as it did. A workflow that
references a community node fails to compile as an unknown node type. Nothing
else in the product changes, and the sidecar costs a declined deployment nothing
at all — no Node process, no runtime directory, and Node is never looked for on
PATH.
