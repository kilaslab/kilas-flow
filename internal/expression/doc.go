// Package expression evaluates the `{{ … }}` templates a node parameter may
// carry.
//
// The grammar is deliberately not a language. An expression is a root, followed
// by field reads, index reads and calls drawn from a closed allowlist. There
// are no operators, no bare identifiers and no general call syntax, so an
// expression can read the data it is given and transform it, and can do nothing
// else. `require('fs')` is not blocked by a denylist — it cannot be written,
// because a body that does not begin with a supported root never parses.
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
//	$input                    every item on each input port
//	$node["Name"].json.field  another node's first output item
//	$('Name').item            the item of that node this one descends from
//	$('Name').first()         its first item
//	$('Name').last()          its last item
//	$('Name').all()           all of its items
//	$env.KEY                  the allowlisted environment
//	$execution.id             this execution's identity
//	$workflow.name            this workflow's identity
//	$itemIndex                the current item's position
//	$now, $today              the current instant and the start of today
//	$fromAI('name')           a parameter an AI agent fills in
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
// A path that does not exist resolves to undefined rather than failing. An
// expression that is only that path yields null, and one mixed with text
// substitutes nothing:
//
//	{{ $json.missing }}           → nil
//	name: {{ $json.missing }}!    → "name: !"
//
// Everything structurally wrong still fails loudly and names the path: an
// unsupported root, an index into something that is not a list, a field read on
// something that is not an object, or a call to a function that is not on the
// allowlist.
//
// # Functions
//
// Calls resolve at parse time, so naming a function that does not exist fails
// when the workflow is saved rather than on the first item that reaches it. Use
// FunctionNames for the current list; it is served to the editor so the client
// does not keep a copy that drifts from this one.
//
//	{{ $json.email.trim().toLowerCase() }}
//	{{ $json.tags.join(", ") }}
//	{{ $now.plusDays(7).format("2006-01-02") }}
//
// # Lineage
//
// `$('Name').item` reads the item a named node produced, and it is narrower
// than it sounds: it requires that node to have produced exactly one item, and
// otherwise fails with the count and the alternatives —
//
//	node "Many" produced 3 items; use .all(), .first() or .last() to choose one
//
// It does not walk the current item's chain back through the graph. Following a
// lineage is what the name suggests and what a reader of an earlier version of
// this comment was told, so it is worth saying plainly: choosing among several
// items by their correspondence to the current one is not yet possible.
//
// What it does do is refuse rather than guess. It never falls back to the first
// item — that answer is correct only when every node processed exactly one
// item, and confidently wrong otherwise.
package expression
