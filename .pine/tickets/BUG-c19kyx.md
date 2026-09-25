---
id: BUG-c19kyx
title: 'Code node: a literal run argument to $items() or .all() is not flagged at import or save'
status: done
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-25T03:02:41Z"
---

# Description

Noted in Task 12 (BUG-kvpx6x). `$items('X', output, run)` and `$('X').all(branch, run)` read one output of a node's latest run; a run earlier than the latest is refused, but only when the code runs, because which run is the latest is known only then (`$items('X', 0, 0)` is fine on a node's first run and refused on its third). So neither the importer nor save-time validation says anything about a run argument, and a workflow whose code reads an earlier run activates and fails, or, inside a try/catch, carries on without that run's items. Corpus 2063 is that case: it reads `$items(name, 0, counter)` in a loop with the run taken from a counter variable, inside a try/catch.

The only run arguments that are always safe are a literal `-1` (n8n's "latest") and `$runIndex` read in lockstep; a literal number, a variable or any other expression may name an earlier run.

# Acceptance Criteria

- [x] Decide whether the analyser should warn (not refuse: the read can be right) at import and save when a run argument to `$items(…)` or `.all(…)` is anything other than a literal `-1` or `$runIndex`, and if so, add it as a non-blocking diagnostic naming the line.

# Notes

## Decision (2026-09-25)

Do not add a diagnostic. The runtime refusal of an earlier run is unchanged.

A run argument other than a literal `-1` or `$runIndex` may name an earlier run, and it may not: `$items('X', 0, 0)` is right on a node's first run and refused on its third. Noting it must not refuse the read, the import, or the save.

Import issues are only `blocking`, `lossy`, and `dropped` (`internal/interop/n8n/n8n.go`). There is no warning severity. Amendment 14 of EPIC-tjnr1z already dropped a non-blocking hint for that reason.

- `blocking` stops the workflow activating (`docs/src/content/docs/guides/n8n-migration.md`, the editor's import report). That refuses a read that can be right.
- `lossy` means the element was carried, but differently. The workflow still activates. The run argument is carried as written, so this word would claim a change that did not happen.
- `dropped` means the element was not carried at all. The workflow still activates. The argument is still in the code and still runs, so this word would claim it was discarded.

Save-time validation has the same gap: `ConfigValidator` returns an error, and `POST /workflows/validate` records every compiler refusal as `blocking`. A note there would stop the node saving.

Neither `lossy` nor `dropped` can say "this might fail when the code reaches it" without changing what those words mean. The Code (JavaScript) page already says an earlier run fails only then; it now also says import and save stay quiet about the argument.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `7d5334fd` (last commit at or before ticket created 2026-09-24)
- Commits (3):
  - `5a95c57a` — BUG-c19kyx: import and save stay quiet about a run argument that might name an earlier run.
  - `b66926de` — BUG-kvpx6x: a read that names a run numbers a node's runs as $runIndex does, skipped deliveries left out, and an older checkpoint still reads one output
  - `b538c7b6` — BUG-kvpx6x: $items and .all(branch, run) read one output of a node's latest run in code and in expressions alike, a lockstep $runIndex included, and an earlier run is refused rather than answered with the latest
- Files changed (base → working tree):

```
 .pine/tickets/BUG-0592hz.md                        |   15 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-3mem9s.md                        |   76 +-
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-a9d2hb.md                        |  185 +-
 .pine/tickets/BUG-c19kyx.md                        |   40 +
 .pine/tickets/BUG-djp647.md                        |   98 +-
 .pine/tickets/BUG-h6tj4e.md                        |   38 +-
 .pine/tickets/BUG-jwhj6y.md                        |   96 +-
 .pine/tickets/BUG-kvpx6x.md                        |  116 +-
 .pine/tickets/BUG-pdsydm.md                        |   96 +-
 .pine/tickets/BUG-qe71kf.md                        |   22 +
 .pine/tickets/EPIC-tjnr1z.md                       |   26 +
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-vjjs8t.md                       |  892 +++-
 CHANGELOG.md                                       |   19 +
 config.example.yaml                                |    2 +-
 .../src/content/docs/concepts/safety-boundaries.md |   54 +-
 docs/src/content/docs/guides/code-javascript.md    |  387 ++
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
 94 files changed, 15688 insertions(+), 2481 deletions(-)
```
