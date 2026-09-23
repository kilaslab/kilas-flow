---
id: FEAT-2m4yvz
title: Generated node catalogue and credential-type reference (one page per node type, searchable by n8n name)
status: todo
priority: low
labels:
    - docs
    - reference
    - generated
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

The catalogue has 68 entries and no docs pages; only the 5 pack entries carry a `documentationUrl`. The credentials page undercounts types (13 vs 15).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-9, DOC-22). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-9: No per-node reference: the catalogue has 68 node entries and no docs pages; only the 5 pack entries carry a documentationUrl

*gap · high · node-catalog*

**n8n:** docs.n8n.io has an integrations page per node with operations, parameters, credentials and examples.

**Steps to reproduce:**

1. `curl -s :18080/api/v1/node-types | python3 …`: 68 entries (58 built-in types and 3 pack types). Only 5 have `documentationUrl`, and those are the packs pointing at external sites.
2. `ls docs/src/content/docs` has no node catalogue section. `guides/n8n-migration.md` has a mapping table, but it lists imports, not parameters.

**Actual:**

A workflow author or integrator cannot look up what a node's parameters, outputs or credential types are without opening the editor. There is also nothing to link from the editor's node panel.

**Expected:**

A generated "Nodes" reference with one page per type and version (parameters from the definition, ports, credential types, the n8n type it maps from, and availability), linked from each node's `documentationUrl`.

**Suggested fix:**

Generate the pages from `GET /node-types` in the same way `generate-api-reference.mjs` works, with a drift gate. Set `documentationUrl` on built-ins to the generated pages.

**Evidence:**

the steps above.

**Related:**

none


## DOC-22: The credentials page undercounts types (13 vs 15) and placements (5 vs 6)

*docs · low · credentials*

**Steps to reproduce:**

1. `concepts/credentials.md:161-178` says "Thirteen types ship". `curl :18080/api/v1/credential-types` returns 15; `gmailOAuth2` and `googleDriveOAuth2Api` are missing from the table.
2. `concepts/credentials.md:123-131` says "The five placements". `internal/credentials/registry.go:42-48` also defines `custom` (used by `httpCustomAuth`).

**Actual:**

The page is stale.

**Expected:**

The counts match, and ideally the table is generated from `GET /credential-types`.

**Suggested fix:**

Generate the table (see the reference ticket).

**Evidence:**

the steps above.

**Related:**

BUG-b4cb1c (drift gate for counts)


# Acceptance Criteria
- [ ] A generated page per node type and version from `/node-types` (parameters, ports, credentials, n8n equivalent)
- [ ] A generated credential-types page
- [ ] `documentationUrl` is wired, so the editor links to the node's page
- [ ] A CI drift gate compares the pages with the live catalogue

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-b4cb1c

# Related Files

# Attachments
