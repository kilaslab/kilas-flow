---
id: BUG-6gkd12
title: 'Round-trip fidelity: node notes, settings, tags dropped; versions shifted (If downgraded); get→validate not symmetric'
status: done
priority: low
labels:
    - importer
    - round-trip
deps:
    - FEAT-e898nz
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-10-09T12:14:05Z"
---

# Description

Export must re-open cleanly in n8n, and the CLI's get → edit → validate loop must accept its own output.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-12, TPL-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## TPL-12: The round trip also drops node notes and workflow settings, and shifts node versions (If is downgraded)

*gap · low · importer*

**n8n:** Node `notes`/`notesInFlow`, workflow `settings` (including `executionOrder`) and `typeVersion` are part of the workflow JSON.

**Steps to reproduce:**

`python3 agents/n8n-templates/roundtrip.py 1954 1750 5170 2462 1747 2753`, then diff `roundtrip/<id>.diff.json`.

**Actual:**

- No node or edge was lost, and placeholders and Code nodes came back unchanged. But:
  - All 24 node notes in 5170 are gone.
  - Every credential reference is stripped.
  - Workflow `settings` (`{"executionOrder":"v1"}` in 2753), `meta`, `pinData` and `tags` are not exported.
  - Versions change: Agent 1.7/1.8 → 3.1, Webhook 1 → 2, Merge 2 → 3, Code 1 → 2, Telegram 1.1 → 1.2, and If 2.2/2.3 → 2 (a downgrade).
- Each version change is listed as lossy.

**Expected:**

Notes and settings round-trip, and a node's authored version is kept when the translator can write it.

**Suggested fix:**

Add a notes field to the canonical node (as planned in BUG-gaavr5), export `settings.executionOrder: "v1"`, and pin the If export to the source version when it is ≥ 2.2.

**Evidence:**

`roundtrip/summary.json`, `roundtrip/5170.diff.json`, `roundtrip/2753.diff.json`, `roundtrip/2462.diff.json`, `internal/interop/n8n/n8n.go:1941` (notes dropped)

**Related:**

BUG-gaavr5 (done). "Per-node notes are discarded" is still unchecked and still reproduces.


## TPL-15: `workflow get` returns a document that `workflow validate` refuses ("unexpected property body.id")

*ux · low · onboarding*

**n8n:** Not applicable.

**Steps to reproduce:**

1. `kilasflow workflow get <id> --url …`, then save `.data.latestVersion.document` to `doc.json`.
2. `kilasflow workflow validate --file doc.json --url …`.
3. I reproduced it on the workflows for 2384 and 1750.

**Actual:**

`{"ok":false,"error":{"code":"bad_request","message":"validation failed","status":422,…"errors":[{"message":"unexpected property","location":"body.id",…`

**Expected:**

The document the API returns validates as-is, because get → edit → validate is the agent loop the CLI is built for. Validate could ignore `id`, or `get` could offer a `--document` form without it.

**Suggested fix:**

Accept and ignore `id` in `WorkflowDocumentInput`, or strip it client-side in `workflow validate`.

**Evidence:**

`agents/n8n-templates/doc_2384.json`, `doc_1750.json`; `WorkflowDocumentInput` in `/api/openapi.json` has `additionalProperties: false` and no `id`.

**Related:**

none


# Acceptance Criteria
- [x] Node `notes`/`notesInFlow` are part of the canonical node and round-trip
- [x] Workflow `settings.executionOrder` is exported; tags round-trip (executionOrder: done 2026-10-08; tags: done 2026-10-09 by FEAT-e898nz, `TestAWorkflowsTagsRoundTrip` in internal/interop/n8n/tags_test.go and the API export check in `TestAnImportedN8nWorkflowKeepsItsTagsAndExportsThem`)
- [x] A node's authored `typeVersion` is kept when the translator can write it (If ≥ 2.2 is not downgraded)
- [x] The document returned by `workflow get` passes `workflow validate` unchanged (`id` is accepted and ignored)

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-gaavr5

# Related Files

# Attachments

## Progress (2026-10-08)

Done, TPL-15: the CLI's workflow verbs (`workflow validate`, and `create`/`update` from a file) drop a document's top-level `id` before sending it (`readWorkflowDocument` in internal/cli/verbs_workflow.go). The document `workflow get` prints therefore validates and saves unchanged. A document without an id is still sent byte for byte. Test: `TestWorkflowValidateSendsADocumentReadBackFromGetWithoutItsID`, which failed first.

The API still refuses `id` on purpose: `TestWorkflowAPIOpenAPIDocumentsLifecycleResponseStatuses` pins that the write schema never exposes the server-owned id. A first attempt that accepted and ignored `id` server-side broke that guard and was reverted.

Open, TPL-12: node `notes`/`notesInFlow` in the canonical node, export of `settings.executionOrder` and tags, and keeping an authored `typeVersion` (If ≥ 2.2) on export. The status stays todo for these.

## Progress (2026-10-08, TPL-12 part)

Done:

- Every n8n export now writes `settings.executionOrder: "v1"`, because that is the order KilasFlow runs every workflow in. Without the key, n8n opens the file as a legacy workflow and runs it in its v0 order. The settings the import carries (the timezone, `executionTimeout` and `errorWorkflow`) still go back out beside it. See `exportSettings` in internal/interop/n8n/n8n.go. On import, `executionOrder` is still reported as an uncarried setting, which is what `TestAnImportedWorkflowKeepsItsTimezone` pins.
- An If node now goes back at its authored version for 2, 2.1, 2.2 and 2.3. The If mapping now has `publishedVersions`, the mechanism the exporter's `pinnedVersion` already used for other nodes. Every 2.x version reads the filter shape the translator writes, and the filter's own `options` (version and typeValidation) were already carried. v1 is a different shape that the import rewrites, so it still goes back at 2. No lossy `typeVersion` entry is reported for 2.x any more.
- Tests: `TestAnExportedWorkflowDeclaresTheExecutionOrderItRunsIn` and `TestAnIfNodeGoesBackAtTheVersionItWasAuthoredAt` in internal/interop/n8n/roundtrip_fidelity_test.go. Both failed before the fix.

Left:

- Tags. `workflow.Document` has no field for tags, and the importer's n8n `Document` does not read `tags` at all, so there is nothing to export. Carrying tags needs a canonical field, which is a schema change.
- Node `notes`/`notesInFlow`. `workflow.Node` has no field for them, and adding one is a schema change (schemas/workflow-v1.schema.json, SDK and web models) that did not fit this time box. The import still reports `notes` as dropped.
- The other version shifts the audit listed (Agent 1.7/1.8 → 3.1, Webhook 1 → 2, Merge 2 → 3, Code 1 → 2, Telegram 1.1 → 1.2) are a different case. Those translators write the newer version's parameter shape, so the change is reported as lossy, and that is correct. Keeping them would take a translator per older shape.

Notes (2026-10-08): a node's `notes` and `notesInFlow` are carried in the canonical node's `settings` (`withNotes` on import, `applyNotes` on export, internal/interop/n8n/n8n.go), beside its error policy. This needs no schema change, and the editor keeps unknown setting keys when it saves. The import no longer reports the note as dropped. `TestANodesNoteRoundTrips` failed first. `TestImportReportsEveryDroppedElement` now names `webhookId` as its node-level dropped element. Still open: tags (the canonical document has no field for them) and keeping the authored versions of the other nodes the audit lists.

Tags (2026-10-08, 23:58): the repository, workflow and API layers have no tags field anywhere, so n8n `tags` have nowhere to be carried. That needs a stored workflow field first, which is FEAT-e898nz; this ticket now depends on it.


## Progress (2026-10-09)

Done, the last open criterion (tags round-trip): the canonical document now has a `tags` field (FEAT-e898nz), the importer reads each n8n `tags` entry's `name` in file order (the id and timestamps are instance-local and are ignored), and the exporter writes `"tags":[{"name":...}]` in document order with no invented ids, leaving the key out when there are none. `TestAWorkflowsTagsRoundTrip` imports a file whose tags carry ids and timestamps, exports it, and imports the export again; the names, order and case come back unchanged and nothing is reported about tags. Every criterion of this ticket is now ticked; it can move to done once FEAT-e898nz does.

What was checked about n8n's side (behaviour only, in my own words, recorded in full on FEAT-e898nz): a tag entry with only a name, no id, imports cleanly, because n8n matches or creates tags by name and generates the id itself; and a tag name is limited to 24 characters, counted in UTF-16 code units.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (10):
  - `54f82333` — merge: a workflow carries tags that the list shows and filters, the CLI and MCP filter by, and n8n import and export round-trip (FEAT-e898nz, BUG-6gkd12)
  - `3c1645e2` — chore(pine): FEAT-e898nz and BUG-6gkd12 record what tags did, the n8n facts checked and the verification run
  - `ce0645fb` — chore(pine): the 2026-10-09 parallel wave moves its tickets to doing
  - `f0a3f9eb` — chore(pine): BUG-6gkd12's tags wait on a workflow tags field, ticketed as its own feature
  - `2d74f979` — BUG-6gkd12: a node's n8n note round-trips, carried in its settings, instead of being dropped on import
  - `ea0f1c4e` — merge: an n8n export declares executionOrder v1 and keeps an If node's authored 2.x version (BUG-6gkd12 partial)
  - `271aac73` — BUG-6gkd12: an n8n export declares executionOrder v1, and an If node goes back at its authored 2.x version instead of being downgraded to 2
  - `5f2cb87a` — BUG-6gkd12: the CLI's workflow verbs drop a document's server-owned id, so what workflow get prints validates and saves unchanged
  - `c3c870a4` — BUG-6gkd12: the document workflow get returns validates and saves unchanged; its id is accepted and ignored
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |    8 +-
 .pine/memory/persistence.md                        |    3 +-
 .pine/memory/testing.md                            |    3 +-
 .pine/memory/web-editor.md                         |    4 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |  464 +++
 .pine/tickets/BUG-0grt9g.md                        |  427 +++
 .pine/tickets/BUG-0xv7bg.md                        |   68 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  198 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   83 +
 .pine/tickets/BUG-2n4rfz.md                        |   58 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   51 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  187 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   70 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-49vf3j.md                        |  428 +++
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   88 +
 .pine/tickets/BUG-5bgx5c.md                        |   81 +
 .pine/tickets/BUG-5dn8hr.md                        |   39 +
 .pine/tickets/BUG-5fhcx7.md                        |   50 +
 .pine/tickets/BUG-5xkexq.md                        |   63 +
 .pine/tickets/BUG-605n21.md                        |  154 +
 .pine/tickets/BUG-66fhea.md                        |  112 +
 .pine/tickets/BUG-6d6wbg.md                        |  115 +
 .pine/tickets/BUG-6gkd12.md                        |  142 +
 .pine/tickets/BUG-719gaz.md                        |  117 +
 .pine/tickets/BUG-9296bf.md                        |  532 +++
 .pine/tickets/BUG-9dw5me.md                        |   66 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        |   80 +
 .pine/tickets/BUG-a7p6c8.md                        |  487 +++
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-argnka.md                        |   34 +
 .pine/tickets/BUG-asdh5q.md                        |  607 +++
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        |  141 +
 .pine/tickets/BUG-b8bwhw.md                        |   67 +
 .pine/tickets/BUG-bcahaj.md                        |  113 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   60 +
 .pine/tickets/BUG-c73h98.md                        |   51 +
 .pine/tickets/BUG-c7s5ss.md                        |   33 +
 .pine/tickets/BUG-cmnsfz.md                        |  457 +++
 .pine/tickets/BUG-cnhq1f.md                        |   54 +
 .pine/tickets/BUG-cqbq25.md                        |   41 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   69 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-e8ytyq.md                        |   58 +
 .pine/tickets/BUG-ecbq28.md                        |  132 +
 .pine/tickets/BUG-epy2se.md                        |  131 +
 .pine/tickets/BUG-fthahg.md                        |  244 ++
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-g9zf51.md                        |  480 +++
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hmp85t.md                        |  116 +
 .pine/tickets/BUG-hnvn3r.md                        |   55 +
 .pine/tickets/BUG-j7qrp2.md                        |   71 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |  527 +++
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kkkrmx.md                        |   35 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   63 +
 .pine/tickets/BUG-n4e97f.md                        |   51 +
 .pine/tickets/BUG-n6p7qy.md                        |  145 +
 .pine/tickets/BUG-n9a6bz.md                        |   67 +
 .pine/tickets/BUG-namghh.md                        |  118 +
 .pine/tickets/BUG-nbymq4.md                        |   82 +
 .pine/tickets/BUG-ngt25j.md                        |  739 ++++
 .pine/tickets/BUG-nn74ph.md                        |   60 +
 .pine/tickets/BUG-nzy3pa.md                        |  228 ++
 .pine/tickets/BUG-p334yw.md                        |   65 +
 .pine/tickets/BUG-p3j233.md                        |   71 +
 .pine/tickets/BUG-pder07.md                        |  430 +++
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   67 +
 .pine/tickets/BUG-ppvyzr.md                        |   65 +
 .pine/tickets/BUG-pzkpfr.md                        |   76 +
 .pine/tickets/BUG-q6b75c.md                        |   70 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  158 +
 .pine/tickets/BUG-rh7mpa.md                        |   62 +
 .pine/tickets/BUG-rs0xq1.md                        |   52 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   68 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |  107 +
 .pine/tickets/BUG-t9e3k5.md                        |   34 +
 .pine/tickets/BUG-tpyg0q.md                        |   65 +
 .pine/tickets/BUG-txafja.md                        |  762 ++++
 .pine/tickets/BUG-v8ksv8.md                        |   79 +
 .pine/tickets/BUG-vbw239.md                        |   31 +
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w9k234.md                        |   61 +
 .pine/tickets/BUG-x28fsx.md                        |  160 +
 .pine/tickets/BUG-x2sxzt.md                        |  525 +++
 .pine/tickets/BUG-x6gyc1.md                        |   77 +
 .pine/tickets/BUG-x94b3d.md                        |   46 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-xkz7qx.md                        |  499 +++
 .pine/tickets/BUG-xpr3jj.md                        |  416 +++
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        |   38 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   66 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-brpz48.md                       |   77 +
 .pine/tickets/EPIC-hkypt5.md                       |    8 +-
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 1234 +++++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   43 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   38 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   40 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   39 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3taswf.md                       |   16 +-
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |   35 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fhj6p.md                       |    2 +-
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   38 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   40 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   39 +
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   37 +
 .pine/tickets/FEAT-9ep5pw.md                       |   38 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cf5n4j.md                       |   21 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-e898nz.md                       |  210 ++
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gcq50s.md                       |    8 +-
 .pine/tickets/FEAT-gzd32h.md                       |   36 +
 .pine/tickets/FEAT-hxztwz.md                       |   45 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jrrqs2.md                       |   21 +
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   44 +
 .pine/tickets/FEAT-mh4e8g.md                       |   38 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   39 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   40 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   44 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qae4sh.md                       |   23 +
 .pine/tickets/FEAT-qdwm1k.md                       |   50 +
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   41 +
 .pine/tickets/FEAT-r87gtj.md                       |   26 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   42 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   36 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   37 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   37 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   38 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       |   99 +
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  448 ++-
 CONTRIBUTING.md                                    |   22 +
 Makefile                                           |   35 +
 README.md                                          |  450 +--
 cmd/kilasflow/boot_logger_test.go                  |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |  116 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  181 +-
 cmd/kilasflow/sqlite_files_test.go                 |   50 +
 config.example.yaml                                |  102 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/credentials.md      |  295 +-
 docs/src/content/docs/concepts/execution-model.md  |   52 +-
 docs/src/content/docs/concepts/expressions.md      |   20 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 docs/src/content/docs/concepts/node-registry.md    |   15 +-
 .../src/content/docs/concepts/safety-boundaries.md |  277 +-
 .../content/docs/concepts/tenancy-and-embedding.md |  100 +-
 docs/src/content/docs/concepts/webhooks.md         |  109 +-
 docs/src/content/docs/guides/code-javascript.md    |  426 +++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |  106 +-
 docs/src/content/docs/guides/n8n-migration.md      |  217 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  184 +-
 docs/src/content/docs/operate/configuration.md     |   16 +
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/security.md          |   47 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/reference/api-contract.md    |   25 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/credentials.md |    6 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |   11 +-
 docs/src/content/docs/reference/cli.md             |  154 +-
 .../content/docs/reference/expression-grammar.md   |   13 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |   17 +-
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |   12 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/datastore-pg.spec.ts                     |    4 +-
 e2e/tests/datastore.spec.ts                        |  115 +-
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/google-oauth.spec.ts                     |   28 +
 e2e/tests/js-code.spec.ts                          |  702 ++++
 e2e/tests/live-backend-api.spec.ts                 |   72 +-
 e2e/tests/n8n-compare.spec.ts                      |   33 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/node-coverage.spec.ts                    |   80 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/agent.go                               |   60 +-
 internal/ai/ai.go                                  |   30 +-
 internal/ai/ai_test.go                             |  131 +
 internal/ai/fromai.go                              |  131 +-
 internal/ai/fromai_test.go                         |  121 +-
 internal/ai/openai.go                              |  247 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  587 ++-
 internal/ai/outputschema_test.go                   |  229 ++
 internal/ai/reasoning_test.go                      |  245 ++
 internal/api/auth_test.go                          |   28 +
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/credential_confinement_test.go        |  273 ++
 internal/api/credential_testable_test.go           |   62 +
 internal/api/credentials_pagination_test.go        |   58 +
 internal/api/credentials_probe_scope_test.go       |  235 ++
 internal/api/credentials_test.go                   |   81 +-
 internal/api/credentials_update_test.go            |  206 ++
 internal/api/datastores_csv_test.go                |   34 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   45 +-
 internal/api/docs.go                               |   10 +-
 internal/api/embed_confinement_test.go             |    9 +-
 internal/api/embed_publish_test.go                 |  160 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/events_test.go                        |  104 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    6 +-
 internal/api/handlers/credentials.go               |  348 +-
 internal/api/handlers/credentials_probe_test.go    |   75 +
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/datastores_csv.go            |   27 +-
 internal/api/handlers/embed.go                     |    2 +-
 internal/api/handlers/embedscope.go                |  156 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |   59 +-
 internal/api/handlers/execution_waiting_test.go    |   82 +
 internal/api/handlers/executions.go                |  139 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/oauth.go                     |  199 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/system.go                    |   22 +-
 internal/api/handlers/workflow_validate.go         |   95 +-
 internal/api/handlers/workflow_validate_test.go    |  171 +
 internal/api/handlers/workflows.go                 |  107 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_publish_test.go      |   87 +
 internal/api/middleware/embed_sentences_test.go    |   24 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/middleware/scope.go                   |   48 +-
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/oauth_test.go                         |  364 +-
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/routes.go                             |   14 +-
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    8 +-
 internal/api/server_test.go                        |   40 +
 internal/api/workflow_tags_test.go                 |  342 ++
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  143 +-
 internal/cli/command_test.go                       |    4 +-
 internal/cli/docs_examples_test.go                 |   52 +
 internal/cli/document_flag_help_test.go            |   31 +
 internal/cli/exit_test.go                          |   26 +
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |  134 +-
 internal/cli/openapi_contract_test.go              |   43 +-
 internal/cli/skills_not_shipped_test.go            |  109 +
 internal/cli/sse.go                                |   27 +-
 internal/cli/verbs_api.go                          |   83 +-
 internal/cli/verbs_api_test.go                     |  324 +-
 internal/cli/verbs_credential.go                   |    4 +-
 internal/cli/verbs_credential_test.go              |   71 +
 internal/cli/verbs_datastore.go                    |    5 +-
 internal/cli/verbs_debug.go                        |   67 +-
 internal/cli/verbs_debug_test.go                   |   82 +
 internal/cli/verbs_exec.go                         |   16 +
 internal/cli/verbs_exec_test.go                    |   43 +
 internal/cli/verbs_skills.go                       |   94 +-
 internal/cli/verbs_skills_test.go                  |  119 +
 internal/cli/verbs_workflow.go                     |  110 +-
 internal/cli/verbs_workflow_tag_test.go            |   84 +
 internal/cli/verbs_workflow_test.go                |   59 +
 internal/config/boot_strictness_test.go            |  216 ++
 internal/config/config.go                          |  364 +-
 internal/config/config_test.go                     |  142 +
 internal/config/sqlite_root_test.go                |   51 +
 internal/credentials/builtin.go                    |   62 +-
 internal/credentials/credentials.go                |  143 +-
 internal/credentials/credentials_test.go           |  164 +-
 internal/credentials/external.go                   |   13 +-
 internal/credentials/oauth.go                      |  117 +-
 internal/credentials/oauth_redirect_test.go        |   41 +
 internal/credentials/oauth_test.go                 |   95 +-
 internal/credentials/probe_test.go                 |  181 +
 internal/credentials/registry.go                   |   99 +-
 internal/credentials/scope.go                      |  102 +
 internal/credentials/scope_test.go                 |  215 ++
 internal/credentials/scrub.go                      |  107 +
 internal/credentials/scrub_test.go                 |   61 +
 internal/credentials/vault.go                      |   10 +-
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    2 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   87 +-
 internal/embed/embed_publish_test.go               |  113 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  511 +++
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |  155 +-
 internal/engine/authenticate_test.go               |   74 +
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/batch_nodes_tolerance_test.go      |  108 +
 internal/engine/checkpoint.go                      |   94 +-
 internal/engine/checkpoint_test.go                 |  101 +
 internal/engine/console_test.go                    |  397 ++
 internal/engine/error_workflow_test.go             |  312 +-
 internal/engine/eval.go                            |  287 +-
 internal/engine/eval_test.go                       |  269 +-
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  131 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |  157 +
 internal/engine/node_branches.go                   |   87 +
 internal/engine/node_timing_test.go                |  211 ++
 internal/engine/per_item_suspend_test.go           |  438 +++
 internal/engine/required_inputs_test.go            |  198 +
 internal/engine/resume_failure_test.go             |  174 +
 internal/engine/run_presentation_test.go           |   77 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          | 1204 +++++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/secret_scrub.go                    |  159 +
 internal/engine/secret_scrub_test.go               |  134 +
 internal/engine/service.go                         |  216 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  259 +-
 internal/engine/wait_error_output_test.go          |  181 +
 internal/engine/wait_links_test.go                 |  159 +
 internal/engine/wait_service.go                    |   87 +-
 internal/engine/wait_service_test.go               |  641 +++-
 internal/engine/worker_test.go                     |    8 +
 internal/events/events.go                          |   37 +-
 internal/events/events_test.go                     |   68 +
 internal/execution/records.go                      |    7 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   38 +
 internal/expression/expression_test.go             |   66 +
 internal/expression/globals.go                     |   22 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/idempotency/once.go                       |   64 +
 internal/idempotency/once_test.go                  |   56 +
 internal/interop/n8n/chatmodel_import_test.go      |  332 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/connection_order_test.go      |   52 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/extract_import_test.go        |   83 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/importer_tail_test.go         |   64 +-
 internal/interop/n8n/legacy_agent_test.go          |  120 +
 internal/interop/n8n/loose.go                      |  252 ++
 internal/interop/n8n/loose_json_test.go            |  239 ++
 internal/interop/n8n/n8n.go                        |  538 ++-
 internal/interop/n8n/n8n_test.go                   |  588 ++-
 internal/interop/n8n/parameters.go                 |  938 ++++-
 internal/interop/n8n/rag_import_test.go            |  210 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |  145 +
 internal/interop/n8n/set_null_import_test.go       |  150 +
 internal/interop/n8n/switch_import_test.go         |  371 ++
 internal/interop/n8n/tags.go                       |  182 +
 internal/interop/n8n/tags_test.go                  |  312 ++
 internal/interop/n8n/testdata/n8n_rag_16706.json   |    2 +-
 internal/interop/n8n/testdata/n8n_rag_3647.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_4799.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_5908.json    |    2 +-
 internal/interop/n8n/tool_variant_import_test.go   |  111 +
 internal/interop/n8n/topology_import_test.go       |  147 +
 internal/interop/n8n/workflow_tool_self_test.go    |   93 +
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  330 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  230 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  295 ++
 internal/jsrun/js/runtime.js                       | 1726 +++++++++
 internal/jsrun/jsrun.go                            |  247 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  152 +
 internal/jsrun/roots_test.go                       |  584 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  304 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  250 ++
 internal/jsworker/doc.go                           |   78 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  952 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/loadoptions/loadoptions.go                |   14 +-
 internal/loadoptions/loadoptions_test.go           |   30 +
 internal/loadoptions/redirect_test.go              |   30 +
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   21 +-
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   60 +-
 internal/nodepack/trigger.go                       |   77 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/property/editor_test.go                   |   51 +
 internal/property/property.go                      |   52 +
 internal/repository/auth.go                        |    9 +-
 internal/repository/credentials.go                 |  137 +-
 internal/repository/credentials_page_test.go       |   69 +
 internal/repository/executions.go                  |  262 +-
 internal/repository/models.go                      |   11 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   22 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/waits.go                       |    4 +
 internal/repository/waits_test.go                  |   68 +
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 +
 internal/repository/workflow_tags_test.go          |  283 ++
 internal/repository/workflows.go                   |   44 +-
 internal/routing/executor.go                       |   13 +-
 internal/runcode/diagnostic_internal_test.go       |   38 +
 internal/runcode/runcode.go                        |   58 +-
 internal/runcode/runcode_test.go                   |   80 +
 internal/runcode/sandbox.go                        |   73 +
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/redact.go                        |   82 +
 internal/safehttp/redact_test.go                   |  108 +
 internal/safehttp/redirect_scope_test.go           |  141 +
 internal/safehttp/safehttp.go                      |   62 +-
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/skills/bundle_test.go                     |   35 +
 internal/skills/skill.go                           |   24 +
 internal/sqlbuild/sqlbuild_test.go                 |    5 +-
 internal/sqlnode/export_test.go                    |   32 +-
 internal/sqlnode/guard_test.go                     |    2 +-
 internal/sqlnode/internal_test.go                  |    1 +
 internal/sqlnode/sqlite.go                         |  302 ++
 internal/sqlnode/sqlite_bounded_test.go            |  242 ++
 internal/sqlnode/sqlite_confinement_test.go        |  300 ++
 internal/sqlnode/sqlite_unix_test.go               |  116 +
 internal/sqlnode/sqlnode.go                        |  100 +-
 internal/sqlnode/sqlnode_test.go                   |   37 +-
 internal/sqlnode/tls_test.go                       |  200 +
 internal/tenantpurge/harness_test.go               |   20 +-
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/wasmpack/hostcalls_http.go                |   24 +-
 internal/wasmpack/probe_test.go                    |    4 +-
 internal/web/embed.go                              |   43 +-
 internal/web/embed_test.go                         |   95 +
 internal/webhook/header_auth_redaction_test.go     |   72 +
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_redaction_test.go       |   74 +
 internal/webhook/lifecycle_scope_test.go           |  129 +
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  581 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 ++
 internal/webhook/secret_record_test.go             |   66 +
 internal/webhook/webhook.go                        |   69 +-
 internal/webhook/webhook_test.go                   |   86 +
 internal/workflow/compiler.go                      |  247 +-
 internal/workflow/compiler_test.go                 |   85 +
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |   18 +
 internal/workflow/document_test.go                 |   44 +
 internal/workflow/shape.go                         |   43 +
 internal/workflow/tags.go                          |  113 +
 internal/workflow/tags_test.go                     |  188 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 nodes/ai.go                                        |  705 +++-
 nodes/ai_fromai_data_test.go                       |  392 ++
 nodes/ai_scope_test.go                             |   52 +
 nodes/ai_test.go                                   |  418 ++-
 nodes/ai_tools_test.go                             |  334 ++
 nodes/annotation.go                                |    1 +
 nodes/batch_shape_test.go                          |  157 +
 nodes/chat.go                                      |    2 +-
 nodes/code.go                                      |   46 +-
 nodes/code_test.go                                 |   48 +
 nodes/core.go                                      |   25 +-
 nodes/credential_redaction_test.go                 |   76 +
 nodes/credential_redirect_test.go                  |  145 +
 nodes/credentialscope_test.go                      |  131 +
 nodes/database.go                                  |   14 +-
 nodes/database_test.go                             |   69 +-
 nodes/datastore.go                                 |  626 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_test.go                            |  127 +-
 nodes/datastore_tool_test.go                       | 1366 ++++++-
 nodes/embedscope.go                                |  141 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   36 +-
 nodes/executors_test.go                            |    2 +
 nodes/extract.go                                   |  250 +-
 nodes/extract_test.go                              |   90 +
 nodes/flow.go                                      |   21 +-
 nodes/glyphs_test.go                               |  150 +
 nodes/google.go                                    |    2 +-
 nodes/http.go                                      |   96 +-
 nodes/http_error_test.go                           |  235 ++
 nodes/item_inputs_test.go                          |  112 +
 nodes/jscode.go                                    |  319 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  207 ++
 nodes/jscode_roots.go                              |   49 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  361 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/model_redirect_internal_test.go              |  189 +
 nodes/mysql_v2.go                                  |    2 +-
 nodes/openai_credential_url_test.go                |   54 +
 nodes/pgvector.go                                  |   18 +-
 nodes/pgvector_customer.go                         |    4 +-
 nodes/pgvector_test.go                             |    4 +-
 nodes/postgres_v2.go                               |    2 +-
 nodes/rag_test.go                                  |    6 +-
 nodes/sidecar_egress.go                            |   28 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/sqlite_attach_test.go                        |    4 +-
 nodes/subworkflow.go                               |   47 +-
 nodes/telegram_download.go                         |   15 +-
 nodes/telegram_lifecycle.go                        |   92 +-
 nodes/testdata/n8n_chat_model_options.json         |    3 +-
 nodes/tool_fromai.go                               |  151 +
 nodes/transform.go                                 |  127 +-
 nodes/webhook.go                                   |    3 +
 nodes/workflow_tool_locator_test.go                |   50 +
 packs/telegram/default_base_url_test.go            |   32 +
 packs/telegram/redaction_test.go                   |   43 +
 packs/waha/waha_test.go                            |   55 +-
 schemas/workflow-v1.schema.json                    |   24 +-
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/generate-api-reference.mjs                 |   51 +-
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 scripts/smoke-cli.sh                               |   10 +-
 sdk/CHANGELOG.md                                   |    7 +
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |  500 ++-
 sdk/src/server.ts                                  |   20 +-
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |   21 +-
 skills/kilasflow-credentials/SKILL.md              |    6 +-
 .../references/CREDENTIAL_TYPES.md                 |   28 +-
 skills/kilasflow-datastore/SKILL.md                |    9 +-
 skills/kilasflow-datastore/references/FILTERS.md   |    6 +-
 skills/kilasflow-debugging/SKILL.md                |    8 +-
 skills/kilasflow-embedding/SKILL.md                |    9 +-
 .../references/EMBED_HANDSHAKE.md                  |    2 +
 .../references/SESSION_AUTHORITY.md                |    6 +-
 skills/kilasflow-error-handling/SKILL.md           |    6 +-
 skills/kilasflow-expressions/SKILL.md              |   12 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 skills/kilasflow-operations/SKILL.md               |    4 +-
 skills/kilasflow-triggers/SKILL.md                 |    4 +-
 skills/using-kilasflow-skills/SKILL.md             |    4 +-
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/auth.json                          |    2 +
 web/messages/en/credentials.json                   |    3 +
 web/messages/en/datastores.json                    |   48 +-
 web/messages/en/editor.json                        |   49 +-
 web/messages/en/executions.json                    |  193 +-
 web/messages/en/nav.json                           |    2 +-
 web/messages/en/properties.json                    |    9 +-
 web/messages/en/settings.json                      |    9 +-
 web/messages/en/workflows.json                     |   25 +-
 web/messages/id/auth.json                          |    2 +
 web/messages/id/credentials.json                   |    3 +
 web/messages/id/datastores.json                    |   48 +-
 web/messages/id/editor.json                        |   59 +-
 web/messages/id/executions.json                    |  193 +-
 web/messages/id/nav.json                           |    2 +-
 web/messages/id/properties.json                    |    9 +-
 web/messages/id/settings.json                      |    9 +-
 web/messages/id/workflows.json                     |   25 +-
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   52 +
 .../lib/api/generated/credentials/credentials.ts   |    6 +-
 web/src/lib/api/generated/executions/executions.ts |    4 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/credentialBody.ts |    4 +-
 .../api/generated/models/credentialBodyFields.ts   |    2 +-
 .../lib/api/generated/models/credentialResource.ts |    5 +-
 .../api/generated/models/credentialTypeResource.ts |    9 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |    2 +-
 .../generated/models/evalExpressionInputBody.ts    |   12 +-
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    3 +
 .../lib/api/generated/models/healthOutputBody.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   16 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../lib/api/generated/models/testPayloadBody.ts    |    4 +-
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../generated/models/validateWorkflowResource.ts   |    6 +
 .../lib/api/generated/models/validationWarning.ts  |   13 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 web/src/lib/api/http.ts                            |   32 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../workflow-editor/canvas-bridge.svelte           |    3 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../components/workflow-editor/canvas-node.svelte  |  110 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/code-editor.svelte  |   92 +
 .../workflow-editor}/diagnostics-section.svelte    |    0
 .../workflow-editor/execution-canvas-node.svelte   |   45 +-
 .../workflow-editor/execution-canvas.svelte        |    6 +-
 .../workflow-editor}/import-report.svelte          |    0
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../components/workflow-editor/node-picker.svelte  |   22 +-
 .../workflow-editor/panel-query-harness.svelte     |   19 +
 .../workflow-editor/paste-report-sheet.svelte      |   45 +
 .../workflow-editor/properties-panel.svelte        |  180 +-
 .../workflow-editor/properties-panel.test.ts       |  174 +
 .../workflow-editor/property-field.svelte          |   28 +-
 .../workflow-editor/property-field.test.ts         |   50 +
 .../workflow-editor/version-panel.svelte           |   15 +-
 .../workflow-editor/webhook-address.svelte         |   91 +
 .../workflow-editor/webhook-address.test.ts        |   63 +
 .../workflow-editor/workflow-editor.svelte         |  464 ++-
 web/src/lib/dashboard/auth-mode.test.ts            |   65 +
 web/src/lib/dashboard/auth-mode.ts                 |   79 +
 web/src/lib/dashboard/credential-scope.test.ts     |   34 +
 web/src/lib/dashboard/credential-scope.ts          |   31 +
 web/src/lib/dashboard/credential-test.test.ts      |   45 +
 web/src/lib/dashboard/credential-test.ts           |   27 +
 web/src/lib/dashboard/sidebar.test.ts              |   34 +
 web/src/lib/dashboard/sidebar.ts                   |   35 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 +-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/datastore/columns.test.ts              |   36 +
 web/src/lib/datastore/columns.ts                   |   30 +
 web/src/lib/datastore/transfer.test.ts             |   33 +
 web/src/lib/datastore/transfer.ts                  |   39 +-
 web/src/lib/embed/embed-editor.svelte              |   94 +-
 web/src/lib/embed/session.svelte.ts                |   68 +-
 web/src/lib/embed/session.test.ts                  |  118 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/canvas-actions.ts      |    2 +
 web/src/lib/workflow-editor/canvas-chrome.test.ts  |   96 +
 web/src/lib/workflow-editor/canvas-chrome.ts       |   62 +
 .../lib/workflow-editor/canvas-projection.test.ts  |  147 +
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   69 +-
 web/src/lib/workflow-editor/chat.ts                |   86 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 ++
 web/src/lib/workflow-editor/document.test.ts       |   73 +-
 web/src/lib/workflow-editor/document.ts            |  265 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   79 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   41 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |  169 +-
 web/src/lib/workflow-editor/execution.ts           |  181 +-
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 web/src/lib/workflow-editor/glyphs.json            |   57 +
 web/src/lib/workflow-editor/layout.ts              |    9 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |  126 +-
 web/src/lib/workflow-editor/node-visual.ts         |  165 +-
 web/src/lib/workflow-editor/picker-rows.test.ts    |  100 +
 web/src/lib/workflow-editor/picker-rows.ts         |   38 +
 web/src/lib/workflow-editor/ports.test.ts          |   37 +
 web/src/lib/workflow-editor/ports.ts               |   38 +-
 web/src/lib/workflow-editor/save-feedback.test.ts  |   52 +
 web/src/lib/workflow-editor/save-feedback.ts       |   41 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   94 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   87 +-
 web/src/lib/workflow-editor/sticky.test.ts         |   45 +-
 web/src/lib/workflow-editor/sticky.ts              |   87 +-
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../lib/workflow-editor/webhook-address.test.ts    |  151 +
 web/src/lib/workflow-editor/webhook-address.ts     |   82 +
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   40 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  131 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   58 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   88 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  245 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |   32 +-
 web/src/routes/+page.svelte                        |    6 +-
 web/src/routes/approve/[token]/+page.svelte        |    8 +
 web/src/routes/login/+page.svelte                  |   70 +-
 1019 files changed, 131525 insertions(+), 4453 deletions(-)
```
