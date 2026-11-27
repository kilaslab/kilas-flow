---
id: FEAT-1mxtsn
title: 'Integrator-first information architecture and landing page (three tracks: Embed, API, Build nodes)'
status: done
priority: high
labels:
    - docs
    - ia
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-11-27T10:17:04Z"
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
- [x] The sidebar matches the IA in EPIC-62zt4j with no orphan pages
  - Evidence: `docs/astro.config.mjs` now has a hand-written sidebar (Start; Embed in your SaaS; Integrate via API; Build nodes for your API; Workflows; Operate; Reference; About these docs) and `assertNoOrphanPages`, which fails `astro build` when a page is in no group. IA entries for pages that do not exist yet (quickstart, how it fits, glossary, ...) wait for their own tickets.
- [x] The landing page has three tracks (Embed in your SaaS / Integrate via API / Build nodes for your API), each one click from its quickstart
  - Evidence: `docs/src/content/docs/index.mdx`. The Embed track starts at the SaaS integration playbook (FEAT-n010f0), because the 15-minute embed quickstart is not written; the API track starts at "Trigger workflows from your app" (FEAT-prw1hw); the nodes track starts at "Build nodes for your API".
- [x] No page says "mostly empty" or quotes a hand-written count that the product can contradict
  - Evidence: the landing, 404 and contributing pages are rewritten; the node-registry table's "46 types today" and "3 types today" are gone. The remaining counts are between `generated:` markers.
- [x] Contributor and internal pages (acceptance capstone, contributing) move to About
  - Evidence: both are in the "About these docs" group. The files did not move, so their URLs still work and no redirect is needed.
- [x] The stale statements from DOC-23 are corrected
  - Done: node-registry counts, the GOWA pack line in the architecture tree, and the n8n scoreboard now names the corpus it measures. Already fixed on main before this ticket: the `deployment.md` duplicate paragraph and the `security.md` heading.
  - Done 2026-11-27: the `Database.TablePrefix` comment now says "at most 16 characters (MaxTablePrefixLength)", regenerated into configuration-reference.md and config.example.yaml. Previously remaining: `operate/configuration-reference.md:129` said "no longer than MaxTablePrefixLength". That page is generated from the Go comment on `Database.TablePrefix` in `internal/config/config.go`, so the fix is a code comment edit plus `make generate-config-reference` (the value is 16, as `operate/deploy-for-saas.md` says).

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-b4cb1c, FEAT-nxxbs5

# Related Files

# Attachments

## Progress (2026-11-27)

- Sidebar regrouped by what an integrator is doing. Pages kept their URLs (nothing moved), so there are no redirects to maintain and CI link validation still passes. `assertNoOrphanPages` in `docs/astro.config.mjs` fails the build on a page in no group; `contributing.md` tells authors to add new pages to the sidebar.
- `index.mdx` leads with the three tracks (three columns on a wide screen, `docs/src/styles/kilasflow.css`), then a short "Not an integrator?" list and a "What is not built yet" paragraph. It quotes no counts. The hero action stays an external link, as the file's comment requires.
- `404.md` and `contributing.md` lost the stub text. `llms.txt` promotes the three tracks' entry pages and no longer says how many skills the bundle holds.
- Left open: DOC-23 item 6 (generated config reference, needs a Go comment change). `start/first-workflow.md` is still a stub and the landing page does not promote it.

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (5):
  - `10693549` — merge: integrator docs IA, events in and out, SaaS playbook (FEAT-1mxtsn, FEAT-prw1hw, FEAT-n010f0)
  - `7dd4beda` — chore(pine): progress notes and acceptance evidence for FEAT-1mxtsn, FEAT-prw1hw, FEAT-n010f0
  - `413c0e00` — FEAT-1mxtsn: lay the three tracks out in one row and lift them above the fold
  - `fc49fd08` — FEAT-1mxtsn: three-track landing page for integrators
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   11 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1668 files changed, 280164 insertions(+), 8130 deletions(-)
```
