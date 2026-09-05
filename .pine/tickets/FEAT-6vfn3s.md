---
id: FEAT-6vfn3s
title: Add the Telegram action node at n8n parity
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-8r9n21
    - FEAT-ztxs5p
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:03:33Z"
updated: "2026-09-05T05:03:33Z"
---

## Scope

The trigger gets a message in; this node is how the bot answers. Match `n8n-nodes-base.telegram`'s resource and operation surface so an imported workflow maps one to one instead of landing on `kilasflow.unsupported`.

**Message**: Send Message, Send Photo, Send Document, Send Animation, Send Audio, Send Video, Send Sticker, Send Media Group, Send Location, Send Chat Action, Edit Message Text, Delete Chat Message, Pin Chat Message, Unpin Chat Message. **Chat**: Get, Get Administrators, Get Member, Leave, Set Title, Set Description. **Callback**: Answer Query, Answer Inline Query. **File**: Get File.

Send Chat Action belongs in the first cut alongside Send Message, not in a later pass. It is what produces the typing indicator, and a bot that thinks for four seconds in total silence reads as broken to the person waiting — that difference is worth more than several of the other operations combined.

The Bot API is regular enough that almost all of this is metadata, not Go: a method name, a chat id, a handful of scalar parameters, a JSON response. It executes on the declarative routing interpreter, which means it also inherits the SSRF policy and the credential host scope for free. The exceptions are the file operations, which need multipart uploads and the binary store, and Send Media Group, whose `media` argument is a JSON array referencing attachments.

Both Telegram types also need importer entries — `n8n-nodes-base.telegram` and `n8n-nodes-base.telegramTrigger` — added to the `mappings` table in `internal/interop/n8n/n8n.go`, whose exact-string matching is what the whole subset claim rests on.

## Acceptance criteria

- [ ] All four resources and every operation listed above are registered, with n8n's own resource and operation values, so an imported node selects the same operation it selected in n8n.
- [ ] Send Message and Send Chat Action work end to end against a real bot: a Telegram Trigger delivery produces a typing indicator and then a reply in the same chat.
- [ ] Every operation that the Bot API expresses as a plain JSON call is declarative routing metadata with no operation-specific Go.
- [ ] Send Photo, Send Document, Send Audio, Send Video, Send Animation and Send Media Group send a binary from the item's binary references as multipart, and also accept a `file_id` or URL, matching what the Bot API accepts.
- [ ] Get File resolves the file path and stores the downloaded content in the binary store, attached to the output item as a binary reference rather than inlined into the item JSON.
- [ ] Both `n8n-nodes-base.telegram` and `n8n-nodes-base.telegramTrigger` import onto the native nodes, `SupportedMappings()` lists them, and export reproduces the original type strings.
- [ ] An operation parameter n8n supports that KilasFlow does not carry produces a named import diagnostic rather than being dropped in silence.
- [ ] Failures report Telegram's own `description` field, since it is the only part of a Bot API error a user can act on.

## Implementation Plan

Hand-write the routing metadata rather than running the pack generator. Telegram publishes its API as HTML documentation, not an OpenAPI document, and the community-maintained specs that do exist are unvetted third-party artifacts; for roughly twenty-one operations with stable parameter names, hand-written metadata reviewed once is cheaper and safer than importing a conversion of someone else's transcription. Keep it declarative anyway, so it runs on the same interpreter and needs no bespoke executor.

Structure it as `resource` and `operation` select parameters gating everything else through visibility conditions, exactly as the generated packs do — which means this node is a good early consumer of the wider `displayOptions` semantics, since several parameters are shown for more than one operation and the current single-key equality condition cannot express that.

Do the JSON operations first and prove the whole resource matrix with them, then add multipart. Multipart is the one place the interpreter's request model has to grow: it currently assembles a JSON or form body, and an upload needs a streamed file part alongside scalar fields. Add it to the interpreter as a body kind rather than special-casing Telegram inside the node, or the Telegram node quietly becomes programmatic again and WAHA's own send-image operations will need the same thing built a second time.

The trap is `chat_id`. Telegram accepts a numeric chat id, a `@channelusername` string, and in some contexts a user id, and the value in an imported workflow is usually an expression reading the trigger item. Send it as the string the parameter resolves to and let Telegram decide; coercing to a number will break every channel username, and coercing to a string is harmless because the API accepts both.

## References

- Roadmap plan, p3 section, entry V2-p3-7: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- Telegram Bot API, https://core.telegram.org/bots/api — the method surface, `getFile` and its file path, and multipart upload of attachments.
- `internal/interop/n8n/n8n.go` — the `mappings` table, `byN8NType`, `SupportedMappings()`.
- `internal/node/registry.go` — `PropertyDefinition` and `VisibilityCondition`, whose single-key equality form is the limit this node runs into.
- n8n 2.34.0 reference checkout, `packages/nodes-base/nodes` — read the Telegram node's own descriptions once the sparse checkout is widened; it currently contains only `HttpRequest`, `If`, `Schedule` and `Set`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 12, 13 — the 27 actions grouped by resource in the picker, and the Resource/Operation cascade with Chat ID, Text, Reply Markup and Additional Fields. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
