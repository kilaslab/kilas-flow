---
title: Expressions
description: The two dialects that mark an expression, the closed grammar that makes a parameter unable to become code, and why $env is narrowed to one prefix.
sidebar:
  order: 4
---

A node parameter can be a fixed value or an expression. An expression reads the
data flowing through the workflow, transforms it a little, and produces a value.
It cannot do anything else, and that is enforced by the grammar rather than by a
list of forbidden things.

## Two dialects, and which one you will see where

This is the single most common source of confusion when moving between the two
formats, so it is worth being explicit.

**n8n marks an expression with a leading `=` on a plain string.**

```json
{ "parameters": { "chatId": "={{ $json.from.id }}" } }
```

**KilasFlow marks one with an explicit object.**

```json
{ "parameters": { "chatId": { "mode": "expression", "value": "{{ $json.from.id }}" } } }
```

Both use the same `{{ }}` interpolation inside. Only the marker differs.

The reason KilasFlow does not simply adopt the `=` prefix is that the prefix is
ambiguous: in n8n, a fixed string that genuinely begins with `=` is
unrepresentable. Requiring an explicit marker means a fixed string containing
`{{ }}` is still just data, and nothing is ever evaluated by accident.

`internal/interop/n8n` translates between the two in both directions, so the
`=` form never reaches the evaluator. An imported workflow's parameters are
rewritten into the explicit form on the way in and back into the `=` form on
export. If you are reading a stored KilasFlow document you will see the object;
if you are reading an n8n export you will see the prefix.

Resolution walks the whole parameter tree, so an expression can sit anywhere
inside a nested object or list, not just at the top level of a parameter.

## What the grammar is

An expression is **JavaScript expression syntax over a closed surface**. It
begins with a root — `$json`, `$node[...]`, `$('Name')`, `$now` and the rest of
the table below — but it is a language, not a single root followed by field
reads: arithmetic and comparison operators, `&&`/`||`/`??`, the ternary,
optional chaining (`?.`), template literals, array and object literals, and
arrow functions inside the list methods all parse. `items.map(i => i.price)`
and `$json.total > 100 ? 'large' : 'small'` are expressions a workflow can be
saved with.

What it is not is a host language. Calls resolve against a closed surface at
parse time — naming a function that does not exist fails when the workflow is
saved, not on the first item that reaches the node — and `require('fs')` is not
blocked by a denylist, it **cannot be written**: there is no `require`, no
`process`, no module access and no way to reach the host at all. Statement-level
syntax is refused too, because a parameter is an expression rather than a
program: no assignment, no `;`, no `let`/`const`, no loop bodies. The shape of
the surface — every root, every method and every namespace — is at
[Expression grammar](/reference/expression-grammar/), and the server serves the
same list at `GET /api/v1/expression-grammar`.

## The roots

| Root | What it reads |
| --- | --- |
| `$json` | the current item's fields |
| `$input` | every item on each input port, plus `.item`, `.first()`, `.last()`, `.all()` and `.isExecuted` |
| `$node["Name"].json.field` | that node's item corresponding to the current one (the paired item, else the same position) |
| `$('Name').item` | that node's single item, when its provenance is intact |
| `$('Name').first()` | its first item |
| `$('Name').last()` | its last item |
| `$('Name').all()` | all of its items |
| `$env.KEY` | the allowlisted environment |
| `$execution.id` | this execution's identity |
| `$workflow.name` | this workflow's identity |
| `$itemIndex` | the current item's position |
| `$now`, `$today` | the current instant, and the start of today |
| `$fromAI('name')` | a parameter an AI agent fills in |

`$now` and `$today` are fixed for the whole evaluation of one parameter tree, so
a node that reads the clock twice sees one instant — and a test can pin it. They
read the workflow's `settings.timezone` and fall back to UTC, and they stringify
as ISO 8601 with milliseconds and an offset rather than as a locale string.
`$vars`, `$runIndex` and `$items('Name')` are also available; the
[grammar reference](/reference/expression-grammar/) lists every root with what
each one supports.

`$fromAI` is gated: it is only meaningful in a parameter an AI agent fills, and
anywhere else it is a clear error rather than a value. Without that gate an
author would use it in an HTTP URL and get something meaningless.

`$('Name').item` is checked against the
[paired-item lineage](/concepts/items-and-lineage/), and resolves only when the
named node produced exactly one item and every item it produced carries intact
provenance. Anything else — a node that merged unrelated streams, one that
changed the item count, or simply one that emitted several items — fails with a
message naming which of those it was.

It never falls back to the first item. That answer is correct only when every
node processed exactly one item, and confidently wrong otherwise, so a refusal
that says why is the better outcome. Reaching a *specific* item of a node that
emitted several is not something `.item` can do today; use `.all()`, `.first()`
or `.last()`.

The list is served to the editor by `GET /api/v1/expression-grammar`, so the
client does not keep a second copy that drifts from the server's. See the
[expression grammar reference](/reference/expression-grammar/) for the roots and
the full function allowlist.

### Three roots you will not see offered

`$parameter`, `$value` and `$credentials` exist for exactly one caller: the
declarative routing interpreter in `internal/routing`, which resolves a
[node pack's](/concepts/node-registry/) request templates. They are refused
everywhere else, and are deliberately absent from the root list served to the
editor, so no user-authored expression is ever written against them.

They live on the one evaluator rather than in a second, private one, and the
reasoning is recorded in the package: a second evaluator over tenant-authored
data would be a second attack surface, and it would drift from this one within a
release.

`$credentials` carries the **non-secret** half of a credential — a base URL,
never a token. The filtering happens at the caller, from the credential type's
own field descriptors, so a type the evaluator has never heard of exposes nothing
rather than everything. See [credentials](/concepts/credentials/).

## Absent values resolve to nothing; wrong shapes fail loudly

A path that does not exist resolves to undefined. An expression that is *only*
that path yields null, and one mixed with text substitutes nothing:

```
{{ $json.missing }}           →  nil
name: {{ $json.missing }}!    →  "name: !"
```

Everything structurally wrong still fails, and names the path: an unsupported
root, an index into something that is not a list, a field read on something that
is not an object, or a call to a function that is not on the allowlist.

The split is intentional. A missing field is an ordinary condition in data that
came from somewhere else, and failing on it would make every optional field a
crash. A field read on a number is a mistake in the workflow, and reporting it is
the only way the author finds out.

## `$env` is narrowed to one prefix

`$env` does **not** expose the process environment. It exposes only variables
whose names begin with `KILASFLOW_WORKFLOW_ENV_`, with that prefix stripped —
so `KILASFLOW_WORKFLOW_ENV_SLACK_URL` is readable as `$env.SLACK_URL` and
nothing else is readable at all.

A name that is not in that map is a **loud error**, not an empty string:
`$env.FOO` fails with `$env.FOO is not set; only variables exported as
KILASFLOW_WORKFLOW_ENV_FOO reach a workflow`. A mistyped credential that
resolved to `""` used to send a request to an empty base URL and look like an
upstream failure; naming the allowlist turns the typo into a message the author
can act on.

The reason is direct: the process environment is where the database DSN and the
credential master key live. A `$env` that read `os.Environ()` would let any
workflow author in any tenant read both.

Two further properties make that hold rather than merely being the intent. The
allowlist is built **once, at composition**, and copied into the execution
service; nothing reads `os.Environ()` during execution, so the set a workflow can
see cannot change under it. And the evaluator has no access to the environment at
all — it reads a map the runtime handed it, which is the same reason the routing
roots have to be granted explicitly rather than assumed.

An operator can of course defeat this by exporting
`KILASFLOW_WORKFLOW_ENV_DSN=…` themselves. Nothing detects that; it is a
deliberate configuration rather than a leak.

## The functions

The full list is served by the evaluator itself and published at
`GET /api/v1/expression-grammar`, and the
[expression grammar reference](/reference/expression-grammar/) describes the
groups. It is a JavaScript method surface: strings, lists, objects, numbers and
dates, plus the `JSON`, `Object`, `Array`, `Math`, `Number` and `DateTime`
namespaces. Where a name is also a JavaScript name it behaves the way JavaScript
does — `replace` replaces the first occurrence and `replaceAll` the rest,
`includes` on a list compares with SameValueZero rather than stringifying, and
`join` with no argument joins with `,`.

```
{{ $json.email.trim().toLowerCase() }}
{{ $json.tags.join(", ") }}
{{ $now.plusDays(7).format('yyyy-MM-dd') }}
```

`format` takes **Luxon** tokens, not a Go layout: `yyyy-MM-dd` and `d. MMM. y`
mean what an n8n author wrote them to mean. `$now` and `$today` also read the
workflow's `settings.timezone`, so `$today` is the local calendar day.

`first()`, `last()` and `all()` accept either a plain list or a node, so
`$('Name').first()` and `$json.tags.first()` are the same function. Both forms
appear in imported workflows, and one function serving both beats two spellings
of the same idea.

## What this deliberately is not

There is no assignment, no declaration, no statement and no user-defined
function: an expression computes a value, it does not run a program. There is
also no host access, which is the part that matters — `$env` reaches only the
variables an operator exported as `KILASFLOW_WORKFLOW_ENV_*`, and nothing in the
surface can open a file, a socket or a module. A workflow that needs a loop, a
variable or a helper function uses the nodes for it: `IF` and `Switch` for
control flow, the Set node's assignment collection for shaping data, the Loop
node for iteration, or — for genuinely arbitrary computation — the Code node,
which runs in the WebAssembly sandbox described under [safety
boundaries](/concepts/safety-boundaries/).

That is the trade the design makes on purpose. Node parameters are authored by
whoever can edit a workflow, which in an embedded multi-tenant deployment is a
customer's end user, so the parameter language is powerful enough to be useful
and has no reach outside the run it is evaluating in.

## API operations

`GET /api/v1/expression-grammar` returns the roots and the function allowlist.
See the [HTTP API reference](/reference/api/).

## Source

`internal/expression/doc.go` (the design rationale, and the most complete prose
on this in the repository), `internal/expression/expression.go` (the marker and
`Resolve`), `internal/expression/parser.go` (what the surface is),
`internal/expression/evaluator.go` (how it evaluates), `internal/expression/methods.go`
(value methods), `internal/expression/globals.go` (namespaces and global
functions), `internal/expression/luxon.go` (dates),
`internal/interop/n8n/parameters.go` (the dialect translation),
`cmd/kilasflow/main.go` (`workflowEnvironment`).
