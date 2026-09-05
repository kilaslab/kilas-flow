---
id: FEAT-gg85se
title: Prove the n8n community template migration end to end
status: todo
priority: high
labels:
    - e2e
    - testing
    - interop
deps:
    - FEAT-cx3hq1
    - FEAT-0556ck
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:03:59Z"
updated: "2026-09-05T12:03:59Z"
---

## Scope

The epic's acceptance scenario is a migration: "the official WAHA chatting template imports, opens in the editor with correct icons and parameter panels, activates, receives a real WhatsApp webhook, and replies — with the same template imported twice for two different clients." Every part of that is currently verified, where it is verified at all, in Go — against a parsed document, not against a browser and not against a running workflow.

`internal/interop/n8n/corpus` scores 39 fixtures into tiers and is a genuinely good instrument, and it stops at the boundary this ticket crosses. It reports whether a document imports and whether the result would compile. It does not open the editor, does not click activate, does not deliver a webhook, and does not observe a reply.

The steps between "imports" and "works" are where a real migration fails, and none of them has coverage:

- **The editor renders it.** Correct icons and parameter panels are named in the acceptance scenario for a reason — before p2-5 the editor's node identity lives in hardcoded frontend maps, and a node with no entry renders as a grey box with no credential picker. An imported workflow full of grey boxes is a failed migration that every API-level test calls a success.
- **Credentials are rebound.** An n8n credential reference is `{id, name}` and is instance-local. A migrating user must create real credentials and attach them, and that is a UI flow.
- **Webhooks are re-pointed.** Import mints a fresh opaque route per tenant and workflow and returns the new URLs, precisely so the same template can serve two clients — `uidx_webhook_bindings_route` being unique across tenants is what made the second import fail before p1-8. The user must copy that URL into the sending system.
- **Activation succeeds.** An imported workflow containing an unsupported capsule cannot activate, by design, because the capsule's validator always fails. The distinction between "imported" and "activatable" is the one a migrating user cares about most.
- **The same template imports twice.** This is the actual business goal — one agency, many clients — and it is the case that used to be broken.

## Acceptance criteria

- [ ] A real n8n community template is imported through the editor, not through `curl`, and the resulting workflow opens on the canvas.
- [ ] Every node in the imported workflow renders with its icon, display name and parameter panel; a node rendering as an unstyled fallback fails the test.
- [ ] The import diagnostics are asserted against an expected set, so a regression that starts dropping a field is caught rather than absorbed.
- [ ] Credentials are created and bound through the UI, and the workflow reaches a state where activation is permitted.
- [ ] The workflow activates, and the minted webhook URL shown to the user is the one that a delivery must be sent to.
- [ ] A delivery to that URL runs the workflow to completion, observed through the execution event stream rather than by polling a database.
- [ ] The same template is imported a second time under a second tenant, both are activated simultaneously, and a delivery to each reaches only its own tenant's execution.
- [ ] A template containing a node with no mapping produces a capsule that renders, preserves its original type and version, and refuses activation with a message naming the node.

## Implementation Plan

Drive the whole thing through the UI, including the import, which is why this depends on V2-p10-20. An API-driven import inside a browser test is a Go test wearing a costume; the migration path this proves is the one a user follows.

Choose fixtures carefully, because licensing bounds them. The WAHA templates repository carries no licence file and the GitHub API reports `license: null`, so `scripts/corpus-sync.sh` fetches them into a gitignored, digest-pinned directory and they are never committed. This suite must use the same mechanism rather than checking a template in, and it must skip cleanly with a message when the corpus is absent — otherwise the suite fails on any machine that has not run `make corpus`.

For the WhatsApp side, do not require a WAHA server. The trigger's contract is an HTTP delivery with a particular body shape and, optionally, an `X-Webhook-Hmac` header; a stub that posts a recorded payload proves the path end to end without infrastructure. Reserve a real WAHA server for the capstone in V2-p11-8, and say in the test which one this is.

The two-tenant case is the most valuable test here and the easiest to write incorrectly. It must use two genuinely distinct tenants through the authenticated API, which is why it depends on `FEAT-ddzk2k` in substance even where it does not in form — before that lands every request resolves to `"default"` and the test would be asserting something that cannot fail. If this suite is written before authentication ships, mark that specific case pending rather than writing a version that passes vacuously.

Assert on the diagnostics as data, not as rendered text. `ImportIssue` carries a severity and a node reference; a test matching on a sentence will break on a copy edit and teach the team to loosen the assertion.

One more case worth including because it is the common one and looks like success: a template that imports cleanly, activates, and receives nothing because the sending system still points at the source instance's path. The test should prove the UI surfaced the new URL prominently enough that a user could not miss it.

## References

- Roadmap plan, p11 section, entry V2-p11-4: `.pine/roadmap.md`.
- `.pine/tickets/EPIC-m42s3g.md` — the acceptance scenario this suite makes executable.
- `internal/interop/n8n/n8n.go` — `Import`, the `mappings` table, `ImportIssue` and its severities.
- `internal/interop/n8n/corpus/BASELINE.md` — the tiers this suite extends past.
- `scripts/corpus-sync.sh` — the digest-pinned, gitignored fixture mechanism and its licensing rationale.
- `nodes/unsupported.go` — the capsule and its always-failing validator.
- `internal/repository/webhooks.go` — `mintWebhookRoute` and the per-tenant route.
- `internal/api/handlers/interop.go` — the import response carrying diagnostics and minted webhook URLs.
- `.pine/tickets/FEAT-0556ck.md` — V2-p10-20, the import UI this suite drives.
- `.pine/tickets/FEAT-ddzk2k.md` — V2-p8-1, without which the two-tenant case cannot fail.
