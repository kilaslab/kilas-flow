---
id: FEAT-1mxtsn
title: 'Integrator-first information architecture and landing page (three tracks: Embed, API, Build nodes)'
status: todo
priority: high
labels:
    - docs
    - ia
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

The landing page is aimed at evaluators and is stale ("thirty-five operations"; there are 81; "mostly empty"). Contributor-only pages sit in the integrator navigation.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-15, DOC-23). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-15: The landing page, 404 page and contributing page describe a site that is "mostly empty" and an API of "Thirty-five operations"

*docs · medium · docs-site*

**Steps to reproduce:**

1. `index.mdx:35-38`: "Thirty-five operations under `/api/v1`". The live `/api/openapi.json` has 81, and `reference/api.md` says 81.
2. `index.mdx:51-58`: "This site is new and mostly empty … Most sections currently hold a stub". `404.md:13-14` and `contributing.md:36-38` say the same. Only `start/first-workflow.md` is a stub today; the site has 50 pages (14 of them generated API pages) and about 12k lines.
3. The landing page is aimed at evaluators. It has no route to "embed in your SaaS", "build nodes" or "API", and its only hero action is GitHub.

**Actual:**

A cold integrator's first impression is "unfinished and unreliable".

**Expected:**

An integrator-first landing page with the three tracks (Embed, Integrate via API, Build nodes) and accurate numbers.

**Suggested fix:**

Rewrite the landing and 404 pages, drop the stub disclaimers, and let the IA ticket own the rest.

**Evidence:**

the file and line references above.

**Related:**

FEAT-nxxbs5 (done)


## DOC-23: Assorted stale or contradictory statements across concept and operate pages

*docs · low · docs-site*

**Steps to reproduce:**

1. `concepts/node-registry.md:13-15` says "58 types … compiled into the binary", and line 227 says `builtin` has "46 types today". Live: 58.
2. `concepts/architecture.md:220`: "WAHA and GOWA (generated)". `packs/gowa/README.md` says "There is no generated pack JSON here yet".
3. `operate/deployment.md:190-207` states the Code-node toolchain paragraph twice (a merge leftover).
4. `operate/security.md:8` has the heading "The API is unauthenticated", when authentication is implemented but off by default.
5. `guides/n8n-migration.md` scoreboard: "Measured 5 September 2026, over 39 fixtures". FEAT-274c4p cites a different baseline (2/45).
6. `operate/configuration-reference.md:120-122` says "no longer than MaxTablePrefixLength", a Go identifier with no value.

**Actual:**

Small inconsistencies erode trust in otherwise careful pages.

**Expected:**

Consistent, current statements.

**Suggested fix:**

Fix each one. Add a counts drift gate.

**Evidence:**

the file and line references above.

**Related:**

BUG-b4cb1c


# Acceptance Criteria
- [ ] The sidebar matches the IA in EPIC-62zt4j with no orphan pages
- [ ] The landing page has three tracks (Embed in your SaaS / Integrate via API / Build nodes for your API), each one click from its quickstart
- [ ] No page says "mostly empty" or quotes a hand-written count that the product can contradict
- [ ] Contributor and internal pages (acceptance capstone, contributing) move to About
- [ ] The stale statements from DOC-23 are corrected

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-b4cb1c, FEAT-nxxbs5

# Related Files

# Attachments
