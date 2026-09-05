---
id: FEAT-1br8at
title: Stop redaction from destroying live execution data
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:02:28Z"
updated: "2026-09-05T05:02:28Z"
---

## Scope

`internal/execution/redact.go` rewrites JSON payloads in place, replacing any value under a sensitive key, and any string that begins with a credential scheme, with the literal `[redacted]`. It runs at three boundaries: inbound webhook ingest (`internal/webhook/webhook.go:249`, inside `requestPayload`), every durable executions write (`internal/repository/executions.go:597-605`, the `payload` helper), and the live event stream (`internal/events/events.go:139`). The first two are destructive — the redacted bytes are what get stored, and `Service.run` rehydrates the trigger item straight back out of `record.Input` via `inputItem` (internal/engine/service.go:298-303, 399-411). The running workflow therefore sees redacted data, not the data that arrived.

Two entries on the key list break WAHA outright. `session` and `sessionid` (redact.go:41-42) are both sensitive keys, so a WAHA webhook envelope whose `session` field names the WhatsApp session becomes `"session": "[redacted]"` before any node runs, and every WAHA action node that reads `$json.session` sends the string `[redacted]` to the API. n8n-style AI memory keyed on `sessionId` collapses every conversation into a single bucket for the same reason, which is a cross-user data leak in a multi-tenant product, not merely a bug.

`looksLikeCredential` (redact.go:136-143) is worse because it inspects content rather than keys. `credentialSchemes` (redact.go:54) includes `"basic "`, so *any* string starting with those six characters is destroyed wherever it appears. A WhatsApp message reading "basic plan pricing?" reaches the AI agent as `[redacted]`. So does "bearer of this letter" and "digest of the meeting".

The purpose the function serves is real: `FEAT-pn3dtq` proved that a credential applied by the HTTP node is recorded as `[redacted]` rather than in the clear, and that guarantee must survive. What must change is that redaction stops standing between the wire and the runtime.

## Acceptance criteria

- [x] A workflow triggered by a webhook whose body contains `session`, `sessionId` and a message beginning `basic ` receives all three values verbatim in `$json`.
- [x] Credential material applied by a node is still never readable in a stored execution record, a node-run record, a live event, or an API response — proven by the existing HTTP-credential end-to-end case.
- [x] Redaction is content-agnostic where the key is not sensitive: an ordinary string is never rewritten because of the words it starts with.
- [x] AI memory keyed on a session identifier keeps distinct buckets for distinct sessions.
- [x] The redaction rules are covered by a table-driven test that names, for each key and each pattern, whether it is redacted and why.
- [x] Existing execution records written before this change still read back without error.

## Implementation Plan

The decision this ticket must settle is *where* redaction runs. Three positions are possible.

Move it to the read boundary only — store the truth, redact on the way out through `internal/api/handlers/workflows.go:455-495` and `internal/events/events.go:139`. This is the strongest for correctness and the weakest for security: real secrets then sit in the database, and any future reader that forgets to redact leaks them. It also contradicts the guarantee `FEAT-pn3dtq` shipped.

Keep it at the write boundary and shrink the rule set — remove `session`, `sessionid`, `otp` and `pin` from the key list, and delete `looksLikeCredential` entirely. Cheap, immediate, and it leaves the write boundary destroying anything genuinely named `token` or `password` that happens to be user data.

Split the boundary — the *trigger payload* is stored unredacted and treated as tenant data, while node inputs, node outputs and errors keep being redacted on write because that is where resolved credentials actually appear. Recommendation: this one, combined with shrinking the key list. Redaction exists to catch credentials the runtime resolved and handed to a node; an inbound webhook body has never contained a KilasFlow credential, so redacting it protects nothing and destroys everything. The header map built in `requestPayload` (webhook.go:219-224) is the one part of the trigger payload that genuinely carries caller credentials, so redact the headers specifically and leave the body, query, method and path intact.

Whichever position is taken, `looksLikeCredential` goes. Matching on `"basic "` as a *value* prefix cannot be made safe: any scheme string is also ordinary prose, and the function has no way to tell a header value from a chat message. If header-value pairs still need covering, match the pair — a `value` sibling of a `name` that is itself a sensitive header key — rather than the content alone.

Order the work: change `redact.go` and its tests first so the rules are provable in isolation; then change `requestPayload` to redact only the header map; then re-verify the `FEAT-pn3dtq` credential non-disclosure case end to end, because that is the guarantee this ticket is most likely to break. Do not touch `internal/events/events.go` — the live stream is a read boundary and redacting there is correct.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-6.
- `internal/execution/redact.go` — `sensitiveKeys`, `credentialSchemes`, `looksLikeCredential`, `isSensitiveKey`.
- `internal/webhook/webhook.go` — `requestPayload` and its `execution.Redact` call.
- `internal/repository/executions.go` — the `payload` helper that redacts on every write.
- `internal/engine/service.go` — `run` and `inputItem`, which rehydrate the trigger item from the stored record.
- `.pine/tickets/FEAT-pn3dtq.md` — the credential non-disclosure guarantee that must survive.

## Outcome

The recommended position was taken: **split the boundary and shrink the rule
set**. Redaction exists to catch credentials the runtime resolved and handed to
a node; a trigger payload has never contained a KilasFlow credential, and the
runner rehydrates the trigger item straight back out of the stored record — so
redacting it protected nothing and put `[redacted]` on the wire in place of the
data that arrived.

### What changed

`session` and `sessionid` are off the key list, with the reason recorded beside
the entry that remains: `sessiontoken` really is a credential, the other two name
a WhatsApp session and a conversation. `otp` and `pin` are off for the same
reason — neither is ever a KilasFlow credential, and a WhatsApp message carrying
a one-time code is exactly this product's traffic.

`looksLikeCredential` is gone entirely. It could not be made safe: any auth
scheme string is also ordinary prose, and a function looking only at a string
cannot tell a header value from a chat message. It destroyed "basic plan
pricing?", "bearer of this letter" and "digest of the meeting". The pair form it
was meant to cover — `{"name": "Authorization", "value": "Bearer …"}` — is now
matched **as a pair**, which is what the plan suggested and what actually
distinguishes the two cases.

The write boundary is split. `payload` still redacts node inputs, node outputs
and errors, because that is where resolved credentials genuinely appear.
`triggerPayload` handles execution input.

### One thing the ticket did not anticipate

The plan put trigger-header redaction only in the webhook handler. Doing that
alone removes the repository as the last line of defence: any trigger source
added later would leak headers by simply forgetting, and
`TestExecutionStoreRedactsCredentialsBeforeStorage` caught exactly that
regression.

So `triggerPayload` redacts the `headers` sub-object and nothing else — body,
query, method and path are stored as they arrived. The webhook handler still
redacts at ingest, so headers never reach storage in the clear even briefly, and
the repository re-checks. `RedactTriggerHeaders` is the narrow function that
does it.

### Verification

- `TestWebhookDeliversSessionAndSchemePrefixedTextVerbatim` runs a WAHA-shaped
  envelope through the real handler and asserts `session`, `sessionId` and a
  message beginning "basic " all reach the stored input verbatim.
- `TestWebhookRedactsInboundHeadersButKeepsTheBody` splits the old bundled test
  so each half states its own reason.
- `TestRedactionRules` is table-driven across eleven cases, each naming whether
  it is redacted and why — both directions, because over-redacting is as much a
  defect here as under-redacting.
- `TestMemoryKeepsDistinctBucketsForDistinctSessions` pins the cross-tenant half:
  with `sessionId` redacted, every conversation shared one bucket.
- The `FEAT-pn3dtq` guarantee was re-verified end to end and holds —
  `TestExecutionAPIRedactsCredentialsInResponses`,
  `TestCredentialAPINeverDisclosesASecretAfterItIsStored`,
  `TestExecutionStoreRedactsCredentialsBeforeStorage` and the six node-level
  credential tests all pass.
- `internal/events/events.go` was not touched. The live stream is a read
  boundary and redacting there is correct.

### Residual, stated plainly

A webhook body containing a key literally named `token` is now stored as it
arrived, where before it was destroyed. That is the deliberate consequence of
treating the trigger payload as tenant data: the workflow cannot run on data
that was redacted before it got there. It is the caller's own body, readable
only by the tenant that owns the workflow, and it never contains a KilasFlow
credential.
