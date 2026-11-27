---
title: Notifications
description: 'Signed outbound deliveries to a host when a run fails or completes, or a workflow is activated or deactivated: the outbox, retries, signatures and the delivery log.'
sidebar:
  order: 6.5
---

A host that embeds kilasflow usually wants to tell its users when a production
run fails or a workflow goes offline, without holding an event stream open per
execution. A tenant can name one endpoint, and kilasflow posts a signed JSON
event to it for:

| Event | Sent when | Default |
| --- | --- | --- |
| `execution.failed` | an execution ends failed: a node error, a timeout, a wait whose deadline passed, or a run abandoned by its worker too often | on |
| `execution.completed` | an execution ends succeeded | off |
| `workflow.activated` | a workflow is published (activated, or a version pinned) | off |
| `workflow.deactivated` | an active workflow is deactivated | off |

A cancelled execution sends nothing.

## Turning it on

Notifications need a signing key. Put one in the environment variable
`notifications.signing_key_env` names (`KILASFLOW_NOTIFICATIONS_SIGNING_KEY` by
default) on every process:

```bash
export KILASFLOW_NOTIFICATIONS_SIGNING_KEY="$(openssl rand -base64 32)"
```

While the variable is unset nothing is written, whatever a tenant's settings
say: an event that could only ever be sent unsigned is not queued.

Then set the tenant's endpoint in the `notifications` section of its
[settings](/operate/tenants/#tenant-settings), which takes the operator
credential:

```bash
curl -X PATCH "$KILASFLOW_URL/api/v1/tenants/acme" \
  -H "Authorization: Bearer $OPERATOR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"settings":{"notifications":{"url":"https://app.example.com/hooks/kilasflow","events":["execution.failed","workflow.deactivated"]}}}'
```

| Field | Value |
| --- | --- |
| `url` | an absolute `http` or `https` URL without credentials; empty or `null` turns notifications off for the tenant |
| `events` | the events above; empty or absent means `execution.failed` only |

The endpoint is also checked on every delivery by the same
[egress policy](/operate/security/) a workflow's HTTP Request node meets
(`outbound.allowed_hosts`, the private-network guard,
`outbound.allowed_private_endpoints`), so an endpoint that resolves to a private
address is refused unless the operator admitted that exact host and port.

## How a delivery travels

The event is written to a durable outbox **in the same database transaction**
as the change it reports. A committed failure therefore always has its
notification, and a change that rolled back never does, whichever process or
worker made it.

A dispatcher in every worker process claims one due event at a time under a
lease, posts it, and records the outcome:

- A `2xx` response marks it delivered.
- Anything else (another status, a timeout, a refused connection, a redirect)
  is a failed attempt. The next one waits 30 seconds doubled per failure, capped
  at an hour, and jittered within the upper half of that wait.
- After `notifications.max_attempts` attempts (8 by default) the event is
  dead: kept in the delivery log, never sent again.

Redirects are not followed: a redirected POST would arrive as a bodiless GET.

Delivery is **at least once**. A dispatcher that dies mid-delivery leaves the
event leased; when the lease runs out another dispatcher sends it again, so the
receiver can see one event twice. Every attempt carries the same event id and
the same body, so deduplicate on the id.

## The request

```http
POST /hooks/kilasflow HTTP/1.1
Content-Type: application/json
webhook-id: evt_01a21c17-36f0-78be-899d-df593b434988
webhook-timestamp: 1795761613
webhook-signature: v1,K5oZfzN95Z9UVu1EsfQmfVNQhnkZ2pj9o9NDN/H/pI4=

{"id":"evt_01a21c17-36f0-78be-899d-df593b434988","type":"execution.failed","tenantId":"acme","createdAt":"2026-11-27T08:20:13.168568Z","data":{"executionId":"exec_…","workflowId":"wf_…","workflowVersionId":"wfv_…","status":"failed","trigger":"webhook","error":{"code":"node.failed","message":"…"},"startedAt":"…","finishedAt":"…"}}
```

A `workflow.*` event's `data` carries `workflowId`, `versionId` and `name`.

## Verifying the signature

The signature is the scheme the inbound `standardWebhooks` HMAC preset
verifies: base64 of HMAC-SHA256 over `{webhook-timestamp}.{raw body}`, behind
`v1,`. Recompute it over the exact bytes received, compare in constant time,
and refuse a timestamp more than five minutes from now. A kilasflow webhook
trigger with `hmac.preset: standardWebhooks` verifies these deliveries as they
are.

Unlike the Standard Webhooks specification, the event id is not part of what
is signed; it is sent beside the signature for deduplication.

Each tenant has its own secret, derived from the deployment key, so the key
itself never leaves the operator and no secret is stored in a settings
document:

```
secret = base64( HMAC-SHA256( key, "kilasflow-notifications:" + tenantId ) )
```

`key` is the environment variable's value as bytes. Hand the tenant's secret
to whoever verifies its endpoint:

```bash
printf 'kilasflow-notifications:%s' acme \
  | openssl dgst -sha256 -hmac "$KILASFLOW_NOTIFICATIONS_SIGNING_KEY" -binary \
  | base64
```

Changing the key changes every tenant's secret at once.

## The delivery log

`GET /api/v1/notifications/deliveries` lists the caller's tenant's events,
newest first, with their state:

```bash
curl "$KILASFLOW_URL/api/v1/notifications/deliveries?status=dead" \
  -H "Authorization: Bearer $TENANT_KEY"
```

Each entry carries the event id and type, the endpoint it was bound for,
`status` (`pending`, `delivered` or `dead`), `attempts`, `lastStatusCode`,
`lastError`, `nextAttemptAt` and the exact `payload`. `status` narrows the
list and `limit` (1 to 200, default 50) bounds it. It needs `workflow:read` on
a tenant key; an embed session and a workflow-bound key are refused, because
the log spans every workflow of the tenant.

Events stay in the log for `notifications.retention` (a week by default,
between an hour and 90 days), delivered, dead or still pending, and are
removed with the tenant when it is [deleted](/operate/tenant-deletion/).

The endpoint an event goes to is fixed when the event is written: changing a
tenant's `url` does not re-route events already queued.
