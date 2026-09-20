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
  `api <operation-id>`, `auth login`, `auth logout` and `pack validate` — either
  need no server or reach several operations, and each says so in its own section
  below.
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

## Command tree (phase 1)

Every row below exists in the binary, and the state column says so: it is kept
because a later phase adds rows (`workflow activate`, `pack install`, the
guarded verbs) and a reader has to be able to tell what is there from what is
planned.

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
| `kilasflow workflow versions` | `list-workflow-versions` | available |
| `kilasflow workflow get-version` | `get-workflow-version` | available |
| `kilasflow workflow publish-events` | `list-workflow-publish-events` | available |
| `kilasflow workflow export` | `export-workflow` | available |
| `kilasflow workflow diagnostics` | `workflow-diagnostics` | available |
| `kilasflow run` | `run-workflow` | available |
| `kilasflow exec list` | `list-executions` | available |
| `kilasflow exec get` | `get-execution` | available |
| `kilasflow exec trace` | `stream-execution-events` | available |
| `kilasflow exec tail` | `stream-execution-events` | available |
| `kilasflow exec cancel` | `cancel-execution` | available |
| `kilasflow node list` | `list-node-types` | available |
| `kilasflow node describe` | `list-node-types` (the catalogue, reduced to one entry) | available |
| `kilasflow node options` | `load-node-property-options` | available |
| `kilasflow credential list` | `list-credentials` | available |
| `kilasflow credential get` | `get-credential` | available |
| `kilasflow credential test` | `test-credential` | available |
| `kilasflow datastore list` | `list-datastores` | available |
| `kilasflow datastore get` | `get-datastore` | available |
| `kilasflow datastore rows` | `list-datastore-rows` | available |
| `kilasflow datastore export` | `export-datastore-rows` | available |
| `kilasflow schedule list` | `list-schedules` | available |
| `kilasflow tenant list` | `list-tenants` | available |
| `kilasflow tenant get` | `get-tenant` | available |
| `kilasflow tenant users` | `list-tenant-users` | available |
| `kilasflow pack validate` | — (local; runs the pack loader) | available |

Three things the design's tree lists are deliberately absent from this phase:

- `kilasflow workflow activate|deactivate|delete` — *guarded*, and phase 2's,
  where a scoped agent token exists to refuse them.
- `kilasflow tenant api-keys` and `kilasflow pack install` — writes (minting a
  credential for another tenant, and installing code), so they belong with the
  guarded verbs in phase 2.
- `kilasflow embed session create`, `kilasflow node icons|grammar`,
  `kilasflow credential types`, `kilasflow schedule create|update|delete`,
  `kilasflow tenant create` — no phase-1 verb. There is a useful operation
  behind each one and no caller that reaches for it often enough to justify a
  name, so `kilasflow api <operation-id>` is the answer rather than a release.
  `kilasflow webhook list` is the one entry blocked on another ticket
  (`FEAT-77rveq`): the operation it would wrap (`GET /workflows/{id}/webhooks`,
  `list-workflow-webhooks`) is served today and is reachable through
  `kilasflow api list-workflow-webhooks`.

The mutating verbs that have no command of their own — `workflow
update|restore|import|duplicate|validate`, `schedule create|update|delete`, the
datastore row writes, `tenant create|delete`, `create-tenant-api-key`,
`create-tenant-user`, `revoke-api-key`, `pack install` — are reachable the same
way. A verb is added when a caller has to reach for the operation often enough
that naming it is worth the surface; until then the escape hatch is the answer,
not a release.

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
- The escape hatch is a **naming** bypass, never an authority bypass: it sends
  the configured credential, adds and removes no scope, and carries a `403`
  through as exit 3 (`scope_denied`) exactly like a named verb.

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

The read surface, plus `create`. Each verb addresses the stable path documented
in the [API contract](/reference/api/) rather than resolving it from
`/api/openapi.json` at call time: `workflow get` should be one request.

```bash
kilasflow workflow list --limit 20 --json
kilasflow workflow get wf_01J8ZP
kilasflow workflow create --file workflow.json --quiet   # the new id
kilasflow workflow versions wf_01J8ZP --cursor cur_2
kilasflow workflow get-version wf_01J8ZP wfv_01J8ZP
kilasflow workflow publish-events wf_01J8ZP
kilasflow workflow export wf_01J8ZP --format n8n
kilasflow workflow diagnostics wf_01J8ZP --version-id wfv_01J8ZP
```

- A listing's payload is `{items, count, nextCursor}`. The API is not uniform —
  workflows answer with a bare array and a cursor in `X-Next-Cursor`, revisions
  with an object carrying both — and normalising that here is what lets one
  agent loop page every listing the same way.
- `workflow create --file <path>|-` sends the document **unchanged**. A document
  that is not valid JSON is refused with exit 2 before a request is made.
- `--quiet` on a listing prints one id per line; on `workflow create` it prints
  the id the API named in its `Location` header.
- `workflow diagnostics` wraps an operation the contract page does not list
  (`GET /workflows/{id}/diagnostics`); the generated reference does.
- Deliberately absent: `update`, `restore`, `import`, `duplicate`, `validate`,
  `activate`, `deactivate`, `delete`. Activation and deletion are guarded and
  belong to phase 2; every one of them is reachable today through
  `kilasflow api <operation-id>`.

## `kilasflow run`

Starts a run, and optionally waits for it.

```bash
kilasflow run wf_01J8ZP --input '{"n":1}' --trigger manual
kilasflow run wf_01J8ZP --input-file payload.json --wait --quiet   # the execution id
```

- The body is `{"input": <json>, "triggerNodeId": <node>}`, and either half may
  be omitted; a bare `run` starts every trigger with no input.
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
kilasflow exec trace exec_01K7 --json
kilasflow exec tail exec_01K7
```

- `exec list` takes `--workflow`, `--status` (repeatable), `--trigger`,
  `--limit` and `--cursor`.
- `exec cancel` is **not** guarded: it stops work rather than publishing or
  destroying anything, and it answers 202 because the server accepted the
  request, not because the run stopped. Read the execution back to see what
  happened.
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
```

- No secret value is ever returned: the API's read projections leave it out, and
  the CLI carries them unchanged.
- `credential test` POSTs, but it is neither a write nor guarded: it runs the
  credential type's declared probe and stores nothing. It prints the verdict
  (`ok`, `detail`, `untestable`, and which fields came from storage rather than
  from the request); `--quiet` prints the API's own boolean, so
  `kilasflow credential test cred_1 --quiet && …` is a real branch.
- Deliberately absent: `create`, `update`, `delete` (phase 2, guarded) and
  `types` (the catalogue is reachable through `kilasflow api
  list-credential-types`).

## `kilasflow datastore`

```bash
kilasflow datastore list
kilasflow datastore get ds_01J8ZP
kilasflow datastore rows ds_01J8ZP --limit 50 --cursor cur_2
kilasflow datastore export ds_01J8ZP > rows.csv
kilasflow datastore export ds_01J8ZP --out rows.csv --json
```

- `datastore rows` reads one page of a datastore's rows. The API's row filter
  (`match`, `columnName`, `condition`, `value`) is not exposed: it is a query
  language rather than a parameter, and `kilasflow api list-datastore-rows
  --query …` is the escape hatch for it.
- `datastore export` is the second exception to the one-envelope rule, and the
  default is the streaming form: `--out -` is what you get without the flag, so
  `> rows.csv` works, and `--out <path>` writes the file `0600` and reports
  `{path, bytes}`. The path is `GET /datastores/{id}/rows/export`.
- Row writes, `create`, `rename`, `delete`, `columns` and `clear` have no verb:
  the writes are phase 2's, and the rest are reachable through
  `kilasflow api <operation-id>`.

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
```

- The server is what enforces the operator requirement: the verb sends the
  credential it was configured with and carries the refusal back as exit 3 with
  `error.code = "scope_denied"`. A customer's key gets the refusal, never a
  quietly filtered listing.
- `tenant users` returns accounts without any password hash; the API's own
  projection has nowhere to put one.
- Deliberately absent: `tenant create` (a write, phase 2) and `tenant api-keys`.
  The only key operation under a tenant is `create-tenant-api-key`, which mints a
  credential for someone else — an operator action, not something to hand an
  agent as an ergonomic verb. `kilasflow api create-tenant-api-key` reaches it,
  and listing the caller's own keys is a different operation
  (`kilasflow api list-api-keys`, `GET /api-keys`).

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
- Deliberately absent: `pack install` (guarded, phase 2).

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
- `error.detail.problem` carries an RFC 9457 problem document verbatim, the way
  the JavaScript SDK's `sdk/src/http.ts` already surfaces it. A non-JSON error
  body (a proxy's HTML page, say) goes under `error.detail.body`, truncated to
  4 KiB. `error.detail.execution` carries the record of a run that failed under
  `run --wait`, which is the one failure that is about the caller's work rather
  than about the call. `error.detail.issues` carries every problem `pack
  validate` found, because its one-line message can only name the first.
- `meta.operation` is the operation id, or the verb path for a local verb, so a
  log line can be traced back to the API call it came from.
- Nothing but the envelope goes to stdout in JSON mode. `--verbose` traces
  requests (`> GET <url>`, `< 200 <n> bytes`) to **stderr**, with
  `Authorization` and any credential field redacted. A token is never printed:
  not in a trace, not in an error message that quotes a URL, and not in a
  problem document the server sent back — the one place a credential could
  reach the envelope from outside the CLI is redacted before it is carried.

`--quiet` prints only the primary identifier — an id, a status — for shell
pipelines. It never turns JSON mode *on*, and `--json` alongside it wins. A
listing prints one id per line, so `kilasflow workflow list --quiet | while read
wf; do …` is a loop over what exists.

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
| 3 | **refused by authority** (`403`, `401`, or a guarded verb without `--yes`) | do not retry; ask the user or drop the step |
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
| `scope_denied` | a `403`: this credential may not do this |
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
code — `execution_failed`, `invalid_pack`, `stream_closed`, `timeout` — and
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

Three verbs write bytes rather than an envelope, because buffering them would
defeat the point:

- `kilasflow exec tail` emits **NDJSON**: one event object per line
  (`{"id","event","data"}`), one line per event, until the run reaches an
  outcome or the deadline passes (which exits 6, so a caller can tell "still
  running" from "finished"). `--json` forces it on a terminal; a pipe gets it
  without the flag.
- `kilasflow datastore export` writes **raw CSV** to stdout, because the point is
  to pipe the bytes somewhere else. `--out -` is the default; `--out <path>`
  writes a file and reports `{path, bytes}` in the envelope instead.
- `kilasflow api <operation-id> --out -` writes the operation's response body to
  stdout **byte for byte**, whatever its media type: the escape hatch is what an
  agent uses for an operation no verb wraps, and its output has to be as
  unfiltered as the API's own.

Each is documented here and in the verb's own `--help`. For a streamed
invocation a failure is reported on **stderr** and in the exit code rather than
as an envelope on stdout, because appending one to a byte stream would corrupt
the thing the caller is reading.

## Guardrails

A *guarded* verb does something that reaches beyond the caller: activating a
workflow publishes a public endpoint; deleting one destroys it. Guarded verbs
refuse to run without `--yes`, exiting 3 with
`error.code = "confirmation_required"` and a message naming what the verb does.

**No verb in this phase is guarded.** Every operation the design marks guarded —
`workflow activate|deactivate|delete`, `credential create|update|delete`,
`datastore delete`, `pack install`, `tenant delete` — belongs to phase 2 or is
blocked on another ticket, so `--yes` is accepted by every verb today and has no
effect. The contract above is what those verbs will follow, and it is written
down now because an agent's instructions have to be able to promise it. The
primitive itself is implemented and proven: `--yes` is parsed, an unguarded verb
ignores it, and a verb marked guarded refuses without it — the guard's own tests
exercise that refusal against a verb registered inside the test, so the day
phase 2 marks the first real verb, the contract is already the behaviour.

`--yes` is never implied by `--json`, by `--quiet`, or by stdout not being a
terminal. Automation that means it says so, and an agent passes `--yes` only
on an explicit instruction from the user — never because the call failed once.
