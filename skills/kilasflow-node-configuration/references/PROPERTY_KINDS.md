# Property kinds, visibility and shared settings

Every node parameter is a `property.PropertyDefinition`, and both the node
catalogue and the credential catalogue validate against it — which is why the
package is a leaf (internal/property/property.go, internal/node/registry.go).
`kilasflow node describe <type>` prints exactly this for one type, at the version
the server would resolve a document to.

## One definition

| Field | Meaning |
| --- | --- |
| `key`, `label`, `description` | the stored name, the caption, the help text |
| `kind` | which control renders it and what value it stores — the closed set below |
| `required` | required for the configurations that show it, not always |
| `default` | what a fresh node (and an absent value) means; under `multipleValues` it describes one element |
| `options` | a select's fixed values, and nothing else |
| `fields` / `groups` | the nested properties of a `collection` / `fixedCollection` |
| `modes` / `mapper` / `assignments` | a locator's modes, a mapper's declaration, an assignment collection's default rows |
| `typeOptions` / `loadOptions` | refinements of the control, and where its values are fetched at edit time |
| `visibleWhen` / `displayOptions` | the visibility rule |

Nested content is in typed fields rather than overloaded onto `options`; a field
whose meaning depended on its sibling kind would produce a JSON schema nothing
can express (internal/property/property.go).

## The fifteen kinds

The set is closed, and an unknown kind is refused at registration
(internal/property/property.go, `KnownKinds`).

| Kind | Stores |
| --- | --- |
| `string` | text; `typeOptions.rows` makes it multi-line |
| `number` | a number |
| `boolean` | true or false |
| `options` | one of the declared `options` values |
| `multiOptions` | several of them, as a list |
| `collection` | an optional group of the properties in `fields`, added as a unit |
| `fixedCollection` | repeatable named groups (`groups[].fields`) |
| `notice` | read-only guidance: it holds no value, is never required, never stored |
| `json` | a raw JSON document |
| `dateTime` | a date and time |
| `keyValue` | rows of name/value pairs |
| `conditions` | the filter conditions the IF, Switch and Filter nodes evaluate |
| `assignmentCollection` | ordered `{name, type, value}` rows whose editor is chosen by each row's type |
| `resourceLocator` | one resource named three ways (searched, by name, by id) plus which way was used |
| `resourceMapper` | a column map typed against whatever a locator picked |

`options` is spelled that way because n8n spells it that way: two names for one
control would make every generated pack guess which one this server speaks.

## Type options

`TypeOptions` refines a control rather than multiplying kinds, and an unknown key
is refused at registration (internal/property/property.go, `TypeOptions`):
`password` masks a field, `rows` makes a string multi-line, `minValue` and
`maxValue` bound a number, `numberPrecision` fixes its decimals,
`multipleValues` makes the property a list, and `multipleValueButtonText` labels
the add button.

`multipleValues` is the one flag to carry across exactly: the property's
`default` then describes **one element**, not the collection. A property with
`multipleValues` and `default: {}` defaults to an empty list whose elements look
like `{}` — it does not default to `{}`.

## Visibility, and why required is computed

- `visibleWhen` is the shorthand: entries on different keys must all match by
  equality, and several entries on one key mean "one of these values".
- `displayOptions` is the full rule — show and hide groups, several accepted
  values per key, operators beyond equality, a type-version gate — and when it is
  set it **replaces** the shorthand, so a property has exactly one rule.
- Visibility is always evaluated against the parameters **with defaults filled
  in** (`WithDefaults`), which is why a rule reading a key nobody ever stored
  still shows the right field.
- A hidden property is not required, so required-ness is computed per node
  (`internal/node/registry.go`, `visibleRequiredParameters`). The compiler
  refuses a missing one as `config.required` at `/nodes/N/parameters/<key>`, and
  a missing credential as `config.required` at `/nodes/N/credentials/<type>`
  (internal/workflow/compiler.go).

## Resource locator

A locator is stored as a self-describing object carrying the `__rl` sentinel —
`{__rl: true, mode, value}`, n8n's own shape — rather than a bare string with a
sibling mode parameter, which loses the pairing the moment visibility hides one
half (internal/property/property.go, `LocatorSentinel`, `WriteLocator`;
`ReadLocator` accepts the old bare string too). Each mode declares how it renders
and, for a list mode, its own loader, so "from list" searches and "by id" does
not. One mode name is refused outright: a mode literally called `expression`
would be indistinguishable from the expression marker, which uses the same
`mode` and `value` keys (`ExpressionModeName`, `ValidateModes`).

## Resource mapper

`resourceMapper` types the columns inside whatever its locator picked. Its
declaration names the schema loader, whether auto-mapping is offered, whether
matching columns are required, and the label of the values column; each column
carries an id, a display name, a type, a required flag, match eligibility,
read-only-ness and any fixed options (internal/property/mapper.go,
`ResourceMapperDeclaration`, `MapperField`). The columns themselves come from the
sibling operation — see LOAD_OPTIONS.md.

## Assignment collection

Rows are `{name, type, value}`; the declared type is a closed set — `string`,
`number`, `boolean`, `array`, `object` — and a row without a name is refused at
registration because it would write nothing (internal/property/property.go,
`AssignmentTypes`, `ValidateAssignments`). One field set twice takes the later
row's value, which is how `kilasflow.set` documents itself (nodes/core.go,
`setNode`).

## Shared settings

Every built-in node declares the same settings block, and they are stored in
`node.settings`, never in `node.parameters` (nodes/core.go, `sharedSettings`):

| Setting | Default | Bound or accepted values |
| --- | --- | --- |
| `continueOnFail` | false | — (a tolerated failure emits an error item) |
| `retryOnFail` | false | — |
| `timeoutSeconds` | 0 | 0 to 86400 |
| `maxTries` | 3 | 1 to 8, shown when `retryOnFail` is on |
| `waitBetweenTries` | 1000 | 0 to 300000 ms, shown when `retryOnFail` is on |
| `alwaysOutputData` | false | — |

The compiler also accepts `onError` in `settings`, one of `stopWorkflow`,
`continueRegularOutput` or `continueErrorOutput`, and adds the node's own
`error` output port when it is the branch form — which is what makes a
connection from that port compile (internal/workflow/compiler.go,
`validateSharedSettings`, `withErrorPort`).

A numeric setting outside its bound, or an `onError` that is not one of the
three, is refused at compile as `config.invalid` at `/nodes/N/settings`.

## Ports, versions and names

- Ports come from the definition, and some are computed: `kilasflow.switch`
  declares one output per rule (bounded by `MaxSwitchOutputs`) and
  `kilasflow.merge` takes its input count from `numberInputs`, while the static
  `inputs`/`outputs` are what an unconfigured node shows (nodes/flow.go,
  `switchNode`; nodes/core.go, `mergeNode`; internal/node/registry.go,
  `PortsFor`).
- A document's `typeVersion` resolves **downward**: the highest registered
  version not greater than the one asked for, or the highest when none is asked
  for. An unknown type is `node.unknown_type`, an unknown-or-too-high version
  `node.unknown_version` (internal/node/registry.go, `Resolve`;
  internal/workflow/compiler.go).
- A type scoped to other tenants is absent from the catalogue, the icon route and
  the loaders, exactly as if it were unregistered; only a document that
  references it reports `node.not_available` (internal/node/visibility.go).
- An undeclared key is not refused generically: the compiler refuses a missing
  required key (`config.required`), a node's own `Validate` may refuse a value it
  cannot use (`config.invalid`, path `/nodes/N/parameters`), and anything else is
  never read by the executor, which reads the keys its definition declares
  (nodes/http.go).

## Source

`internal/property/{property,loader,mapper}.go`, `internal/node/{registry,visibility}.go`,
`internal/workflow/compiler.go`, `internal/cli/verbs_node.go`,
`nodes/{core,flow,http,code}.go`,
`docs/src/content/docs/concepts/node-registry.md`,
`docs/src/content/docs/reference/cli.md` (`kilasflow node describe`).
