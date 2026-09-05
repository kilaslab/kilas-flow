---
id: FEAT-sdjdh2
title: Surface activation notices in the editor
status: todo
priority: medium
created: "2026-09-05T11:02:47Z"
updated: "2026-09-05T11:02:47Z"
parent: EPIC-m42s3g
phase: p8
---

## Scope

`POST /workflows/{id}/activate` now answers with an `ActivationResource`: the workflow plus a `notices` array, each naming a node and something activation could not do for the user. The WAHA trigger produces one when auto-registration is off — *"add this URL to the session's webhook configuration"* — and the Telegram trigger will produce the same shape.

Nothing reads it. The SPA has no activation control at all: `web/src/routes/(dashboard)/app/workflows/+page.svelte` renders `workflow.active` as an Active/Draft dot and there is no button to change it, so the notice is carried by the API and shown to nobody.

That is the exact failure the notice exists to prevent, one layer up. A trigger whose service was never told where to deliver looks identical to one that is listening, and a user who never sees the notice has no way to learn the difference until a message goes unanswered.

## Acceptance criteria

- [ ] The workflow list and the editor can activate and deactivate a workflow.
- [ ] Every notice the activation response carries is shown, named to its node, and stays visible until the user dismisses it — a toast that disappears in four seconds is the same as no notice.
- [ ] A notice carrying a URL offers a copy control, since pasting it into somebody else's console is the whole point.
- [ ] A workflow that activates with no notices shows no notice UI at all.
- [ ] Activation failing with a 502 — a trigger that could not register — reads as a failure rather than as a notice, and the workflow is shown as inactive, which is what the server rolled it back to.

## References

- `internal/api/handlers/workflows.go` — `Activate`, `ActivationResource`, `ActivationNotice`.
- `internal/webhook/lifecycle.go` — `Notice`, `NoticeSource`, `GatedLifecycle.ActivationNotice`.
- `packs/waha/manifest-*.json` — the WAHA trigger's `notice` text and its `{{ url }}` placeholder.
- FEAT-bp0ytb, which added the notice and could not surface it.
