---
id: BUG-a9d2hb
title: Intl zone names for Europe/Dublin are swapped on Linux, where Debian's tzdata stores winter as negative daylight time
status: done
priority: low
parent: EPIC-tjnr1z
created: "2026-09-24T13:54:27Z"
updated: "2026-09-25T02:31:40Z"
---

# Description

`internal/jsrun`'s `TestZoneNamesMatchNodeForEveryZone` fails on Linux in
the golang:1.27-bookworm image. It fails on main too (checked 2026-09-24,
main at 1c51601), not only on a branch. For Europe/Dublin and Eire, the
winter and summer names come out swapped: January gives "GMT+0" / "Irish
Standard Time" where Node gives "GMT" / "Greenwich Mean Time", and July the
other way round.

Debian's tzdata stores Dublin in the "vanguard" form, where winter is the
negative daylight saving time. Go's embedded data and macOS store it the
other way. Found while running FEAT-21h6xp's Linux container matrix; it
passes on macOS.

# Acceptance Criteria
- [x] Dublin's names match Node's whichever tzdata form the system has, or
      the test pins the zone database it compares against.

# Notes

## Plan

Name the higher of the two offsets in a saving pair as daylight, instead of
trusting the tzdata isdst flag. Do not import `time/tzdata`: that would pin
every zone to Go's rearguard copy and grow a small binary by 413008 bytes
(plain 1938802, with the import 2351810; the zip itself is 408467). The
system database stays the source of the offsets, which already agree across
forms. An interval with no further transition keeps the database flag, so a
settled standard change is not read as a saving.

## Progress (2026-09-25)

`onDaylightTime` compares the instant's offset with the neighbouring interval
the file flags the other way, and uses the daylight name only when this
offset is the higher one. Europe/Dublin and Eire then print GMT / Greenwich
Mean Time in January and GMT+1 / Irish Standard Time in July on both forms.
A synthetic version-1 TZif holds each form, so the check does not depend on
the host database.

Africa/Casablanca and Africa/El_Aaiun are the other negative-saving zones.
Their table entries have no long or short name, so January and July render
as the offset (GMT+1 in 2026, outside Ramadan). Both forms describe that
same offset. They match the Node golden on macOS (rearguard) and on Debian
(vanguard).

Africa/Windhoek on Debian has sat at +02 since 2017 (standard), with the
previous interval an hour lower and flagged as a saving. Treating that
history as the other half of a pair turned Central Africa Time into
GMT+02:00. An interval that does not end keeps the database's own flag.

`TestZoneNamesMatchNodeForEveryZone` is the check that no other zone's
January or July name moved. It passes on macOS, in `golang:1.27-bookworm`,
and in `gcr.io/distroless/static-debian12:nonroot`. The stock distroless
image does carry Debian tzdata: a probe there shows Dublin's January isdst
set and Windhoek's previous offset at +01, the vanguard shape. The zone-name
tests were run as that image's entrypoint against its own zoneinfo.

# Related Files
- internal/jsrun/intl.go
- internal/jsrun/intl_test.go

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `1c516014` (last commit at or before ticket created 2026-09-24)
- Commits (2):
  - `a767d14c` — BUG-a9d2hb: zone names follow the higher offset so Dublin matches Node on either tzdata form
  - `a39831a1` — FEAT-21h6xp: the safety boundaries say what confines a worker and what does not, and the ticket moves to testing with its decisions and follow-ups
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .pine/memory/code-node.md                          |    2 +
 .pine/tickets/BUG-0592hz.md                        |   15 +
 .pine/tickets/BUG-2vcwjf.md                        |   35 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-a9d2hb.md                        |   70 +
 .pine/tickets/BUG-c19kyx.md                        |   22 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-k99658.md                        |  183 +-
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-qe71kf.md                        |   22 +
 .pine/tickets/EPIC-tjnr1z.md                       |   26 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-21h6xp.md                       |  223 +-
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-afkx3k.md                       |   93 +-
 .pine/tickets/FEAT-f40kg4.md                       |   38 +
 .pine/tickets/FEAT-vjjs8t.md                       |  266 +-
 CHANGELOG.md                                       |   18 +
 Makefile                                           |   35 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |   27 +-
 config.example.yaml                                |   20 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/guides/code-javascript.md    |  375 ++
 docs/src/content/docs/guides/community-nodes.md    |    3 +-
 docs/src/content/docs/guides/n8n-migration.md      |  210 +-
 .../docs/operate/configuration-reference.md        |   32 +-
 docs/src/content/docs/operate/deployment.md        |   18 +-
 .../content/docs/reference/expression-grammar.md   |    2 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 e2e/fixtures/epic-code.ts                          |  271 +
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/tests/js-code.spec.ts                          |   76 +-
 go.mod                                             |    2 +-
 internal/config/config.go                          |   56 +-
 internal/config/config_test.go                     |   31 +
 internal/engine/runindex_skip_test.go              |  105 +
 internal/engine/runner.go                          |   30 +-
 internal/expression/globals.go                     |    5 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  116 +-
 internal/jsrun/analyze.go                          |   32 +-
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/bounds_test.go                      |  115 +-
 internal/jsrun/buffer_test.go                      |   37 +
 internal/jsrun/codec.go                            |   79 +-
 internal/jsrun/codec_internal_test.go              |   48 +
 internal/jsrun/comparator_test.go                  |    4 +-
 internal/jsrun/console_test.go                     |   14 +-
 internal/jsrun/corpus/BASELINE.md                  |  412 ++
 internal/jsrun/corpus/MANIFEST.json                |  769 +++
 internal/jsrun/corpus/baseline.json                | 3464 +++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 +++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/doc.go                              |    3 +
 internal/jsrun/engine.go                           |   39 +-
 internal/jsrun/export_test.go                      |    8 +
 internal/jsrun/guards_test.go                      |   11 +-
 internal/jsrun/helpers.go                          |   31 +-
 internal/jsrun/helpers_test.go                     |  112 +
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             |  868 +++-
 internal/jsrun/intl_internal_test.go               |  204 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/items.go                            |   94 +-
 internal/jsrun/js/modules/errors.js                |  279 +
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/runtime.js                       |  104 +-
 internal/jsrun/jsrun.go                            |    4 +-
 internal/jsrun/jsrun_test.go                       |   14 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |    4 +
 internal/jsrun/roots.go                            |    7 +
 internal/jsrun/roots_test.go                       |  154 +-
 internal/jsrun/run.go                              |   39 +-
 internal/jsrun/security_test.go                    |  271 +
 internal/jsrun/surface_internal_test.go            |  488 ++
 internal/jsrun/testdata/parity/date-options.json   | 5382 +++++++++++++-------
 internal/jsrun/testdata/parity/dates.json          |   13 +-
 internal/jsrun/testdata/parity/errors.json         |  293 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |    7 +
 internal/jsrun/testdata/parity/utf8.json           | 2598 ++++++++++
 internal/jsrun/testdata/parity/zones.json          |  228 +
 internal/jsrun/testdata/surface.txt                |  498 ++
 internal/jsrun/web_test.go                         |    6 +
 internal/jsrun/wire.go                             |    6 +
 internal/jsrun/wording.go                          |  149 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  268 +
 internal/jsrun/wrapper.go                          |   25 +-
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 +
 internal/jsworker/confine_linux_test.go            |  501 ++
 internal/jsworker/confine_test.go                  |  234 +
 internal/jsworker/doc.go                           |   32 +-
 internal/jsworker/helpers_test.go                  |   31 +-
 internal/jsworker/jsworker_test.go                 |  132 +-
 internal/jsworker/limits_linux.go                  |   12 +-
 internal/jsworker/limits_other.go                  |   22 +-
 internal/jsworker/load_test.go                     |  124 +
 internal/jsworker/pool.go                          |  179 +-
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |   12 +-
 internal/jsworker/security_test.go                 |   51 +
 internal/jsworker/worker.go                        |   19 +-
 nodes/jscode_helpers_test.go                       |   22 +
 nodes/jscode_lineage_test.go                       |   45 +
 nodes/jscode_roots.go                              |    2 +-
 nodes/jscode_roots_test.go                         |   54 +
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  322 ++
 scripts/js-parity/record.mjs                       |  346 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 128 files changed, 25800 insertions(+), 2436 deletions(-)
```
