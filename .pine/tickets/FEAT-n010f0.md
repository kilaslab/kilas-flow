---
id: FEAT-n010f0
title: 'SaaS integration playbook: one end-to-end page from host control plane to production (plus agent-as-tool recipe)'
status: todo
priority: high
labels:
    - docs
    - saas
    - embedding
parent: EPIC-62zt4j
created: "2026-09-23T02:07:33Z"
updated: "2026-09-23T02:07:33Z"
---

# Description

A host SaaS team needs one page that walks the whole integration in order. Today the pieces are scattered over the embedding guide, the tenancy concept page, the example READMEs and code comments, and some of them contradict each other. Examples: the embedding guide says keys are "minted at the store layer", although the operator tenant API exists; it says the SDK is not on npm while the SDK README says `npm install`; and it says the Data table node will arrive "once it lands", although it exists.

# Acceptance Criteria
- [ ] One page covers, in order: provisioning tenants from the host control plane; storing and rotating tenant keys; mapping host roles to scopes; building the list and create UI with the tenant key; minting embed sessions and seeding confinement; proxying activation; provisioning credentials; dispatching host events into workflows; idempotency; tenant deletion; observability
- [ ] A recipe for a workflow used as a synchronous tool by the host's own AI agent: Webhook + Respond to Webhook, `webhook.response_timeout`, idempotency, and schema ownership
- [ ] Every command and snippet on the page is verified in CI, or taken from the tested reference host
- [ ] The stale embedding-guide statements are corrected (the store-layer key, the npm status, "once it lands")

# Implementation Plan

# Notes

Source: the 2026-09-23 host-SaaS integration review (gaps D1 and D8). Product gaps are in EPIC-7c3ry9.

# Related Files

# Attachments
