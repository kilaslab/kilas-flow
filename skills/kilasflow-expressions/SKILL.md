---
name: kilasflow-expressions
description: Use when a node parameter must read or transform a value from the data flowing through a workflow, or when an expression was refused, resolved to null, or read a field from the wrong item. Triggers on "{{ }}", "$json", "$node", "$now", Luxon, "expression error".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow api
  - kilasflow node describe
  - kilasflow run
  - kilasflow exec get
  - kilasflow exec trace
kilasflow_operations:
  - get-expression-grammar
  - list-node-types
  - run-workflow
  - get-execution
  - stream-execution-events
kilasflow_nodes:
  - kilasflow.code
  - kilasflow.set
kilasflow_expression_roots:
  - $json
  - $input
  - $node
  - $items
  - $(
  - $now
  - $today
  - $env
  - $execution
  - $fromAI
kilasflow_not_shipped:
  - 'No server-side expression evaluation: debug eval is not shipped, so an expression is proved by a run and read back from the trace'
---

## Non-negotiables

1. The allowlist is the server's, not yours. A body must begin with a root on a fixed list, and every call resolves against a closed set of names (internal/expression/roots.go, `Roots`; internal/expression/methods.go, `FunctionNames`). Read the live list with `kilasflow api get-expression-grammar` (`get-expression-grammar`) before writing a root or a function you have not seen served: a name recalled from another tool resolves to nothing here.
2. A parameter is evaluated only when it carries the marker: `{"mode": "expression", "value": "{{ … }}"}`. A plain string containing `{{ }}` is still data (internal/expression/expression.go, `IsExpression`). n8n's leading `=` is translated by the importer and never reaches the evaluator (internal/interop/n8n).
3. There is no debug eval. Nothing evaluates a body for you, and `get-expression-grammar` returns the surface rather than a value. Prove an expression by running the workflow — `kilasflow run <workflowId> --wait` (`run-workflow`) — and read the result back from the run.
4. `$env` is not the process environment and an expression is not where a secret goes: `$env` reaches only variables exported as `KILASFLOW_WORKFLOW_ENV_*` (cmd/kilasflow/main.go, `workflowEnvironment`), and a credential is attached to a node by id.

## Strong defaults

- Read the parameter you are about to fill: `kilasflow node describe <type>` (`list-node-types`) prints the keys, their kinds and which of them accept a value. The body of an expression parameter may mix literal text with `{{ }}` placeholders (internal/expression/expression.go, `Evaluate`).
- The current item is `$json`: `{{ $json.email }}`. A path that does not exist resolves to undefined — null on its own, nothing when mixed with text — while a structural mistake names the path and fails the node (docs/src/content/docs/reference/expression-grammar.md, "Absent values").
- An earlier node's corresponding item is `$node["Name"].json.field`: the paired item when the runtime recorded lineage, otherwise the item at the same position, and never the first item (internal/expression/roots.go, `nodeWrapper`).
- A node's whole output is reached through `$('Name')`: `.first()`, `.last()`, `.all()` and `.params` answer for a node that has produced output in this run, while `$('Name').item` is the single paired item and refuses with a reason when that node emitted several or lost provenance (internal/expression/roots.go, `nodeRootValue`; docs/src/content/docs/concepts/items-and-lineage.md).
- The input side answers both spellings: `$input.item`, `$input.first()`, `$input.last()` and `$input.all()` are n8n's API, and `$input.main[0].field` reads the port map by name (internal/expression/roots.go, `inputSource`).
- Items as a list are `$items('Name')` for a named node and `$items()` for this node's own input; naming a node that produced nothing is an error rather than an empty list (internal/expression/roots.go, `callRoot`).
- Dates: `$now` and `$today` are one instant for the whole evaluation of a parameter tree, read in the workflow's `settings.timezone` and falling back to UTC, so `$today` is the local calendar day (internal/expression/roots.go, `clock` and `location`). `format` and `toFormat` take Luxon tokens — `yyyy-MM-dd`, `d. MMM. y` — not Go's reference layout, and the getters `.year`, `.hour`, `.weekday` read like properties (internal/expression/luxon.go; docs/src/content/docs/reference/expression-grammar.md, "Format tokens").
- `$fromAI('name')` is a marker an AI agent fills, valid only inside a parameter an agent fills; anywhere else it is an error rather than a value (internal/expression/roots.go, `callRoot`).
- Shaping many fields is the `kilasflow.set` node's job: its assignment collection holds `{name, type, value}` rows whose values are expressions, so one node replaces a pile of repeated expressions (nodes/core.go, `setNode`; internal/property/property.go, `Assignment`).
- Reach for `kilasflow.code` last. A parameter expression is evaluated by the runtime as the node runs, once per item (nodes/http.go:253), while a Code node is a WebAssembly program compiled on demand under a time and memory limit (nodes/code.go, `CodeExecutor`), and it is `Unavailable` on a deployment with no toolchain (docs/src/content/docs/concepts/node-registry.md, "Unavailable").
- `$execution.id` and `$execution.mode` name the run, which is what lets an outgoing body carry a per-run identifier (internal/expression/roots.go, `resolveRoot`).
- When an expression fails, read the node run rather than guessing: `kilasflow exec trace <executionId>` (`stream-execution-events`) shows the live feed and names the failing node, and `kilasflow exec get <executionId>` (`get-execution`) carries each node run's error payload as `{code, message}` — the message is the one the evaluator produced, such as `expression root "$foo" is not supported` (internal/engine/service.go, `structuredError`).

## Decision tree

```
what should the parameter read?
|
+-- a field of the item being processed
|     -> {{ $json.field }}          (missing path yields null, not an error)
|
+-- a field of another node's output
|     -> $node["Name"].json.field   the item corresponding to this one
|        or $('Name').first() / .last() / .all() when the count is not one
|        or $('Name').item when exactly one item and provenance is intact
|
+-- every item of a node, or of this node's input
|     -> $items('Name') / $items()
|
+-- everything arriving on a port
|     -> $input.all(), or $input.main[0].field for one port by name
|
+-- the clock
|     -> $now / $today, then .format('yyyy-MM-dd') with Luxon tokens
|
+-- a parameter an AI agent fills
|     -> $fromAI('name')
|
+-- a value only the process knows
|     -> $env.KEY (exported as KILASFLOW_WORKFLOW_ENV_KEY); a missing key is an error
|
+-- a loop, an aggregate, or a helper function
|     -> kilasflow.code (compiled, sandboxed, bounded) - the escape hatch, not the default
|
+-- many fields shaped from one item
      -> kilasflow.set with an assignment collection
```

## Not shipped yet

- No server-side expression evaluation: debug eval is not shipped, so an expression is proved by a run and read back from the trace — there is no operation that evaluates a body or a value against a sample item. Run the workflow with `kilasflow run <workflowId> --wait`, then read the run: `kilasflow exec get <executionId>` carries the node-run trace and its `{code, message}` error payload, and the live feed from `kilasflow exec trace <executionId>` names which node failed.

## Anti-patterns

- "I'll write a Code node to pull one field out" → a compiled sandbox program, a build step and a toolchain dependency for a single read that the parameter language already does → write the parameter as an expression; keep `kilasflow.code` for loops and aggregation.
- "`$node['X'].json` is the first item of X" → on a multi-item flow that silently repeats one row → it is the item corresponding to the current one; ask for a specific one with `$('X').first()`, `.last()` or `.all()`.
- "`$('X').item` gave me a refusal" → the named node emitted several items or lost provenance, and the message names which → `.first()`, `.last()` or `.all()`; do not reach for the first item by reflex.
- "My expression returned null" → a missing path is undefined by design, not a bug → check the field name against the item; a crash instead means a structural fault: an unsupported root, an unknown callable, a node that never ran, or a value whose lineage is unknown.
- "`$env` is empty / `$env` failed" → only variables exported with the `KILASFLOW_WORKFLOW_ENV_` prefix are visible, and a key that is not there is a loud error rather than an empty string → export the prefixed name (cmd/kilasflow/main.go, `workflowEnvironment`).
- "`$json.total / 2` gave a concatenated or rounded answer" → arithmetic on a field that is not a number, which is ordinary in JSON that came from somewhere else → convert explicitly with the value methods (`toNumber`, `toFixed`) before computing.
- "`format('YYYY-MM-DD')` did not render the date" → that is a Go reference layout, and this dialect reads Luxon tokens, where only `yyyy` is the four-digit year and a token outside the supported subset is emitted as written → use `yyyy-MM-dd` and check the token table (internal/expression/luxon.go; docs/src/content/docs/reference/expression-grammar.md).
- "I'll rewrite every field with an expression in each node" → the same reads repeated and drifting apart → one `kilasflow.set` with an assignment collection.
- "It saves, so the expression is valid" → the server parses a body when it evaluates it, not when the document is saved (internal/expression/evaluator.go, `evaluate`) → the first run is where a bad root or callable shows up, so run it and read the trace.

## Reference files

| File | Read when |
| --- | --- |
| EXPRESSION_ROOTS.md | you need the root table, what each root yields, the node accessors, the lineage rule, the `$env` narrowing, or the date dialect |
