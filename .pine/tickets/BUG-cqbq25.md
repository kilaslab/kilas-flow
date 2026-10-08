---
id: BUG-cqbq25
title: 'Code node: TextEncoder#encodeInto reports the wrong read count for non-ASCII text'
status: done
priority: low
created: "2026-09-25T09:51:03Z"
updated: "2026-10-08T15:45:29Z"
---

# Description

The JS runtime's `TextEncoder#encodeInto` (`internal/jsrun/js/modules/web.js`) computed `read` by decoding the written UTF-8 bytes as UTF-16LE, which yields half the byte count. `read` is meant to be the number of UTF-16 code units consumed from the source string; it was wrong for every input, ASCII included, and most visibly for non-ASCII text. Code that loops on `read` to stream a string into a fixed buffer skips or repeats characters.

# Steps to Reproduce

1. Code node: `return [{ json: new TextEncoder().encodeInto('héllo', new Uint8Array(10)) }]`.

# Expected

`{ read: 5, written: 6 }`, as Node 24 returns. A surrogate pair counts 2 toward `read`, a lone surrogate (written as U+FFFD) counts 1, and a destination too small stops at a whole character boundary.

# Actual

`{ read: 3, written: 6 }`. `'abc'` into 8 bytes gave `read: 1`.

# Acceptance Criteria
- [x] `read` counts UTF-16 code units consumed from the source (a surrogate pair counts 2) and `written` counts bytes, matching Node 24 for ASCII, 2-, 3- and 4-byte characters and lone surrogates
- [x] A destination too small stops at a whole character boundary, with `read` and `written` describing exactly what was written
- [x] Tests cover the cases above against values recorded from Node 24

# Related Files

`internal/jsrun/js/modules/web.js`, `internal/jsrun/web_test.go`.

# Attachments

## Fix (2026-10-08)

`encodeInto` encodes the source once through the native UTF-8 codec. When it fits, `read` is the source's length and `written` the byte count. Otherwise it walks the source by code point (1/2/3 bytes per BMP unit, 4 for a valid surrogate pair counting 2 units, 3 for a lone surrogate counting 1) and stops before the first character that would overflow, then copies that byte prefix. It now also refuses a non-string `src` and a non-`Uint8Array` `dest` with Node's `ERR_INVALID_ARG_TYPE` messages instead of coercing or failing on `.set`.

Test: `TestTextEncoderEncodeIntoCountsCodeUnitsReadAndBytesWritten` in `internal/jsrun/web_test.go`, with every expected value taken from Node 24.21 (ASCII, accented, emoji, cuts inside 2-, 3- and 4-byte characters, lone surrogate whole and cut, empty, zero-length destination, a subarray destination, a Buffer destination, and the three argument refusals). `go test ./internal/jsrun/` passes apart from `TestLuxonMatchesTheRecordedNodeGoldens` (Sydney DST), which fails identically on unmodified main and is unrelated.
