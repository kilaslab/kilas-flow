---
title: Expression grammar
description: Every root and every function an expression may use, with what each one takes and returns.
sidebar:
  order: 4
---

The complete grammar. For *why* it is shaped this way — the two dialects, the
closed allowlist, the narrowed `$env` — see
[expressions](/concepts/expressions/).

A running server publishes this same list at
`GET /api/v1/expression-grammar`, generated from the evaluator itself, so the
editor validates against the server rather than a copy that drifts from it. That
endpoint is authoritative for the instance you are talking to.

## Marking an expression

A parameter is evaluated only when it carries the explicit marker:

```json
{ "mode": "expression", "value": "{{ $json.email }}" }
```

A fixed string containing `{{ }}` is still data. An imported n8n workflow uses
the other dialect — a leading `=` on a plain string — and the importer translates
between the two, so the `=` form never reaches the evaluator.

Resolution walks the whole parameter tree, so the marker may appear at any depth
inside a nested object or list.

## Roots

An expression body must begin with one of these. Anything else is a parse error.

| Root | Yields |
| --- | --- |
| `$json` | the current item's `json` fields |
| `$input` | every item on each input port, keyed by port name |
| `$node["Name"]` | the named node's first output item |
| `$('Name')` | the named node, as a receiver for `.item`, `.first()`, `.last()`, `.all()` |
| `$env` | the allowlisted environment (see below) |
| `$execution` | `.id` and `.mode` |
| `$workflow` | `.id`, `.name` and `.active` |
| `$itemIndex` | the current item's position, as a number |
| `$now` | the current instant |
| `$today` | the start of today |
| `$fromAI('name')` | a parameter an AI agent fills in |

`$now` and `$today` are fixed for the whole evaluation of one parameter tree.

`$fromAI` is an error anywhere other than a parameter an AI agent fills.

`$env.KEY` reads only process environment variables named
`KILASFLOW_WORKFLOW_ENV_KEY` — the prefix is required when setting the variable
and stripped when reading it. Nothing else in the process environment is
reachable.

### Node accessors

| Form | Yields |
| --- | --- |
| `$('Name').item` | that node's single item, when its provenance is intact |
| `$('Name').first()` | its first item |
| `$('Name').last()` | its last item |
| `$('Name').all()` | all of its items |
| `$node["Name"].json.field` | a field of its first output item |

`.item` is checked against [paired-item lineage](/concepts/items-and-lineage/)
and resolves only when the named node produced **exactly one** item and every
item it produced carries intact provenance. Otherwise it **fails with the
reason** — no provenance recorded, lineage lost, no items, or several items —
rather than falling back to the first item. To reach one of several, use
`.all()`, `.first()` or `.last()`.

`$('Name')` naming a node that has not produced output in this run is an
**error**, not an absent value: a node that never ran cannot be what the author
meant. `$node["Name"]` behaves the other way — it is an ordinary field read on a
map, so an unknown name resolves to undefined and yields `null`.

### Roots you cannot use

`$parameter`, `$value` and `$credentials` exist for the declarative routing
interpreter only. They are refused in any user-authored expression, and are
absent from the list this endpoint serves, so the editor never offers them.

## Functions

Nineteen, and this is the whole list. A call to anything else fails at parse
time, which means when the workflow is saved rather than when an item reaches
the node.

### Strings

| Call | Arguments | Returns |
| --- | --- | --- |
| `toUpperCase()` | — | the string, upper-cased |
| `toLowerCase()` | — | the string, lower-cased |
| `trim()` | — | the string without leading or trailing whitespace |
| `split(separator)` | 1 | a list of the pieces |
| `replace(old, new)` | 2 | the string with **every** occurrence replaced |
| `startsWith(prefix)` | 1 | boolean |
| `endsWith(suffix)` | 1 | boolean |

### Strings or lists

| Call | Arguments | Returns |
| --- | --- | --- |
| `includes(value)` | 1 | boolean — substring for a string, membership for a list |
| `length()` | — | number — **runes** for a string, entries for a list, keys for an object |

### Any value

| Call | Arguments | Returns |
| --- | --- | --- |
| `toString()` | — | the value rendered as text |
| `toNumber()` | — | a number; fails on a string that is not one |

### Lists and nodes

`first()`, `last()` and `all()` accept either a plain list or a node, so
`$json.tags.first()` and `$('Name').first()` are the same function.

| Call | Arguments | Returns |
| --- | --- | --- |
| `join(separator)` | 1 | a string; list only |
| `first()` | — | the first entry, or undefined when empty |
| `last()` | — | the last entry, or undefined when empty |
| `all()` | — | the whole list |

### Dates

These take a date receiver — `$now` or `$today`, or the result of another date
function.

| Call | Arguments | Returns |
| --- | --- | --- |
| `format(layout)` | 1 | a string, using a **Go** layout such as `2006-01-02` |
| `toISOString()` | — | RFC 3339 in UTC |
| `plusDays(n)` | 1 | a date |
| `minusDays(n)` | 1 | a date |

:::caution
`format` takes a Go reference-time layout, **not** a `YYYY-MM-DD` pattern. An
expression imported from n8n will carry a Luxon pattern, which produces the
pattern text rather than a formatted date.
:::

## Absent values

A path that does not exist resolves to undefined:

```
{{ $json.missing }}           →  null
name: {{ $json.missing }}!    →  "name: !"
```

An expression that is only the missing path yields null; one mixed with literal
text substitutes nothing.

Structural mistakes still fail loudly and name the path: an unsupported root, an
index into something that is not a list, a field read on something that is not an
object, a call to a function that is not on this page, or a call with the wrong
number of arguments.

## What the grammar does not have

No operators — no arithmetic, no comparison, no concatenation beyond
interpolation. No conditionals. No variables. No user-defined functions. No
general call syntax, so a receiver can only be followed by a name from the table
above.

Control flow belongs to the `IF`, `Switch` and `Filter` nodes; shaping belongs to
the Set node's assignment collection; arbitrary computation belongs to the Code
node, which runs in the sandbox described under
[safety boundaries](/concepts/safety-boundaries/).

## Source

`internal/expression/roots.go` (`Roots`), `internal/expression/functions.go`
(`FunctionNames` and every implementation), `internal/expression/expression.go`
(`IsExpression`, `Resolve`), `internal/expression/doc.go` (the rationale),
`internal/api/handlers/nodes.go` (the endpoint that serves this list).
