---
id: BUG-2vcwjf
title: 'Code node: an engine error read in a promise rejection handler keeps goja''s words'
status: todo
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:27:15Z"
updated: "2026-09-24T15:27:15Z"
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

- [ ] `.catch(e => …)`, `.then(_, e => …)` and `Promise.allSettled` reasons read V8's words for the errors BUG-jwhj6y rewords, pinned by probes in scripts/js-parity/record-engine.mjs / testdata/parity/errors.json.
- [ ] An error the code builds itself still keeps its words there.
