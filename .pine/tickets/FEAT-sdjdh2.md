---
id: FEAT-sdjdh2
title: Surface activation notices in the editor
status: done
priority: medium
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T11:02:47Z"
updated: "2026-09-05T16:08:39Z"
---

## Scope

`POST /workflows/{id}/activate` now answers with an `ActivationResource`: the workflow plus a `notices` array, each naming a node and something activation could not do for the user. The WAHA trigger produces one when auto-registration is off — *"add this URL to the session's webhook configuration"* — and the Telegram trigger will produce the same shape.

Nothing reads it. The SPA has no activation control at all: `web/src/routes/(dashboard)/app/workflows/+page.svelte` renders `workflow.active` as an Active/Draft dot and there is no button to change it, so the notice is carried by the API and shown to nobody.

That is the exact failure the notice exists to prevent, one layer up. A trigger whose service was never told where to deliver looks identical to one that is listening, and a user who never sees the notice has no way to learn the difference until a message goes unanswered.

## Acceptance criteria

- [x] The workflow list and the editor can activate and deactivate a workflow.
- [x] Every notice the activation response carries is shown, named to its node, and stays visible until the user dismisses it — a toast that disappears in four seconds is the same as no notice.
- [x] A notice carrying a URL offers a copy control, since pasting it into somebody else's console is the whole point.
- [x] A workflow that activates with no notices shows no notice UI at all.
- [x] Activation failing with a 502 — a trigger that could not register — reads as a failure rather than as a notice, and the workflow is shown as inactive, which is what the server rolled it back to.

## References

- `internal/api/handlers/workflows.go` — `Activate`, `ActivationResource`, `ActivationNotice`.
- `internal/webhook/lifecycle.go` — `Notice`, `NoticeSource`, `GatedLifecycle.ActivationNotice`.
- `packs/waha/manifest-*.json` — the WAHA trigger's `notice` text and its `{{ url }}` placeholder.
- FEAT-bp0ytb, which added the notice and could not surface it.

## Work evidence

Done. The activation answer now reaches a user on both surfaces that can produce it.

### What was built

- `web/src/lib/workflow-editor/activation.ts` — the reading of the activation answer, kept out of the components because two surfaces activate a workflow and a component that owns its own parsing grows a second, slightly different copy of it. It resolves each notice's `nodeId` to the name the user gave that node on the canvas, pulls the URL back out of the sentence the notice states it in (so a copy control has something to copy), keys notices so two from the same node can be dismissed apart, and turns a failed activation into a sentence that says the workflow was left inactive when the status is 502.
- `web/src/lib/components/workflow-editor/activation-notices.svelte` — the panel. One row per notice: the node name, the message, the URL as selectable `<code>` with a **Copy URL** button beside it, and a per-notice dismiss. Nothing expires on a timer. It renders no markup at all when there are no notices, and an always-mounted `sr-only` live region announces their arrival — a live region that appears with its content is one several screen readers never read.
- `web/src/lib/components/workflow-editor/workflow-editor.svelte` — an Activate/Deactivate button in the toolbar, an Active/Inactive state readout in the toolbar's status region, an `Activation failed:` banner in the destructive register, and the notice panel. Activation is offered only when a caller supplies both `onActivate` and `onDeactivate`, so the embed — whose host decides when its own workflows go live — is unchanged.
- `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` — wires `activateWorkflow` / `deactivateWorkflow`, holds the notices, and refetches the workflow after a failed activation rather than guessing whether the rollback landed.
- `web/src/routes/(dashboard)/app/workflows/+page.svelte` — each row gets a real Activate/Deactivate button as a sibling of the row link (a button nested in an anchor is invalid markup and never receives the click), the notices render as a card above the list under a heading naming the workflow, and the list is re-read from the server either way so the row's dot cannot claim something the server did not do. The decorative `MoreHorizontal` icon, which suggested an action and performed none, is gone.

### Two judgement calls worth recording

- Activating is blocked while the canvas is dirty, sharing the hint Run already shows. Activation pins the latest *saved* revision, so activating over unsaved edits publishes something other than what the user is looking at. Deactivating is never ambiguous that way and is never blocked.
- A failed activation refetches rather than assuming inactive. A 502 is rolled back, but the 500 branch in `Activate` fires when the rollback itself failed and may have left the workflow active with a trigger that registered nothing. Guessing either way reproduces the exact mismatch the notices exist to prevent.

### Verification

`pnpm check` was first proved to actually check something — a deliberate type error planted in `activation.ts` and again in `activation-notices.svelte` was caught both times — before being trusted.

```
$ pnpm check      # web/
COMPLETED 1346 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS

$ npx vitest run  # web/
Test Files  23 passed (23)
     Tests  251 passed (251)

$ pnpm build      # web/
✓ built in 3.03s
```

The 18 new tests in `web/src/lib/workflow-editor/activation.test.ts` were shown to fail without the behaviour they cover, by mutating the implementation and restoring it:

| Mutation | Tests that failed |
| --- | --- |
| Notice keeps the raw node ID instead of the canvas name | 2 |
| No trailing-punctuation stripping on an extracted URL | 2 |
| No 502 rollback sentence | 1 |
| Notice key drops the index, so one node's notices collide | 2 |
| `notices: null` not defended against | 1 |

### Ticket accuracy

Everything the ticket asserted held up. `internal/api/handlers/workflows.go` really does carry `Activate`, `ActivationResource` and `ActivationNotice`; `internal/webhook/lifecycle.go` really does carry `Notice`, `NoticeSource` and `GatedLifecycle.ActivationNotice`; the WAHA `notice` text and its `{{ url }}` placeholder are in `packs/waha/manifest-202409.json`, `manifest-202502.json` and both `pack-trigger-*.json`; and the list page really did render only a dot with no way to change it.

One thing the ticket did not know: orval had **already** generated `activateWorkflow`, `deactivateWorkflow`, `ActivationResource` and `ActivationNotice` into `web/src/lib/api/generated/`, so no client regeneration and no server change were needed. The work was purely a matter of calling what was already there. The wire field is `nodeId`, not `nodeID`.
