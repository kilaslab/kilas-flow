# Validation checklist

Three different things can refuse a workflow, at three different moments, and
the message says which. This file is what each refusal means and the fix.

## 1. The document does not decode

`create-workflow` and `update-workflow` decode the body strictly before
anything else looks at it (internal/workflow/document.go, `DecodeDocument` and
`validateJSONSchemaShape`):

- a field the document shape does not define is refused rather than ignored;
- `nodes` and `connections` must be arrays and `settings` an object, even when
  empty — a missing array is not an empty array;
- every node needs `id`, `name`, `type`, `typeVersion` and `position`, and
  `position` needs `x` and `y`;
- every connection needs `id`, `kind`, `source` and `target`, and each endpoint
  needs `nodeId` and `port`.

The fix is the shape, not a retry: a decoder error names the field it wanted.

## 2. The draft is invalid

A document that decodes can still fail draft validation (`ValidateDraft` in
internal/workflow/document.go, applied by both handlers). It answers **422**
with the detail `workflow draft is invalid` and the first fault as its message
(internal/api/handlers/workflows.go, `draftProblem`).

| Message | What it means | Fix |
| --- | --- | --- |
| `workflow schemaVersion N is unsupported` | the only accepted value is 1 (`workflow.CurrentSchemaVersion`) | send `"schemaVersion": 1` |
| `workflow id is required` | create is sent a document with an empty id | remove the id: the server assigns it (create) or takes it from the path (update) |
| `workflow name is required` / `must not exceed 255 characters` | the workflow name is missing or longer than 255 runes | give it a name a person would recognise |
| `workflow nodes, connections, and settings are required` | one of the three was omitted or sent as `null` | send all three, empty ones as `[]` or `{}` |
| `workflow node id is required` / `node id "X" is duplicated` | node keys must be present and unique | give every node its own key |
| `workflow node "X" name is required` | a node has no display name | name it; expressions refer to nodes by this name (see NAMING_AND_DESCRIPTIONS.md) |
| `workflow node "X" type is required` | the node has no type | set the registered type, e.g. `kilasflow.set` |
| `workflow node "X" credential "c" reference is required` | a credential entry is an empty string | reference a credential by id, or remove the entry |
| `workflow connection id is required` / `connection id "X" is duplicated` | connection keys must be present and unique | give every edge its own key |
| `workflow connection "X" kind is invalid` | the kind is not one of the thirteen channels | use the `ai_`-prefixed lowerCamel spelling from internal/workflow/document.go |
| `workflow connection "X" source and target endpoints are required` | an endpoint is missing `nodeId` or `port` | name both ends |

Draft validation deliberately accepts a work-in-progress graph: it is the
compiler, not the save, that requires a runnable one.

An **update** carries one more refusal: when the base version it was given
(`If-Match` header, or `baseVersionId` in the body) is not the latest revision,
the answer is **409** — "This workflow changed since you loaded it". Re-read the
workflow, re-apply the change, save again (internal/api/handlers/workflows.go).

## 3. The graph does not compile

Compilation is what proves a graph runs, and it happens on **activation**
(internal/repository/workflows.go, inside the activation transaction) and when a
**run** is requested (internal/repository/executions.go). A compile failure is
**422** `workflow validation failed`, and each issue carries a stable code, a
JSON path, and the node or connection it is about (internal/workflow/compiler.go;
the code and ids are attached by `compileProblem` in
internal/api/handlers/workflows.go).

| Code | What it means | Fix |
| --- | --- | --- |
| `workflow.invalid_topology` | the graph itself is wrong: no nodes at all, a connection whose ends are not registered nodes, a duplicate connection, or a cycle that is not a back edge closing onto a loop node | fix the shape the message names |
| `node.unknown_type` | the node's `type` is not a registered type | list the types from the server and use one of them |
| `node.unknown_version` | the `typeVersion` is not a version that type registers | use the registered version, or leave `typeVersion` unset for the current one |
| `node.not_available` | the type exists but is scoped to other tenants, so it is not visible to this one | pick a type your tenant can see. The catalogue is served per tenant, and a scoped type is absent from it exactly as if it were unregistered (internal/api/handlers/nodes.go) |
| `port.unknown` | a connection names a port the node does not declare | use a declared port name |
| `port.incompatible` | the connection's kind does not match both endpoint ports | an `ai_languageModel` edge connects to a language-model port, not a `main` one |
| `port.full` | the destination port already has as many connections as it accepts | remove an edge, or route through a node that merges |
| `port.required` | a port the node declares as required has no connection | connect it |
| `port.node_not_allowed` | the port names which node types it accepts, and this end is not one | put a node the port accepts on that end |
| `config.required` | a parameter the node declares as required is absent, or a credential it requires is unattached | set the named parameter (`/nodes/N/parameters/<key>`) or attach the named credential (`/nodes/N/credentials/<type>`) |
| `config.invalid` | a parameter or shared setting is present but not something the node accepts | use the declared kind and options (see the node-configuration skill); the path says whether it is a parameter or a setting |

`typeVersion` left unset is legal and means "the registry's current version";
the compiler resolves it.

## Reading a diagnostics report

`kilasflow workflow diagnostics <workflowId>` (`workflow-diagnostics`, with
`--version-id` to read an older revision) returns the import report stored with
a revision (internal/api/handlers/interop.go). It is empty for a workflow built
in the editor — `source` is what tells an empty report from a revision nobody
imported:

- `source` — what was imported, when anything was;
- `importedAt` — when the import ran;
- `issues[]` — every element the import could not carry faithfully, each with a
  `severity` of `blocking`, `lossy` or `dropped` (internal/interop/n8n/n8n.go):
  `blocking` stops the workflow running, `lossy` was carried differently and
  `dropped` was not carried at all.

The report is stored per revision by migration
`000012_workflow_import_diagnostics`, so re-reading an old revision answers what
that revision was imported as, not what the newest one is.
