---
name: kilasflow-node-configuration
description: Use when configuring or reading a node's parameters and shared settings, or when a parameter is missing, hidden, refused or not the one you meant. Triggers on "node describe", "node options", "parameter", "load options", "resourceMapper", "config.invalid".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow node list
  - kilasflow node describe
  - kilasflow node options
  - kilasflow api
  - kilasflow workflow get
  - kilasflow run
kilasflow_operations:
  - list-node-types
  - load-node-property-options
  - load-node-property-schema
  - get-node-icon
  - run-workflow
kilasflow_nodes:
  - kilasflow.httpRequest
  - kilasflow.set
  - kilasflow.switch
  - kilasflow.merge
  - kilasflow.code
kilasflow_expression_roots: []
kilasflow_not_shipped: []
---

## Non-negotiables

1. A parameter name recalled from training data is a silent failure. The catalogue is the only source of a node's shape: read it with `kilasflow node describe <type>` (`list-node-types`) before writing a key. A node's executor reads the keys its own definition declares (nodes/http.go), so a guessed key is a value nothing reads, and a missing required one is refused when the graph compiles, as `config.required` (internal/workflow/compiler.go).
2. Never invent a selectable value. When a property's valid values live on the customer's own service, the list comes from the server: `kilasflow node options <type> --property <key>` (`load-node-property-options`). A made-up value is refused as `config.invalid` at best, and a request against the wrong resource at worst.
3. Shared settings are not parameters. They are stored in `node.settings` rather than `node.parameters`, are validated separately, and the registry refuses a definition that declares one key in both (internal/node/registry.go, `validateGroupsDoNotCollide`).
4. Read the document before you configure it: `kilasflow workflow get <workflowId>` (`get-workflow`) is what is stored, and a node's `typeVersion` decides which registered definition its parameters are read against (internal/node/registry.go, `Resolve`).

## Strong defaults

- Start at the catalogue. `kilasflow node list` (`list-node-types`) prints the types this tenant can see; `kilasflow node describe <type>` reduces that to one entry, at the version the server would resolve a document to (the highest registered), so it describes the node that would actually run (internal/cli/verbs_node.go, `runNodeDescribe`). An unknown type exits 4 with the nearest catalogue names in the message (verbs_node.go, `closeTypeHint`).
- `describe` prints `key`, `kind`, `required`, `default`, `options`, visibility and the nested carriers. Read all of them before editing a parameter: a `default` is the server's answer for an absent value, so a property that declares one is not required (internal/node/registry.go, `requiredParameters`).
- Required-ness is computed per node, not read from a static list, because visibility is conditional: a hidden property is not required, and a required one is only required for the configuration that shows it (internal/node/registry.go, `visibleRequiredParameters`).
- Visibility is `visibleWhen` (equality shorthand) or `displayOptions` (the full rule, which replaces the shorthand), always evaluated against the parameters with defaults filled in (internal/property/property.go, `WithDefaults`, `VisibleProperty`). That is why a field you expected is absent: `kilasflow.merge`'s `fieldsToMatch` appears only when `mode` is `combineByFields` (nodes/core.go, `mergeNode`).
- Some ports are computed from the configuration: `kilasflow.switch` declares one output per rule, and `kilasflow.merge` takes its input count from `numberInputs`, so a connection must name a port the configuration actually produced (nodes/flow.go, `switchNode`; nodes/core.go, `mergeNode`).
- The `kind` decides the stored value shape, and the set of kinds is closed — the server refuses an unknown one at registration (internal/property/property.go, `KnownKinds`). See references/PROPERTY_KINDS.md for every kind and what it stores.
- A value on the customer's service is loaded, not guessed: `kilasflow node options <type> --property <key>` takes `--version`, `--mode` (a resource locator's mode) and `--credential <credentialId>`. A partially configured node goes through the escape hatch instead: `kilasflow api load-node-property-options --path type=<type> --body @node.json`. See references/LOAD_OPTIONS.md.
- A resource mapper's columns are the sibling operation: `kilasflow api load-node-property-schema --path type=<type> --body @mapper.json` (`load-node-property-schema`).
- Presentation is served, not remembered: `kilasflow api get-node-icon --path type=<type>` (`get-node-icon`, with `--query theme=light` or `dark`) answers only for a node that ships artwork; a `builtin:` glyph resolves to a component the editor already has and returns no bytes (internal/node/registry.go, `BuiltinIconPrefix`; internal/api/handlers/nodes.go, `Icon`).
- A document may carry a version this installation does not register: resolution is downward, so a node imported as n8n's Set 3.4 runs against version 1's shape without the document being rewritten, and `typeVersion` left unset means the registry's current version (internal/node/registry.go, `Resolve`; docs/src/content/docs/concepts/node-registry.md, "A worked example").
- Shared settings every built-in declares are `continueOnFail`, `retryOnFail`, `timeoutSeconds`, `maxTries`, `waitBetweenTries` and `alwaysOutputData` (nodes/core.go, `sharedSettings`). The compiler bounds the numeric ones and accepts `onError` as `stopWorkflow`, `continueRegularOutput` or `continueErrorOutput`, adding an `error` output port for the branch form (internal/workflow/compiler.go, `validateSharedSettings`, `withErrorPort`).
- A node's own timeout parameter is not the shared `timeoutSeconds` setting — the two collided, which is why some nodes read a renamed key as well (nodes/core.go, `legacyTimeoutKey`; nodes/http.go, `requestTimeoutSeconds`).
- Worked examples of the two shapes. `kilasflow.httpRequest` declares `method` and `url` as required, `queryParameters` as a `keyValue` property shown only when `sendQuery` is true, and its own `requestTimeoutSeconds` beside the shared setting (nodes/http.go, `httpRequestNode`). `kilasflow.set` requires `assignments` only when `mode` is `manual` and `jsonOutput` only when `mode` is `raw`, so one of the two is always absent by design (nodes/core.go, `setNode`).
- Prove a configuration by running it: `kilasflow run <workflowId> --wait` (`run-workflow`). The compiler answers `config.required` for a missing required parameter or credential and `config.invalid` for a value the node refuses, before an execution record exists (internal/workflow/compiler.go).
- `kilasflow.code` is not how you configure anything: it is a program compiled on demand inside a sandbox, and it is `Unavailable` on a deployment without a toolchain (nodes/code.go; docs/src/content/docs/concepts/node-registry.md, "Unavailable").

## Decision tree

```
what are you configuring?
|
+-- a parameter of a node type
|     -> kilasflow node describe <type>
|        read key, kind, required, default, options, visibility
|
+-- a value from a fixed list
|     -> one of the definition's own options; never a value you invented
|
+-- a value that lives on the customer's service
|     -> kilasflow node options <type> --property <key> [--version] [--mode] [--credential <credentialId>]
|        a partially configured node: kilasflow api load-node-property-options --path type=<type> --body @node.json
|
+-- a resource mapper's columns
|     -> kilasflow api load-node-property-schema --path type=<type> --body @mapper.json
|
+-- a field you cannot see
|     -> the visibility rule, evaluated against parameters with defaults filled in
|
+-- a port that is not there
|     -> a computed port: fix the configuration that produces it (rules, numberInputs)
|
+-- a key you remember from another tool
|     -> not a parameter of this type: do not write it
|
+-- the value only exists while the workflow runs
|     -> an expression in the parameter (the expressions skill), not a new node
|
+-- refused with config.required or config.invalid
      -> the parameter or credential the message names, in references/PROPERTY_KINDS.md
```

## Anti-patterns

- "I know the parameter name" → nothing reads the key, and a required one is refused at compile as `config.required` → `kilasflow node describe <type>` first, and write only the keys it prints.
- "I'll write the select options myself" → the list belongs to the service, so an invented value is refused as `config.invalid` or sends a request against another resource → `kilasflow node options <type> --property <key>`.
- "The field disappeared" → visibility: it is shown only for the mode or toggle you set, and the rule is evaluated against parameters with defaults filled in → read the property's visibility rule and set the field it depends on.
- "More retries and a longer wait will fix it" → the compiler bounds `maxTries` at 8 and `waitBetweenTries` at 300000 ms → choose a value inside the bound; a configuration outside it is refused as `config.invalid`.
- "`onError` is a parameter" → it is a shared setting with three accepted values, and only `continueErrorOutput` declares the `error` output port → set the setting and connect the `error` port if you want the branch.
- "The dropdown is empty so the service has nothing" → an empty list carries a `reason`, and a dependency holding an expression cannot be resolved while editing → read the reason; supply the values the loader's `dependsOn` names, or configure it from a saved document.
- "I'll guess `typeVersion`" → leave it unset for the registry's current version, or name one the catalogue serves; an unregistered version is refused as `node.unknown_version` (internal/workflow/compiler.go).
- "The icon route 404s, so the node does not exist" → a 404 is also what a type scoped to other tenants answers, exactly as if it were unregistered (internal/api/handlers/nodes.go, `Icon`, `List`) → check the catalogue, not the icon route.
- "The save returned 2xx, so the parameters landed" → a save is structural and says nothing about the keys you meant → re-read with `kilasflow workflow get <workflowId>`, then run it.

## Reference files

| File | Read when |
| --- | --- |
| PROPERTY_KINDS.md | you need what a parameter's `kind` stores, the type options, the visibility rules, the shared settings and their bounds, or a locator's and mapper's stored shape |
| LOAD_OPTIONS.md | a property's values come from the customer's own service, a mapper's columns are needed, or a loaded list came back empty or refused |
