# Naming and descriptions

Names in a workflow document are load-bearing: connections and expressions
address nodes by them. This file is what each name is for, what the registry
refuses, and what to check before renaming anything.

## Two names per node: the key and the display name

A node instance in the canonical document has both (internal/workflow/document.go):

- `id` — the node's key. Connections address it: a connection's `source.nodeId`
  and `target.nodeId` carry this value, and it is what a validation error means
  when it says `node "X"`.
- `name` — the display name a person reads, and the name expressions resolve
  against: `$('Name')`, `$items('Name')` and `$node["Name"]` all look a node up
  by this string (internal/expression/roots.go).

Draft validation enforces unique `id`s and non-empty `name`s, and nothing more.
It does **not** refuse two nodes with the same display name — and the runner
records each completed node's output keyed by display name (internal/engine/runner.go),
so two nodes sharing a name overwrite each other's output as far as expressions
are concerned. Keep display names unique even though nothing stops you.

## Renaming a node breaks every expression that names it

`$('Old Name')` resolves against the nodes that have already produced output in
this run, and a name that is not there fails rather than resolving to nothing:

```
$('Old Name') names a node that has not produced output in this run
```

(internal/expression/roots.go). So a rename is a refactor, not a label change:
before saving a rename, search the whole document — every node's `parameters`,
`settings` and any Code node body — for the old name, and update each reference.
The engine tolerates a missing node in one direction only: a node that never ran
under that name is an error, so a half-applied rename fails at run time with the
message above, usually on the first item.

## Node type names come from the registry

The `type` field names a registered type, not a label you choose. The registry
refuses a definition that (internal/node/registry.go):

- has no type or no positive version;
- has no display name or no category;
- declares no group, or a group it does not know;
- has no executor binding;
- declares an input or output port with no name, or with a connection kind the
  document does not model, or two ports with the same name in one direction;
- declares the same key as both a parameter and a shared setting — one of the
  two has to be renamed, because they are separate values at run time;
- claims the `kilasflow.` namespace without being built in: the prefix is
  reserved for built-in nodes (internal/node/registry.go, `BuiltinPrefix`).

A tenant-scoped type is visible only to the tenants it names, and to everyone
else it is absent — a 404 on a specific type is that rule, not a typo to work
around (internal/api/handlers/nodes.go).

## Connection channel names

The `kind` of a connection is one of thirteen channels, spelled exactly as the
document spells them: `main`, then `ai_` followed by lowerCamel —
`ai_agent`, `ai_chain`, `ai_document`, `ai_embedding`, `ai_languageModel`,
`ai_memory`, `ai_outputParser`, `ai_retriever`, `ai_reranker`,
`ai_textSplitter`, `ai_tool`, `ai_vectorStore` (internal/workflow/document.go).

The casing is load-bearing: `ai_language_model` is not a kind, and a compiled
connection whose kind does not match both endpoint ports is refused with
`port.incompatible`.

## What a person reads

- Workflow `name` is required and must not exceed 255 runes
  (internal/workflow/document.go).
- A node's display name is what the editor, the error messages and the
  expression surface all use — so it should say what the node does in the
  workflow's own vocabulary (`Fetch orders`, not `HTTP Request 2`).
- A node definition's display subtitle, when a pack author writes one, may only
  read `$parameter.<key>`: it is rendered from the node's own parameters and
  from nothing else (internal/node/registry.go).
