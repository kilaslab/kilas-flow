---
title: Expression grammar
description: Every root and every function an expression may use, with what each one takes and returns.
sidebar:
  order: 4
---

The complete grammar. For *why* it is shaped this way — the two dialects, the
closed allowlist, the narrowed `$env` — see
[expressions](/concepts/expressions/).

A running server publishes its own list at
`GET /api/v1/expression-grammar`, generated from the evaluator itself, so the
editor validates against the server rather than a copy that drifts from it. That
endpoint is authoritative for the instance you are talking to, and it is the
exhaustive list. This page describes the shape of each group, the behaviours that
surprise an author arriving from n8n, and the limits.

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

## Syntax

A body is a JavaScript expression, not a path. Everything below parses and
evaluates:

- **literals** — numbers, `'…'`, `"…"`, `` `…${…}…` ``, `true`, `false`, `null`,
  `undefined`, array literals and object literals
- **access** — `.field`, `["key"]`, `[0]`, and the optional forms `?.field`,
  `?.[0]`, `?.()`
- **operators** — `+ - * / %`, unary `! - +`, `=== !== == != < <= > >=`, `&&`,
  `||`, `??`, the ternary `? :`, and parentheses
- **calls** — `name(args)`, method calls, expression arguments, and `...spread`
  in an argument list
- **arrow functions** — `t => …`, `(a, b) => …`, `() => …`, usable as the
  argument to `map`, `filter`, `find`, `findIndex`, `some`, `every`, `reduce` and
  `flatMap`

This is the expression subset n8n workflows actually use, and a grammar that
understood only a root followed by field reads refused most of it:

```
{{ $json.items.filter(i => i.price > 10).map(i => i.name) }}
{{ $json.total / 100 }}
{{ $json.nickname ?? $json.name }}
{{ `Order ${$json.id} shipped` }}
```

Refused at parse time — an error when the workflow is saved, not a value:

- assignment (`=`, `+=`, `++`), and the comma operator
- `;`
- declarations (`let`, `const`, `var`), `function`, `class`, `new`, `import`,
  `typeof`, `instanceof`
- a call to a name that is not on the served list. `exec`, `eval`, `require` and
  `constructor` are not blocked by a denylist — they are simply not names this
  grammar has
- a root that is not in the table below
- a bare identifier that is neither an arrow parameter nor a namespace (`JSON`,
  `Object`, `Array`, `Math`, `DateTime`)

There are no comments, and a comma is only legal inside an argument list, an
array or an object.

Nothing here is statement-level. There is no assignment, no declaration and no
import, so a body has nothing to do except read data and transform it — which is
the property the whole design is built around, and widening the syntax did not
weaken it.

## Roots

An expression body must begin with one of these. Anything else is a parse error.

| Root | Yields |
| --- | --- |
| `$json` | the current item's fields |
| `$input` | the n8n input API, and the port map underneath it (see below) |
| `$node["Name"]` | the named node's output (see below) |
| `$('Name')` | the named node, as a receiver for `.item`, `.first()`, `.last()`, `.all()`, `.params` |
| `$items('Name', output?, run?)` | that node's items on one output as a list, output 0 unless `output` names another (an IF's `false` branch is 1); `$items()` is this node's own input items. Only the node's latest run is kept: `run` may name it by its number (`$runIndex` in a loop that runs in step) or as `-1`, and an earlier run is an error rather than the latest run's items |
| `$env` | the allowlisted environment (see below) |
| `$execution` | `.id`, `.mode`, `.resumeUrl` and `.approvalUrl` |
| `$workflow` | `.id`, `.name` and `.active` |
| `$itemIndex` | the current item's position, as a number |
| `$runIndex` | the current run of this node, as a number |
| `$vars` | the workflow's static variables, as an object |
| `$now` | the current instant, as a date |
| `$today` | the start of today, as a date |
| `$fromAI('name')` | a parameter an AI agent fills in |

`$now` and `$today` are fixed for the whole evaluation of one parameter tree, so
two expressions in the same node cannot disagree about the time.

`$fromAI` is an error anywhere other than a parameter an AI agent fills.

`$vars` holds what the runtime was given for this workflow, and nothing else: a
variable that was never set stays absent rather than being invented.

`$input` answers both shapes an author may have written. `$input.item`,
`$input.first()`, `$input.last()`, `$input.all()` and `$input.isExecuted` are the
n8n API; `$input.main[0].field` and `$input.main[0].json.field` read the port map
by name. `$input.params` is undefined, as it is in n8n. When there is no `main`
port, the n8n accessors read the first port in name order.

### Node accessors

| Form | Yields |
| --- | --- |
| `$node["Name"].json.field` | a field of the corresponding item |
| `$node["Name"].field` | the same value, unwrapped |
| `$node["Name"].binary` | its attachments |
| `$('Name').item` | the item this one descends from, or a refusal |
| `$('Name').first()` | its first item |
| `$('Name').last()` | its last item |
| `$('Name').all()` | all of its items |
| `$('Name').params` | that node's own resolved configuration |
| `$('Name').isExecuted` | boolean |

`$node["Name"].json` is the item that **corresponds to the current one**: the
paired item when the runtime established lineage, and otherwise the item at the
same position. It is not the first item. That fallback is correct only when the
source node produced exactly one item, and silently repeats the first row of a
multi-item flow — a Split Out followed by `$node["Split Out"].json.v` returned
the first value three times instead of a, b, c.

`.item` is checked against [paired-item lineage](/concepts/items-and-lineage/)
and gives you the paired item or nothing: when the runtime could not establish
the correspondence it **fails with the reason** — a node that merged unrelated
streams, one that changed the item count, or one that emitted several items with
no lineage — rather than falling back to the first item. To reach one of several,
use `.all()`, `.first()` or `.last()`.

`$('Name')` and `$items('Name')` naming a node that has not produced output in
this run are an **error**, not an absent value: a node that never ran cannot be
what the author meant. `$node["Name"]` behaves the other way — it is an ordinary
field read on a map, so an unknown name resolves to undefined and yields `null`.

### `$env`

`$env.KEY` reads only process environment variables named
`KILASFLOW_WORKFLOW_ENV_KEY` — the prefix is required when setting the variable
and stripped when reading it. Nothing else in the process environment is
reachable.

The whole map is readable — `Object.keys($env)` lists what did reach the
workflow — but a key that is **not** there is a loud error rather than an empty
string:

```
$env.FOO is not set; only variables exported as KILASFLOW_WORKFLOW_ENV_FOO reach a workflow
```

That is deliberate. A missing credential that resolved to `""` sent a request to
an empty base URL and looked like an upstream failure; naming the allowlist turns
a typo into a message the author can act on.

### Roots you cannot use

`$parameter`, `$value` and `$credentials` exist for the declarative routing
interpreter only. They are refused in any user-authored expression, and are
absent from the list this endpoint serves, so the editor never offers them.

## Functions

Calls resolve at parse time, so a call to a name that is not on the list fails
when the workflow is saved rather than on the first item that reaches the node.
`GET /api/v1/expression-grammar` is the exhaustive list; what follows is the
grouping, and the parts of each group that differ from the JavaScript an author
already knows.

### Strings

`toUpperCase`, `toLowerCase`, `trim`, `trimStart`, `trimEnd`, `split`, `replace`,
`replaceAll`, `startsWith`, `endsWith`, `includes`, `indexOf`, `lastIndexOf`,
`slice`, `substring`, `charAt`, `at`, `padStart`, `padEnd`, `repeat`, `match`,
`concat`.

| Call | Note |
| --- | --- |
| `replace(old, new)` | replaces the **first** occurrence, as JavaScript does |
| `replaceAll(old, new)` | replaces every one |
| `.length` | a **property**, not a call: `str.length` works and `str.length()` is refused, exactly as n8n refuses it |
| `includes(text)` | substring test |

`.length` counts runes, so a non-Latin character or an emoji counts once.

### Lists

`join`, `includes`, `indexOf`, `lastIndexOf`, `slice`, `concat`, `sort`, `map`,
`filter`, `find`, `findIndex`, `some`, `every`, `reduce`, `flat`, `flatMap`,
`reverse`, `first`, `last`, `all`, `sum`, `count`, `removeDuplicates`, `unique`.

`map`, `filter`, `find`, `findIndex`, `some`, `every`, `reduce` and `flatMap`
take an arrow function; `reduce` may also take a seed.

| Call | Note |
| --- | --- |
| `join(separator)` | `join()` with no argument joins with `,` |
| `includes(value)` | **SameValueZero**, not a stringified comparison |
| `.length` | a property, not a call |
| `count()` | the number of entries |
| `unique()`, `removeDuplicates()` | the list without repeats |
| `sort()` | JavaScript's default string ordering, returned as a copy |

The `includes` comparison is the trap that bites hardest coming from n8n,
because both answers look equally plausible:

```
{{ [3,1,2,2].includes(2) }}     →  true
{{ [3,1,2,2].includes('2') }}   →  false
```

Comparing entries as text made both of those the opposite of n8n's answer.

`first()`, `last()` and `all()` accept a plain list, a node or an input source, so
`$json.tags.first()` and `$('Name').first()` are the same function.

### Any value, objects and numbers

Any value carries `toString`, `toNumber`, `toBoolean`, `toJsonString`,
`toDateTime`, `isEmpty`, `isNumeric`, and the extractors `extractEmail`,
`extractDomain`, `extractUrl` and `extractNumber`. Numbers add `toFixed`.

`toJsonString()` renders a value as JSON. An object or list interpolated into
text already renders as JSON, rather than Go's `map[k:v]`; a list joins with
commas.

`.length` is defined on strings and lists, not on objects, so an object is
counted through its keys: `Object.keys($json.rows).length`.

### Namespaces

Namespace objects carry the names a JavaScript author expects to find.

| Namespace | Members |
| --- | --- |
| `JSON` | `stringify`, `parse` |
| `Object` | `keys`, `values`, `entries`, `fromEntries` |
| `Array` | `isArray`, `from` |
| `Math` | `abs`, `ceil`, `floor`, `round`, `trunc`, `sqrt`, `cbrt`, `sign`, `log`, `log2`, `log10`, `exp`, `sin`, `cos`, `tan`, `pow`, `hypot`, `min`, `max`, plus `Math.PI` and `Math.E` |
| `Number` | `isInteger`, `isFinite`, `parseInt`, `parseFloat` |
| `DateTime` | `now`, `local`, `utc`, `fromISO`, `fromMillis`, `fromSeconds`, `fromFormat`, `fromJSDate` |

`parseInt`, `parseFloat`, `Number`, `String`, `Boolean`, `isNaN`,
`encodeURIComponent`, `encodeURI`, `decodeURIComponent` and `decodeURI` are also
plain global calls, as they are in JavaScript.

`$jmespath()` exists as a name and refuses with a message: it is not implemented,
and a refusal is better than a plausible wrong answer.

### Dates

A date receiver is `$now`, `$today`, or the result of another date function.

| Call | Arguments | Returns |
| --- | --- | --- |
| `format(pattern)`, `toFormat(pattern)` | 1 | a string, using the tokens below |
| `plus(duration)`, `minus(duration)` | 1–2 | a date — a duration object (`{days: 1}`) or a number with a unit (`plus(1, 'day')`) |
| `plusDays(n)`, `minusDays(n)` | 1 | a date; the same idea spelled the older way |
| `startOf(unit)`, `endOf(unit)` | 0–1 | a date; defaults to the millisecond, and weeks start on Monday as Luxon's do |
| `diff(other, unit)` | 1–2 | a number in the given unit, defaulting to milliseconds |
| `setZone(zone)` | 1–2 | the same instant in an IANA zone; an unknown zone is an error |
| `toISO()`, `toJSON()` | — | ISO 8601 with milliseconds and an offset |
| `toISODate()` | — | `2026-09-19` |
| `toISOTime()` | — | `15:36:41.792+07:00` |
| `toISOString()` | — | UTC with `Z` |
| `toMillis()`, `toSeconds()`, `valueOf()`, `toUnixInteger()` | — | epoch numbers |
| `toObject()` | — | the calendar fields as an object |
| `toJSDate()` | — | a `time.Time` |
| `toUTC()` | 0–2 | the same instant in UTC |
| `isValid()` | — | boolean |

The getters read like properties: `.year`, `.month`, `.day`, `.hour`, `.minute`,
`.second`, `.millisecond`, `.weekday` (Luxon numbering — Monday is 1, Sunday is
7), `.zoneName`, `.offset` (minutes) and `.isValid`.

```
{{ $now.year }}
{{ $today.plus({days: 14}).toISODate() }}
{{ DateTime.fromMillis($json.ts).setZone('Asia/Jakarta').toFormat('d. MMM. y') }}
```

#### Format tokens

`format` and `toFormat` take **Luxon** tokens, not a Go reference-time layout,
because that is what the patterns in imported workflows already are. A pattern
such as `yyyy-MM-dd` is a calendar date, and `d. MMM. y` is a pattern that
appears in real imported workflows; both now produce what their author intended.

| Token | Renders |
| --- | --- |
| `y`, `yy`, `yyyy` | the year as written, two-digit, four-digit |
| `M`, `MM`, `MMM`, `MMMM` | the month as a number, padded, short name, full name |
| `L`, `LL`, `LLL`, `LLLL` | the same, standalone |
| `d`, `dd` | the day of the month |
| `E`/`c`, `EEE`/`ccc`, `EEEE`/`cccc` | the weekday as a number (Monday is 1), short name, full name |
| `H`, `HH`, `h`, `hh` | the hour, 24-hour or 12-hour |
| `m`, `mm`, `s`, `ss` | minutes and seconds |
| `S`, `SSS` | tenths, milliseconds |
| `a` | `AM` or `PM` |
| `Z`, `ZZ`, `z` | the offset (`+07:00`, `+0700`) or the zone name |
| `q` | the quarter |
| `D`, `DD` | `9/19/2026` and `September 19, 2026` |
| `t`, `tt`, `T`, `TT` | time presets |

Text inside `'…'` or `[…]` is literal, which is Luxon's own escaping. A token
outside this subset is emitted as written, so a pattern this runtime cannot read
is visible in the output rather than silently dropped.

`DateTime.fromFormat(text, pattern)` reads back a smaller subset — `y yy yyyy
M MM MMM MMMM d dd H HH h hh m mm s ss SSS a Z ZZ` — and fails naming the pattern
it could not read, rather than returning a plausible date.

#### Timezone, and how a date leaves an expression

`$now`, `$today` and `DateTime.local()` read the clock in the workflow's
`settings.timezone`, falling back to UTC. That is what makes `$today` the local
calendar day rather than midnight UTC. A zone that cannot be loaded falls back to
UTC rather than failing an execution that is already running.

A lone `{{ $now }}` is a `time.Time` — the value the Set, DateTime and IF nodes
already accept. In text, and inside an object or a list, the same instant renders
as ISO 8601 with milliseconds and an offset:

```
2026-09-19T15:36:41.792+07:00
```

## Absent values

A path that does not exist resolves to undefined:

```
{{ $json.missing }}           →  null
name: {{ $json.missing }}!    →  "name: !"
```

An expression that is only the missing path yields null; one mixed with literal
text substitutes nothing.

Structural mistakes still fail loudly and name the path: an unsupported root, a
call to a function that is not on the served list, a call with the wrong number
of arguments, a syntax error, a read from a node that never ran, or a read of a
value whose lineage is unknown.

The split is intentional. A missing field is an ordinary condition in data that
came from somewhere else, and failing on it would make every optional field a
crash. A typo in a function name is a mistake in the workflow, and the only way
the author finds out is to hear about it.

## What the grammar still refuses

Statement-level syntax, and any access to the host. No assignment, no `;`, no
declaration, no `function`, no import, no `this`; every callable name comes from a
closed list and every root from the table above.

That is the sentence that makes "a tenant can write any expression they like"
safe: a node parameter is authored by whoever can edit a workflow, which in an
embedded multi-tenant deployment is a customer's end user. `require('fs')` is not
blocked by a denylist — it cannot be written, because a body that does not begin
with a supported root never parses and an unknown callee never resolves.

Control flow belongs to the `IF`, `Switch` and `Filter` nodes; shaping belongs to
the Set node's assignment collection; reaching the network or the filesystem
belongs to a node, which runs in the sandbox described under
[safety boundaries](/concepts/safety-boundaries/).

## Source

`internal/expression/roots.go` (`Roots`, and how `$node` and `$('Name')`
resolve), `internal/expression/methods.go` (`FunctionNames` and the receiver
methods), `internal/expression/globals.go` (namespaces, global functions and the
routing-only refusal), `internal/expression/parser.go` (the accepted syntax),
`internal/expression/evaluator.go` (evaluation, operators, `$env`),
`internal/expression/luxon.go` (the date format and parse tokens),
`internal/expression/expression.go` (`IsExpression`, `Resolve`),
`internal/expression/doc.go` (the rationale), `internal/api/handlers/nodes.go`
(the endpoint that serves this list).
