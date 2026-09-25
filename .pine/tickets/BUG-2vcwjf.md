---
id: BUG-2vcwjf
title: 'Code node: an engine error read in a promise rejection handler keeps goja''s words'
status: testing
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

- [x] `.catch(e => …)`, `.then(_, e => …)` and `Promise.allSettled` reasons read V8's words for the errors BUG-jwhj6y rewords, pinned by probes in scripts/js-parity/record-engine.mjs / testdata/parity/errors.json.
- [x] An error the code builds itself still keeps its words there.

# Notes

## Plan

`errors` is the first module, so a wrap of `Promise.prototype.then` there is what `crypto` and `helpers` capture. The wrap calls the original `then` after replacing a function `onRejected` with one that runs `reword` and then the handler. `.catch` and `Promise.allSettled` both reach `then`, so they get the same words. `reword` already returns at once for anything but a goja TypeError, and a second call finds V8's words and stops, so it stays idempotent. The original `then` still attaches the reaction, so goja's rejection tracker is unchanged. The installed function is declared `function then(onFulfilled, onRejected)`, which keeps the name and the length.

`.then(onFulfilled, onRejected)` does not see a throw from `onFulfilled`; the probe chains `.then(undefined, onRejected)` onto the promise that rejected, which is what the ticket's `.then(_, onRejected)` is.

## Progress

2026-09-25: probes recorded from Node 24. The new wording test failed on goja's sentences (`Object has no member 'map'`, `Cannot read property 'field' of undefined`, `Value is not an object: 5`) and passed after the wrap. A rejection with no handler still fails the run; one `.catch` reads does not.
