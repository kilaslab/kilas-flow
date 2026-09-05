---
id: FEAT-ztxs5p
title: Add the Telegram trigger with self-registering webhooks
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-91as16
    - FEAT-0f87fn
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:03:01Z"
updated: "2026-09-05T05:03:01Z"
---

## Scope

Telegram is the fast proof of the whole phase. A bot token from BotFather costs nothing, needs no WhatsApp infrastructure and no customer instance, and a Telegram Trigger into an AI Agent into a Send Message reply exercises exactly the same path a WAHA template will: a registry-driven webhook binding, a trigger-shaped item, credential-scoped outbound calls, and a running graph. It is the epic's first acceptance scenario for that reason.

Match `n8n-nodes-base.telegramTrigger` parameter for parameter, so an imported workflow lands on an identical form. The **Trigger On** multi-select carries n8n's exact list: `*` (all updates except Chat Member, Message Reaction and Message Reaction Count), Message, Edited Message, Channel Post, Edited Channel Post, Callback Query, Inline Query, Chosen Inline Result, My Chat Member, Chat Member, Chat Join Request, Poll, Poll Answer, Pre-Checkout Query, Shipping Query, Message Reaction, Message Reaction Count, Chat Boost, Removed Chat Boost, Business Connection, Business Message, Edited Business Message, Deleted Business Messages, Purchased Paid Media. The additional fields are Download Images/Files with its Image Size sub-option, Restrict to Chat IDs, and Restrict to User IDs. The `*` exclusion list is not n8n's invention — the Bot API itself documents `chat_member`, `message_reaction` and `message_reaction_count` as the three update types not delivered by default.

Unlike the WAHA trigger, this one registers itself. Activation calls `setWebhook` with the workflow's own URL, the selected `allowed_updates`, and a `secret_token`; deactivation calls `deleteWebhook`. Telegram returns the secret in the `X-Telegram-Bot-Api-Secret-Token` header on every delivery, so verification is a constant-time comparison and there is no excuse for skipping it. This is the first node that needs the activate and deactivate lifecycle hooks, and the first that needs webhook binding extraction to be registry-driven — `internal/webhook/webhook.go`'s `Extract` filters on one node type supplied at `cmd/kilasflow/main.go:138`, so no trigger but `kilasflow.webhook` can bind a path today.

The credential is a bot token. `internal/credentials` has no type that fits and no way to add one from outside its package-level `definitions` map, and `web/src/lib/workflow-editor/credentials.ts`'s `BY_NODE_TYPE` decides whether the editor shows a credential picker at all.

## Acceptance criteria

- [ ] The trigger registers with n8n's exact Trigger On list and the three additional fields, and selecting `*` sends no `allowed_updates` narrower than the Bot API's own default set.
- [ ] Activation calls `setWebhook` with the workflow's URL, the selected updates and a generated `secret_token`; deactivation calls `deleteWebhook`; both go through `safehttp` and the credential store.
- [ ] Every delivery is rejected unless `X-Telegram-Bot-Api-Secret-Token` matches, compared in constant time, before an execution is created.
- [ ] A `telegramApi` credential type holds the bot token as a secret field, is never returned after storage, and scopes outbound calls to the Telegram API host.
- [ ] Restrict to Chat IDs and Restrict to User IDs drop a non-matching update without starting an execution, and the drop is visible in logs rather than silent.
- [ ] Download Images/Files fetches the update's file through `getFile` into the binary store and attaches it to the item as a binary reference, honouring the Image Size sub-option; with the option off, no file is fetched.
- [ ] An update type the node does not know is passed through to the item rather than rejected, so a new Bot API update type does not break an active workflow.
- [ ] A local development mode exists that receives updates without a public URL, and the README documents both it and the tunnel route.

## Implementation Plan

Order the work so each step is testable: credential type, then the node definition and its webhook binding, then the lifecycle hooks and `setWebhook`, then secret verification, then the restrict filters, then downloads. The lifecycle hooks are the piece with the most reach — they are the same hooks WAHA's optional auto-registration uses — so implement them as the trigger-lifecycle work defines them and resist adding a Telegram-shaped variant.

Take the local development mode, and implement it as an explicit Delivery parameter with `webhook` as the default and `polling` as the alternative. It is the difference between the owner being able to test on a laptop and not, `getUpdates` is a documented Bot API method with the same `allowed_updates` argument, and the two modes share everything downstream of the update. Implement polling as a goroutine owned by the activation lifecycle, one per active trigger, cancelled on deactivation. Be honest in the parameter description that it is single-process: it is correct for the single binary shipped today and will need revisiting when work moves across processes. Telegram refuses `getUpdates` while a webhook is set, so switching modes must delete the webhook first — that failure is confusing enough to deserve its own error message.

The traps are all in the details. `secret_token` accepts only `A-Z`, `a-z`, `0-9`, `_` and `-`, 1 to 256 characters, so generate it from an alphabet that respects that rather than from arbitrary base64. `setWebhook` requires a public HTTPS URL, so activation must fail with a message naming that requirement rather than a bare API error when the configured public URL is not one. And the Bot API has grown update types beyond n8n's list — the current documentation includes several that n8n's selector does not offer — which is exactly why the list is pinned to n8n's for import parity while unknown incoming updates still pass through.

One thing cannot be confirmed from the reference checkout as it currently stands: the sparse checkout contains only `HttpRequest`, `If`, `Schedule` and `Set` under `packages/nodes-base/nodes`, so the exact credential `name` and the exact option values must be read off the Telegram node once the checkout is widened. Treat `telegramApi` as the expected name, not a verified one, until then.

## References

- Roadmap plan, p3 section, entry V2-p3-6: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- Telegram Bot API, https://core.telegram.org/bots/api — `setWebhook` (`url`, `allowed_updates`, `drop_pending_updates`, `secret_token` of 1–256 characters from `A-Z a-z 0-9 _ -`), the `X-Telegram-Bot-Api-Secret-Token` header, `deleteWebhook`, `getUpdates`, and the note that `chat_member`, `message_reaction` and `message_reaction_count` are not delivered by default.
- `internal/webhook/webhook.go` — `Extract` and its single hardcoded node type, wired at `cmd/kilasflow/main.go:138`; `requestPayload`'s fixed item envelope.
- `nodes/webhook.go` — `webhookTrigger`, the authentication modes, and `WebhookPath`.
- `internal/credentials/credentials.go` — the closed `definitions` map and `Apply`'s switch.
- `web/src/lib/workflow-editor/credentials.ts` — `BY_NODE_TYPE`, which gates the credential picker.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 11 — the Telegram Trigger NDV to match: Webhook URLs, credential, two notice blocks, Trigger On as multi-select chips, Additional Fields, Test this trigger. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
