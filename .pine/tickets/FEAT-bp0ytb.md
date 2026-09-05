---
id: FEAT-bp0ytb
title: Add the WAHA trigger with per-event outputs
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-qe6wb8
    - FEAT-91as16
    - FEAT-5kv1jq
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:01:37Z"
updated: "2026-09-05T05:01:37Z"
---

## Scope

WAHA delivers every event of a session to one webhook URL and distinguishes them by a `body.event` field. The n8n WAHA Trigger turns that single stream into a fan-out node with one output per event type, so a workflow wires `message` to one branch and `message.ack` to another. Reproducing that shape is what lets an imported WAHA template keep its wiring: the connections in the JSON are output indexes, and an index means nothing unless the outputs are in the same order.

The orders differ per version. `202409` publishes 20 events and `202502` publishes 26, and the two orderings diverge from index 5 onward — so the index-to-event table is a property of the typeVersion, not of the node, and has to be generated from the vendored spec rather than typed by hand once. Register the trigger at both versions, exactly as the action pack is.

Two engine facts make this node the sharpest test of the p1 work. First, `dependenciesComplete` in `internal/engine/runner.go` returns true as soon as every upstream node has *run* — it never asks whether the connecting port carried any items — so a 20-output trigger that emits on one port today would still start every one of the other 19 branches. Second, `internal/webhook/webhook.go`'s `requestPayload` hardcodes one item shape, `{method, path, headers, query, body}`, for every trigger; WAHA templates read `$json.event`, `$json.session` and `$json.payload` at the top level, so the envelope has to be node-type aware before any of this routes correctly.

The n8n node also does two things badly that KilasFlow should not copy. It registers nothing with WAHA, so the URL has to be pasted into the session configuration by hand and an imported workflow silently receives nothing; and it verifies no signature, so anyone who learns the URL can inject events.

## Acceptance criteria

- [ ] The trigger is registered at both `202409` and `202502`, with one named output per event, and the index-to-event table for each version is generated from the vendored spec.
- [ ] An inbound delivery is routed to exactly the output matching its `body.event`, and only that branch executes; a delivery carrying an unknown event is routed to a catch-all output rather than dropped.
- [ ] The trigger item carries WAHA's envelope at the top level, so `$json.event`, `$json.session` and `$json.payload` resolve without a wrapper path.
- [ ] Activation registers a webhook binding for the trigger through registry-driven extraction, with no node type hardcoded at the composition root.
- [ ] Optional auto-registration installs the workflow's own webhook URL into the WAHA session on activation and removes it on deactivation, using the `wahaApi` credential; it is off by default.
- [ ] When auto-registration is off, activation returns a notice naming the exact URL to paste into WAHA's session configuration, so an imported workflow cannot look correct while receiving nothing.
- [ ] When a webhook secret is configured, `X-Webhook-Hmac` is verified as SHA-512 over the raw request body and a failing delivery is rejected before an execution is created.
- [ ] Repeated deliveries carrying the same `X-Webhook-Request-Id` produce one execution, since WAHA retries.

## Implementation Plan

Generate the event table alongside the action pack — same spec, same generator run, one more output artifact — so the two can never drift. The trigger definition then declares `len(events) + 1` output ports, and the executor is a few lines: read `event` from the trigger item, look up the port index for the node's own version, and emit `request.Input` on that index with every other port empty.

That executor is correct and still useless without branch pruning: with today's runner every branch downstream of an empty port runs anyway. Do not work around it inside the node — a node cannot stop a downstream node from running — and do not ship this ticket without a test that wires two events to two branches, delivers one, and asserts the other branch produced no node run.

Settle the two open questions here rather than deferring them. Take auto-registration, implemented as an opt-in parameter that is off by default: default-on would change the behaviour of an imported workflow relative to n8n and would write to a customer's WAHA instance as a side effect of activation, which is not a thing an import should do silently. It uses the activate and deactivate lifecycle hooks the trigger-lifecycle work introduces, calling `PUT /api/sessions/{session}` through the routing interpreter so the same SSRF policy and credential scope apply. Take HMAC verification too, active whenever a secret is configured on the node: it costs a comparison, it is the only thing standing between a leaked URL and injected WhatsApp events, and it needs the raw request bytes — which is precisely why the raw-body capture work has to land first, since `requestPayload` re-marshals the body through `map[string]any` and the original bytes are gone by the time any handler sees them.

The trap is the notice. An activation notice that only appears in an API response nobody reads is the same as no notice; it has to reach the import screen and the editor's activation path, or the failure mode this ticket exists to prevent survives intact.

## References

- Roadmap plan, p3 section, entry V2-p3-4: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `internal/engine/runner.go` — `dependenciesComplete` and `nodeInput`; `NodeOutput` is indexed by the definition's declared output-port order.
- `internal/webhook/webhook.go` — `requestPayload`'s fixed `{method, path, headers, query, body}` envelope, and `Extract`, which filters on one node type supplied at composition (`cmd/kilasflow/main.go:138`).
- `nodes/webhook.go` — `webhookTrigger`, `executeWebhook` and `WebhookPath` as the shape a new trigger follows.
- `nodes/core.go` `ifNode` — the existing two-output fan-out and how named ports map to output indexes.
