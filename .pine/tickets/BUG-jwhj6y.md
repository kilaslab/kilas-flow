---
id: BUG-jwhj6y
title: 'Code node: errors thrown by built-ins are worded as goja and Go word them, not as V8 does'
status: todo
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T14:07:22Z"
updated: "2026-09-24T14:07:22Z"
---

# Description

Found by `make js-diff` over the Code-node compatibility corpus (FEAT-afkx3k). Across the 331 bodies it ran under both engines, **no body computed a different result**. Where the two differ is the text of the errors the engine's built-ins throw: 104 bodies failed under both, and most of their messages are worded differently. Two bodies also printed a caught error's message to the console, so the difference reaches output a user reads.

These are goja's own messages, and JSON.parse's come from Go's `encoding/json`. A user moving a workflow from n8n sees different words for the same mistake, and code that tests `error.message` (for example `includes('is not valid JSON')`) takes a different branch.

# Minimal reproduction

Each body alone, all-items mode:

| Body | Node 24 (and so n8n) | KilasFlow |
|---|---|---|
| `const none = undefined; return [{ json: { v: none.field } }]` | `TypeError: Cannot read properties of undefined (reading 'field')` | `TypeError: Cannot read property 'field' of undefined` |
| `const value = {}; return value.map((x) => x)` | `TypeError: value.map is not a function` | `TypeError: Object has no member 'map'` |
| `return JSON.parse('sample')` | `SyntaxError: Unexpected token 's', "sample" is not valid JSON` | `SyntaxError: invalid character 's' looking for beginning of value` |
| `try { JSON.parse('{"a":') } catch (e) { return [{ json: { m: e.message } }] }` | `{ m: 'Unexpected end of JSON input' }` | `{ m: 'Unexpected end of JSON input (EOF)' }` |

# Acceptance Criteria

- [ ] Decide which messages are worth matching. JSON.parse's are the likeliest to be tested by user code and are produced on the Go side, so they can be reworded there; the property-access ones would need goja's own text rewritten on the way out of `errors.go`.
- [ ] Whatever is reworded is pinned by a golden recorded from Node 24 (as `testdata/parity` is), and `make js-diff` shows it.
