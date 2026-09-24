---
id: BUG-jwhj6y
title: 'Code node: errors thrown by built-ins are worded as goja and Go word them, not as V8 does'
status: testing
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

- [x] Decide which messages are worth matching. JSON.parse's are the likeliest to be tested by user code and are produced on the Go side, so they can be reworded there; the property-access ones would need goja's own text rewritten on the way out of `errors.go`.
- [x] Whatever is reworded is pinned by a golden recorded from Node 24 (as `testdata/parity` is), and `make js-diff` shows it.

# Notes

## Plan (2026-09-24, Task 13 of EPIC-tjnr1z)

- Measure first: `make js-diff` before the change, and add a count of "both failed" bodies whose messages match Node's word for word, so the before/after is a number rather than a reading of the log.
- JSON.parse: wrap the global JSON.parse (a new runtime module, `errors`) so a SyntaxError goja raises is re-raised with the message V8 gives for that text. The message is worked out by a small scanner in JavaScript that follows JSON's grammar and reports the first fault the way V8 names it (unexpected token with its context, unexpected end, the positional "Expected …" messages with line and column). JavaScript rather than Go because V8's positions and context windows are in UTF-16 units, which a JS string already is.
- Property reads: goja's "Cannot read property 'x' of undefined" (and "… of undefined or null") become V8's "Cannot read properties of undefined (reading 'x')".
- Not a function / not a constructor: goja's "Object has no member 'x'", "Value is not an object: v", "Not a function: …" and "Value is not a constructor", when the error's own frame is the user's code at a call's opening parenthesis (or a `new`), become "<callee> is not a function" / "… is not a constructor", the callee printed from the parsed body the way V8 prints it. Only the forms recorded from Node are printed; anything else keeps goja's words.
- Caught errors must read the same as uncaught ones: every `catch (e)` in the body is given, in the parsed tree only, a first statement that rewords `e`, through a wrapper parameter no source text can name. Uncaught errors are reworded on the way out.
- Goldens recorded from Node 24 by a dev-only recorder into internal/jsrun/testdata/parity/errors.json.

## Progress (2026-09-24)

Measured with `make js-diff`, which now also counts the "both failed" bodies whose error reads word for word as Node's ("worded as Node words it"): **22 of 101 before, 77 of 101 after**. "console printed differently" 2 -> 1 (3068/5 now prints V8's JSON.parse message; what is left is the frame name, `at parse (native)` against Node's `at JSON.parse (<anonymous>)`).

What is reworded (all pinned by internal/jsrun/testdata/parity/errors.json, recorded from Node 24 by scripts/js-parity/record-engine.mjs, and checked both uncaught and caught by wording_test.go):

- **JSON.parse.** A new runtime module, js/modules/errors.js, replaces the global JSON.parse. It calls goja's own and, when that throws a SyntaxError, re-raises it with V8's message for the text: a scanner in JavaScript (UTF-16 positions, as V8 counts them; its own bracket stack, no recursion) reports the first fault as V8 names it: `Unexpected token 'x', "…" is not valid JSON` with V8's context window (whole text up to 20 units, else 10 each side with `...`), `"undefined" is not valid JSON` for the four texts V8 names whole, `Unexpected end of JSON input`, and the positional messages (`Expected property name or '}'`, `Expected ':' after property name`, `Expected ',' or '}' after property value`, `Expected ',' or ']' after array element`, `Expected double-quoted property name`, `Unterminated string`, `Bad control character in string literal`, `Bad escaped character`, `Bad Unicode escape`, `No number after minus sign`, `Unexpected number`, `Unterminated fractional number`, `Exponent part is missing a number`, `Unexpected non-whitespace character after JSON`) with ` at position N (line L column C)`. 176 texts in the golden. A SyntaxError the reviver throws passes through unchanged; JSON.parse keeps its name and length; the stack still shows `at parse (native)` where the code called it. The runner's own input parsing keeps the original, captured before any module runs.
- **Property reads.** goja's "Cannot read property 'x' of undefined" and "… of undefined or null" -> "Cannot read properties of undefined (reading 'x')".
- **Not a function / not a constructor.** goja's "Object has no member 'x'", "Value is not an object: v", "Not a function: …" (at a call) and "Value is not a constructor" / "Value is not an object: v" (at a `new`) -> "<callee> is not a function" / "<callee> is not a constructor", when the error's innermost frame is the user's code at that call's opening parenthesis or that `new`'s keyword. The callee is printed from the parsed body (analyze_errors.go `calleeText`) in V8's forms, each recorded: names, `this`, `.`/`?.` chains, bracket keys (a string key prints as `.key`, a number in JavaScript's number form, a name, member, call, `this`, `true`, `null`), calls as `(...)`, and `(intermediate value)` for the object of what an `await` or a `new` produced. Any other form keeps goja's words.

How a caught error gets the words: goja offers no hook where it makes these errors, so the parsed tree (never the text) gives the wrapper function one more parameter, under a name no source text can spell (`kilasflow:reword`), and gives every `catch (e)` a first statement handing `e` to it; runtime.js passes the errors module's `reword` as that argument, and calls it from `describe` for an uncaught error. The rewording rewrites `message` and the first line of `stack`. The compiled wrapper's shape changed, so wrapperVersion is now jsrun-3.

**Kept in goja's words** (listed, with what jsrun says, in wording_test.go `keptWording`; each fails the test if it starts matching Node):

- A **null** base: goja reports every property read of null as a read of undefined, so it reads "of undefined" where V8 says "of null" (`'abc'.match(/x/).map(…)`). jsrun cannot tell them apart faithfully.
- A **symbol** key read of undefined: goja prints the symbol's description, V8 `Symbol(k)`.
- Callees V8 prints in forms not recorded or not reproduced: a string or array literal (`"s".q`, `[1,2].q`), a comma expression (`(0 , a.q)`), a computed sum (`a[(k + "x")]`), a tagged template.
- A callback that is not a function (`[1].map(1)`: V8 "number 1 is not a function"), thrown inside the built-in.
- Setting a property of undefined ("Cannot convert undefined or null to object" vs "Cannot set properties of undefined (setting 'x')"), destructuring undefined ("Value is not object coercible"), iterating undefined or an object ("… is not iterable" naming the expression), the `in` operator on a primitive: V8 names keys, values or expressions goja's message leaves out.
- A catch whose parameter is a destructuring pattern (`catch ({ message })`) reads the error before any statement runs, so it sees goja's words. An error handled through a promise's rejection handler (`.catch(e => …)`, `.then(_, e => …)`) rather than a catch clause also keeps goja's words until it is thrown out uncaught. eval() and new Function() code is not instrumented.
- A `TypeError` the code builds itself with one of goja's exact messages would be reworded too; no n8n code writes goja's words.

Found while measuring, not in scope: corpus bodies 1534/8, 2307/6, 2652/7 now fail with the same words as Node but a different text, because `Buffer.from(…, 'base64').toString()` of invalid UTF-8 gives one U+FFFD where Node gives one per invalid byte (a Buffer decoding difference, filed as BUG-46g75c).
