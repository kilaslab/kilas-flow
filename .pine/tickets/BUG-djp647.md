---
id: BUG-djp647
title: 'Code node: a file returned inline as base64 binary is refused at run time, where n8n stores it'
status: todo
priority: medium
labels:
    - code-node
    - javascript
    - n8n
parent: EPIC-tjnr1z
created: "2026-09-24T14:07:17Z"
updated: "2026-09-24T14:07:17Z"
---

# Description

Found by `make js-diff` over the Code-node compatibility corpus (FEAT-afkx3k): one body among the 500 most-viewed n8n templates builds a file in code and returns it inline, and KilasFlow refuses the result.

**What n8n does** (read from n8n's Code node result validation outside this repository; behaviour only): a returned item's `binary` only has to be an object. An entry of the form `{ data: <base64 text>, mimeType, fileName }` is accepted as the file's contents and flows on to the next node as binary data. This was the usual way to make a file in a Code or Function node before `this.helpers.prepareBinaryData` existed, so older templates use it.

**What KilasFlow does:** `jsrun` accepts a returned binary entry only when it references a file the node was given or stored with `prepareBinaryData` (EPIC-tjnr1z amendment 10), so the inline entry is refused with `item 0 has a binary "data" that is not a file reference`.

This may be the intended boundary: amendment 10 kept file bytes out of the returned JSON. If so, it should be a refusal the importer and validation can name, not a run-time surprise. If not, the runtime can store inline base64 the way `prepareBinaryData` does.

# Minimal reproduction

All-items mode, one input item:

```js
items[0].binary = { data: { data: Buffer.from('<p>hi</p>').toString('base64'), mimeType: 'text/html', fileName: 'page.html' } }
return items
```

- n8n: one item whose binary `data` is a 9-byte text/html file named page.html
- KilasFlow: `item 0 has a binary "data" that is not a file reference; binary entries can only pass on files the node was given or stored with prepareBinaryData`

# Acceptance Criteria

- [ ] Decide: store inline base64 binary returned by code as a file (bounded by `MaxFileBytes`), or keep refusing it.
- [ ] Either way the answer is the same at import, validate and run: stored and passed on, or refused through `jsrun.Refusal` in one sentence that names `prepareBinaryData` as the alternative.
