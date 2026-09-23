---
id: FEAT-mq412g
title: Importer resolves community node types to installed sidecar/pack nodes
status: todo
priority: medium
labels:
    - n8n
    - importer
    - community-nodes
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

Third-party nodes appear in 15.6% of the newest templates; Apify alone is in 6.4%. Even with the npm package installed in the sidecar, the import still yields a placeholder.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-20). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A workflow built on a community node references it as `<npm package>.<node name>`. Third-party nodes appear in 9.0% of templates overall and **15.6% of the newest**. `@apify/n8n-nodes-apify.apify` alone appears in 32/998 (6.4% of the newest) and is unlock #12.

# Steps to Reproduce

1. Import Manual → Apify, using the real instance from template 18849 (`@apify/n8n-nodes-apify.apify`), workflow wf_01a0cbc7-e454-79fa-8fc7-ffe8ce27161f.
2. Read `internal/interop/n8n/n8n.go:706-713` (`byN8NType` consults only the static `mappings` table).
3. Read `internal/sidecarnode/convert.go:201`.

# Expected

When the tenant's catalogue contains a sidecar or pack node whose source package and node name match the n8n type, the importer maps onto it (version-checked), and otherwise names the package to install.

# Actual

- The import yields a blocking placeholder.
- Sidecar nodes register as `sidecar.<slug(package)>.<slug(node)>` and packs as `pack.<name>`. The importer never looks them up.
- Even an operator who installs `@apify/n8n-nodes-apify` in the sidecar cannot import a workflow that uses it without a code change adding a mapping entry. FEAT-ed6wdy chose namespaced types and "requires a mapping entry".

# Acceptance Criteria
- [ ] Sidecar and pack definitions record their source npm package and node name
- [ ] `byN8NType` consults the tenant catalogue before falling back to a placeholder
- [ ] When the package is missing, the placeholder names it ("install npm package X in the sidecar")

# Implementation Plan

Record the source `package` and `node name` on sidecar and pack definitions, and consult the tenant catalogue in `byN8NType` before falling back to the placeholder. The placeholder reason should say "install npm package X in the sidecar".

# Notes

Related tickets: FEAT-7cg0cd, FEAT-ed6wdy

Related (from the audit): FEAT-7cg0cd, FEAT-ed6wdy (both done; neither covers import resolution)

# Related Files

`results-extra.json` (Apify community), `internal/interop/n8n/n8n.go:803-816`, `internal/sidecarnode/convert.go:201`, `.pine/tickets/FEAT-ed6wdy.md:52`

# Attachments
