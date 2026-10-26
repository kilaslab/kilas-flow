---
id: FEAT-pt6ge9
title: Dynamic option loading (`loadOptions`) in node packs
status: done
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
updated: "2026-10-25T16:39:43Z"
---

# Description

The host's WhatsApp/CRM actions need pickers filled from the host API: channels, agents, tags, team members and message templates. Packs support only static options; the only internal loader is the operation cascade (`internal/nodepack/nodepack.go:255-290`, `internal/property/property.go:298-318`). Free-text ID fields were the top usability blocker in the host's own engine.

# Acceptance Criteria
- [ ] A parameter may declare `typeOptions.loadOptions: { request: {method, url, qs}, output: { rootProperty, value, label, description? }, dependsOn?: [keys] }`.
- [x] The request runs with the node's credential under the egress policy and, in an embed, is bounded to the session's workflow (existing `/node-types/{type}/load-options` behaviour; the pack loader only joins it).
- [x] It works for `options`, `multiOptions` and `resourceLocator` list mode.
- [x] Errors show inline (the editor's loader-aware controls render the endpoint's `reason`; no web change needed).
- [x] `nodepackgen` validates the block (parameterKeys += loadOptions/modes with strict-decode reports; the loader rules run through ValidateBytes → Load).

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

**Premise wrong, gap real.** An HTTP options loader already exists: `property.OptionsLoader` with `Source: "http"` (Method, Endpoint, BaseURLParameter, CredentialType, ItemsPath, LabelTemplate, ValueField, DependsOn) at internal/property/loader.go:20-66, resolved at internal/loadoptions/loadoptions.go:167-200 and used by built-in nodes (nodes/ai.go:248, :368). Other internal loaders exist too (loadoptions/datastores.go, schema.go).
- The AC's `typeOptions.loadOptions` placement conflicts with the model: `LoadOptions` sits beside TypeOptions on `PropertyDefinition` (property.go:419-422), `resourceLocator` list mode has its own (property.go:87-88), and pack TypeOptions rejects unknown keys (internal/nodepack/validate.go:417-420).
- Real work: let pack parameters carry the existing `loadOptions` (allowlists at validate.go:308 and convert.go:90 pass only `typeOptions`), then validate it in nodepackgen. Rewrite the first AC against `OptionsLoader` before starting.
- Refs: nodepack.go:268-274 is the cascade loader; property.go:298-318 is now the TypeOptions struct.

## Fix

Audit follow-ups held: `Parameter` now carries the existing `property.OptionsLoader` (and `modes`, so a pack can declare a `resourceLocator` at all — the manifest had no words for locator modes either), and `definition()` maps both into the definition the catalogue serves. The editor side needed nothing: property-field.svelte already reads `property.loadOptions` and `locatorMode.loadOptions`, and the load-options endpoint already takes the loader from the registered definition, bounds internal lookups by an embed session's workflow, and runs the request under the egress policy with the node's credential.

- Pack loader rules (checked at load, reported by `nodepackgen validate` with file and path): only `options`/`multiOptions` properties load (a locator declares its loader on the list mode); a pack's loader must be an `http` source — the internal registry is the server's own; `ValidateLoader`/`ValidateModes` apply as everywhere else; a loader naming a `credentialType` must name one the pack requires (the only types a binding carries — same rule a `secretCredential` answers to).
- `validate.go` parameterKeys gained `loadOptions` and `modes` with strict-decode reports for both blobs.
- The n8n transcription path (`DeclarativeProperty`) carries a declarative `loadOptions`/`modes` instead of dropping them; an n8n `loadOptionsMethod` (a function name) remains outside that vocabulary.
- Docs: node-authoring "Loading options from the customer's service" (block fields, locator modes, inline errors, internal-source refusal), node-packs reference parameter list.

Tests: internal/nodepack/loadoptions_test.go — a host-API-shaped pack (options + multiOptions + locator with typed and list modes) validates and loads; ten refusal cases through ValidateBytes; and the definition-produced loader resolved end-to-end through loadoptions.Resolver against a stub service with the credential applied and labels rendered from `labelTemplate`.

# Related Files

# Attachments
