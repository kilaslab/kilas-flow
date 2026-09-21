# Loading a property's values

Some parameters have no fixed values to declare: the valid ones live on the
customer's own service. Those are resolved at edit time by two operations, and
the loader is always taken from the registered definition — never from the
request, because the request is an unsaved, partially configured node straight
from a browser (internal/loadoptions/loadoptions.go, `Load`;
internal/api/handlers/nodes.go, `LoadOptions`).

## The two operations and how to reach them

| Operation | Path | For |
| --- | --- | --- |
| `load-node-property-options` | `POST /node-types/{type}/load-options` | an option list: `{label, value}` pairs |
| `load-node-property-schema` | `POST /node-types/{type}/load-schema` | a resource mapper's columns |

A list that depends only on the property is one verb away:

```bash
kilasflow node options kilasflow.postgres --property table --mode list --credential <credentialId>
kilasflow api load-node-property-options --path type=<type> --body @node.json
kilasflow api load-node-property-schema --path type=<type> --body @mapper.json
```

`kilasflow.postgres` is the worked example worth knowing: its `table` locator's
`list` mode loads from the internal `sql.tables` loader with the `schema`
parameter as its dependency and the `postgres` credential type, and its
`columns` resource mapper loads from `sql.mappingColumns` with `schema` and
`table` as dependencies (nodes/postgres_v2.go; internal/loadoptions/sql.go).

`--property` is required and the verb refuses to run without it; `--version`,
`--mode` and `--credential` are sent only when given (internal/cli/verbs_node.go,
`runNodeOptions`). A node that is already half configured is more than the verb
exposes: `parameters` and `workflowId` are part of the operation's body and only
the escape hatch carries them (internal/api/handlers/nodes.go, `loadOptionsInput`).

## The request body

| Field | Meaning |
| --- | --- |
| `property` | the parameter key whose values to load (required) |
| `version` | the node type version; omit for the registered default |
| `mode` | for a resource locator, which mode is asking |
| `parameters` | the node as configured so far — the loader's dependencies read it |
| `credentialId` | a credential **in the caller's own tenant**; inline credential fields are never accepted |
| `workflowId` | bounds an internal lookup for an embed session |

## What a loader is

`OptionsLoader` is data, never a function name: the server resolves a declared
loader, so a request cannot choose what runs (internal/property/loader.go).

| Field | Meaning |
| --- | --- |
| `source` | `http` (an outbound request, governed by the egress policy) or `internal` (a lookup inside this process) |
| `endpoint` | an HTTP path where `{{ key }}` is substituted with that dependency's value, **URL-escaped** |
| `method` | the HTTP method; empty means `GET` |
| `baseUrlParameter` | the parameter holding the service's own base address, prepended and validated as a URL rather than escaped |
| `credentialType` | the credential type the request is signed with |
| `itemsPath` | a dot-separated path to the list in the response; empty means the response is the list |
| `labelTemplate` | renders each option's label from the item's fields |
| `valueField` | the field each option's value is read from |
| `name` | which internal loader to run, resolved by name and never by request |
| `dependsOn` | the parameter keys the loader reads — also what tells the panel to refetch |

An `http` loader needs an endpoint and a value field; an `internal` one needs a
name (internal/property/loader.go, `ValidateLoader`).

## The result

```json
{ "options": [ { "label": "…", "value": "…" } ], "reason": "…" }
```

`reason` explains an empty list the caller can act on, rather than leaving an
empty dropdown that reads as "the service has nothing"
(internal/loadoptions/loadoptions.go, `Result`). A successful load is served
`Cache-Control: private, max-age=30` and cached in-process under a key that
includes the tenant, the workflow, the loader and every dependency value
(internal/api/handlers/nodes.go; internal/loadoptions/loadoptions.go,
`cacheKey`).

## Refusals, and what each means

| Status | Message | Cause |
| --- | --- | --- |
| 404 | that node type is not registered | a wrong type, or one this tenant cannot see |
| 404 | that node type has no such property | the key is not a parameter of that type — the answer to a guessed name |
| 422 | that property's options are fixed and do not need loading | the property declares its own `options` |
| 422 | that resource locator has no list for the mode you asked for | the `mode` has no loader of its own |
| 422 | that property is not a resource mapper and has no column list | `load-node-property-schema` against any other kind |
| 422 | the node type version is not a decimal number | a malformed `version` |
| 502 | the loader's own message | the outbound request failed, the service answered 4xx/5xx, or the body was not JSON |
| 403 | this embed session is scoped to workflow … | an embed session asking for another workflow's options |

An empty list with a reason is a **200**, not an error. The two reasons met most
often are a dependency that holds an expression — which cannot be resolved while
editing, because there is no item and no run, and a guessed value would produce a
confidently wrong list — and a missing credential
(internal/api/handlers/nodes.go, `LoadOptions`; internal/loadoptions/
loadoptions.go, `loadHTTP`).

## Credentials and reach

- A loader that declares a credential type needs one of that type, resolved by id
  under the caller's tenant: a credential from another tenant resolves to
  nothing, so the answer never confirms that it exists elsewhere
  (internal/loadoptions/loadoptions.go, `withCredential`).
- An outbound loader is checked against the same egress policy the HTTP node
  applies, **before** the request is dialled, and the credential's own allowed
  hosts bound the target too — a host the credential cannot reach is refused
  rather than contacted (internal/loadoptions/loadoptions.go, `loadHTTP`;
  internal/safehttp).
- The remote response body is never echoed into the error: it may carry whatever
  the credential just authenticated against.
- An embed session may read with a credential only when the published revision it
  is confined to actually uses it, and only for its own workflow
  (internal/api/handlers/nodes.go, `scopeFor`;
  docs/src/content/docs/reference/api-contract.md).

## A resource mapper's columns

`load-node-property-schema` reuses the same body, the same registry validation
and the same tenancy gate, and answers with a column list rather than an option
list: each column carries an `id`, a `displayName`, a `type`, a `required` flag,
`canBeUsedToMatch`, `defaultMatch`, `readOnly` and any fixed `options`
(internal/api/handlers/nodes.go, `LoadSchema`, `MapperColumn`).

It is deliberately **uncached** (`Cache-Control: no-store`), unlike an option
list: a mapping validated against a stale column set would refuse a column that
exists or accept one that no longer does. Its loader is internal-only today,
because a schema source over HTTP would need a response shape nothing yet
describes (internal/loadoptions/schema.go).

## Source

`internal/api/handlers/nodes.go` (`loadOptionsInput`, `LoadOptions`,
`LoadSchema`), `internal/loadoptions/{loadoptions,schema}.go`,
`internal/property/loader.go`, `internal/cli/verbs_node.go`,
`docs/src/content/docs/reference/cli.md` (`kilasflow node list`, `kilasflow node describe`, `kilasflow node options`),
`docs/src/content/docs/concepts/node-registry.md`.
