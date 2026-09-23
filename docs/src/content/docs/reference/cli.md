---
title: Agent CLI
description: The `kilasflow <verb>` surface for agents and scripts — the command tree, the JSON envelope, exit codes, and how a token is resolved without ever being printed.
sidebar:
  order: 5
---

KilasFlow is driven from three places: the editor, the HTTP API, and this
command line. The CLI adds no capability the API does not already have — it is
a client of `/api/v1`, one verb per operation — but it makes the API usable
from a shell and, more to the point, from an agent that has to decide what to do
next from an exit code and one JSON document.

Two rules keep it honest:

- **One verb per operation, no aliases.** Every verb maps to exactly one
  operation id, and that mapping is recorded in the command's own metadata.
  `run` is the only way to start a run; `workflow run` does not exist. There is
  one documented exception, and it runs the other way: `node list` and `node
  describe` are two views of one operation (`list-node-types`), because there is
  no operation that returns a single node and reading a 90-entry catalogue to
  answer "what does httpRequest take?" is the wrong shape for an agent's context.
  The verbs that are local rather than remote — `version`, `help`, `context`,
  `api <operation-id>`, `auth login`, `auth logout`, `pack validate`, the five
  `skills` verbs and `mcp serve` — either need no server or reach several
  operations, and each says so in its own section below.
- **`kflow` is not an alias.** The stale `bin/kflow` binary was a build of the
  server under an older name. It was deleted rather than kept as a second
  entry point, so there is one name to document and one to remember.

## Coexistence with the server binary

The binary keeps its original meaning: **`kilasflow` with no subcommand serves**.
The container entrypoint and every Compose file depend on that, so it cannot
change.

```bash
kilasflow                      # serves: API, worker, scheduler
kilasflow serve                # the same thing, spelled out, for scripts
kilasflow -config config.yaml  # serves with a named configuration file
kilasflow version              # a CLI verb
```

The dispatcher only claims an invocation whose first word is a verb. An
invocation that starts with a flag (`-config`, `-version`, `-role`, `-h`) or
that names no verb belongs to the server, and a CLI verb never parses the
server's flags.

## Command tree

Every row below exists in the binary, and the state column says so. A row
marked *guarded* refuses to run without `--yes` **and** refuses a scoped agent
token outright — see [Guardrails](#guardrails).

| Verb | Operation id | State |
| --- | --- | --- |
| `kilasflow version` | — (local; probes `get-health`) | available |
| `kilasflow health` | `get-health` | available |
| `kilasflow ready` | `get-ready` | available |
| `kilasflow help` | — (local; generated from the verb registry) | available |
| `kilasflow serve` | — (the server) | available |
| `kilasflow api <operation-id>` | any id from `/api/openapi.json` | available |
| `kilasflow context` | — (local briefing over read operations) | available |
| `kilasflow auth login` \| `logout` | — (local credential storage) | available |
| `kilasflow auth whoami` | `get-me` | available |
| `kilasflow workflow list` | `list-workflows` | available |
| `kilasflow workflow get` | `get-workflow` | available |
| `kilasflow workflow create` | `create-workflow` | available |
| `kilasflow workflow validate` | `validate-workflow-document` | available |
| `kilasflow workflow duplicate` | `duplicate-workflow` | available |
| `kilasflow workflow versions` | `list-workflow-versions` | available |
| `kilasflow workflow get-version` | `get-workflow-version` | available |
| `kilasflow workflow publish-events` | `list-workflow-publish-events` | available |
| `kilasflow workflow export` | `export-workflow` | available |
| `kilasflow workflow diagnostics` | `workflow-diagnostics` | available |
| `kilasflow workflow activate` | `activate-workflow` | available, *guarded* |
| `kilasflow workflow deactivate` | `deactivate-workflow` | available, *guarded* |
| `kilasflow workflow delete` | `delete-workflow` | available, *guarded* |
| `kilasflow run` | `run-workflow` | available |
| `kilasflow exec list` | `list-executions` | available |
| `kilasflow exec get` | `get-execution` | available |
| `kilasflow exec trace` | `stream-execution-events` | available |
| `kilasflow exec tail` | `stream-execution-events` | available |
| `kilasflow exec cancel` | `cancel-execution` | available |
| `kilasflow exec retry` | `retry-execution` | available |
| `kilasflow debug eval` | `eval-expression` | available |
| `kilasflow node list` | `list-node-types` | available |
| `kilasflow node describe` | `list-node-types` (the catalogue, reduced to one entry) | available |
| `kilasflow node options` | `load-node-property-options` | available |
| `kilasflow credential list` | `list-credentials` | available |
| `kilasflow credential get` | `get-credential` | available |
| `kilasflow credential test` | `test-credential` | available |
| `kilasflow credential create` | `create-credential` | available, *guarded* |
| `kilasflow credential update` | `update-credential` | available, *guarded* |
| `kilasflow credential delete` | `delete-credential` | available, *guarded* |
| `kilasflow datastore list` | `list-datastores` | available |
| `kilasflow datastore get` | `get-datastore` | available |
| `kilasflow datastore rows` | `list-datastore-rows` | available |
| `kilasflow datastore export` | `export-datastore-rows` | available |
| `kilasflow datastore create` | `create-datastore` | available, *guarded* |
| `kilasflow datastore rename` | `rename-datastore` | available, *guarded* |
| `kilasflow datastore delete` | `delete-datastore` | available, *guarded* |
| `kilasflow datastore clear` | `clear-datastore` | available, *guarded* |
| `kilasflow datastore columns add` | `add-datastore-column` | available, *guarded* |
| `kilasflow datastore columns rename` | `rename-datastore-column` | available, *guarded* |
| `kilasflow datastore columns drop` | `delete-datastore-column` | available, *guarded* |
| `kilasflow schedule list` | `list-schedules` | available |
| `kilasflow tenant list` | `list-tenants` | available |
| `kilasflow tenant get` | `get-tenant` | available |
| `kilasflow tenant users` | `list-tenant-users` | available |
| `kilasflow tenant delete` | `delete-tenant` | available, *guarded* (operator credential) |
| `kilasflow pack validate` | — (local; runs the pack loader) | available |
| `kilasflow skills list` \| `show` \| `install` \| `check` \| `export` | — (local; the bundle embedded in the binary) | available |
| `kilasflow mcp serve` | — (local; publishes the command tree as MCP tools on stdin/stdout) | available |

Two things the design's tree lists are deliberately absent:

- `kilasflow tenant api-keys` and `kilasflow pack install` — writes (minting a
  credential for another tenant, and installing code). The mint is reachable
  through `kilasflow api create-tenant-api-key`; `pack install` has **no server
  operation behind it at all** — the pack surface is a local loader — so there
  is nothing for a verb to drive, guarded or not.
- `kilasflow embed session create`, `kilasflow node icons|grammar`,
  `kilasflow credential types`, `kilasflow schedule create|update|delete`,
  `kilasflow tenant create` — no verb. There is a useful operation behind each
  one and no caller that reaches for it often enough to justify a name, so
  `kilasflow api <operation-id>` is the answer rather than a release.
  `kilasflow webhook list` is the one entry blocked on another ticket
  (`FEAT-77rveq`): the operation it would wrap (`GET /workflows/{id}/webhooks`,
  `list-workflow-webhooks`) is served today and is reachable through
  `kilasflow api list-workflow-webhooks`.

The mutating verbs that have no command of their own — `workflow
update|restore|import`, `schedule create|update|delete`, the
datastore row writes, `tenant create`, `create-tenant-user`, `revoke-api-key` —
are reachable the same way. A verb is added when a caller has to reach for the
operation often enough that naming it is worth the surface; until then the
escape hatch is the answer, not a release.

## The escape hatch: `kilasflow api <operation-id>`

Every operation a running server serves is reachable by its operation id, so
there are no dead ends: an operation nobody has written a verb for is callable
today, and the CLI can be complete on day one.

```bash
kilasflow api --list                            # every id this server serves
kilasflow api get-workflow --path id=wf_01J8ZP
kilasflow api run-workflow --path id=wf_1 --body '{"input":{"n":1}}'
kilasflow api export-datastore-rows --path id=ds_1 --out rows.csv
```

- `--list` prints the operation ids **the running server serves**, read from its
  `/api/openapi.json` at call time, sorted. Nothing is compiled in: a table
  baked into the binary would describe a server the CLI is not talking to.
  `--quiet` prints one id per line for a shell pipeline.
- `--path name=value` fills the `{name}` placeholders of the operation's served
  path (repeatable, value URL-escaped). A placeholder with no value — including
  one whose value is empty or only whitespace, which is what an unset shell
  variable produces — is exit 2 before the operation's request is sent, naming
  every `--path` it needs and writing nothing to `--out`. The one request such a
  call does make is the `/api/openapi.json` read that resolves the operation, so
  with the server unreachable there is nothing to resolve against and the call
  fails as `network_error` instead of reaching the refusal. It has to be refused
  here: the substituted path is a real-looking route the SPA answers with
  `200 text/html`, so no status code can report it. A `--path` the operation
  does not use is ignored.
- `--query name=value` and `--header name=value` add query parameters and
  request headers.
- `--body` takes JSON, `@<file>`, or `-` for stdin; `--body-file <path>` reads a
  file. An inline body must be valid JSON; a file or stdin may be anything,
  because operations such as the CSV import take bytes that are not JSON.
- A JSON response is printed unmodified under `data`. A response that is not
  JSON (CSV, an event stream) is carried as `data.raw` (base64) plus
  `data.contentType`. `--out <path>` writes the bytes to a file and reports the
  path in the envelope; `--out -` writes them to stdout raw.
- `meta.operation` is the resolved operation id, so an `api` call traces back to
  the API operation in a log line exactly like a named verb.
- `--quiet` prints the response's `id` field — the identifier a caller would
  pipe into the next command. A response without one (a listing, a 204) prints
  nothing rather than a guess.
- **An unknown operation id is refused with exit 2 before the operation is
  called.** It has to be: an unknown `/api/v1` path falls through to the SPA,
  which answers `200 text/html`, so a status code cannot tell a wrong path from
  a right one. The only request such a refusal makes is the `/api/openapi.json`
  read that makes the refusal possible.
- **A guarded operation is guarded by id, not only by name.** If the operation
  id names one of the operations a guarded verb wraps (`activate-workflow`,
  `delete-workflow`, `create/update/delete-credential`,
  `create/rename/delete/clear-datastore`, the column operations,
  `delete-tenant`), `api` asks for the same `--yes` and the same tenant-wide
  key that verb would, before `/api/openapi.json` is even read: without
  `--yes` the call is refused with exit 3 (`confirmation_required`) and sends
  nothing at all, and `--yes` on a scoped agent token is still refused with
  `scope_denied`. The escape hatch is a **naming** bypass, never a consent or
  an authority bypass: it sends the configured credential, adds and removes no
  scope, and carries a `403` through as exit 3 (`scope_denied`) exactly like a
  named verb.

## `kilasflow context`

The first command of an agent session: one read-only document describing the
CLI, the server, the caller, and what exists.

```bash
$ kilasflow context --json
```

`data` holds six sections:

| Section | Holds |
| --- | --- |
| `cli` | the binary's `version` |
| `server` | `url`, `ready`, and the `get-health` body |
| `identity` | `tenantId`, `kind`, `label`, `keyId`, `userId` |
| `workflows` | `count`, `items` (`id`, `name`, `active`), `truncated` |
| `datastores` | `count`, `items` (`id`, `name`), `truncated` |
| `nodeTypes` | `count` |

The two listings are capped at 20 items and set `truncated` when the server says
there is another page (`X-Next-Cursor`), so a briefing never turns into a
download. `nodeTypes` reports the catalogue size only.

Each section fails **independently**: a section the caller may not read, or one
that is not configured, is recorded under its own `error.code` and the command
still exits 0. "There are no workflows" and "listing workflows was refused" are
different facts, and an agent that cannot tell them apart plans the wrong next
step — an auth-disabled instance genuinely has no identity, so `get-me`'s `401`
belongs in `identity.error`, not in the exit code. Readiness is the one
exception: when `get-ready` fails, the command fails with that status's exit
code, because a briefing about an instance that cannot serve would only hide the
reason.

## `kilasflow auth`

The credential verbs. `login` and `logout` are local: they validate a token
against `get-me` and write the caller's own configuration file. They carry no
operation id because the API's `login` and `logout` operations mint and clear a
*browser cookie*, which is not what this stores.

```bash
printf '%s' "$TOKEN" | kilasflow auth login --token -   # validate, then store at 0600
kilasflow auth whoami --json                            # the identity it resolves to
kilasflow auth logout                                   # remove the token, keep the URL
```

- `auth login` takes the token from stdin (`--token -`), from `--token-file`, or
  from `KILASFLOW_TOKEN`. **`--token <value>` is refused** (exit 2): a credential
  on a command line lands in shell history and in the process listing, which is
  the whole reason this verb exists.
- The token is validated against `get-me` **before** it is written. An invalid
  token is never stored, so the next command fails where the mistake is rather
  than somewhere else.
- `auth login` prints where the credential went and as whom it acts
  (`url`, `tenantId`, `kind`, `configPath`) — never the token itself.
- `auth logout` removes the `token` key and keeps `url`. With no configuration
  file it is a no-op that still exits 0.
- `auth whoami` prints `tenantId`, `kind`, `label`, `keyId`, `userId`. The
  credential has no field in that projection and never will.

## `kilasflow workflow`

The read surface, plus `create` and the guarded lifecycle verbs. Each verb
addresses the stable path documented in the [API contract](/reference/api/)
rather than resolving it from `/api/openapi.json` at call time: `workflow get`
should be one request.

```bash
kilasflow workflow list --limit 20 --json
kilasflow workflow get wf_01J8ZP
kilasflow workflow create --file workflow.json --quiet   # the new id
kilasflow workflow validate --file patch.json            # exit 0, then read data.valid
kilasflow workflow validate --file - --quiet < patch.json  # true / false
kilasflow workflow duplicate wf_01J8ZP --name "Orders (copy)" --quiet  # the copy's id
kilasflow workflow versions wf_01J8ZP --cursor cur_2
kilasflow workflow get-version wf_01J8ZP wfv_01J8ZP
kilasflow workflow publish-events wf_01J8ZP
kilasflow workflow export wf_01J8ZP --format n8n
kilasflow workflow diagnostics wf_01J8ZP --version-id wfv_01J8ZP
kilasflow workflow activate wf_01J8ZP --yes      # publishes the endpoint
kilasflow workflow deactivate wf_01J8ZP --yes
kilasflow workflow delete wf_01J8ZP --yes
```

- A listing's payload is `{items, count, nextCursor}`. The API is not uniform —
  workflows answer with a bare array and a cursor in `X-Next-Cursor`, revisions
  with an object carrying both — and normalising that here is what lets one
  agent loop page every listing the same way.
- `workflow create --file <path>|-` sends the document **unchanged**. A document
  that is not valid JSON is refused with exit 2 before a request is made.
- `workflow validate --file <path>|-` sends the same draft document and saves
  nothing. `data` is `{valid, diagnostics}` where each diagnostic is the import
  path's own resource — `{severity, nodeId?, nodeName?, field?, type?,
  typeVersion?, reason}`, with `severity: "blocking"` and the explanation under
  `reason` — carried unchanged, so a client renders the dry run's findings the
  way it renders an import report. **A document that does not validate is an
  answer, not a failure**: the operation is `200` with `valid:false`, so the
  verb exits 0 and `--quiet` prints the verdict (`true`/`false`), which is what
  a pipeline branches on. The exit codes reserved for authority and for absence
  still apply — a `403` exits 3, a `404` exits 4.
- `workflow duplicate <id>` copies a workflow. `--name <name>` names the copy;
  without it the server derives one and the request carries no body. The answer
  is the copy's own resource (`201`), and `--quiet` prints the copy's id, taken
  from the `Location` header the API set. It is not guarded: a copy publishes
  nothing and destroys nothing.
- `--quiet` on a listing prints one id per line; on `workflow create` it prints
  the id the API named in its `Location` header; on `workflow validate` the
  verdict; on `workflow duplicate` the copy's id; on `activate`, `deactivate`
  and `delete` the workflow id.
- `workflow diagnostics` wraps an operation the contract page does not list
  (`GET /workflows/{id}/diagnostics`); the generated reference does.
- **`activate`, `deactivate` and `delete` are guarded.** Activating compiles the
  latest revision and pins it as active, which is what publishes a public
  endpoint; deactivating takes it offline; deleting destroys the workflow. Each
  needs `--yes`, and each refuses a scoped agent token outright — see
  [Guardrails](#guardrails). `delete` answers 204, so its `data` is
  `{id, status}` rather than a resource.
- Deliberately absent: `update`, `restore`, `import`. Every one of them is
  reachable today through `kilasflow api <operation-id>`.

## `kilasflow run`

Starts a run, and optionally waits for it.

```bash
kilasflow run wf_01J8ZP --input '{"n":1}' --trigger manual
kilasflow run wf_01J8ZP --input-file payload.json --wait --quiet   # the execution id
kilasflow run wf_01J8ZP --revision wfv_01J8ZP                     # a pinned revision
```

- The body is `{"input": <json>, "triggerNodeId": <node>, "workflowVersionId":
  <revision>}`, and every field may be omitted; a bare `run` starts every
  trigger, with no input, on the workflow's active revision.
- `--revision <versionId>` pins the run to one revision instead of the active
  one, so a caller can run a document it just validated before making it active.
  Read the id from `workflow versions`.
- Without `--wait`, `data` is the execution request the server accepted (202).
- With `--wait`, the run is read back every `--poll` (default 500 ms) until it
  reaches a terminal state. The deadline is `--timeout` when the caller passes
  one and **five minutes** when they do not: waiting for a run is the one thing
  this CLI does that is expected to take minutes.
- A run that **succeeded** exits 0 with `data` set to the finished record. A run
  that **failed or was cancelled** exits 1 with
  `error.code = "execution_failed"` and the whole record under
  `error.detail.execution` — a pipeline that asked a question is told "no", and
  the record explains why without a second request. `queued`, `running`,
  `cancelling` and `waiting` are not terminal: a cancellation that has been
  requested is not one that happened, and a run waiting on an external resume
  has not finished.
- A run that had not finished when the deadline passed exits 1 with
  `error.code = "timeout"` and a message naming the execution, so the caller can
  keep watching with `exec get`.
- `--quiet` prints the execution id **before** it starts waiting, so a pipeline
  can trap it even when the CLI is interrupted mid-wait.

## `kilasflow exec`

Execution history and the live feed.

```bash
kilasflow exec list --workflow wf_01J8ZP --status failed --limit 10
kilasflow exec get exec_01K7
kilasflow exec cancel exec_01K7
kilasflow exec retry exec_01K7 --quiet            # the new execution id
kilasflow exec trace exec_01K7 --json
kilasflow exec tail exec_01K7
```

- `exec list` takes `--workflow`, `--status` (repeatable), `--trigger`,
  `--limit` and `--cursor`.
- `exec cancel` is **not** guarded: it stops work rather than publishing or
  destroying anything, and it answers 202 because the server accepted the
  request, not because the run stopped. Read the execution back to see what
  happened.
- `exec retry <executionId>` starts a fresh execution of the same workflow, with
  the same input and the same trigger, from a finished one. It sends **no body**
  — the operation copies the execution it reads — and answers `201` with the new
  execution, whose id `--quiet` prints. A retry of an execution that is still
  running is a `409`, which exits 5 (`conflict`): re-read, then decide.
- `exec trace` collects the feed into one envelope:
  `{executionId, events: [{id, event, data}], terminal, lastEventId}`. It stops
  at the run's outcome rather than waiting for the server to close the stream,
  and its deadline (`--timeout`, default 30 s) is **not** a failure: a run that
  is still going answers `terminal: false` with the `lastEventId` to resume from
  (`--from <lastEventId>`, sent as the standard `Last-Event-ID` header).
- `exec tail` is the second documented exception to the one-envelope rule: it
  writes one JSON object per line (`{"id","event","data"}`) in JSON mode and one
  line of text otherwise, so events arrive before the run ends. It exits 0 when
  the run reaches an outcome, and **6** when its deadline passes
  (`error.code = "timeout"`) or the feed closes without an outcome
  (`stream_closed`): "still running" and "finished" have to be different exit
  codes, because tail has no envelope to say it in. Without `--timeout` it has
  no deadline and follows the run to its end.
- Both stream verbs are bounded on purpose. A feed for a live run never closes
  on its own, and an agent that cannot set a deadline on it cannot use it.

## `kilasflow debug`

Answering a question about a run without starting another one.

```bash
kilasflow debug eval '$json.n + 1' --execution exec_01K7
kilasflow debug eval '$node["HTTP Request"].body.id' --execution exec_01K7 --node "HTTP Request"
kilasflow debug eval '$json.n' --execution exec_01K7 --quiet   # the value alone
```

- `debug eval <expression> --execution <id>` evaluates one expression against the
  execution's stored context: the node outputs the trace view already shows,
  rebuilt by the server with the evaluator the runtime already uses for workflow
  documents. It answers `{value, type}` — one value, not a re-run, and nothing is
  saved or executed. `--node <nodeId>` narrows the context to one node's output.
- **`--execution` is required, and is not defaulted to the newest run.** The
  route makes the execution the context source, so guessing which run the caller
  meant would answer a question nobody asked; the verb refuses with exit 2 and
  names `exec list` for finding an id. `--node` is the only optional narrowing.
- The verb is **not** guarded and carries no guardrail refusal of its own: it
  reads one execution the caller can already read (`exec get`), so its authority
  is exactly the scope the server asks for. A refusal is the server's to make —
  a `403` exits 3 (`scope_denied`), and another tenant's execution is a `404`
  that exits 4, the same as any other execution read.
- `--quiet` prints the value as compact JSON, so
  `kilasflow debug eval '$json.n' --execution exec_1 --quiet` is usable in a
  pipeline; the type stays in the envelope, because `1` and `"1"` are the same
  question answered two ways.

## `kilasflow node`

The server's node catalogue is the only source of a node's parameter shape, so
these verbs are how an agent finds out what it may put in a document.

```bash
kilasflow node list --json                       # the whole catalogue
kilasflow node describe httpRequest              # one entry, newest version
kilasflow node options httpRequest --property channel --credential cred_1
```

- `node list` and `node describe` drive the same operation. `describe` needs no
  version argument: when a type is registered at several versions it answers with
  the one the server would resolve a document to (the highest), so the definition
  it prints is the definition that would run.
- An unknown type exits 4 (`not_found`) with the nearest catalogue names in the
  message — a type is a name the caller has to remember, and a bare "not found"
  is not something an agent can act on. `--quiet` on `node list` prints one type
  per line, which is exactly what `describe` takes.
- `node options` resolves a property whose valid values live on the customer's
  own service, which is why it needs `--credential`: the loader runs under the
  host's egress policy with a credential from the caller's own tenant, and the
  server refuses a credential id that is not. `--property` is required; the
  request body carries `property`, and `version`, `mode` and `credentialId` only
  when they were given.
- The catalogue is not paged (the operation takes no parameters), so neither
  verb takes `--limit` or `--cursor`.

## `kilasflow credential`

```bash
kilasflow credential list --limit 20
kilasflow credential get cred_01J8ZP
kilasflow credential test cred_01J8ZP --quiet   # true / false
kilasflow credential create --file smtp.json --yes --quiet   # the new id
kilasflow credential update cred_01J8ZP --file smtp.json --yes
kilasflow credential delete cred_01J8ZP --yes
```

- No secret value is ever returned: the API's read projections leave it out, and
  the CLI carries them unchanged.
- `credential test` POSTs, but it is neither a write nor guarded: it runs the
  credential type's declared probe and stores nothing. It prints the verdict
  (`ok`, `detail`, `untestable`, and which fields came from storage rather than
  from the request); `--quiet` prints the API's own boolean, so
  `kilasflow credential test cred_1 --quiet && …` is a real branch.
- `create` and `update` take `--file <path>|-` and send the document
  **unchanged**, the way `workflow create` does: the fields a credential type
  declares are the server's, so a body that is not valid JSON is refused with
  exit 2 before a request is made, and the verb never has to know a type's
  shape. A field sent as the redaction placeholder keeps the stored secret.
- **`create`, `update` and `delete` are guarded**: a credential is a secret every
  workflow in the tenant can reach, so storing, replacing and removing one needs
  `--yes` and a tenant-wide key — see [Guardrails](#guardrails). `delete`
  answers 204, so its `data` is `{id, status}`.
- Deliberately absent: `types` (the catalogue is reachable through
  `kilasflow api list-credential-types`, and `kilasflow api
  test-credential-payload` tests an unsaved one).

## `kilasflow datastore`

```bash
kilasflow datastore list
kilasflow datastore get ds_01J8ZP
kilasflow datastore rows ds_01J8ZP --limit 50 --cursor cur_2
kilasflow datastore export ds_01J8ZP > rows.csv
kilasflow datastore export ds_01J8ZP --out rows.csv --json
kilasflow datastore create orders --yes --quiet          # the new id
kilasflow datastore rename ds_01J8ZP orders-2026 --yes
kilasflow datastore columns add ds_01J8ZP note --type string --yes
kilasflow datastore columns rename ds_01J8ZP note memo --yes
kilasflow datastore columns drop ds_01J8ZP memo --yes
kilasflow datastore clear ds_01J8ZP --yes                # reports the rows removed
kilasflow datastore delete ds_01J8ZP --yes
```

- `datastore rows` reads one page of a datastore's rows. The API's row filter
  (`match`, `columnName`, `condition`, `value`) is not exposed: it is a query
  language rather than a parameter, and `kilasflow api list-datastore-rows
  --query …` is the escape hatch for it.
- `datastore export` is the second exception to the one-envelope rule, and the
  default is the streaming form: `--out -` is what you get without the flag, so
  `> rows.csv` works, and `--out <path>` writes the file `0600` and reports
  `{path, bytes}`. The path is `GET /datastores/{id}/rows/export`.
- **`create`, `rename`, `delete`, `clear` and the three column verbs are
  guarded.** They are the schema half of the surface, and a data table is what
  every workflow in the tenant reads and writes: each needs `--yes` and a
  tenant-wide key — see [Guardrails](#guardrails).
- `create` takes the name as its argument, because the API's own description of
  the operation is that columns are added afterwards. `columns add` takes the
  column name and `--type string|number|boolean|date`; `columns rename` takes
  the datastore, the column and the new name; `columns drop` takes the datastore
  and the column. `delete` and `columns drop` answer 204, so their `data` is
  `{id, status}`; `clear` reports the rows it removed as `{deleted}`.
- Row writes (`insert`, `update`, `upsert`, `delete`, `increment`, and the CSV
  import) have no verb: they are row data rather than the tenant's schema, and
  they are reachable through `kilasflow api <operation-id>`.

## `kilasflow schedule`

```bash
kilasflow schedule list --limit 20
```

Read-only on purpose: `create`, `update` and `delete` change when a workflow
runs, which is the kind of decision design §4.7 reserves for the user. They are
reachable through `kilasflow api create-schedule` and friends, and phase 2 can
give them guarded verbs.

## `kilasflow tenant`

The operator's view of the deployment. Every verb here needs an **operator
credential** — an API key scoped to the operator tenant.

```bash
kilasflow tenant list
kilasflow tenant get acme
kilasflow tenant users acme
kilasflow tenant delete acme --yes          # irreversible, operator only
```

- The server is what enforces the operator requirement: the verb sends the
  credential it was configured with and carries the refusal back as exit 3 with
  `error.code = "scope_denied"`. A customer's key gets the refusal, never a
  quietly filtered listing.
- `tenant users` returns accounts without any password hash; the API's own
  projection has nowhere to put one.
- **`tenant delete` is guarded** on top of the operator requirement: it deletes
  the tenant and everything it owns — executions and their payload files,
  workflows and their revisions, credentials, schedules, datastores and their
  physical tables, accounts and keys — so it needs `--yes`, and a scoped agent
  token is refused before the deletion is attempted. It is idempotent and
  answers with what it removed rather than 204: `data` is
  `{tenantId, tenantRemoved, removed, datastoreTables, binaries}`, and repeating
  the call until every count is zero and `tenantRemoved` is false is how a
  client that lost the first answer confirms the deletion finished.
- Deliberately absent: `tenant create` (a write no guarded verb needs yet) and
  `tenant api-keys`. The only key operation under a tenant is
  `create-tenant-api-key`, which mints a credential for someone else — an
  operator action, not something to hand an agent as an ergonomic verb.
  `kilasflow api create-tenant-api-key` reaches it, and listing the caller's own
  keys is a different operation (`kilasflow api list-api-keys`, `GET /api-keys`).

## `kilasflow pack validate`

A pack is a file on the machine the command runs on, so this verb is **local**:
no server, no credential, no request. It wraps the same loader the server uses,
which is the point — what it accepts is what the server would register.

```bash
kilasflow pack validate ./packs/telegram
kilasflow pack validate ./packs/telegram/pack.json --json
```

- A directory is checked the way an install reads it: the `pack.sha256` sidecar
  first, then `pack.json`. A **missing path** is exit 2, because nothing was
  checked and "your pack is invalid" would be a lie.
- `data` is `{path, ok, issues}` with every problem the validator found, not just
  the first: an author fixing one problem per run is the failure this exists to
  prevent. Each issue carries `severity` (`error` — everything the validator
  reports blocks an install), `file`, the JSON `path` inside that file, and the
  `message`.
- When there are issues the command exits **1** with
  `error.code = "invalid_pack"`, so a pipeline that installs a pack stops. The
  complete list travels under `error.detail.issues`, because the one-line message
  can only name the first.
- Deliberately absent: `pack install`. The design marks it guarded, but the pack
  surface is a **local loader with no server operation behind it**, so there is
  nothing for a verb to drive and nothing for the guard to protect.

## `kilasflow skills`

The bundle of agent skills — the always-on router plus one skill per domain —
travels **inside the binary**, embedded at build time the way the SPA is. These
five verbs are therefore local: no server, no credential, no checkout, and no
filesystem access beyond the directory an install writes into. That is what
makes them work from the distroless image, where there is nothing to read but
`/app/kilasflow`.

```bash
kilasflow skills list                      # names, versions, triggers
kilasflow skills show kilasflow-triggers   # the SKILL.md itself
kilasflow skills show kilasflow-triggers --reference WEBHOOK_DELIVERY.md
kilasflow skills install                   # ./.agents/skills, project scope
kilasflow skills install --target claude --scope user
kilasflow skills check
kilasflow skills export --format tar --out kilasflow-skills.tar
```

- `install` takes `--target claude|codex|agents|dir:<path>` and `--scope
  project|user`. **Project scope is the default** and resolves under the
  checkout; user scope resolves under `HOME`. A relative `dir:<path>` resolves
  under the scope root as well, and an absolute one is taken as given. The
  default destination is `./.agents/skills`, the generic harness directory this
  repository keeps the pine skill in.
- What an install writes is one directory per skill — `SKILL.md` and its
  `references/*.md` — plus the generated `index.json` at the root: byte for byte
  the files the binary carries, `0644` under `0755` directories, because the
  bundle is published documentation rather than tenant data.
- A file that is already identical counts as installed rather than as work, so
  re-running the verb is idempotent. A file that **differs** stops the install
  with `error.code = "install_conflict"` (exit 5) and the edit left intact: a
  bundle that is half this build's and half yours is the one state nothing can
  describe afterwards. `--force` is what replaces it, and `--dry-run` reports
  where the bundle would go and writes nothing.
- `check` compares an installed bundle with the binary's — every file byte for
  byte, and each installed `SKILL.md`'s `kilasflow_skills_version` stamp — and
  exits **1** with `error.code = "skills_drift"` when they differ, naming the
  file and both versions. Drift is what tells a caller to re-install rather than
  to re-read, so it is a failure with a code and not a warning. The whole list
  travels under `error.detail.issues`, each with a `kind` of `missing`,
  `unreadable`, `version`, `content` or `extra` — the last one for a skill
  directory the binary does not ship, which is the one drift a byte comparison
  cannot see. Nothing is ever repaired: a check that updated silently is a check
  whose answer nobody can trust.
- `export --format json` (the default) writes the index a harness reads without
  parsing markdown, and `--format tar` writes the whole bundle as one
  deterministic archive — path order, no timestamps, no owner — so two exports
  of one binary are byte-identical and a tarball can be cached or checksummed.
  `--out -` (the default) streams it.
- Deliberately absent: a `cursor` target, because naming that directory is that
  harness's business and `dir:<path>` already covers it. Also absent: any verb
  that installs from a checkout or the network, which would be a second source of
  truth for a bundle whose whole point is that the binary is the source.

## `kilasflow mcp serve`

The Model Context Protocol adapter. It publishes this binary's command tree as
MCP **tools** over stdio, so a harness that speaks MCP drives exactly the verbs a
shell drives. It is a verb of this binary rather than a second executable, so the
shipped image carries one command.

```bash
kilasflow mcp serve --url http://127.0.0.1:8080 --token "$KILASFLOW_TOKEN"
```

- **One tool per verb**, named after the verb's path with spaces replaced by
  underscores (`workflow_get`, `datastore_columns_rename`). A tool's
  `inputSchema` is generated from the verb's own metadata, never from a second
  list: the flags its `Flags` function registers become properties — so a flag
  added to a verb appears in the schema without this page being edited — and the
  positional arguments the verb declares (`workflow id`, `revision id`) become
  required properties.
- **A call is a CLI invocation.** The arguments are turned back into argv and
  dispatched through the same `Verb` the CLI dispatches, so the same guard, the
  same configuration chain and the same envelope apply. The result is that
  envelope verbatim in one text block, with `isError` set when the command
  failed: a `404` arrives as `error.code = "not_found"`, not as a protocol
  error.
- **Guarded verbs take `confirm: true`** where the CLI takes `--yes`, and only
  there. Without it the call is refused by the same guard — the result carries
  `error.code = "confirmation_required"` and **nothing is sent**, not even the
  identity read. The adapter passes `--yes` only when `confirm` is true, and it
  never infers consent.
- **`api` also takes `confirm`.** It is not itself a guarded tool — most
  operations it reaches need none — but an `operation_id` that a guarded tool
  wraps asks `api`'s own `confirm` for the same consent that tool would need,
  checked once the id is resolved, before `/api/openapi.json` is read. `api`
  and every guarded tool carry `annotations.destructiveHint: true`
  (the MCP `2026-07-28` schema); `api` always does, because which operation a
  call reaches is not known until the call is made.
- **The server's configuration is the tool call's configuration.** `--url`,
  `--token`, `--config` and `KILASFLOW_URL`/`KILASFLOW_TOKEN` are resolved once,
  when `mcp serve` starts, and every tool call inherits what was resolved; none
  of them is a tool property, so a credential never travels through a client's
  transcript. `--timeout` is the deadline for one tool call.
- **stdin belongs to the protocol.** `--file -` and `--body -` read standard
  input, and on `mcp serve` that stream is the transport: a call asking for `-`
  is refused with a message naming the way out rather than swallowing the next
  request.
- **Descriptions come from the bundle.** A tool's description is the verb's own
  summary, plus its refusal sentence when it is guarded, plus the name of the
  skill in `skills/index.json` that teaches it — so the adapter cannot describe a
  capability the CLI does not have, and a renamed skill shows up in the tool list
  without anything here being edited.
- **What it speaks**: the `initialize` handshake and `tools/list`, `tools/call`
  and `ping`, over newline-delimited JSON-RPC on stdin and stdout. The protocol
  revisions it supports are the handshake ones — `2025-11-25`, `2025-06-18` and
  `2025-03-26` — which describe tools identically; a client asking for one of
  them has its own version echoed back, and anything else is answered with the
  newest. The modern `initialize`-less revisions (`2026-07-28` and later) are out
  of scope here.
- **Deliberately not shipped**: the streamable HTTP transport, which design §6
  puts after stdio; `resources`, `prompts`, sampling and elicitation, which the
  adapter answers with `method not found` rather than declaring capabilities it
  does not implement; and the two verbs that are this process's own server modes
  (`serve` and `mcp serve`), which are not published as tools.

## Output contract

One JSON envelope per invocation, on stdout, when `--json` is passed **or**
stdout is not a terminal. Human-readable text otherwise. The rule is about
where the output is going: a pipeline is read by a program, a terminal by a
person, and an agent can force either.

```json
{ "ok": true,  "data": { }, "meta": { "operation": "run-workflow", "durationMs": 42 } }
{ "ok": false, "error": { "code": "scope_denied", "message": "…", "status": 403,
                          "detail": { "problem": { } } } }
```

A real one, from this build against a local server:

```json
{"ok":true,"data":{"status":"ok","version":"0.1.0-dev"},"meta":{"operation":"get-health","durationMs":2}}
```

- `data` is the operation's response body, unmodified. When it is an object,
  the API's own fields are what a caller reads — the CLI does not rename them.
- `error.detail.problem` carries an RFC 9457 problem document as the server
  sent it, the way the JavaScript SDK's `sdk/src/http.ts` already surfaces it,
  less anything that could be a secret. An `errors[]` value at the location
  `body` is dropped, because that is where an older server echoed a refused
  body back whole, and a field named like a credential — `token`, `password`,
  `fields`, and `value` inside an error's own value — reads `[redacted]`. A
  compile refusal's codes and an idempotency conflict's come through whole. A
  non-JSON error body (a proxy's HTML page, say) goes under
  `error.detail.body`, truncated to 4 KiB. `error.detail.execution` carries the
  record of a run that failed under `run --wait`, which is the one failure that
  is about the caller's work rather than about the call. `error.detail.issues`
  carries every problem `pack validate` found, because its one-line message can
  only name the first.
- `meta.operation` is the operation id, or the verb path for a local verb, so a
  log line can be traced back to the API call it came from.
- Nothing but the envelope goes to stdout in JSON mode. `--verbose` traces
  requests (`> GET <url>`, `< 200 <n> bytes`) to **stderr**, with
  `Authorization` and any credential field redacted. A token is never printed:
  not in a trace, not in an error message that quotes a URL, and not in a
  problem document the server sent back — the one place a credential could
  reach the envelope from outside the CLI is redacted before it is carried.

`--quiet` prints only the primary result — an id, a status, or a verb's own
verdict (a validation's `true`/`false`, an evaluated expression's value) — for
shell pipelines. It never turns JSON mode *on*, and `--json` alongside it wins.
A listing prints one id per line, so `kilasflow workflow list --quiet | while
read wf; do …` is a loop over what exists.

```bash
$ kilasflow health --quiet
ok

$ kilasflow workflow create --file wf.json --quiet   # the new workflow id
wf_01J8ZP…
```

## Exit codes

The exit code is the machine-readable half of the contract: it tells an agent
whether to fix the call, stop and ask, wait, or report.

| Code | Meaning | Agent response |
| --- | --- | --- |
| 0 | success | continue |
| 1 | error (5xx, network, unexpected shape) | report, do not retry blindly |
| 2 | usage error | fix the invocation |
| 3 | **refused by authority** (`403`, `401`, a guarded verb without `--yes`, or a guarded verb on a scoped token) | do not retry; ask the user or drop the step |
| 4 | not found (`404`) | the resource does not exist for this tenant |
| 5 | conflict (`409`) — optimistic concurrency, idempotency mismatch | re-read, then decide |
| 6 | not ready (`503`, or `429`) — migrations outstanding, subsystem unconfigured, throttled | wait, then retry |

Statuses the design's table does not name are mapped as well, and the mapping
is part of the contract: `400` and `422` exit 2 (`bad_request`), `401` exits 3
(`unauthenticated`), `429` exits 6 (`rate_limited`).

Code 3 is the important one, because it is not one condition. Three different
refusals share it and are told apart by `error.code`:

| `error.code` | Means |
| --- | --- |
| `scope_denied` | a `403`, or a guarded verb called with a scoped agent token: this credential may not do this, whatever the flags |
| `unauthenticated` | a `401`: no usable credential was presented |
| `confirmation_required` | a guarded verb was called without `--yes` |

The codes the CLI derives from a status, or raises for a local failure, are
`usage`, `bad_request`, `not_found`, `conflict`, `not_ready`, `rate_limited`,
`server_error`, `network_error` (the request never produced a response),
`http_error` (a status the contract does not name), `config_error` (a
configuration file that could not be read or written), `unexpected_response` (a
response body that is not the shape the call needs), `output_error` (the
invocation was right and the write it asked for was refused: exit 1, as a full
disk or a read-only mount is not a mistake in the call) and `error` (a local
failure that is not an HTTP result). That is the generic vocabulary, not a
closed set: a verb that can name a failure only it can produce raises its own
code — `execution_failed`, `invalid_pack`, `stream_closed`, `timeout`,
`skills_drift`, `install_conflict`, `bundle_unreadable` — and
documents it in that verb's section above.

## Configuration and token handling

Precedence, highest first:

1. `--url` / `--token`
2. `KILASFLOW_URL` / `KILASFLOW_TOKEN`
3. `--config <path>` — a TOML file naming the server and the token
4. `~/.config/kilasflow/config.toml`, created `0600`

`--config <path>` **replaces** the default path rather than merging with it, and
`--config ''` means "no configuration file at all". The built-in fallback is
`http://127.0.0.1:8080`, the default installation's address. An explicitly empty
`--url ''` means "no server" — `version` then reports only the binary's own
version, and a verb that needs a server exits 2 with a message naming all four
ways to set one.

The file is TOML, holds `url` and `token`, and is written `0600` by
`auth login`, which also tightens the mode of a file it did not create: this
file holds a credential, and a mode that has always been wrong is not made right
by being rewritten. The directory above it is created `0700`.

The token is a secret and is treated as one:

- `auth login --token -` reads it from stdin, so it never lands in shell
  history or a process listing, and `auth login --token <value>` is refused
  outright.
- `--token-file` exists for CI and is refused when the file is group- or
  world-readable, with a message naming the `chmod 600` that fixes it.
- The token is never printed. `auth whoami` prints the tenant, the key and the
  binding — never the credential.
- A `401` from a missing or invalid credential exits 3 with
  `error.code = "unauthenticated"`, which is a different decision from the
  `scope_denied` a `403` produces.

## The exceptions to "always one envelope"

Six verbs write bytes rather than an envelope, because buffering them would
defeat the point:

- `kilasflow exec tail` emits **NDJSON**: one event object per line
  (`{"id","event","data"}`), one line per event, until the run reaches an
  outcome or the deadline passes (which exits 6, so a caller can tell "still
  running" from "finished"). `--json` forces it on a terminal; a pipe gets it
  without the flag.
- `kilasflow datastore export` writes **raw CSV** to stdout, because the point is
  to pipe the bytes somewhere else. `--out -` is the default; `--out <path>`
  writes a file and reports `{path, bytes}` in the envelope instead.
- `kilasflow skills export` writes **the index or the tarball**, the same way and
  for the same reason: `--out -` is the default, and `--out <path>` reports
  `{format, path, bytes, skills}` in the envelope.
- `kilasflow api <operation-id> --out -` writes the operation's response body to
  stdout **byte for byte**, whatever its media type: the escape hatch is what an
  agent uses for an operation no verb wraps, and its output has to be as
  unfiltered as the API's own.
- `kilasflow skills show` writes **the document** — `SKILL.md`, frontmatter
  included, or one reference file — on a pipe as well as on a terminal, so
  `kilasflow skills show kilasflow-triggers > SKILL.md` lifts one file out of the
  binary. `--json` asks for the envelope instead, with the document in
  `data.content`, and `--quiet` prints its path. It is the one streamed verb
  whose stream is silent by default rather than on a flag, and it is here because
  the alternative writes JSON into a file named `.md`.
- `kilasflow mcp serve` writes **JSON-RPC frames**, because stdout is the
  protocol's transport: a client reads one message per line and nothing else
  may appear there. The adapter's own diagnostics go to stderr, and so does a
  failure — a frame stream with an envelope appended to it would be a stream the
  client can no longer parse.

Each is documented here and in the verb's own `--help`. For a streamed
invocation a failure is reported on **stderr** and in the exit code rather than
as an envelope on stdout, because appending one to a byte stream would corrupt
the thing the caller is reading.

## Guardrails

A *guarded* verb does something that reaches beyond the caller: activating a
workflow publishes a public endpoint; deleting one destroys it; storing a
credential hands every workflow in the tenant a secret. Guarded verbs have **two
gates**, and they answer different questions.

**Consent: `--yes`.** A guarded verb refuses to run without it, exiting 3 with
`error.code = "confirmation_required"` and a message naming what the verb does.
Consent is never inferred — not from `--json`, not from `--quiet`, not from
stdout being a pipe rather than a terminal. Automation that means it says so, and
an agent passes `--yes` only on an explicit instruction from the user, never
because the call failed once.

**Authority: a tenant-wide key.** A guarded verb also refuses a **scoped agent
token**, whatever the flags say, exiting 3 with `error.code = "scope_denied"`
and a message naming what the verb would do and what would be allowed to do it.
`--yes` is the caller's consent, not their authority: a key minted for one
workflow cannot publish a public endpoint by being confirmed twice. The check is
the `scopes` list on the caller's own identity (`auth whoami` / `GET
/api/v1/auth/me`) — a tenant-wide key carries none — and it is made **before**
the verb's operation, so a scoped token never reaches a mutation the server
would then have to refuse.

The order of the two matters in one direction: without `--yes` nothing is sent
at all, not even the identity read, because a refusal about consent costs the
caller nothing. With `--yes` on a scoped token, the answer is
`confirmation_required`'s sibling `scope_denied`: the caller can confirm all it
likes and it will still be refused, so the code it gets back is the one that
means "drop this step".

Every guarded verb carries its refusal sentence in the verb registry, and
`kilasflow help` marks each of them `[requires --yes]`. As of this build they
are: `workflow activate|deactivate|delete`,
`credential create|update|delete`, `datastore create|rename|delete|columns
add|columns rename|columns drop|clear`, and `tenant delete`. `pack install` also
carries the mark in the design but has no server operation behind it — the pack
surface is a local loader — so there is nothing for a verb to guard yet.

Both gates apply through [the escape hatch](#the-escape-hatch-kilasflow-api-operation-id)
too: an operation id that a guarded verb wraps is guarded whichever name
reaches it, so `kilasflow api delete-workflow` asks for the same `--yes` and
the same tenant-wide key `kilasflow workflow delete` would, and the MCP `api`
tool asks for the same `confirm`.
