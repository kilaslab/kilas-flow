---
id: BUG-0592hz
title: 'Code node: TextDecoder fatal mode does not throw on a UTF-16 lone surrogate'
status: testing
priority: low
labels:
    - code-node
    - javascript
created: "2026-09-25T02:10:44Z"
updated: "2026-09-25T02:10:44Z"
---

Left by the BUG-46g75c review. `TextDecoder` with `fatal: true` throws when the decode contains U+FFFD and Go's `utf8.Valid` rejects the bytes. That matches Node for UTF-8. A lone surrogate under `utf-16le` does not throw, and Node does. Pre-existing, outside the UTF-8 ticket, and not an EPIC-tjnr1z acceptance item.

Not a child of EPIC-tjnr1z.

# Acceptance Criteria

- [x] `new TextDecoder('utf-16le', { fatal: true }).decode(...)` throws a TypeError with code `ERR_ENCODING_INVALID_ENCODED_DATA` and Node's message on a lone surrogate or an odd trailing byte.
- [x] Without `fatal`, the same bytes decode to U+FFFD where Node puts one.
- [x] Every encoding our TextDecoder accepts is covered (`utf-8`, and `utf-16le` with its `utf-16` alias).

# Notes

## Plan (2026-09-25)

What Node 24.16 does, checked by running it locally (`node -e`):

- `utf-16le` (and the `utf-16` label, which Node reports as `utf-16le`) in fatal mode throws a TypeError, code `ERR_ENCODING_INVALID_ENCODED_DATA`, for a lone lead surrogate, a lone trail surrogate, a lead followed by something other than a trail, and an odd byte left at the end. The message names `utf-16le` whichever of the two labels was used.
- Without fatal, each of those becomes U+FFFD: `[00 D8]` -> U+FFFD, `[00 DC 41 00]` and `[00 D8 41 00]` -> U+FFFD then "A", `[41 00 42]` -> "A" then U+FFFD. A lead surrogate and an odd byte both left at the end, `[00 D8 42]`, are ONE U+FFFD, not two.
- Our decoder today goes through the Buffer codec's `utf16le`, which drops an odd trailing byte (right for `Buffer#toString`, wrong for TextDecoder) and never reports a lone surrogate, so fatal mode never throws.

Plan: a Go WHATWG UTF-16LE decoder in `internal/jsrun/codec.go` (from the Encoding Standard's shared UTF-16 decoder) that also says whether it replaced anything; two natives beside the UTF-8 pair (`codec.textUTF16LE` for the text, `codec.validUTF16LE` for fatal mode); `web.js` TextDecoder uses them and names its own encoding in the error. `Buffer#toString('utf16le')` keeps its lenient codec. TDD through a Code-node body in `web_test.go`.

Decision: our TextDecoder accepts only `utf-8` and `utf-16le` (with `utf-16`). `utf-16be` is not added: that would be a new feature, not this fix. Node's `utf-16be` does not validate surrogates at all (it returns a lone `D8` unit as a character), so there is no fatal behaviour of Node's to match there beyond the odd byte.

## Progress (2026-09-25)

Done, awaiting review.

- `internal/jsrun/codec.go`: `decodeUTF16LEWHATWG`, the Encoding Standard's shared UTF-16 decoder read little endian. It returns the text and whether anything was replaced. Two natives, `codec.textUTF16LE` and `codec.validUTF16LE`, sit beside the UTF-8 pair.
- `internal/jsrun/js/modules/web.js`: TextDecoder's `utf-16le` decodes through the new native and no longer through Buffer's `utf16le` codec. Fatal mode checks both encodings, and the error names the decoder's own encoding.
- `Buffer#toString('utf16le')` is unchanged. It still drops an odd trailing byte, as Node's Buffer does.
- Test: `TestTextDecoderFatalModeRefusesMalformedUTF16` in `internal/jsrun/web_test.go` covers the five malformed shapes under both labels, fatal and lenient, plus a valid pair in fatal mode and the UTF-8 message. RED showed that fatal mode never threw and that lenient mode dropped the odd byte. It is GREEN now, and the surface test still passes: the script surface is unchanged because natives are not on it.
- Seen and left alone: `TextEncoder#encodeInto` reports `read` wrongly. For `'hié'` it gives 2 where Node gives 3, because it decodes the written UTF-8 bytes as UTF-16LE to count them. This is pre-existing and outside this ticket.
