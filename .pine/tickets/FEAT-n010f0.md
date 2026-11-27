---
id: FEAT-n010f0
title: 'SaaS integration playbook: one end-to-end page from host control plane to production (plus agent-as-tool recipe)'
status: doing
priority: high
labels:
    - docs
    - saas
    - embedding
parent: EPIC-62zt4j
created: "2026-09-23T02:07:33Z"
updated: "2026-11-27T08:08:26Z"
---

# Description

A host SaaS team needs one page that walks the whole integration in order. Today the pieces are scattered over the embedding guide, the tenancy concept page, the example READMEs and code comments, and some of them contradict each other. Examples: the embedding guide says keys are "minted at the store layer", although the operator tenant API exists; it says the SDK is not on npm while the SDK README says `npm install`; and it says the Data table node will arrive "once it lands", although it exists.

# Acceptance Criteria
- [x] One page covers, in order: provisioning tenants from the host control plane; storing and rotating tenant keys; mapping host roles to scopes; building the list and create UI with the tenant key; minting embed sessions and seeding confinement; proxying activation; provisioning credentials; dispatching host events into workflows; idempotency; tenant deletion; observability
  - Evidence: `docs/src/content/docs/guides/saas-playbook.md`, sections 1 to 11 in that order, plus a go-live checklist. It links to the deep pages rather than repeating them.
- [x] A recipe for a workflow used as a synchronous tool by the host's own AI agent: Webhook + Respond to Webhook, `webhook.response_timeout`, idempotency, and schema ownership
  - Evidence: the "Recipe" section of the playbook. The two nodes were run against a built binary (`200 {"orderId":"A-1001","status":"shipped"}`; a repeated `X-Tool-Call-Id` answered `{"duplicate":true,...}`).
- [ ] Every command and snippet on the page is verified in CI, or taken from the tested reference host
  - Partly: the snippets come from `sdk/examples/reference-host`, the generated API reference, or were run by hand against a binary built from this branch with authentication on (tenant and key provisioning, key rotation and revoke, embed session with `actor`, credential create, scoped key, tenant delete twice, the tool workflow). The reference host itself is not run in CI, and no new CI test was added, so this box stays open.
- [x] The stale embedding-guide statements are corrected (the store-layer key, the npm status, "once it lands")
  - Evidence: "once it lands" and the Data table node fixed; "the import response is the only call that reports the webhook address" fixed (`GET /workflows/{id}/webhooks` does too); the SDK datastore-method claim fixed; step 5 now prefers answering with the run id. The "minted at the store layer" text was already gone on main, and the guide's "SDK is not on npm yet" is correct (no tag exists; `guides/sdk.md` says the same).

# Implementation Plan

# Notes

Source: the 2026-09-23 host-SaaS integration review (gaps D1 and D8). Product gaps are in EPIC-7c3ry9.

# Related Files

# Attachments

## Progress (2026-11-27)

- The playbook lives at `guides/saas-playbook.md` and is the first link of the Embed track on the landing page.
- Behaviour the code showed that the old docs did not say, now in the playbook: a tenant needs no user account to be embedded (tenant + key is enough); a tenant key mints its own successor with `POST /api-keys` and the new key revokes the old one; the embed confinement is read from the workflow's active revision (the latest if none is active) at mint time and fixed in the token, so credentials must be referenced in the document before the mint.
- Contradiction left for a code-side fix: `sdk/README.md` still says `npm install @kilasflow/sdk` under Install, while no `sdk-v*` tag exists and `docs/.../guides/sdk.md` says the package is not on npm. The docs side is correct; the README is outside `docs/`. Also `sdk/examples/reference-host/server.mjs:328` says the SDK has no datastore methods, which is no longer true.
