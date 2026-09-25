---
id: BUG-46g75c
title: 'Code node: Buffer decodes invalid UTF-8 to fewer replacement characters than Node'
status: done
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:14:38Z"
updated: "2026-09-25T02:10:31Z"
---

# Description

Found by `make js-diff` while measuring BUG-jwhj6y. Decoding bytes that are not valid UTF-8 gives fewer replacement characters than Node does. Node follows the WHATWG decoder: each byte that cannot start or continue a sequence becomes its own U+FFFD. The Code node's Buffer gives one U+FFFD for the whole run of bad bytes.

Three corpus bodies (1534/8, 2307/6, 2652/7) decode a base64 stand-in with `Buffer.from(content, 'base64').toString()` and then `JSON.parse` it. Both engines fail with the same words, but the text they quote differs: `"�"` in KilasFlow, `"����"` in Node.

# Minimal reproduction

All-items mode:

```js
const s = Buffer.from('sample', 'base64')   // <Buffer b1 a9 a9 95>
return [{ json: { text: s.toString(), length: s.toString().length } }]
```

- Node 24 / n8n: `length: 4` (four U+FFFD)
- KilasFlow: `length: 1`

# Acceptance Criteria

- [x] `Buffer#toString('utf8')` (and TextDecoder, if it shares the path) replaces invalid bytes as Node does, pinned by a golden recorded from Node 24 that covers lone continuation bytes, truncated sequences, overlong forms and surrogate encodings.

# Notes

## Plan (Task 14, epic/buffer-utf8)

Root cause: `internal/jsrun/codec.go`'s `encodeBytes` (bytes -> JS string, the
`codec.encode` native) uses `strings.ToValidUTF8(string(data), "�")` for
the `utf8` case. Go's `ToValidUTF8` collapses each *run* of invalid bytes into
one replacement character; WHATWG's UTF-8 decoder (which Node's Buffer and
TextDecoder both implement) emits one U+FFFD per byte that cannot start or
continue a sequence, and one U+FFFD for a truncated-but-valid prefix (the
"maximal subpart" rule). `codec.encode` is the single native every byte->string
utf8 path goes through: `Buffer#toString('utf8')` (buffer.js), `TextDecoder`
non-fatal decode (web.js, shares the exact same native call), and
`getBinaryDataBuffer(...).toString()` (returns a Buffer, same `toString`).
`atob`/`btoa` use `latin1`, not `utf8`, so they are unaffected (and correctly
so — latin1 has no invalid byte).

Fix: replace the `utf8` case in `encodeBytes` with a hand-written decoder
implementing the WHATWG "UTF-8 decoder" algorithm (Encoding Standard
https://encoding.spec.whatwg.org/#utf-8-decoder), written from the spec's
prose/pseudocode, not from any V8/Node source (clean-room constraint). Kept as
a small unexported function in codec.go next to `encodeBytes`.

Golden: extend `scripts/js-parity/record.mjs` (dev-only, run under Node 24) to
record `Buffer.from(bytes).toString('utf8')` over a byte-sequence sweep: every
single byte alone (256), a full second-byte boundary sweep for each of the 7
lead-byte classes with a distinct WHATWG boundary pair (C2, E0, E1, ED, F0,
F1, F4), a third/fourth-byte sweep for one 3-byte and one 4-byte lead, plus
explicit overlong, surrogate, >U+10FFFF and truncated-sequence cases and a few
valid-text sanity cases. Written to
`internal/jsrun/testdata/parity/utf8.json`. A Go test in
`internal/jsrun/codec_internal_test.go` loads it and checks `encodeBytes`
against every case (fast, no VM). A handful of VM-level tests (buffer_test.go,
web_test.go) confirm `Buffer#toString`, `Buffer.from(...).toString()`,
`TextDecoder().decode()` (default and `fatal: true`), and
`getBinaryDataBuffer(...).toString()` all go through the fixed path, using the
ticket's own repro (`Buffer.from('sample', 'base64').toString()` -> 4 x
U+FFFD).

Decision: `decodeString`'s `toWellFormed` (JS string -> bytes, lone-surrogate
replacement) is a different problem (a UTF-16 JS string can hold a lone
surrogate; UTF-8 cannot) and is out of scope for this ticket, which is about
bytes -> string.

## Progress 2026-09-25 (done, status testing)

The plan above is implemented. `encodeBytes`'s `utf8` case now calls
`decodeUTF8WHATWG` in `internal/jsrun/codec.go`, a state machine written from
the Encoding Standard's own UTF-8 decoder algorithm: it tracks how many
continuation bytes the sequence still needs and the narrowed [lower, upper]
range the byte after E0, ED, F0 and F4 must fall in, emits one U+FFFD per byte
that can neither start nor continue a sequence (reprocessing that byte as a
possible lead, as the spec's "prepend" step says), and one U+FFFD for a valid
prefix truncated at the end of the buffer.

`codec.encode` is the one native every bytes -> string utf8 path reaches, so
the single change covers `Buffer#toString('utf8')`, `Buffer.from(..).toString()`,
`TextDecoder` non-fatal (web.js calls the same native) and
`getBinaryDataBuffer(..).toString()` (it returns a Buffer). `atob`/`btoa` ask
for `latin1`, which has no invalid byte, so they are unaffected, as expected.
No other Go-side byte -> string conversion feeds a JS string:
`strings.ToValidUTF8` survives only in `toWellFormed`, which is the
string -> bytes direction and out of scope (see the decision above).

Decisions taken unattended, both matching Node 24:

- `TextDecoder`'s `fatal: true` needed no change. web.js already throws the
  `ERR_ENCODING_INVALID_ENCODED_DATA` TypeError when `codec.validUTF8` says the
  bytes are not valid UTF-8, and Go's `utf8.Valid` rejects exactly what WHATWG
  rejects (overlongs, surrogates, past U+10FFFF). Checked against Node 24 over
  all 2593 golden cases: Node's `TextDecoder` output equals its
  `Buffer#toString('utf8')` output in every case, and `fatal: true` throws in
  exactly the cases whose decode contains a U+FFFD. So one golden recorded from
  Buffer pins both decoders.
- The golden sweeps each lead byte's *second* byte over the whole 0x00-0xFF
  range for one representative of each of the seven distinct boundary classes
  (C2, E0, E1, ED, F0, F1, F4) rather than every lead byte, which keeps
  `testdata/parity/utf8.json` to 2593 cases while still covering every boundary
  the decoder checks.

The corpus baseline is unchanged on purpose: it records a body's outcome
(`threw: SyntaxError` for 1534/8 and the other two), not the text the error
quotes, and the outcome does not move — only the quoted `"����"` now matches
Node.

Verified: `go build ./...`, `go vet ./...`, `go test ./...` all clean;
`go test -race -count=1 ./internal/jsrun/...` ok; `node
scripts/js-parity/record.mjs --check` reports no drift against Node v24.16.0.
Rebased onto local `main`.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `e61945bc` (last commit at or before ticket created 2026-09-24)
- Commits (4):
  - `bce984f0` — BUG-46g75c: bytes become a string the way Node's WHATWG UTF-8 decoder does, one replacement character per bad byte
  - `0ab3350d` — WIP BUG-46g75c: WHATWG UTF-8 decoding in progress (unreviewed, stopped for the usage limit)
  - `5eea0002` — BUG-46g75c: ticket travels with the fix, plan noted
  - `b641f2d0` — BUG-jwhj6y: the errors a Code node's JavaScript reads from JSON.parse, a property of undefined and a call of what is not a function are worded as Node 24 words them
- Files changed (base → working tree):

```
 .pine/tickets/BUG-2vcwjf.md                        |   35 +
 .pine/tickets/BUG-3mem9s.md                        |   76 +-
 .pine/tickets/BUG-46g75c.md                        |  124 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-c19kyx.md                        |   22 +
 .pine/tickets/BUG-djp647.md                        |   98 +-
 .pine/tickets/BUG-h6tj4e.md                        |   38 +-
 .pine/tickets/BUG-jwhj6y.md                        |   96 +-
 .pine/tickets/BUG-kvpx6x.md                        |  116 +-
 .pine/tickets/BUG-pdsydm.md                        |   96 +-
 .pine/tickets/BUG-qe71kf.md                        |   22 +
 .pine/tickets/EPIC-tjnr1z.md                       |   26 +
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .../src/content/docs/concepts/safety-boundaries.md |    6 +-
 docs/src/content/docs/guides/code-javascript.md    |   52 +-
 .../content/docs/reference/expression-grammar.md   |    2 +-
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
 internal/jsrun/engine.go                           |   17 +-
 internal/jsrun/export_test.go                      |    8 +
 internal/jsrun/helpers.go                          |   31 +-
 internal/jsrun/helpers_test.go                     |  112 +
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             |  823 ++-
 internal/jsrun/intl_internal_test.go               |   44 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/items.go                            |   94 +-
 internal/jsrun/js/modules/errors.js                |  279 +
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/runtime.js                       |  104 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |    4 +
 internal/jsrun/roots.go                            |    7 +
 internal/jsrun/roots_test.go                       |  154 +-
 internal/jsrun/run.go                              |   36 +-
 internal/jsrun/testdata/parity/date-options.json   | 5382 +++++++++++++-------
 internal/jsrun/testdata/parity/dates.json          |   13 +-
 internal/jsrun/testdata/parity/errors.json         |  293 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |    7 +
 internal/jsrun/testdata/parity/utf8.json           | 2598 ++++++++++
 internal/jsrun/testdata/parity/zones.json          |  228 +
 internal/jsrun/testdata/surface.txt                |    1 +
 internal/jsrun/web_test.go                         |    6 +
 internal/jsrun/wire.go                             |    6 +
 internal/jsrun/wording.go                          |  149 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  268 +
 internal/jsrun/wrapper.go                          |   25 +-
 internal/jsworker/doc.go                           |    7 +-
 internal/jsworker/helpers_test.go                  |   18 +
 internal/jsworker/jsworker_test.go                 |   44 +-
 internal/jsworker/pool.go                          |    4 +-
 nodes/jscode_helpers_test.go                       |   22 +
 nodes/jscode_lineage_test.go                       |   45 +
 nodes/jscode_roots.go                              |    2 +-
 nodes/jscode_roots_test.go                         |   54 +
 scripts/js-diff/harness.mjs                        |   38 +-
 scripts/js-parity/record-engine.mjs                |  322 ++
 scripts/js-parity/record.mjs                       |  346 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 79 files changed, 13478 insertions(+), 2235 deletions(-)
```
