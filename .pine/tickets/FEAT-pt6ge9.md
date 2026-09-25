---
id: FEAT-pt6ge9
title: Dynamic option loading (`loadOptions`) in node packs
status: todo
priority: high
labels:
    - saas
    - packs
    - loadoptions
deps:
    - FEAT-n12211
    - FEAT-r267jj
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

The host's WhatsApp/CRM actions need pickers filled from the host API: channels, agents, tags, team members and message templates. Packs support only static options; the only internal loader is the operation cascade (`internal/nodepack/nodepack.go:255-290`, `internal/property/property.go:298-318`). Free-text ID fields were the top usability blocker in the host's own engine.

# Acceptance Criteria
- [ ] A parameter may declare `typeOptions.loadOptions: { request: {method, url, qs}, output: { rootProperty, value, label, description? }, dependsOn?: [keys] }`.
- [ ] The request runs with the node's credential under the egress policy and, in an embed, is bounded to the session's workflow.
- [ ] It works for `options`, `multiOptions` and `resourceLocator` list mode.
- [ ] Errors show inline.
- [ ] `nodepackgen` validates the block.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

**Premise wrong, gap real.** An HTTP options loader already exists: `property.OptionsLoader` with `Source: "http"` (Method, Endpoint, BaseURLParameter, CredentialType, ItemsPath, LabelTemplate, ValueField, DependsOn) at internal/property/loader.go:20-66, resolved at internal/loadoptions/loadoptions.go:167-200 and used by built-in nodes (nodes/ai.go:248, :368). Other internal loaders exist too (loadoptions/datastores.go, schema.go).
- The AC's `typeOptions.loadOptions` placement conflicts with the model: `LoadOptions` sits beside TypeOptions on `PropertyDefinition` (property.go:419-422), `resourceLocator` list mode has its own (property.go:87-88), and pack TypeOptions rejects unknown keys (internal/nodepack/validate.go:417-420).
- Real work: let pack parameters carry the existing `loadOptions` (allowlists at validate.go:308 and convert.go:90 pass only `typeOptions`), then validate it in nodepackgen. Rewrite the first AC against `OptionsLoader` before starting.
- Refs: nodepack.go:268-274 is the cascade loader; property.go:298-318 is now the TypeOptions struct.

# Related Files

# Attachments
