// Package expression evaluates the `{{ … }}` templates a node parameter may
// carry.
//
// The bodies are JavaScript expressions, which is what n8n evaluates them as.
// The grammar covers the expression subset those workflows actually use:
// literals, array and object literals, template literals, member and index
// access, optional chaining, calls, arrow functions, unary and binary
// operators, the ternary, `??`, `&&` and `||`. A parameter can read the data it
// is given and transform it with the functions on this package's list, and can
// do nothing else.
//
// What is deliberately absent is anything statement-level: there is no
// assignment, no `;`, no comma operator, no declaration, and no access to the
// host. `require('fs')` is not blocked by a denylist — it cannot be written,
// because a body that does not begin with a supported root never parses, and
// every callable name is checked against the allowlist when the expression is
// parsed. Widening the syntax from the root-and-accessors grammar this package
// started with removed a door that blocked legitimate workflows; it did not
// open one onto code.
//
// # Marking an expression
//
// A parameter is evaluated only when it carries the explicit marker
// `{"mode":"expression","value":"…"}`. A fixed string containing `{{ }}` is
// still data. n8n marks an expression with a leading `=` on a plain string
// instead; the importer translates between the two and this package never sees
// that form.
//
// # Roots
//
//	$json                     the current item's fields
//	$input.item               the item being processed
//	$input.first()/last()/all()  the items on this node's main input
//	$input.main[0].field      the port map, which KilasFlow also documents
//	$items('Name')            another node's items
//	$node["Name"].json.field  another node's corresponding output item
//	$('Name').item            the item of that node this one descends from
//	$('Name').first()/last()/all()
//	$('Name').params          that node's own configuration
//	$env.KEY                  the allowlisted environment
//	$execution.id             this execution's identity
//	$workflow.name            this workflow's identity
//	$itemIndex, $runIndex     the current item's and run's position
//	$vars                     the workflow's static variables
//	$now, $today              the current instant and the start of today
//	$fromAI('name')           a parameter an AI agent fills in
//
// A lone `{{ $input }}` is the port map, `{"main": [ … ]}`, which is what a Set
// node assigns from it; the accessors above resolve on top of that shape.
// Nothing this package owns ever reaches a parameter: a lone `{{ $now }}` is a
// time.Time, a namespace such as `{{ JSON }}` is `{}` because every member of
// one is a function, and a function or a lineage refusal is an error rather
// than a value. A `{{` inside surrounding text still resolves through the same
// rules, with a list joining by comma and an object rendered as JSON.
//
// # Routing roots
//
// Three more roots exist for one caller, the declarative interpreter in
// `internal/routing`, and are refused everywhere else: `$parameter` reads the
// node's own resolved parameters, `$value` is the property a `routing.send`
// template is rewriting, and `$credentials` carries the **non-secret** fields of
// the node's credential — a base URL, never a token. They are absent from
// Roots(), so the editor never offers them and no user-authored expression is
// written against them. They exist here rather than in a second evaluator
// because a second evaluator over tenant-authored data is a second attack
// surface that would drift from this one within a release.
//
// # Absent values
//
// Reading something that is not there follows JavaScript: it is undefined, not
// an error. That covers a missing object key, an index past the end of a list
// or a string, and a field read on a number or a string. An expression that is
// only that path yields null, and one mixed with text substitutes nothing:
//
//	{{ $json.missing }}           → nil
//	{{ $json.tags[5] }}           → nil
//	name: {{ $json.missing }}!    → "name: !"
//
// Everything structurally wrong still fails loudly and names the path: an
// unsupported root, a call to a name that is not on the allowlist, a syntax
// error, or a read of a value that carries no lineage.
//
// An absent value inside a hand-built object is left out of JSON rather than
// written as null — `JSON.stringify({a: undefined, b: 1})` is `{"b":1}`, as in
// JavaScript — while an array slot keeps its null and a non-finite number
// becomes one, because JSON cannot carry NaN or Infinity.
//
// # Functions
//
// Calls resolve at parse time, so naming a function that does not exist fails
// when the workflow is saved rather than on the first item that reaches it. Use
// FunctionNames for the current list; it is served to the editor so the client
// does not keep a copy that drifts from this one.
//
// Every name that is also a JavaScript name behaves the way JavaScript does,
// because a workflow written for n8n assumes it does. `replace` replaces the
// first occurrence and `replaceAll` the rest; `includes` compares with
// SameValueZero rather than stringifying; `join` defaults to a comma; `.length`
// is a property rather than a call; a list in text joins with commas and an
// object is JSON rather than Go's `map[k:v]`.
//
//	{{ $json.email.trim().toLowerCase() }}
//	{{ $json.tags.join(", ") }}
//	{{ $json.profile.toJsonString() }}
//	{{ JSON.stringify($json.body, null, 2) }}
//	{{ Object.keys($json.rows).length }}
//	{{ $json.items.filter(i => i.price > 10).map(i => i.name) }}
//
// # Dates
//
// `$now`, `$today` and `DateTime` are Luxon-shaped: `toFormat`/`format` take
// Luxon tokens (`yyyy-MM-dd`, `d. MMM. y`), `plus`/`minus` take a duration
// object (`{days: 1}`) or a number with a unit, and `startOf`/`endOf` take a
// unit name. They read the workflow's `settings.timezone`, so `$today` is the
// local calendar day rather than midnight UTC.
//
// A date never escapes as this package's own type. A lone `{{ $now }}` is a
// time.Time — what the Set, DateTime and IF nodes accept — and a date inside an
// object or a list marshals as ISO 8601 with milliseconds and an offset, which
// is the string n8n writes.
//
// # Lineage
//
// `$('Name').item` reads the item of a named node that the current item
// descends from, and it refuses rather than guesses: when the runtime could not
// establish the correspondence it fails with the reason and the alternatives —
//
//	node "Many" produced 3 items; use .all(), .first() or .last() to choose one
//
// `$node["Name"].json` — the legacy form every imported expression uses — reads
// the paired item when there is one and otherwise the item at the same
// position, which is the correspondence n8n uses. It never falls back to the
// first item for every current item, which is correct only when the source node
// produced exactly one.
package expression
