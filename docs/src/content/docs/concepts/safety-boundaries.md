---
title: Safety boundaries
description: Everything a running workflow can and cannot reach, in one place, so an operator does not have to read Go to find out.
sidebar:
  order: 9
---

A workflow is authored by whoever can edit it, which in an embedded multi-tenant
deployment is a customer's end user. This page enumerates what such a workflow
can reach and what stops it, so you can answer that question without reading the
source.

It is deliberately one page rather than one per package. The boundaries are a
single idea implemented in four places, and reading them apart is how you miss
that two of them do not talk to each other.

## Outbound HTTP

Every outbound request a node makes — the HTTP Request node, a declarative pack's
generated request, a chat model call, a Telegram call, edit-time option loading —
goes through `internal/safehttp`. Nothing gets its own `http.Client`.

Three checks run at three different moments, and the separation is the point.

**Before the dial, on the URL.** The scheme must be `http` or `https`, the
hostname must be non-empty, and if the deployment configured an
`outbound.allowed_hosts` list the host must be on it. Its own comment is careful
to call this "a first pass only": the address a hostname resolves to is checked
again at dial time, which is what actually stops DNS rebinding.

**At dial time, on the address actually connected to.** The dialer resolves the
host itself, walks the returned addresses in order, skips any the policy refuses,
and connects to the **literal IP** of the first one that passes — so the address
that was inspected is the address that was contacted. A resolver that returned a
public address on the first lookup and a private one on the second has nothing to
gain.

Note that skipping rather than failing is the behaviour: a hostname resolving to
both a private and a public address is still reachable, over the public one. The
guard is "this connection did not go anywhere internal", not "this hostname has
no internal address anywhere in its record".

Refused ranges, with the reason each is on the list:

| Range | Why |
| --- | --- |
| loopback (`127.0.0.0/8`, `::1`) | the host's own services |
| unspecified (`0.0.0.0`, `::`) | resolves to a local interface |
| link-local (`169.254.0.0/16`, `fe80::/10`) | **carries the cloud metadata service — the single most valuable SSRF target on a hosted install** |
| multicast and interface-local multicast | not a request target |
| private (`10/8`, `172.16/12`, `192.168/16`) | the deployment's own network |
| shared address space (`100.64.0.0/10`) | carrier-grade NAT |
| `192.0.0.0/24`, `240.0.0.0/4` | reserved |
| unique-local IPv6 (`fc00::/7`) | the IPv6 private range |
| NAT64 (`64:ff9b::/96`) | reaches an IPv4 private address through translation |

IPv4-mapped IPv6 forms are refused too, so `::ffff:127.0.0.1` does not slip past.

**On every redirect hop.** A redirect can point anywhere, so the destination gets
the same scheme and allowlist check the original URL did — and because each hop
re-enters the dialer, the address check runs again as well. After five hops the
request fails.

### The two levers do not consult each other

This is the single most misunderstood thing in this area, so it is worth stating
flatly:

- `outbound.allowed_hosts` is a **hostname** allowlist. It is read at pre-flight
  and never looks at an IP.
- `outbound.allow_private_networks` is a blanket **IP-range** disable. It is read
  at dial time and never looks at the allowlist.

Neither reads the other. An allowlisted host that resolves to `10.0.0.1` is still
refused at dial. A private address is still reachable when
`allow_private_networks` is on, allowlist or not.

The practical consequence: **there is no way today to permit one specific
loopback service without permitting all private addresses.** Reaching a local
model server at `127.0.0.1:11434` requires `allow_private_networks`, which
removes the guard for everything. Do not set it to make a test pass — a suite
running with it on has a security posture production does not, and can never
catch a regression in the guard.

An entry of the form `*.example.com` matches any subdomain and **not** the bare
parent domain.

### Bounds

| Bound | Default | Config key |
| --- | --- | --- |
| Redirects | 5 | `outbound.max_redirects` |
| Response body | 8 MiB | `outbound.max_response_bytes` |
| Request timeout | 30 s | `outbound.timeout` |

A configured value of `0` means "keep the default", not "unbounded". An
over-limit response body is truncated at the limit and reported as truncated,
rather than read into memory.

Note that `http.ProxyFromEnvironment` is honoured, so a proxy set in the
process environment is used. That is normally what an operator wants, and it is
worth knowing it is there.

:::caution[Two call sites use the default policy, not the configured one]
Edit-time option loading and the webhook lifecycle coordinator construct their
clients from `safehttp.DefaultPolicy()` rather than from the policy built out of
`outbound.*`. The address, redirect and response-size guards therefore still
apply to them with their default values, but a custom `outbound.allowed_hosts`,
`outbound.timeout` or `outbound.max_response_bytes` does **not**. Verified
against `cmd/kilasflow/main.go` at the time of writing.
:::

### Credentials narrow it further

A [credential's](/concepts/credentials/) own `allowedDomains` is checked against
the target host **before the secret touches the request**, in all four places a
credential can be applied. It is a narrowing on top of the policy above, never a
replacement: a credential permitted to reach `example.com` still cannot reach it
if the deployment's outbound policy refuses the resolved address.

## Databases

There are two separate protections here and the structural one matters more.

**A node never sees a DSN.** SQL node connections are built from scratch out of
credential fields and opened through `database/sql`, never through KilasFlow's
own GORM handle. There is no default connection, no inferred one, and no
selectable one — so a workflow cannot name a connection string of its own. The
`internal/database` package is explicit that its handle is KilasFlow's storage
only and is never exposed to workflows.

**A SQLite credential cannot open KilasFlow's own file.** This is the backstop
for the one case where the structural separation is not enough, because a SQLite
credential names a filesystem path and that path could coincidentally be the
engine's database. The guard:

- refuses a URI form outright — a `file:` prefix or a `?` could carry `?mode=` or
  an attached database, so only plain file paths are accepted;
- refuses `:memory:`, which is not a durable target;
- resolves symlinks through the directory and then the file before comparing, so
  `/var/…` and `/private/var/…` on macOS normalise to the same thing — exactly
  the gap a guard must not have;
- compares by inode when both files exist, so a hard link or a relative spelling
  cannot slip past;
- checks the `-wal`, `-shm` and `-journal` sidecars as well as the file itself.

On a match the connection is refused. The same guard is applied at **edit time**,
so a SQLite credential naming KilasFlow's own database is refused when it is
tested exactly as it is when a workflow runs.

On a PostgreSQL-backed installation this guard is empty, because there is no
filesystem path to protect and a node can never be handed the engine's DSN. There
is no host-and-port comparison for PostgreSQL or MySQL targets; the structural
separation is the whole defence there.

**Query ceilings clamp rather than refuse.** A deployment sets `sql.max_rows`
(default 50,000) and `sql.max_statement_timeout` (default 5 minutes); a node's own
limits default to 10,000 rows and 30 seconds. A node asking for more than the
ceiling is quietly clamped to it and told which values were clamped, rather than
failing — the workflow still runs, bounded.

## The Code node

A Code node's Go source is compiled to a WebAssembly module and run under
[wazero](https://wazero.io) **with no host functions at all**.

The module gets WASI's standard streams and nothing else — no preopened
directory, no environment, no arguments, no host imports. The package states the
conclusion directly: there is no capability through which user code could reach
the filesystem, the network, another process, or KilasFlow's own database. It is
not that those are blocked; there is nothing to call.

The guest is compiled with `GOPROXY=off`, so user code builds against the
standard library only and a *build* cannot reach the network either. Notably the
wrapper imports `os` and `net` into the guest **deliberately**: they compile fine
and then fail at run time inside the sandbox, which is a far better guarantee
than "it would not have compiled".

| Bound | Default |
| --- | --- |
| Run time | 10 s |
| Memory | 256 pages = 16 MiB |
| Output | 4 MiB |
| Toolchain build | 90 s |

These are compile-time defaults with no configuration keys. A workflow may
**tighten** them through the node's `scriptTimeoutSeconds` and `memoryMB`
parameters — each applied only if strictly smaller — and can never raise them.

There are three separate costs in running a Code node and only one of them is
charged to the user's time limit: the toolchain build (cached by source hash),
wazero's translation of the module to machine code (cached per process), and the
user's program actually running. Only the third is bounded by the time limit. If
a Code node ever reports a limit for work that plainly does not take that long,
something has crept back inside that timer.

**A deployment may not be able to run Code nodes at all.** The Go toolchain is
not in the distroless image. That is reported through the node catalogue's
`unavailable` field so the editor says so before a workflow is saved, rather than
being discovered when the workflow runs.

## Expressions

A node parameter cannot become code. The [expression grammar](/concepts/expressions/)
is a root followed by field reads and calls from a closed allowlist — no
operators, no bare identifiers, no general call syntax — so `require('fs')` is
not blocked by a denylist, it cannot be written. Unknown functions fail at parse
time, which means at save time.

`$env` exposes only variables prefixed `KILASFLOW_WORKFLOW_ENV_`, so a workflow
cannot read the database DSN or the credential master key out of the process
environment. The allowlist is built once at composition; nothing reads
`os.Environ()` during execution.

## Payloads

Binary payloads are bounded by `binary.max_bytes`, default 16 MiB, and an
oversized write is refused rather than truncated. The store is scoped to the
tenant and execution before an executor sees it. Every path segment — tenant,
execution and reference ID alike — is validated against a conservative pattern
rather than trusting where it came from.

An unset `binary.root` disables payload storage entirely, and a node that needs
it fails with a message saying so rather than silently dropping an attachment.

## Inbound requests

The [webhook surface](/concepts/webhooks/) bounds the request body at 1 MiB
(`webhook.max_body_bytes`) and the synchronous response wait at 30 seconds
(`webhook.response_timeout`). Route identifiers carry 128 bits of entropy from
`crypto/rand`, and every kind of miss returns an identical `404`.

## What is *not* defended

These are as important as the list above.

**Authentication is off by default.** With `auth.enabled` unset, nothing under
`/api/v1` requires a credential. Reaching the port is equivalent to being an
administrator, and the server logs that on every boot. See
[tenancy and the embed boundary](/concepts/tenancy-and-embedding/).

**There is no rate limiting** on the API or on the webhook surface.

**There is no execution retention.** Nothing prunes execution rows, so history
grows without bound and every input and output a workflow ever handled stays
readable. The one exception is not a retention policy: reclaiming an expired
lease deletes that execution's node runs so the recovered attempt can rewrite
them.

**A recovered execution re-runs from the beginning.** There is no checkpointing,
so a workflow with non-idempotent side effects can perform them twice if its
worker dies mid-run.

**A workflow can consume its worker.** A `Wait` node holds a worker slot for the
whole pause, bounded by `execution.default_timeout` (60 s) and refused outright
above an hour, and the pool is `execution.max_concurrent` deep — 10 by default.

**Anyone who can write a workflow can exfiltrate any credential in their
tenant.** No credential endpoint returns a plaintext secret — reads come back
redacted, and the one path that decrypts is called only by the runtime — but a
caller who can author and run a workflow can point a credential at a host they
control and read it off the wire, subject only to that credential's
`allowedDomains` and the egress policy. Write access to workflows is therefore
equivalent to read access to secrets, and should be granted on that basis.

## Configuration caveat

`config.example.yaml` does not cover every section the code defines, and the
sections it omits include `outbound`, `webhook`, `embed`, `auth` and `sql` —
which are precisely the ones on this page. `internal/config/config.go` is the
only complete list until
[the generated configuration reference](/operate/configuration/) exists.

## Source

`internal/safehttp/safehttp.go` (`Policy.CheckURL`, `Policy.CheckAddress`,
`ReadBody`, and the `DialContext` and `CheckRedirect` closures `NewClient`
builds), `internal/sqlnode/sqlnode.go` (`Guard`,
`sqlitePath`, `Ceiling`), `internal/runcode/` (the wazero sandbox and its
limits), `internal/credentials/credentials.go` (`AllowsHost`),
`internal/binary/binary.go`, `internal/expression/doc.go`,
`cmd/kilasflow/main.go` (`outboundPolicy`, `databaseGuard`,
`workflowEnvironment`, `nodeAvailability`).
