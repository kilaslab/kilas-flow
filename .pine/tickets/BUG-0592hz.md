---
id: BUG-0592hz
title: 'Code node: TextDecoder fatal mode does not throw on a UTF-16 lone surrogate'
status: todo
priority: low
labels:
    - code-node
    - javascript
created: "2026-09-25T02:10:44Z"
updated: "2026-09-25T02:10:44Z"
---

Left by the BUG-46g75c review. `TextDecoder` with `fatal: true` throws when the decode contains U+FFFD and Go's `utf8.Valid` rejects the bytes. That matches Node for UTF-8. A lone surrogate under `utf-16le` does not throw, and Node does. Pre-existing, outside the UTF-8 ticket, and not an EPIC-tjnr1z acceptance item.

Not a child of EPIC-tjnr1z.
