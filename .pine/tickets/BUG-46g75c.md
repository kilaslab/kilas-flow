---
id: BUG-46g75c
title: 'Code node: Buffer decodes invalid UTF-8 to fewer replacement characters than Node'
status: todo
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
