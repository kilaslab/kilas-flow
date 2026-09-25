# Changelog

Notable changes to KilasFlow are recorded here. The format is [Keep a
Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## How this file is kept

- **No release has been cut yet.** `git tag` is empty, nothing is published to
  `ghcr.io/kilaslab/kilasflow`, and `@kilasflow/sdk` is not on npm, so `main` is
  what everyone runs and `[Unreleased]` is currently the whole history.
- A change a user can observe gets its entry under `[Unreleased]` in the pull
  request that makes it, under one of `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed` or `Security`.
- Cutting a release moves `[Unreleased]`'s entries under the new version's own
  heading. Two tag namespaces exist and each publishes exactly one artefact:
  `vX.Y.Z` publishes the container image and `sdk-vX.Y.Z` publishes
  `@kilasflow/sdk`. What each one does is in `.github/workflows/release.yml`.
- The SDK keeps its own history in [`sdk/CHANGELOG.md`](sdk/CHANGELOG.md); this
  file is the product.

## [Unreleased]

### Added

- Code (JavaScript) (`kilasflow.jsCode`): n8n JavaScript Code nodes run, on
  goja, an ECMAScript engine linked into the binary, with no Node.js process
  involved. An imported Code node keeps its source byte for byte and runs, and
  one imported earlier as the placeholder runs without importing again; a new
  one can be added from the palette. The code gets both of n8n's modes and its
  roots (`$input`, `items`, `$json`, `$('Node')`, `$node`, `$workflow`,
  `$execution`, `$now` and the rest), Luxon, lodash, Node's `crypto`, `Buffer`,
  `URL` and `util`, `Intl`, timers and `console` (kept with the run, in the
  execution page's Console tab and the `code.console` event);
  `this.helpers.httpRequest`, `getBinaryDataBuffer` and `prepareBinaryData`,
  each carried out by the server under the egress policy; and
  `$getWorkflowStaticData`, saved as n8n saves it. The Sort node's Code
  comparator runs on the same runtime. What the engine would run differently
  from V8 is refused by name at import and at validate, and a use that cannot
  be seen until the code runs fails by name then. Every script runs on a fresh
  engine, in a worker process apart from the server, confined on Linux by
  namespaces and landlock as far as the kernel allows, under the
  `code.javascript_*` limits. Python Code nodes still do not run. See the [Code
  (JavaScript)](/guides/code-javascript/) page.
- The canvas Chat panel renders replies as markdown (lists, emphasis, code,
  links), streams the reply while the model writes it, shows each tool call as
  a collapsible step, and links every reply to its execution. Markup in a reply
  is shown as text, never rendered. A run refused by validation lists each
  blocking issue by node name, and clicking one opens that node. Sending from a
  canvas with unsaved edits saves them first.
- The execution event stream names every event it sends: `execution.waiting`,
  `webhook.response` and the eight `ai.*` progress events are now named frames,
  and a type a client does not know yet arrives as `execution.event` with its
  real name in `type`.
- Open-source project files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, this changelog, GitHub issue forms and a pull request template.
- Opt-in JavaScript sidecar (`sidecar.enabled`): operators can load programmatic
  community n8n packages and run their `execute()` in per-tenant Node processes,
  through the same egress policy as native nodes. Off by default; see the
  [JavaScript sidecar](/operate/javascript-sidecar/) page for the runtime image,
  the trust model and what it does not protect.
- `code.go_binary`, `code.cache_dir` and `code.cache_max_bytes` configure the Go
  Code node: which `go` command compiles it, where compiled artifacts and
  wazero's translations of them are kept, and how much disk they may use. The
  cache directory defaults to `./data/codecache`, inside the data volume the
  container image already persists, so a Code node's compiled artifact survives a
  restart — which matters most on a deployment with no toolchain, where a lost
  artifact cannot be rebuilt. Both caches are keyed on the runtime version, so an
  artifact built against an older host contract is rebuilt rather than loaded.
  Setting `code.cache_dir` to an empty value keeps every cache in memory, exactly
  as before.
- Scoped agent tokens: `POST /api/v1/api-keys` accepts `scopes`, `workflowId` and
  `expiresAt`, so an agent gets a key that can do one family of things —
  `workflow:read|write|run`, `datastore:read|write` — optionally bound to a
  single workflow and with an expiry. `GET /auth/me` reports all three. One
  middleware arm beside the embed gate refuses a scoped key the operations a
  person keeps: activation and publishing, deletion, import, `/tenants`, key
  management, embed sessions, credential mutation, datastore schema work,
  approvals, and everything no arm names. A key bound to one workflow reads
  another as `404`, so it cannot probe for ids it does not hold; a tenant-wide
  key and a browser session are unaffected.

- Fourteen guarded verbs — `workflow activate|deactivate|delete`, `credential
  create|update|delete`, `datastore create|rename|delete|clear|columns …`,
  `tenant delete` — behind two gates: `--yes` is consent and a tenant-wide key
  is authority, so a scoped agent token is refused with `scope_denied` whatever
  the flag says, and neither gate sends a request when it refuses.

- The debug primitives an agent's loop was missing: `workflow validate --file`
  (compile a document without saving it), `workflow duplicate`,
  `exec retry`, `run --revision` (pin a revision) and `debug eval`
  (evaluate an expression against one execution's stored node outputs, read-only,
  under the runtime's own evaluator and environment allowlist).

- Every workflow revision and publish event now records who made it —
  `actorKind`, `actorLabel`, `actorKeyId`, exposed on both listings — and the
  `X-KilasFlow-Skills-Used` header a CLI sends on mutating calls is stored as
  `actorMeta`, so the skills that actually get used are measurable.

- `kilasflow mcp serve`: an MCP server over stdio whose tools are generated from
  the command tree — one per verb, its flags as the tool's properties, and
  `confirm: true` on the guarded ones. A tool call becomes the same invocation
  the CLI dispatches, so the adapter can never describe a capability the CLI does
  not have.

- `kilasflow skills list|show|install|check|export`: the agent skills bundle
  ships **inside the binary** and these five verbs read it, install it into a
  harness directory (`--target claude|codex|agents|dir:<path>`, `--scope
  project|user`, project by default), compare an installed copy with the binary's
  and exit non-zero on drift, and export it as the harness index or a
  deterministic tarball. They are local: no server, no credential, and no
  checkout — which is what makes them work from the distroless image. See
  [`kilasflow skills`](/reference/cli/#kilasflow-skills).

- Pack-trigger lifecycle templates can send a structured parameter (a list or
  an object) as JSON through `{{ .ParameterJSON.<key> }}`, and a `set` request
  can `capture` values from its JSON answer (`capture: {key: "data.id"}`) for
  its `check` and `remove` to read as `{{ .Captured.<key> }}`. Captured values
  are sealed at rest with the credential encryption key, in a new
  `webhook_routes.lifecycle_state` column (migration 000023), and nothing is
  kept without that key. A trigger's HMAC check can take its secret from a
  captured value (`secretCapture`); such a trigger refuses deliveries until the
  secret has been kept.

### Changed

- `execution.default_timeout` defaults to 2 minutes instead of 1. An AI agent on
  a local or reasoning model routinely needs longer than a minute. Set
  `execution.default_timeout` (`KILASFLOW_EXECUTION_DEFAULT_TIMEOUT`) or a
  workflow's own `executionTimeout` to change it.
- A streamed model request is bounded by silence rather than by total length:
  the node's Timeout option (or the outbound default) now limits the wait for
  the first byte and every pause between chunks, so a reasoning model that
  streams for minutes is no longer cut off mid-answer.
- The Code node's deployment diagnostic now names what an operator has to
  provide — the minimum Go version, the exact binary that was looked for, and the
  two ways to provide it (`KILASFLOW_CODE_GO_BINARY` / `code.go_binary`, or the
  toolchain's `bin` directory on `PATH`) — instead of saying only that no
  compiler was found. The same text is used by the node catalogue, the error a
  Code node run returns and the editor's compilation status.
- A Code node that hits one of its limits now says which one it hit. Running out
  of memory inside the sandbox used to surface as the Go runtime's own
  `fatal error: out of memory` (or as a bare "exited with status 2"); it now
  reads `code ran out of memory inside its 256-page (16 MiB) memory limit`, and
  a code body whose compiled module declares more memory than the deployment's
  limit allows is refused with `this module cannot start inside the
  N-page (N MiB) memory limit` before it runs. Each of the four limits — wall
  clock, linear memory, output bytes and host calls — is a named error a caller
  can test for rather than a message to read.

- `webhook.require_auth` (environment: `KILASFLOW_WEBHOOK_REQUIRE_AUTH`) refuses
  a delivery to any webhook trigger that does not authenticate its callers, with
  a `403` naming the workflow and the fix. It is off by default, a trigger that
  verifies its own senders counts as authenticated, and the boot log states the
  posture either way.

- Per-tenant node visibility: a pack can declare the tenants its node type is
  visible to with a `visibleTo` manifest field, and an operator can override it
  with `packs.visible_to` (environment: `KILASFLOW_PACKS_VISIBLE_TO`). A node
  type outside a tenant's set is invisible in `GET /node-types` and its
  siblings, and compiling or running a document that references it fails with
  the new diagnostic `node.not_available`, distinct from `node.unknown_type`.

- The `@kilasflow/sdk` tarball now ships its `LICENSE` and `CHANGELOG.md`,
  `make sdk-release-check` proves the package the way a consumer installs it,
  and the `sdk-vX.Y.Z` release runbook lives in `sdk/RELEASING.md`.

- `kilasflow <verb>` is a command line as well as a server: bare `kilasflow`
  still serves, every verb in
  [`reference/cli.md`](docs/src/content/docs/reference/cli.md) maps to exactly
  one API operation, and `kilasflow api <operation-id>` reaches every operation
  in the API contract. `--json` prints one stable envelope per invocation and
  the exit code says whether to fix the call, ask the user, wait or report — a
  refused scope is its own code, and a guarded verb refuses without `--yes`.
  `make smoke-cli` proves the tree against a booted server.

- Idempotent execution requests and datastore writes: an `Idempotency-Key`
  header on `POST /api/v1/workflows/{id}/run`, `POST /api/v1/datastores/{id}/rows`
  and `POST /api/v1/datastores/{id}/rows/upsert` makes a retry return the first
  request's outcome instead of repeating its side effect, with durable
  tenant-scoped keys in a new `idempotency_keys` table, retention set by
  `idempotency.retention`, and `Retry-After` on the in-flight 409. The host SDK
  exposes it as `idempotencyKey` on `runWorkflow`, `insertDatastoreRow` and
  `upsertDatastoreRow`.

- `DELETE /api/v1/tenants/{id}` on the operator surface: deletes a customer and
  everything it owns — executions and their payload files, workflows and
  versions, credentials, schedules, webhook deliveries, datastores with their
  physical tables, vector rows, accounts and keys — and answers with the counts
  it removed, per table. It is irreversible, needs the operator credential, and
  is idempotent: repeat it until it reports zeros. See
  [Tenant deletion](docs/src/content/docs/operate/tenant-deletion.md).

- Datastore: an `Increment` row operation on the Data table node, the
  `POST /api/v1/datastores/{id}/rows/increment` operation, and
  `incrementDatastoreRows` in the SDK. One statement adds to a number column on
  every matching row — atomic per row on SQLite and PostgreSQL alike — and
  returns each row as that statement left it, so a counter no longer loses a
  write to a concurrent reader-writer.

- Datastore: an optional `ifUpdatedAt` precondition on the row update and delete
  operations. The write lands only if the row still carries the stamp the caller
  read; a stale stamp answers 409 with the row's current `updatedAt` in
  `errors[0].value`, so the caller retries without a second read.

- Datastore: an upsert whose filter is a single `id eq` condition is one
  `INSERT ... ON CONFLICT (id) DO UPDATE` statement on both drivers. It creates a
  missing row at exactly that id and never inserts the same id twice. Explicit
  ids are bounded to 1..9007199254740991.

### Changed

- Datastore: `updatedAt` is strictly increasing per row. Every write takes the
  greater of the wall clock and one millisecond past the row's current stamp, so
  "untouched since I read it" is a question the column can answer. Under a
  sustained burst above about a thousand writes a second to one row, the stamp
  runs ahead of the wall clock by the length of the burst.

- A webhook binding that had no minted route (only a database that predates
  routes holds one) is given one at boot by the `webhook_route_backfill`
  migration, so its address changes from `/webhook/<label>` to
  `/webhook/<route>`. Read it with `GET /workflows/{id}/webhooks`, and activate a
  Telegram or WAHA workflow again so the sender is given the new address.
  Bindings that already have a route keep it.

- `embed.session_ttl` now sets the default lifetime of an embed session token
  (it was read by nothing); values below one second or above 30 minutes are
  refused at startup.

- `branding.name` and `branding.logo` now default the header of embedded editor
  sessions that carry no branding of their own (they were read by nothing); the
  default of `branding.name` changed from `KilasFlow` to empty, so existing
  embeds keep the headerless look they have today.

- `GET /node-types` and its sibling operations (icon, load-options,
  load-schema) are narrowed to the caller's tenant, embed sessions included, so
  a node type scoped to other tenants answers exactly as an unregistered one
  does. The catalogue responds `Cache-Control: private, no-cache` because its
  body now depends on who is asking.

- The executions list asks for 50 runs a page, says how many it is showing and
  whether more are available, and the workflows page shows the workspace total
  in its heading and "N of M" while a search narrows the list.

- Boot now migrates every datastore to the schema version the build serves,
  after the SQL migrations and before the listener opens, and **refuses to
  start** when it cannot: a datastore ahead of the build, or behind it with no
  step to reach it, is named on stderr and the process exits `1` instead of
  serving requests it would refuse anyway. A datastore an older peer process
  created behind is picked up by a retry every thirty seconds, without a
  restart.

- `GET /api/v1/ready` gained a `datastores` block (schema versions and counts
  only) and answers `503` while a datastore migration is outstanding. The same
  block is carried in that `503`'s problem document, so the spread stays
  machine-readable in the state that reports it.

- `DELETE /api/v1/tenants/{id}` on the operator surface: deletes a customer and
  everything it owns — executions and their payload files, workflows and
  versions, credentials, schedules, webhook deliveries, datastores with their
  physical tables, vector rows, accounts and keys — and answers with the counts
  it removed, per table. It is irreversible, needs the operator credential, and
  is idempotent: repeat it until it reports zeros. See
  [Tenant deletion](docs/src/content/docs/operate/tenant-deletion.md).

- A data table's name is unique within its tenant, compared without regard to
  case or to the spaces around it, the way n8n keeps names unique within a
  project. Creating or renaming onto a taken name answers `409` naming the
  table that holds it, and a By Name reference that more than one table would
  answer is refused rather than resolved to whichever the catalogue listed
  first. Migration 000022 renames the duplicates a database already holds: the
  oldest table of each name keeps it, and every other becomes
  `<name> (<id>)`. A workflow that addressed one of the renamed tables By Name
  now reaches the oldest table of that name instead, so point it at the new
  name or at the id. The "From list" picker tells tables that share a name
  apart by the end of their id.

- A Data table Tool performs the operation it is set to — get, insert,
  update, upsert or delete — where before it only ever read. An Insert with
  nothing to write still reads, which keeps every tool built before writes
  existed reading; an Update or Upsert with nothing to write is refused. On a
  tool that writes, the model supplies values only: a `$fromAI` call or an
  expression may sit in a mapped column's value or a condition's value, and the
  rest of the write (which column a condition reads, its operator, any or all,
  the mapping) is written literally and refused at save if it is not. An n8n
  Data Table Tool imports with its write operation. A match it cannot carry
  imports as "all" and blocking, and the table name, which no tool reads, is
  dropped.

- `kilasflow api <operation>` and the MCP `api` tool ask for the same
  confirmation a guarded verb does when the operation id names one it wraps —
  `--yes` on the command line, `confirm: true` over MCP — and the same
  tenant-wide key. Without it nothing is sent. The MCP `api` tool is annotated
  as destructive.

- A cron that can never fire (`0 0 31 2 *`) is refused with `422`, on a
  schedule and on activating a workflow whose Schedule Trigger carries one. A
  schedule already stored with one is deactivated at the next tick instead of
  firing on every tick, and no longer holds up the others.

- `server.public_url` now also decides the origin the embedded editor's own
  saves and runs carry. Set it whenever KilasFlow sits behind a proxy that
  rewrites `Host`, or those requests answer `403`, and for any deployment that
  browsers reach but the internet does not.


### Removed

- `branding.favicon` and `branding.powered_by` (read by nothing; a configuration
  that still sets them is warned about at start).


### Fixed

- Code (JavaScript): `this.helpers.httpRequest` rejects a status outside 2xx
  as n8n's does: an `AxiosError` reading "Request failed with status code
  404", with `status` and axios's `code`, and with no `response`, since n8n's
  has none. It used to read "The request failed with status 404 Not Found",
  so code matching n8n's message took the wrong branch.
- A node with Always Output Data (`alwaysOutputData`) no longer runs when the
  node before it sent it nothing. It used to run on no input and hand on an
  empty item, so the nodes after it ran too, and a Loop Over Items whose body
  carried the setting fed that item back into the loop and never finished
  until the execution timed out. As in n8n, the setting only adds the empty
  item when the node did run and returned nothing. A run resumed after a Wait
  likewise no longer runs a sibling branch that had been sent no items.
- `$('Name').all()`, `.first()` and `.last()` read the output of that node the
  current node is connected to, as n8n does, in expressions and in Code alike,
  and in the expression debugger. They used to read every output joined, so
  after an IF `.all()` held both branches and `.last()` could be the other
  branch's item. A node the current one is not connected to is read at its
  first output. In Code, `.first(branch, run)` and `.last(branch, run)` take
  the arguments `.all()` already took.
- A Code node in "Run Once for All Items" mode that continues on failure
  answers a throw with one error item, as n8n's does, not one per input item,
  so the node after it runs once; the item carries no input item's fields, and
  the failure is not retried (a worker that crashed or could not start is
  still an ordinary failure, retried and tolerated per item). A Code node's
  error items carry `error` as n8n's text, the message and the line, such as
  `boom [line 1]`, without the error's type or the item, so
  `{{ $json.error }}` reads the text and `{{ $json.error.message }}` is empty,
  as in n8n. Other nodes' error items keep the `{ message, node }` object.
- A chat model node that never set "Stream output" now streams, as the editor
  already showed. An absent `stream` key used to mean off.
- An agent ended by the workflow's own execution timeout says so, and names
  `executionTimeout` and `execution.default_timeout`, instead of blaming the
  deployment's ten-minute model ceiling.
- Clicking a validation issue in the editor opens the node it names. Svelte
  Flow's stale selection report used to deselect it again at once.
- Datastore: `Insert` on SQLite read its id back with a second connection
  running `SELECT last_insert_rowid()`, which is per-connection state, so a
  concurrent insert could return another writer's row. Both drivers now use
  `INSERT ... RETURNING id`.

- Datastore: `updatedAt` could repeat within one millisecond, so a precondition
  holding a stale stamp was accepted and a compare-and-swap retry loop lost
  updates. The stamp no longer repeats.

- Datastore: `Increment` issued `SELECT`, `UPDATE`, `SELECT` while the node and
  API wording claimed one statement. It is one `UPDATE ... RETURNING` now, and
  the rows it returns are that statement's own images.

- Auto-refresh on the executions list now keeps working, and keeps the runs you
  have already loaded, when the workspace has more runs than fit on one page,
  and again after the tab has been in the background.

- An embedded editor saves and runs from a host page on another origin. Its
  requests carry KilasFlow's own origin, which used to be refused with `403`;
  they now pass when the frame names the host the session was minted for.

- Guarded CLI verbs and MCP `confirm: true` calls work with auth off, the
  default, where every one used to fail with `401`. The CLI confirms that auth
  is off from the server's OpenAPI document, so a wrong credential on a server
  with auth on is still refused.

- A Wait inside Loop Over Items resumes on every batch. The second batch used to
  fail with "suspended node already completed"; a resume that is refused now
  marks the Wait failed instead of leaving every node green.

- `$('Node').item` resolves through a node that changed the item count and on
  both branches of an error output or a continue-on-fail failure. Behind an IF,
  Set, Merge or a loop that follows such a node, where the pairing is not
  recorded exactly, it is refused rather than guessed from a position, and a
  sub-workflow's items no longer carry the child run's pairing back into the
  caller.

- Deleting an active workflow unregisters its triggers with the remote service,
  the way deactivating one does.

- Boolean arguments to MCP tools (`run.wait`, `api.list`,
  `skills_install.dry_run`) reach their flags, both true and false.

- Searching a data table's rows no longer starts an endless request loop, and
  switching between data tables no longer fetches twice or shows the previous
  one while the next loads.

- A timestamp that was never set shows as "—" rather than as "Jan 1".

- n8n import: a node whose `typeVersion` is a string (`"3.4"`) imports at that
  version, where the whole file used to be refused with a Go decoding error.
  `maxTries`, `waitBetweenTries`, `position` and a connection's `index` accept
  a numeric string too. The node flags are on only when they are literally
  `true`, which is how n8n reads them. A value that is not a number at all is
  named in the import report, and a file that still cannot be read is refused
  in words that name the node and field rather than a Go struct.

- n8n import: a loop that does not close onto Loop Over Items is reported as
  `blocking`, one entry per loop, naming its nodes in order. It used to import
  with nothing blocking, and then run and activate refused it with
  `workflow.invalid_topology`. The report uses the compiler's own cycle rule,
  so the two cannot disagree. It names the loop even beside other blocking
  issues, where the compiler would only reach the loop once those were fixed.


### Security

- Updating a credential keeps every field and the scope the request leaves
  out. An update used to clear any field it did not send, so a rename silently
  dropped a JWT private key or the refresh token Connect stored. It also read a
  missing `allowedDomains` as "unrestricted", so a rename let the secret go to
  any host. Now a field is cleared by sending it empty, and the scope only by
  sending an empty list. Creating a credential refuses the redaction
  placeholder as a value instead of storing the bullets as the secret, and a
  listing masks only the secrets that are set: an optional one that was never
  written reads as empty.

- A JavaScript worker process runs one tenant's Code-node and Sort-comparator
  scripts and never another's, so code that escaped the engine and stayed in a
  worker cannot see a later tenant's jobs. A script whose tenant has no idle
  worker starts a fresh one, which costs one cold start (about 8 ms); at
  `code.javascript_max_concurrent` workers, the worker idle longest, another
  tenant's, is stopped to make room.

- A refused request's problem document no longer echoes the request back. A
  missing or unexpected property, or a body that does not parse, used to answer
  with the whole body, a credential's secrets included; on an operation that
  carries a secret (credentials, login, users and API keys) no value is echoed
  at all. The CLI and the MCP tool results also redact whatever an older server
  or a proxy still echoes, and `--verbose` no longer prints a credential's
  secret from the request body it traces.

- Another page that frames the embedded editor and hands it a host's token is
  refused on every request the frame makes, reads included: the frame names the
  host it completed the handshake with, and a request naming any other is
  refused.

- Pack-trigger lifecycle templates escape each value for where it lands: a
  path segment or a query value in a URL, a string in a JSON body. A value that
  would stand before a URL's path, a header value with a line break, a dot
  segment or a body that is not valid JSON is refused, and nothing is sent. A
  session name could previously move a WAHA registration, with the tenant's API
  key, to another endpoint.
