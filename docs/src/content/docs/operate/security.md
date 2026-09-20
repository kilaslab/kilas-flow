---
title: Security posture
description: 'The whole security boundary on one page: what is defended, what is not, and what isolates.'
sidebar:
  order: 4
---

## The API is unauthenticated

Nothing under `/api/v1` requires a credential unless the operator turns
identity on (`auth.enabled`, with a signing key and a bootstrapped account on
the same first start). Listing workflows, creating them, running them,
reading executions and managing credentials are otherwise open to anything
that can reach the port — and the server says so at every boot until identity
is enabled.

The embed session token works in the opposite direction to the one people
usually expect: it **restricts** a request to a single workflow and a set of
scopes. A request without a token is not rejected — it is passed through with
full access.

So KilasFlow must sit behind something that authenticates, on a network that
does not expose it directly. Treat reaching the port as equivalent to being
an administrator, because it is.

## What is defended today

**Outbound requests refuse internal infrastructure by default.** The egress
policy in the `outbound` section governs every workflow HTTP request (nodes,
option loaders and trigger executors alike): private networks are refused
unless `allow_private_networks` is turned on, which is what stops a
tenant-authored URL probing the cloud metadata service or a neighbouring
internal service. `allowed_hosts` narrows further when set, and
`allowed_private_endpoints` admits one `host:port` at a time — a loopback
model server, a test stub — without handing every request the whole internal
network. Redirects, response size and timeout are all bounded. A self-hosted
operator opts out explicitly. Never set `allow_private_networks: true` to
reach one loopback dependency; name the endpoint instead.

**Embedding fails closed.** The `embed` section's allowlist decides which
pages may host the editor, and empty means disabled entirely — even with a
signing key set. Session tokens live at most 30 minutes (default 15, set with
`embed.session_ttl`) because a token travels through a host page and sits in a
browser. The signing key is deliberately a different variable from the
dashboard auth key, so a forged value of one kind can never be presented as
the other.

**The internal database has a guard, on both drivers.** A workflow database
credential naming KilasFlow's own database is refused before a socket or a file
is opened, and again at dial time: on SQLite the path and its `-wal`, `-shm` and
`-journal` siblings are resolved through symlinks and matched, and on PostgreSQL
the connection's host, port and database name are matched against the
installation's own DSN. A table prefix does not scope the refusal — anything
holding the connection can read every table under it, so the whole database is
refused however the credential spells its target.

**SQL targets are checked like HTTP ones.** A network database credential is
also subject to the instance egress policy: a host that resolves to loopback,
private, link-local or otherwise internal address is refused before anything
dials unless the policy explicitly allows it, and a credential carrying an
allowed-domains list reaches only the hosts on it. What the guard does not do is
police the *host application* that shares the database: it stops KilasFlow's own
SQL nodes, so a shared-database deployment still needs a dedicated schema — the
isolation story below is about the other direction.

**Credentials are sealed.** Stored credentials are encrypted at rest with
AES-256-GCM. The key is read from the environment and never from the
configuration file; without it, credential storage is disabled rather than
silently falling back to something weaker. Workflow `$env` expressions can
never reach it either: only `KILASFLOW_WORKFLOW_ENV_*` is exposed to
workflows, so a workflow can never read the DSN or the master key.

**The master key can come from a manager, and credential fields can point at
one.** A stored credential field may hold an `ext://<binding>/<key>`
reference instead of a sealed secret. The reference is sealed into the row
like any secret and resolved on the Resolve path at the moment the runtime
needs it, against a manager binding that belongs to the calling tenant — a
reference authored under one tenant cannot read another tenant's binding.
Resolved values are cached in process memory with a bounded TTL, never
written to disk, and dropped when the credential or the binding changes. The
manager leg (Vault KV v2 today) travels through the same egress policy as
every workflow HTTP request: a manager on loopback or a private network needs
an `allowed_private_endpoints` entry, never `allow_private_networks: true`.
A manager that is configured but unreachable at boot refuses startup with a
named error rather than silently disabling credential storage; a deployment
that configures nothing keeps the environment-variable key path unchanged.

**Approval waits hand out single-use resume tokens.** A suspended execution
is resumed by an unguessable per-execution token that works exactly once and
stops working at its deadline — a second call, a call for an already-answered
request, and a call past expiry are each refused with their own message.
Tokens are looked up under the caller's tenant, and resume from an embedded
session is refused: an approval decision must not arrive through a host page.
The waiting event on the live feed carries the node and the deadline, never
the token and never run data.

**Redaction is a read-surface guarantee, not a storage one.** A trigger delivery
is the caller's own data and the stored record *is* the input the run executes
on, so the execution row keeps it exactly as it arrived — headers included — and
an imported workflow checking its own `headers['x-api-key']`, or reading a
cookie, sees what the caller sent rather than a placeholder. Every surface that
hands a record back redacts instead: API responses, the live event feed and the
editor's inspector pass the payload through the same rule, which normalises
header names and withholds credential keys as `[redacted]`. The node-run trace
keeps redacting on the way *in*, so a credential the runtime resolved never
lands in a node's stored input or output. What follows for an operator is the
part worth reading twice: **a raw table dump, a database backup or a support
export carries inbound trigger headers and bodies verbatim.** Treat those files
as credential-bearing, and prefer the API over a SQL client when handing run
data to somebody else.

**Webhook routes are unguessable rather than authenticated.** The route
segment carries 16 bytes of entropy, because this endpoint is very often
called by a third party that cannot hold a credential. Every request that
does not resolve to an active binding gets the same `404` with the same body,
so the endpoint cannot be used to enumerate which workflows exist. Individual
trigger types verify a delivery on top of that — a Telegram secret header, an
HMAC over the raw body for WAHA — and a failed check is a `401` with no run
recorded. Where being unguessable is not enough, set `webhook.require_auth` to
turn that posture into a required-credential one: it refuses any delivery to a
trigger that does not authenticate its callers with a `403` naming the workflow
and the fix, and the boot log states which posture is running.

**The bundled API reference makes no external requests.** The `/docs` page is
served with a strict Content-Security-Policy and its JavaScript is vendored
into the binary, so it works air-gapped and an embedding customer's traffic
never reaches a third party.

## What isolates, and what does not

A table prefix is a naming convention and not an isolation boundary. It keeps
KilasFlow's tables from colliding with a host application's in a shared
database; it does not keep anything from reading them. The only configuration
that genuinely isolates is KilasFlow's objects in a dedicated schema, owned
by a role with no rights on the host application's schema, with `search_path`
set on the KilasFlow connection.

Related and equally load-bearing: every stored row carries a tenant identifier
and every repository call takes a tenant scope, and the tenant is resolved from
whatever authenticated the request — an embed session first, then a signed-in
session or an API key. A row belonging to another tenant does not fail a
permission check; it does not exist. What a tenant *is*, though, is a row: an
id, a name and timestamps, with no per-tenant configuration beyond which node
types it may see (set in the operator's configuration, not stored on the tenant)
and no per-tenant quota, and every tenant's rows share the same tables. With
authentication off, which is the default, every caller is the operator and every
request resolves to the one tenant named `default`.
