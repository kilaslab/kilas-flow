---
title: The node registry
description: What a node definition is, how a requested type version is resolved downward, and the three sources a definition can come from.
sidebar:
  order: 5
---

The registry is the catalogue of every node this binary can run. It is assembled
during composition at startup and is read-only for the rest of the process's
life, which is what makes compiled graphs and the metadata served to the editor
deterministic.

Today it holds **49 distinct node types across 56 type-and-version pairs**: 46
types (51 pairs) compiled into the binary, and 3 types (5 pairs) from the
declarative packs it ships with. The list of record is
[`GET /api/v1/node-types`](/reference/api/), which serves one entry per
type-and-version pair: the numbers here are measured from it, and it is what the
editor reads.

## What a definition holds

`node.Definition` is the complete server-owned description of one node at one
version. It splits into three concerns that are worth separating in your head,
because they are served to different audiences.

**Identity and contract** — the type string, the version, the input and output
ports, the parameters, the shared settings, and the credential types the node can
use. This is what the editor renders and what the compiler validates a document
against.

**Behaviour bindings** — `ExecutorID`, `Validate`, `PortsFor`, `LifecycleID`.
`ExecutorID` is deliberately an opaque, server-only identifier and is omitted
from every API response: it is a binding into the executor registry, and a
client that could see it would be reading an implementation detail it has no use
for.

**Presentation** — icon, colour, subtitle template, category, group,
documentation URL. This moved onto the definition from the SPA, and the reason is
worth knowing: everything visual used to be hardcoded in the frontend against
KilasFlow's own node types, so a node the server added arrived on the canvas as a
grey box with no subtitle. That is tolerable for a closed list written in this
repository and stops being tolerable the moment a generated pack brings a hundred
operations nobody will hand-write frontend entries for.

`Group` and `Category` look redundant and are not. `Category` is a panel-grouping
label; `Group` is behavioural and multi-valued — what a node *does*. The picker
used to decide whether a node could start a workflow by comparing its category
display string, which is behaviour inferred from a caption.

`Unavailable` is stamped on the way out by the API rather than set by any
definition, because it is a property of the running deployment rather than of the
node. It carries the reason this install cannot run this node — a distroless
image has no Go toolchain, so it cannot build a Code node — so the editor can say
so before a workflow is saved. It is presentation, not policy: the node still
refuses at run time. A user must never discover after activating a workflow that
the deployment was never able to run one of its nodes.

### Ports are sometimes computed

A definition carries static `Inputs` and `Outputs`, and may also carry a
`PortsFor` callback. A Switch has one output per rule the user wrote and a Merge
has as many inputs as it was told to take, so their ports are not a property of
the type at all. Declaring a fixed maximum instead would show dead ports on the
canvas and refuse to import a workflow with one rule more than the maximum.

The static lists stay, and are what the picker advertises and what the editor
shows for an unconfigured node — a picker cannot ask a node that does not exist
yet how many ports it will have. When `PortsFor` is set, its result replaces both
for *that* node, before any connection is resolved against them.

A port can declare more than a name and a channel. `Required` refuses a save when
nothing is connected — an agent with no language model cannot do anything, and
saying so at compile time beats failing on the first item. `MaxConnections`
bounds how many edges it accepts, which is what stops three language models being
attached to one agent. `AllowedNodeTypes` restricts which node types may connect,
and it is enforced at compile time rather than only in the editor, so an imported
document cannot bypass it.

## Versions are fixed-point decimals

`TypeVersion` is not an integer, because n8n does not use integers — core nodes
are on 4.2, 3.4 and 1.1, and WAHA uses `YYYYMM` values like `202502`. It is not a
float either, because a float is an unsafe map key: `4.2` and
`4.2000000000000002` are different keys for the same version, and a registry
lookup that fails one time in a million is miserable to find.

So it is a fixed-point decimal held as a scaled integer — comparable, ordered,
and a valid map key. The fractional part is a decimal *fraction*, not a second
integer, which means `4.2` and `4.20` are the same version and 4.2 is greater
than 4.15. Reading it as two integers would order those two backwards.

On the wire it stays a JSON number, because that is what n8n writes and what
every already-persisted KilasFlow document says. Emitting a string would have
been an API break dressed up as a refactor. It is parsed from the literal text
rather than through a float, so `4.2` cannot arrive as `4.199999999999999`, and a
quoted number is tolerated because some hosts stringify numeric fields.

## Resolution is downward, never upward

Three lookups exist, and the difference matters:

- `Get(type, version)` returns one **exact** pair. It has one caller: the
  declarative routing interpreter, which has already resolved a version and needs
  that definition rather than a near match.
- `Resolve(type, version)` returns **the highest registered version that is less
  than or equal to the one asked for**, or the highest registered version when
  nothing is asked for. It fails when every registered version is higher than the
  request.
- `Lookup` is `Resolve` reshaped for the compiler, and is how a document is
  validated.

The node-types API serves `List` and resolves individual types with `Resolve`,
so the editor sees the same downward resolution the compiler will apply.

The direction is the whole point. Resolving *upward* would silently run a
workflow written for version 2 against version 3's parameter shape, which is a
behaviour change disguised as a lookup. Resolving downward can only ever give a
workflow the shape it was written for or an older one — and failing outright when
even the oldest registered version is newer says plainly that this installation
cannot run this workflow rather than guessing.

### A worked example

Import an n8n workflow containing a Set node at `typeVersion` 3.4. KilasFlow
registers `kilasflow.set` at version 1 only.

1. The importer maps the type and **keeps 3.4 in the document**. It does not
   rewrite the version down to 1.
2. The compiler asks the registry for `("kilasflow.set", 3.4)`.
3. No exact pair exists. `Resolve` scans every registered version of that type,
   discards any greater than 3.4 — there are none — and returns the highest of
   what remains, which is version 1.
4. The node compiles and runs against version 1's parameter shape.

The value of not rewriting the document is what happens next. The day a version
3.4 of that node is registered, the same document resolves to it and lands on the
right shape, with no migration and no edit. The document recorded what the author
meant; the registry decides what this installation can offer.

The failure case is deliberately loud. Ask for version 1 of a node registered
only at version 2, and compilation fails with `node.unknown_version` rather than
`node.unknown_type` — the compiler asks `HasType` separately so it can tell "no
such node" from "not that version of it".

## Properties

A parameter is described with the shared property language in
`internal/property`, which lives in its own leaf package because two catalogues
need it and neither may depend on the other: a node describes its parameters with
it, and a [credential type](/concepts/credentials/) describes its fields with it.

`Kind` is a **closed set of fifteen**, and it is the single authority both
catalogues validate against — an unknown kind is refused at registration:

`string`, `number`, `boolean`, `options`, `multiOptions`, `collection`,
`fixedCollection`, `notice`, `json`, `dateTime`, `keyValue`, `conditions`,
`assignmentCollection`, `resourceLocator`, `resourceMapper`.

A few of those names encode a decision:

`options` rather than `select`, because that is what n8n calls it, and two names
for one control would mean every generated pack has to remember which one this
server speaks.

`resourceLocator` stores a self-describing object carrying an `__rl` sentinel,
exactly as n8n does, rather than a bare string with a sibling `…Mode` parameter.
A locator is imported and exported far more often than it is authored, and the
sibling form loses the pairing the moment a visibility rule hides one half of
it. One mode name is forbidden: a mode literally called `expression` would make a
locator indistinguishable from the
[expression marker](/concepts/expressions/), which has the same `mode` and
`value` keys.

`assignmentCollection` is its own kind rather than a preset over
`fixedCollection`, which it resembles from a distance. A fixed collection is a
repeatable group of *declared* properties; an assignment's control is decided row
by row by a sibling field, which a generic group cannot express without the panel
special-casing it anyway.

Nested content is carried in typed fields — `Fields` for a collection, `Groups`
for a fixed collection, `Modes` for a locator, `Assignments` for an assignment
collection — rather than overloaded onto `Options`. n8n overloads one field for
all of them, which makes its meaning depend on the sibling kind and produces a
JSON schema the generated TypeScript cannot express as anything better than
`unknown`.

One `TypeOptions` flag has to be carried across exactly, because getting it wrong
silently corrupts every imported node: under `multipleValues`, a property's
`Default` describes **one element**, not the collection. A property with
`multipleValues` and `default: {}` defaults to an empty list whose elements look
like `{}` — it does not default to `{}`.

### Conditional visibility

A property can be hidden until another field has a particular value.
`displayOptions` is the full rule — show *and* hide groups, several accepted
values per key, and gating on the node's type version. `visibleWhen` is a
shorthand for the common case where every condition must match by equality.

When `displayOptions` is set it **replaces** `visibleWhen` rather than combining
with it, so a property has exactly one rule and there is never a question of
which wins. The shorthand is kept because it is genuinely the common case, and
the older single-value form it replaced could not express "one of these", could
not hide, and could not gate on version.

Visibility is always evaluated against parameters *with defaults filled in*. A
rule reading `mode` on a node whose `mode` was never written would otherwise see
nothing and hide a field the user is looking at — which is how a Set node saved
before it had a mode ends up with no visible fields at all.

This is also why required-ness is computed per node rather than read from a
static list. Required stopped being a property of the type the moment visibility
became conditional: a node where a chat ID is required only when the resource is
`message` would otherwise be unactivatable in every other configuration.

## Three sources

`Source` says where a definition came from, and it is set by the registration
path, never read from the definition itself. A pack that could declare itself
built-in would inherit the built-in namespace and win every precedence contest,
so the tag has to be a property of *how* something was registered rather than of
what it claims.

| Source | Meaning | Status |
| --- | --- | --- |
| `builtin` | compiled into this binary | 46 types today |
| `pack` | a declarative node pack | 3 types today |
| `sidecar` | an implementation running outside this process | the constant exists; nothing registers one |

`Register` adds a built-in. `RegisterFrom` adds a pack or a sidecar and
**refuses** `builtin` outright. There is exactly one namespacing rule and it is
absolute: the prefix `kilasflow.` may only be claimed by built-in registration. A
pack registering into it is rejected at startup, because a pack shadowing — or
being mistaken for — a node this project ships is a supply-chain problem rather
than a naming one.

A collision on a `(type, version)` pair is **refused, not silently replaced**,
and the error names both sources. "Last wins" would make the catalogue depend on
load order, which changes when a directory listing does. Built-ins register first
and always win, which is what makes the catalogue independent of load order in
the first place.

### What a pack is

A pack is a JSON manifest. It describes resources, operations and properties, and
carries enough routing metadata for `internal/routing` to build an HTTP request at
run time. There is no Go in a pack, which is what makes it possible for one to be
generated. The manifest is the unit either way; what differs is where it is
found — embedded in the binary with the packs this repository ships, or loaded
from a directory `packs.dir` names, with a checksum sidecar beside it.

The three rejected alternatives are recorded in the package and each is
instructive. Not generated Go: 124 operations of it would drown every future diff
in this repository. Not an OpenAPI document parsed at server start: that puts a
third-party file on the boot path, where a spec change becomes a startup failure
rather than a build failure. And not a dump of the internal types: the file is
shaped the way the thing it describes is shaped — a list of resources, each a
list of operations, each with its request and its parameters — so one operation's
whole story sits in one place and a review diff is local.

A pack **cannot name its own executor.** Loading sets `ExecutorID` to the routing
interpreter's, because a pack that could choose its binding could claim any
executor the server has registered, including one with privileges no pack should
reach.

Routing metadata is held in a separate registry keyed by `{node type, version}`
and deliberately *not* on the definition, because the definition is served by the
node-types API and is a published contract. Putting a `routing` field on it would
hand a node's outbound request internals — URLs, header names, credential
template references — to every browser that opens the editor, to describe
something the editor has no use for.

The two travel together through one registration call, which is what keeps them
paired. Be precise about the strength of that guarantee, though: loading a pack
produces the definition and its routing description in the same function and
binds the executor itself, so a routing-bound definition with no description
cannot be *constructed*. It is not separately *verified* — registration checks
that the named executor is installed, but a nil description would simply skip
routing registration and surface only at run time, as "no routing description is
registered for this type and version". The invariant is structural rather than
asserted.

Three pack types ship today: Telegram, hand-written; and WAHA plus its trigger,
generated from a vendored OpenAPI document at two API versions each.

:::note
Two packs ship inside the binary, and there is a third install path that needs no
rebuild: point `packs.dir` at a directory and every pack directory under it is
loaded at startup through the same `Decode` → `Load` → `Register` path the
embedded packs take. Each pack directory holds `pack.json` and a `pack.sha256`
sidecar carrying the manifest's digest, and the loader refuses a missing or
mismatching checksum rather than loading it — these files become node definitions
with outbound requests and credential bindings, and a mutable directory on a
server is a place where a file can change without anyone deciding it should. Any
failure refuses the boot, naming the pack and the reason; an absent or empty
directory is a normal silent condition, which is what the default deployment has.
:::

## Boot-time checks

One invariant is proven at startup rather than discovered later. Every
`LifecycleID` a registered node declares is verified to have an implementation
bound, once both the packs and the built-ins have registered. Discovering a
missing binding at the first activation would mean a workflow that saves,
activates, and silently never registers itself with the remote service — no
error, just an active workflow that is unreachable.

Two more are enforced at registration rather than by a later sweep: a pack whose
executor binding is not installed is refused, and a `(type, version)` collision
is refused with both sources named.

## API operations

`GET /api/v1/node-types` serves the catalogue.
`GET /api/v1/node-types/{type}/icon` serves a node's artwork.
`POST /api/v1/node-types/{type}/load-options` resolves the selectable values for
a property whose valid values live on the customer's own service — the loader is
taken from the registered definition, never from the request.
`POST /api/v1/node-types/{type}/load-schema` resolves a resource mapper's
columns. See the [HTTP API reference](/reference/api/).

## Source

`internal/node/registry.go` (`Definition`, `Source`, `BuiltinPrefix`,
`Register`, `RegisterFrom`, `Resolve`, `Lookup`, `validateDefinition`),
`internal/property/property.go` (the closed kind set, `TypeOptions`,
visibility), `internal/workflow/typeversion.go`, `internal/nodepack/nodepack.go`
(the pack format), `internal/routing/doc.go` (why routing metadata is data and
what the interpreter refuses), `nodes/` (the built-in implementations),
`packs/` (the shipped packs).
