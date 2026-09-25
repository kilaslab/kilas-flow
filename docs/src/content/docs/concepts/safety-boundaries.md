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
single idea implemented in several places, and reading them apart is how you miss
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

### The levers do not consult each other

This is the single most misunderstood thing in this area, so it is worth stating
flatly:

- `outbound.allowed_hosts` is a **hostname** allowlist. It is read at pre-flight
  and never looks at an IP.
- `outbound.allow_private_networks` is a blanket **IP-range** disable. It is read
  at dial time and never looks at the allowlist.
- `outbound.allowed_private_endpoints` is a list of literal `host:port` pairs,
  also read at dial time. A target on it is admitted past the address guard and
  nothing else is.

None of them read each other. An allowlisted host that resolves to `10.0.0.1` is
still refused at dial. A private address is still reachable when
`allow_private_networks` is on, allowlist or not.

The practical consequence is about which lever to reach for, not about
all-or-nothing. `allowed_hosts` narrows by hostname and must still clear the
address check, so it can never admit an internal target on its own. The setting
that admits one is `outbound.allowed_private_endpoints`, which takes a literal
`host:port` and lets that target — and nothing else — through the address guard:
use it for a loopback model server at `127.0.0.1:11434` instead of
`allow_private_networks`, which lifts the guard for every outbound request in the
installation. Do not set that one to make a test pass — a suite running with it on
has a security posture production does not, and can never catch a regression in
the guard.

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

Both of the call sites that used to differ here are now constructed with the
configured policy: the edit-time option loader and the webhook lifecycle
coordinator are both handed the policy built out of `outbound.*`, so a custom
allowlist, timeout or response-size bound applies to them exactly as it applies
to a workflow's own HTTP node. `cmd/kilasflow/main.go` is where that wiring
lives, and it is worth re-reading whenever a section is added to the policy.

### Credentials narrow it further

A [credential's](/concepts/credentials/) own `allowedDomains` is checked against
the target host **before the secret touches the request**, in all four places a
credential can be applied. It is a narrowing on top of the policy above, never a
replacement: a credential permitted to reach `example.com` still cannot reach it
if the deployment's outbound policy refuses the resolved address.

### The JavaScript sidecar (opt-in)

The claim above — *every* outbound request a node makes goes through
`internal/safehttp` — holds for a community node's HTTP: it is proxied back to
the host and issued through the same client, under the deployment's policy and
the conjunction of every credential's `allowedDomains`.

Its *other* routes rest on a defence-in-depth guard rather than that client. A
package can only open a socket, resolve a name, send a datagram or call `fetch`
through a JavaScript guard and Node's permission model; any direct attempt fails
the run, even if the package swallowed the error. Those are seat belts, not a
sandbox against malicious code, and the [sidecar page](/operate/javascript-sidecar/)
states exactly what they do not stop. For a hard boundary, use `sidecar.wrapper`
or the container network policy.

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

**A SQLite credential stays inside its tenant's directory.** Refusing
KilasFlow's own file is not enough on a multi-tenant install. A path read as the
credential spells it reaches every other tenant's databases too, and creates a
file anywhere the process can write. SQLite paths are therefore confined to
`<sql.sqlite_root>/<tenant>/` and read relative to it. The guard, built once
for the process, is narrowed to the tenant at each call site: node run, option
loader, credential test.

The guard refuses:

- an absolute path;
- a `..` that leaves the directory;
- any symbolic link on the way. A tenant cannot make one through a workflow, so
  one found there was put there by someone else;
- anything that is not a regular file.

The tenant ID has to be one plain directory name. `sql.sqlite_unconfined: true`
is the single-tenant escape hatch back to unconfined paths, and it is warned
about at boot. The zero guard, and an empty root, refuse every SQLite credential.

**A SQLite open cannot hold a request or a worker.** The driver opens the file
with no context, and only a running statement can be interrupted, so a blocked
open used to ignore every deadline. It now runs in a goroutine the caller
abandons at its deadline, or after 30 seconds when the caller has none. If the
driver returns later, the handle it opened is closed. While that open is still
stuck, the same file is refused straight away rather than queued behind it, and
at most eight stuck opens are allowed in the process.

A network target has its own half of the same guard. A PostgreSQL or MySQL
credential whose database name, port and host match the installation's own DSN is
refused before anything dials, and when the spelling differs the addresses both
names resolve to are compared, so a second name for the same server is refused
too. A table prefix does not scope the refusal — anything holding the connection
can read every table under it — so the whole database is refused however the
credential spells its target. What this is not is a defence against the *host
application* that shares the database: it stops KilasFlow's own SQL nodes, and a
shared-database deployment still needs a dedicated schema.

**Query ceilings clamp rather than refuse.** A deployment sets `sql.max_rows`
(default 50,000) and `sql.max_statement_timeout` (default 5 minutes); a node's own
limits default to 10,000 rows and 30 seconds. A node asking for more than the
ceiling is quietly clamped to it and told which values were clamped, rather than
failing — the workflow still runs, bounded.

## The Go Code node

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
wazero's translation of the module to machine code (cached per process, and
across restarts when `code.cache_dir` is set), and the user's program actually
running. Only the third is bounded by the time limit. If a Code node ever
reports a limit for work that plainly does not take that long, something has
crept back inside that timer.

**A deployment may not be able to run Code nodes at all.** The Go toolchain is
not in the distroless image. That is reported through the node catalogue's
`unavailable` field so the editor says so before a workflow is saved, rather than
being discovered when the workflow runs.

The message names what to provide, because there is no way to install a package
in a shell-less image. A toolchain is roughly 270MB; mount one and point the
server at it with `code.go_binary` (`KILASFLOW_CODE_GO_BINARY`), or put its `bin`
directory on the process's `PATH`. Code nodes whose source was already compiled
keep running from the artifact cache either way, so losing the toolchain costs
new code, not the workflows already in use. A node pack is different: it ships as
WebAssembly its author already compiled, so it needs no toolchain at all.

Three keys describe the caches, all under `code.`: `go_binary` (default `go`),
`cache_dir` (default `./data/codecache`, inside the data volume) and
`cache_max_bytes` (default 2 GiB, `0` unbounded). A compiled artifact, wazero's
translation of it and the toolchain's build cache all live under `cache_dir`, and
all of them are keyed on the runtime version, so an artifact built against an
older host contract is rebuilt rather than loaded. Those directories hold native
machine code the server executes, so they must be writable only by the kilasflow
user; setting `cache_dir` to an empty value keeps every cache in memory instead.

## The JavaScript Code node

Code (JavaScript) runs n8n-style JavaScript on [goja](https://github.com/dop251/goja),
an ECMAScript engine written in Go and linked into the binary. No Node.js is
involved and nothing is installed beside the server.

**What the code can reach is what the Go behind its globals can reach, and that
is nothing outside its own run.** The engine has no host API of its own: every
global a script sees — `$input`, `$('Name')`, `console`, `require()` and the
modules it returns — is a function KilasFlow wrote or a vendored library's
(Luxon, lodash, and the `Buffer` and `URL` of goja's companion `goja_nodejs`),
and `internal/jsrun`, the package they live in, may not import anything that
opens a file, a socket or a process. A test in `internal/guardrails` enforces
that on every run, rather than a review having to notice. Another walks each
global, including a symbol-keyed one, and the own properties of each sampled
instance, against a reviewed list. It does not call a getter or a function, so
a value that exists only as a call's result is listed only when a sample builds
it. It fails on anything new, and on any Go value whose fields or methods a
script could read or call.
`require()` answers from a fixed list — `lodash`,
`luxon`, `crypto`, `util`, `buffer`, `url` — and there is no npm. `$env` holds
the same `KILASFLOW_WORKFLOW_ENV_` allowlist an expression sees, and a file on an
item crosses as its metadata, never its bytes.

**Code is read before it runs.** Constructs the engine would run differently
from V8, and bodies too large or too deeply nested to parse safely, are refused
by name when the workflow is saved or imported, so a workflow that uses one
never activates. The [Code (JavaScript)](/guides/code-javascript/#what-is-refused-and-when)
page lists them.

Every bound on a script — its own running time, its input, its returned items
and its console output, its helper calls, its worker's heap and how many
scripts run at once — has a default and a key, listed with the bounds that are
fixed on the [Code (JavaScript)](/guides/code-javascript/#limits-and-configuration)
page. A node may **tighten** the time limit and can never raise it. The time
limit charges the user's program only — starting the engine, loading a library
and handling the input and output are not counted — so a limit reported for
work that plainly does not take that long means something has crept inside
that clock. `code.javascript_enabled: false` turns the node off: it is greyed
out in the editor and every run is refused, naming the key.

### Worker processes

goja cannot interrupt a single built-in call, and a built-in can be asked to
allocate or loop as far as a number tells it: `'x'.repeat(2**30)` is one call.
The runtime refuses the known cases up front with a `RangeError`, but a list of
known cases is a denylist, and a denylist cannot promise it is complete. So no
script runs in the server process. Each runs in a **worker**: the kilasflow
binary itself, started again by the server, holding one script at a time on a
fresh engine.

- The server keeps at most `code.javascript_max_concurrent` workers, starts
  them when a script needs one, reuses them, retires one that has been idle for
  five minutes, and replaces each after a thousand runs.
- A worker runs one tenant's scripts and never another's. A script whose
  tenant has no idle worker gets a fresh one, and when the server already
  keeps as many workers as it may, the worker that has been idle longest,
  another tenant's, is stopped to make room. A deployment without tenants is
  one group.
- The server prepares every job itself — the input is encoded and checked
  against its cap before a worker sees it — and nothing is run in the server.
  Validation parses a node's code there, and compiles it only to see that it
  compiles. While a script runs, the worker asks the server for what the code
  reads from other nodes, over the same pipes.
- The server trusts a worker only as far as its code could go. Every message
  of a job carries that job's nonce. The input's lineage and file references
  never leave the server: a worker hands back the code's results as the JSON
  the code returned, and the server decodes them itself, checking the output
  and console caps and every file a result names or gives inline as base64;
  the server, never the worker, stores a file given inline. A worker that
  breaks any of this fails its run with an engine fault and is never used
  again.
- A worker still running at twice its time limit plus five seconds is stuck in
  something its own clock cannot stop. The server kills it, and the run fails
  with the time-limit error. Cancelling an execution kills the worker running
  it.
- A worker that dies mid-run fails that run with the memory-limit error when it
  ran out of memory or was killed by the kernel, and with an engine fault
  naming its exit otherwise, which the server logs with the start and the end
  of what the worker wrote to stderr. The next run gets a fresh worker; the
  server is not involved.
- A worker's environment holds only the marker that makes it one,
  `GOMAXPROCS`, `GOMEMLIMIT`, `GOTRACEBACK`, and the server's `TZ` and
  `ZONEINFO` if set — none of the server's configuration or secrets. It starts
  in `/` and exits when the server closes its stdin.
- On Linux, each worker also has an address-space limit of four times its heap
  ceiling plus 3 GiB, which turns an allocation no watchdog could stop into the
  worker failing to allocate; sets its `oom_score_adj` to 1000, so the kernel
  chooses a worker before the server when memory runs out; runs with
  `no_new_privs`; and is killed by the kernel if the server dies, even from
  inside a built-in that never reads its stdin again. The server makes itself
  undumpable, so its environment cannot be read through `/proc` by another
  process of its user, a worker included, and it leaves no core dump.

### What confines a worker

On Linux a worker is also a privilege boundary, as far as the kernel grants one.
What keeps a script from reading a file is first that the engine exposes nothing
that opens one; the layers below are for code that escaped the engine itself.

| Layer | What it takes away | Where it comes from |
| --- | --- | --- |
| User namespace | The worker is `nobody` (65534) in a user namespace of its own, with no capability, even over its own namespaces | the server, when it starts the worker |
| Own user | With `code.javascript_worker_uid` and `code.javascript_worker_gid` set, the worker runs as that user and group, with none of the server's groups, so the server's files are closed to it by their permissions too, as long as they are not world-readable, and it cannot signal the server or change its limits | the server; needs `CAP_SETUID` and `CAP_SETGID`, and the kilasflow binary executable by that user |
| PID namespace | The server and every other process have no number the worker could signal, trace, or change the limits of | the server |
| Network namespace | Only a loopback interface, and that down: no connection leaves the worker | the server |
| IPC namespace | No System V or POSIX queue or shared memory is shared with anything outside | the server |
| Own session | A signal to its own process group reaches only the worker | the server |
| Landlock | No file may be opened, created, removed, renamed or run, except reading the time zone database, so `Intl` and luxon zones still resolve; no tracing of a process outside the worker. From Linux 6.7 (landlock ABI 4) also no TCP connection or bind; from Linux 6.12 (ABI 6) also no signal to, and no abstract Unix socket of, a process outside the worker | the worker itself, before it reads its first job |
| Undumpable | No other process of the same user can trace it or read its memory through `/proc` | the worker itself |

The worker's pipes to the server are open before any of this, so jobs, the
questions a script asks and the helpers the server carries out for it
(`this.helpers.httpRequest` is made by the server, never by the worker) are
unaffected. A worker's cold start is about 8 ms with every layer in place, a
quarter of a millisecond more than without the namespaces, and a worker is
reused for up to a thousand jobs of the same tenant, so code that escaped the
engine and stayed in a worker never sees another tenant's jobs. A tenant's
script that finds no worker of its own pays that cold start once.

**What the kernel will not grant costs that layer and nothing else.** The server
asks for the strongest start first and falls back when the kernel refuses it —
Docker's default seccomp profile refuses user namespaces, for one. It keeps to
what it got once a worker has started with it, and asks for the stronger start
again every ten minutes, since a refusal can pass; a start that fails whatever it
asks for, such as a binary the worker may not run, gives nothing up. A worker whose kernel has no landlock (before Linux 5.13, or with
the LSM not enabled) runs without it. The server logs once, when its first worker
starts, which layers are in place and which are missing and why:

```text
level=INFO msg="JavaScript workers are confined" active="user namespace, PID namespace, network namespace, IPC namespace, undumpable, landlock (files, TCP, signals)"
level=WARN msg="JavaScript workers are only partly confined" active="undumpable, landlock (files, TCP, signals)" missing="own user; PID namespace; network namespace; IPC namespace" why="the kernel refused a worker's own user, PID, network and IPC namespaces: operation not permitted"
```

A configured worker user is never given up. The server starts one worker as
that user when it boots and refuses to boot when it cannot, naming the keys; the
user must not be the server's own. Outside Linux
none of these layers exist, and the log says so once.

What the layers do not cover, so that nobody reads more into them:

- In a user namespace the worker is still the server's user to the host's
  filesystem. Landlock is what closes the server's files to it; where the kernel
  has no landlock, only a configured worker user does.
- Landlock does not govern connecting to a Unix socket by its path, such as a
  local database's. The network namespace does not either; a configured worker
  user whose permissions exclude the socket does.
- Where the kernel grants no PID namespace and no worker user is configured —
  which is the case for the distroless image under Docker's default seccomp
  profile — the worker runs as the server's user. Landlock then keeps its
  tracing to itself on any kernel, but its signals only from Linux 6.12: on
  older kernels such as 5.15, 6.1 or 6.6, code that escaped the engine could
  signal the server, and on any kernel it could change the server's resource
  limits. Close this by configuring a worker user, or by allowing user
  namespaces (a seccomp profile that allows `clone` with `CLONE_NEWUSER`).
- There is deliberately no seccomp filter. A system-call profile is left to a
  change written and reviewed by a person.

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
`crypto/rand`, and every kind of miss returns an identical `404`. Every answer
the surface sends carries a `Content-Security-Policy` sandbox without
`allow-same-origin`, so a page a workflow returns runs in an opaque origin rather
than as the instance, and a workflow-set policy cannot loosen it — see
[responses render sandboxed](/concepts/webhooks/#responses-render-sandboxed).

## What is *not* defended

These are as important as the list above.

**Authentication is off by default.** With `auth.enabled` unset, nothing under
`/api/v1` requires a credential. Reaching the port is equivalent to being an
administrator, and the server logs that on every boot. See
[tenancy and the embed boundary](/concepts/tenancy-and-embedding/).

**There is no general rate limiting.** The one limiter is on sign-in: ten attempts
a minute per account and per address, plus a process-wide cap on how many password
hashes may be computed at once, so a flood of logins cannot fill every core of the
API. Nothing rate-limits the rest of the API, and nothing at all rate-limits the
webhook surface — an inbound trigger is as fast as its sender chooses to make it.

**Execution retention is off by default.** `execution.retention` deletes a
finished execution — with its node runs and its stored payloads — once it has been
finished for longer than the configured age, and a pruner sweeps every fifteen
minutes. Zero, the default, keeps everything: history grows without bound and
every input and output a workflow ever handled stays readable. Two things that are
not retention policies are easy to confuse with one: reclaiming an expired lease
deletes that execution's node runs so the recovered attempt can rewrite them, and
a suspension keeps the checkpoint it will resume from.

**A recovered execution re-runs from the beginning.** A suspension is the only
thing that carries a checkpoint; a worker that died mid-run is reclaimed and
restarts the graph, so a workflow with non-idempotent side effects can perform
them twice. After two hand-offs the execution is settled `failed` rather than run
again, which bounds the repeats but not the first duplication.

**A wait no longer holds a worker, but it does hold a row.** A `Wait` node parks
the execution in storage — status `waiting`, no worker and no lease — so a pause
of hours costs a row rather than a slot and the pool of
`execution.max_concurrent` (10 by default) stays free for other runs. What is
bounded is the suspension: seven days at most, and a call-resumed wait that nobody
answers fails by name at its own deadline. A run's own budget is still
`execution.default_timeout` (2 minutes) unless the workflow names its own
`settings.executionTimeout`.

**Anyone who can write a workflow can exfiltrate any credential in their
tenant.** No credential endpoint returns a plaintext secret — reads come back
redacted, and the one path that decrypts is called only by the runtime — but a
caller who can author and run a workflow can point a credential at a host they
control and read it off the wire, subject only to that credential's
`allowedDomains` and the egress policy. Write access to workflows is therefore
equivalent to read access to secrets, and should be granted on that basis.

**Storage keeps what the caller sent.** Redaction is a read-surface guarantee:
API responses, the live event feed and the inspector withhold credential keys and
normalise header names, but a raw table dump, a database backup or a support
export carries inbound trigger headers and bodies exactly as they arrived. See
[the security posture](/operate/security/) for what that asks of an operator.

## Configuration is generated from the code

`config.example.yaml` and
[the configuration reference](/operate/configuration-reference/) are generated
from the `Config` structs by `make generate-config-reference`, not written by
hand, so every section the code defines appears in both — the pages on this site
name the keys they rely on rather than reproducing the list. A struct change with
no regenerated pair fails `make generate-config-reference-check`, which is what
stops a list like this one from silently going stale.

## Source

`internal/safehttp/safehttp.go` (`Policy.CheckURL`, `Policy.CheckAddress`,
`ReadBody`, and the `DialContext` and `CheckRedirect` closures `NewClient`
builds), `internal/sqlnode/sqlnode.go` (`Guard`,
`sqlitePath`, `Ceiling`), `internal/runcode/` (the wazero sandbox and its
limits), `internal/credentials/credentials.go` (`AllowsHost`),
`internal/binary/binary.go`, `internal/expression/doc.go`,
`internal/repository/execution_retention.go` (`PruneExpired`),
`internal/engine/wait_service.go` (suspension, resume and the wait sweep),
`internal/api/middleware/loginlimit.go` (the sign-in limiter),
`cmd/kilasflow/main.go` (`outboundPolicy`, `databaseGuard`,
`workflowEnvironment`, `nodeAvailability`).
