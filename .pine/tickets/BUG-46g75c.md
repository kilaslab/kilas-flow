---
id: BUG-46g75c
title: 'Code node: Buffer decodes invalid UTF-8 to fewer replacement characters than Node'
status: doing
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

- [ ] `Buffer#toString('utf8')` (and TextDecoder, if it shares the path) replaces invalid bytes as Node does, pinned by a golden recorded from Node 24 that covers lone continuation bytes, truncated sequences, overlong forms and surrogate encodings.

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
