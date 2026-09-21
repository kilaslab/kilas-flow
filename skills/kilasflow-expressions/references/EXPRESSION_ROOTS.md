# Expression roots and the value surface

The complete surface is served by the evaluator itself:
`GET /api/v1/expression-grammar` (`get-expression-grammar`) returns exactly two
lists — `roots` from `expression.Roots()` and `functions` from
`expression.FunctionNames()` (internal/api/handlers/nodes.go, `Grammar`). The
endpoint is authoritative for the instance in front of you; this file is the
shape of each group and the behaviour that surprises an author.

## The marker decides whether anything is evaluated

A parameter is an expression only as
`{"mode": "expression", "value": "{{ $json.email }}"}`. `IsExpression` requires
both keys, so a fixed string that happens to contain `{{ }}` is still data
(internal/expression/expression.go). Resolution walks the whole parameter tree,
so the marker may sit anywhere inside a nested object or list. An imported n8n
workflow writes the same body behind a leading `=` on a plain string; the
importer translates between the two, so that form never reaches the evaluator
(internal/interop/n8n).

## Roots

A body must begin with one of these (internal/expression/roots.go, `Roots`).

| Root | Yields |
| --- | --- |
| `$json` | the current item's fields |
| `$input` | n8n's input API over the port map underneath it |
| `$node` | every completed node by display name — `$node["Name"].json.field` |
| `$items` | a function: `$items('Name')` is that node's items, `$items()` this node's input items |
| `$(` | the prefix of the callable form `$('Name')` |
| `$env` | the allowlisted environment |
| `$execution` | `.id`, `.mode`, `.resumeUrl`, `.approvalUrl` |
| `$workflow` | `.id`, `.name`, `.active` |
| `$itemIndex`, `$runIndex`, `$vars` | the current item's position, this node's run counter, and the workflow's static variables |
| `$now`, `$today` | the current instant, and the start of today, as dates |
| `$fromAI` | a function: `$fromAI('name')` is a parameter an agent fills |

`IsCallableRoot` is the server's own answer to "is this a function": `$fromAI`,
`$items` and `$(` are, everything else holds a value. Calling a value root, or
reading a function root without calling it, is refused with a message that says
which of the two you did. `$parameter`, `$value` and `$credentials` belong to the
declarative routing interpreter inside a node pack and are refused in any
expression a person writes; they are absent from the served list, which is why no
editor offers them (internal/expression/roots.go, `resolveRoot`).

## Node accessors, and the one that refuses

| Form | Yields |
| --- | --- |
| `$node["Name"].json.field` | a field of the item corresponding to the current one |
| `$node["Name"].field` / `.binary` | the same value unwrapped, and its attachments |
| `$('Name').item` | the paired item, or a refusal naming the reason |
| `$('Name').first()` / `.last()` / `.all()` | its first, last, or every item |
| `$('Name').params` / `.isExecuted` | that node's resolved configuration, and whether it produced output |

`$node["Name"].json` is the **corresponding** item — the paired item when the
runtime established lineage, otherwise the item at the same position — never the
first item, which is silently wrong on any multi-item flow
(internal/expression/roots.go, `nodeWrapper`).

`.item` does not fall back: it resolves only when the named node produced exactly
one item whose provenance is intact, and otherwise fails with the reason rather
than a plausible wrong item — the node recorded no provenance, an item is marked
lost so the correspondence changed, the node produced nothing, or it produced
several and you should use `.all()`, `.first()` or `.last()`
(internal/expression/roots.go, `nodeRootValue`;
docs/src/content/docs/concepts/items-and-lineage.md).

Naming a node that has not produced output in this run is an **error** for
`$('Name')` and `$items('Name')`. `$node["Name"]` behaves the other way: it is a
field read on a map, so an unknown name yields null.

## `$input`, in both spellings

`$input.item`, `$input.first()`, `$input.last()`, `$input.all()` and
`$input.isExecuted` are n8n's object API; `$input.main[0].field` reads the port
map by name. `$input.params` is undefined, as it is in n8n, and when there is no
`main` port the n8n accessors read the first port in name order
(internal/expression/roots.go, `inputSource`).

## `$env` is one prefix wide

`$env.KEY` reads only a variable exported as `KILASFLOW_WORKFLOW_ENV_KEY` — the
prefix required when setting it, stripped when reading it — and nothing else in
the process environment is reachable, which is where the database DSN and the
credential master key live (cmd/kilasflow/main.go, `workflowEnvironment`). The
whole map is still readable: `Object.keys($env)` lists what reached the workflow.
A name that is not there is a loud error rather than an empty string —
`$env.FOO is not set; only variables exported as KILASFLOW_WORKFLOW_ENV_FOO reach a workflow`
(internal/expression/roots.go, `envSource`).

## The clock and the date dialect

`$now` is the instant every expression in one parameter tree reads, and `$today`
is the start of that day, so two expressions in the same node cannot disagree
about the time. Both read the workflow's `settings.timezone` and fall back to
UTC, which is what makes `$today` the local calendar day
(internal/expression/roots.go, `clock`/`location`).

A date carries the method surface an author expects — `plus`, `minus`,
`plusDays`, `startOf`, `endOf`, `diff`, `setZone`, `toISO`, `toISODate`,
`toMillis`, `toUTC`, `isValid` — plus Luxon's getters as properties (`.year`,
`.month`, `.day`, `.hour`, `.weekday` with Monday as 1, `.zoneName`, `.offset`),
in internal/expression/luxon.go and internal/expression/methods.go:
`{{ $today.plus({days: 14}).toISODate() }}` and
`{{ $now.setZone('Asia/Jakarta').toFormat('d. MMM. y') }}`.

`format` and `toFormat` read **Luxon** tokens (`yyyy`, `MM`, `MMM`, `dd`, `HH`,
`mm`, `SSS`, `Z`, `q`, …), never a Go reference-time layout, and text inside
`'…'` or `[…]` is literal. `DateTime.fromFormat(text, pattern)` reads back a
smaller subset and fails naming the pattern rather than returning a plausible
date, and a lone `{{ $now }}` is the `time.Time` the Set, DateTime and IF nodes
accept (docs/src/content/docs/reference/expression-grammar.md).

## Functions

The list is closed and served: strings, lists, any-value extractors and coercions
(`toString`, `toNumber`, `toJsonString`, `isEmpty`, `extractEmail`,
`extractNumber`), numbers (`toFixed`), the namespaces `JSON`, `Object`, `Array`,
`Math`, `Number` and `DateTime`, and the plain globals `parseInt`, `parseFloat`,
`isNaN`, `encodeURIComponent` and the rest (internal/expression/globals.go).

Where a name is also a JavaScript name it behaves as JavaScript does: `replace`
replaces the first occurrence and `replaceAll` every one, `includes` on a list
compares with SameValueZero rather than stringifying, `.length` is a property
rather than a call and is defined on strings and lists but not on objects, and
`first()`, `last()` and `all()` accept a plain list, a node or an input source
(internal/expression/methods.go). `$jmespath()` exists as a name and refuses: it
is not implemented, and a refusal beats a plausible wrong answer.

## Absent values fail softly; wrong shapes fail loudly

A path that does not exist is undefined — null on its own, nothing when mixed
with text. A structural fault fails and names the path: an unsupported root, a
call to a name that is not served, the wrong number of arguments, a syntax error,
a read from a node that never ran, or a value whose lineage is unknown. The split
is deliberate: a missing field is ordinary, while a typo in a function name is a
mistake only the author can fix (internal/expression/doc.go).

| Message | Cause |
| --- | --- |
| `expression root "$foo" is not supported` | the root is not on the served list |
| `expression contains unsupported syntax at "…"` | statement-level or otherwise unparseable syntax |
| `$('X') names a node that has not produced output in this run` | a name nothing produced under |
| `$env.FOO is not set; only variables exported as … reach a workflow` | a key outside the prefix |
| `setZone("X") is not a known IANA zone` | an unknown zone on a date |

The evaluator parses a body when it evaluates it
(internal/expression/evaluator.go, `evaluate` → `parseExpression`), so a refused
root or callable surfaces as a failed node run — `node.failed` with a
`{code, message}` payload — rather than as a save-time refusal
(internal/engine/service.go, `structuredError`).

## Source

`internal/expression/{roots,methods,globals,evaluator,parser,luxon,expression,doc}.go`,
`internal/api/handlers/nodes.go` (`get-expression-grammar`),
`internal/property/property.go`, `cmd/kilasflow/main.go`,
`docs/src/content/docs/reference/expression-grammar.md`,
`docs/src/content/docs/concepts/{expressions,items-and-lineage}.md`.
