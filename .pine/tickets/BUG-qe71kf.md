---
id: BUG-qe71kf
title: 'Code node: files stored for a run that then fails stay unreferenced until the execution''s storage goes'
status: done
priority: low
labels:
    - code-node
    - binary
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-25T03:06:55Z"
---

# Description

Noted in Task 12 (BUG-djp647). A Code node's result may give several files inline as base64; the server stores them one by one after the run succeeded (`Job.storeInline`, `internal/jsrun/inline.go`). If storing one fails part-way (a storage error, the run cancelled), the files already stored stay in the execution's binary storage with nothing referencing them, and the run fails. A failed run that called `prepareBinaryData` leaves its files the same way.

They are removed with the execution's storage, so nothing leaks past it; the cost is dead bytes for the execution's lifetime.

# Acceptance Criteria

- [x] Decide whether files stored for a run that then failed are removed at once (the binary store would need a delete by ID scoped to the execution), or documented as kept until the execution's storage goes.

# Notes

## Plan

Look at `internal/binary.Store` before choosing. If it already deletes one payload by id inside an execution, or that is a few lines on a method it has, remove the files at once and test that a second store failure drops the first id without touching another execution. Otherwise document the keep-until-the-execution-goes behaviour on the Code (JavaScript) page, next to binary data, and record why here.

## Decision (2026-09-25)

Keep the files until the execution's storage goes. Documented on the Code (JavaScript) page, in the binary-data paragraph. No store or runtime change.

`Store` deletes only by execution (`DeleteExecution`, the whole directory) or by tenant (`DeleteTenant`). `Put` and `Get` are the only operations that name one id, and neither removes a payload that was stored successfully. A delete by id would be a new method on `Store`, `FileStore` and `Scoped`, and a delete that itself failed would be a new failure path on a run that has already failed. `prepareBinaryData` stores during the run, over the worker protocol; after that worker is gone, cleaning up would also mean guessing which ids this run created. The files are removed with the execution's storage, so nothing leaks past it.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `7d5334fd` (last commit at or before ticket created 2026-09-24)
- Commits (2):
  - `c548beb6` — BUG-qe71kf: files a failed Code-node run already stored stay until the execution's storage goes
  - `4c45bea1` — BUG-djp647: any truthy id makes a returned binary entry a reference, as in n8n, and the server holds inline files and helper calls to one host-call budget
- Files changed (base → working tree):

```
 .pine/tickets/BUG-0592hz.md                        |   15 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-3mem9s.md                        |   76 +-
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-a9d2hb.md                        |  185 +-
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-djp647.md                        |   98 +-
 .pine/tickets/BUG-h6tj4e.md                        |   38 +-
 .pine/tickets/BUG-jwhj6y.md                        |   96 +-
 .pine/tickets/BUG-kvpx6x.md                        |  116 +-
 .pine/tickets/BUG-pdsydm.md                        |   96 +-
 .pine/tickets/BUG-qe71kf.md                        |   34 +
 .pine/tickets/EPIC-tjnr1z.md                       |   26 +
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-vjjs8t.md                       |  892 +++-
 CHANGELOG.md                                       |   19 +
 config.example.yaml                                |    2 +-
 .../src/content/docs/concepts/safety-boundaries.md |   54 +-
 docs/src/content/docs/guides/code-javascript.md    |  391 ++
 docs/src/content/docs/guides/community-nodes.md    |    3 +-
 docs/src/content/docs/guides/n8n-migration.md      |  210 +-
 .../docs/operate/configuration-reference.md        |    2 +-
 docs/src/content/docs/operate/deployment.md        |    4 +-
 .../content/docs/reference/expression-grammar.md   |    2 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 internal/config/config.go                          |    2 +-
 internal/engine/runindex_skip_test.go              |  105 +
 internal/engine/runner.go                          |   30 +-
 internal/expression/globals.go                     |    5 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  116 +-
 internal/jsrun/analyze.go                          |   32 +-
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/buffer_test.go                      |   37 +
 internal/jsrun/codec.go                            |   79 +-
 internal/jsrun/codec_internal_test.go              |   48 +
 internal/jsrun/comparator_test.go                  |    4 +-
 internal/jsrun/console_test.go                     |    2 +-
 internal/jsrun/corpus/BASELINE.md                  |   67 +-
 internal/jsrun/corpus/baseline.json                |  145 +-
 internal/jsrun/corpus/jsdiff_test.go               |   13 +-
 internal/jsrun/corpus/scoreboard_test.go           |    6 +
 internal/jsrun/doc.go                              |    3 +
 internal/jsrun/engine.go                           |   27 +-
 internal/jsrun/export_test.go                      |    8 +
 internal/jsrun/helpers.go                          |   31 +-
 internal/jsrun/helpers_test.go                     |  112 +
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             |  868 +++-
 internal/jsrun/intl_internal_test.go               |  204 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/items.go                            |   94 +-
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/runtime.js                       |  104 +-
 internal/jsrun/jsrun.go                            |    4 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |    4 +
 internal/jsrun/roots.go                            |    7 +
 internal/jsrun/roots_test.go                       |  154 +-
 internal/jsrun/run.go                              |   40 +-
 internal/jsrun/security_test.go                    |   23 +-
 internal/jsrun/surface_internal_test.go            |  134 +-
 internal/jsrun/testdata/parity/date-options.json   | 5382 +++++++++++++-------
 internal/jsrun/testdata/parity/dates.json          |   13 +-
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |    7 +
 internal/jsrun/testdata/parity/utf8.json           | 2598 ++++++++++
 internal/jsrun/testdata/parity/zones.json          |  228 +
 internal/jsrun/testdata/surface.txt                |   21 +-
 internal/jsrun/web_test.go                         |    6 +
 internal/jsrun/wire.go                             |    6 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |   25 +-
 internal/jsworker/doc.go                           |    7 +-
 internal/jsworker/helpers_test.go                  |   18 +
 internal/jsworker/jsworker_test.go                 |   44 +-
 internal/jsworker/load_test.go                     |   24 +-
 internal/jsworker/pool.go                          |    4 +-
 nodes/jscode_helpers_test.go                       |   22 +
 nodes/jscode_lineage_test.go                       |   45 +
 nodes/jscode_roots.go                              |    2 +-
 nodes/jscode_roots_test.go                         |   54 +
 scripts/js-diff/harness.mjs                        |   38 +-
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  346 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 94 files changed, 15813 insertions(+), 2481 deletions(-)
```
