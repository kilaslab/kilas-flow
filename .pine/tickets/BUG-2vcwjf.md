---
id: BUG-2vcwjf
title: 'Code node: an engine error read in a promise rejection handler keeps goja''s words'
status: done
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:27:15Z"
updated: "2026-09-25T02:57:30Z"
---

# Description

Left over from BUG-jwhj6y. The engine's TypeErrors get V8's words where code first reads them: at the start of each `catch (e)` clause, and on the way out uncaught. A promise rejection handler is not a catch clause, so an error the code only sees there keeps goja's words:

```js
const value = {}
return Promise.resolve().then(() => value.map((x) => x)).catch((e) => [{ json: { m: e.message } }])
```

- Node 24 / n8n: `{ m: 'value.map is not a function' }`
- KilasFlow: `{ m: "Object has no member 'map'" }`

The same goes for `.then(_, onRejected)` and `Promise.allSettled` reasons. `try { await … } catch (e)` is already covered.

# Suggested approach

In js/modules/errors.js, wrap `Promise.prototype.then` so that its onRejected (which `.catch` reaches through `then`) is called with the reason passed through `reword` first. `reword` is idempotent and returns at once for anything but a TypeError in goja's words. Keep `then`'s name and length. The runtime's unhandled-rejection tracking must keep working. The wrapper runs in the code's own time, which is fine.

# Acceptance Criteria

- [x] `.catch(e => …)`, `.then(_, e => …)` and `Promise.allSettled` reasons read V8's words for the errors BUG-jwhj6y rewords, pinned by probes in scripts/js-parity/record-engine.mjs / testdata/parity/errors.json.
- [x] An error the code builds itself still keeps its words there.

# Notes

## Plan

`errors` is the first module, so a wrap of `Promise.prototype.then` there is what `crypto` and `helpers` capture. The wrap calls the original `then` after replacing a function `onRejected` with one that runs `reword` and then the handler. `.catch` and `Promise.allSettled` both reach `then`, so they get the same words. `reword` already returns at once for anything but a goja TypeError, and a second call finds V8's words and stops, so it stays idempotent. The original `then` still attaches the reaction, so goja's rejection tracker is unchanged. The installed function is declared `function then(onFulfilled, onRejected)`, which keeps the name and the length.

`.then(onFulfilled, onRejected)` does not see a throw from `onFulfilled`; the probe chains `.then(undefined, onRejected)` onto the promise that rejected, which is what the ticket's `.then(_, onRejected)` is.

## Progress

2026-09-25: probes recorded from Node 24. The new wording test failed on goja's sentences (`Object has no member 'map'`, `Cannot read property 'field' of undefined`, `Value is not an object: 5`) and passed after the wrap. A rejection with no handler still fails the run; one `.catch` reads does not.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `7442889a` (last commit at or before ticket created 2026-09-24)
- Commits (2):
  - `1c80fe80` — BUG-2vcwjf: a rejection handler reads an engine TypeError in V8's words
  - `a240a7dd` — BUG-jwhj6y: an error the code builds itself keeps its words, even goja's or Node 14's; only errors the engine made are reworded
- Files changed (base → working tree):

```
 .pine/tickets/BUG-0592hz.md                        |   15 +
 .pine/tickets/BUG-2vcwjf.md                        |   47 +
 .pine/tickets/BUG-3mem9s.md                        |   76 +-
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-9hx5xm.md                        |   59 +-
 .pine/tickets/BUG-a9d2hb.md                        |  185 +-
 .pine/tickets/BUG-c19kyx.md                        |    6 +-
 .pine/tickets/BUG-djp647.md                        |   61 +-
 .pine/tickets/BUG-jwhj6y.md                        |   96 +-
 .pine/tickets/BUG-kvpx6x.md                        |   71 +-
 .pine/tickets/BUG-pdsydm.md                        |   61 +-
 .pine/tickets/EPIC-tjnr1z.md                       |   26 +
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-vjjs8t.md                       |  667 ++-
 CHANGELOG.md                                       |    3 +-
 .../src/content/docs/concepts/safety-boundaries.md |   10 +-
 docs/src/content/docs/guides/code-javascript.md    |   91 +-
 internal/engine/runindex_skip_test.go              |  105 +
 internal/engine/runner.go                          |   29 +-
 internal/expression/roots.go                       |   22 +-
 internal/jsrun/analyze.go                          |   32 +-
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/buffer_test.go                      |   37 +
 internal/jsrun/codec.go                            |   79 +-
 internal/jsrun/codec_internal_test.go              |   48 +
 internal/jsrun/console_test.go                     |    2 +-
 internal/jsrun/corpus/BASELINE.md                  |   16 +-
 internal/jsrun/corpus/baseline.json                |   33 +-
 internal/jsrun/corpus/jsdiff_test.go               |   13 +-
 internal/jsrun/engine.go                           |   17 +-
 internal/jsrun/export_test.go                      |    8 +
 internal/jsrun/helpers_test.go                     |   13 +
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/intl.go                             |  868 +++-
 internal/jsrun/intl_internal_test.go               |  204 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/runtime.js                       |   14 +-
 internal/jsrun/jsrun.go                            |    4 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |    4 +
 internal/jsrun/run.go                              |    4 +-
 internal/jsrun/security_test.go                    |   23 +-
 internal/jsrun/surface_internal_test.go            |  128 +-
 internal/jsrun/testdata/parity/date-options.json   | 5382 +++++++++++++-------
 internal/jsrun/testdata/parity/dates.json          |   13 +-
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |    7 +
 internal/jsrun/testdata/parity/utf8.json           | 2598 ++++++++++
 internal/jsrun/testdata/parity/zones.json          |  228 +
 internal/jsrun/testdata/surface.txt                |   20 +-
 internal/jsrun/web_test.go                         |    6 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |   12 +-
 internal/jsworker/load_test.go                     |   24 +-
 nodes/jscode_roots.go                              |    2 +-
 nodes/jscode_roots_test.go                         |    5 +-
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  346 +-
 65 files changed, 13706 insertions(+), 2090 deletions(-)
```
