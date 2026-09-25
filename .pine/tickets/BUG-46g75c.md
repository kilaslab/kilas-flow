---
id: BUG-46g75c
title: 'Code node: Buffer decodes invalid UTF-8 to fewer replacement characters than Node'
status: testing
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:14:38Z"
updated: "2026-09-24T15:14:38Z"
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
