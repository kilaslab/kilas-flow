---
name: using-kilasflow-skills
description: Use when starting a KilasFlow session, and before any workflow, node, trigger, credential, datastore, run or debugging work, whenever a verb, an operation id or a skill has to be chosen. Triggers on "kilasflow", "workflow", "node", "run", "trigger", "credential", "datastore", "activate", "debug".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow context
  - kilasflow api
  - kilasflow node list
  - kilasflow node describe
  - kilasflow node options
  - kilasflow workflow list
  - kilasflow workflow get
  - kilasflow workflow create
  - kilasflow run
  - kilasflow exec get
  - kilasflow exec trace
  - kilasflow credential list
  - kilasflow datastore rows
kilasflow_operations:
  - get-workflow
  - run-workflow
  - get-execution
  - stream-execution-events
  - list-node-types
  - load-node-property-options
  - activate-workflow
  - delete-workflow
  - import-workflow
  - create-credential
  - upsert-datastore-row
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped: []
---

## Non-negotiables

1. Invoke the matching skill before you act, not after something failed. The tree under "Decision tree" is the map: a workflow, node, trigger, credential, datastore, run or debugging task has a skill that already carries the surface, and loading it costs less than one wrong call.
2. Read the surface from the instance; never recall it. Node types, parameter names, credential types and expression roots change between versions, and they are served: `kilasflow node list` (`list-node-types`) and `kilasflow node describe <type>` (`list-node-types`) for a node, `kilasflow node options <type> --property <key>` (`load-node-property-options`) for what a field may hold, and `kilasflow api <operation-id>` (`api`) for anything else the running server serves. A remembered parameter name is a silent failure: the server ignores configuration a node does not declare.
3. Validate, then verify. A 2xx is not verification and draft validation is structural — it says nothing about a graph that runs. Save, re-read with `kilasflow workflow get <workflowId>` (`get-workflow`) and check the document that came back; run, then read the trace with `kilasflow exec get <executionId>` (`get-execution`) and `kilasflow exec trace <executionId>` (`stream-execution-events`).
4. Secrets never appear in a workflow document, a CLI argument or chat. A credential is referenced by id, its value is supplied once by whoever owns it, and it is never echoed into a transcript — the kilasflow-credentials skill carries the rule in full.
5. Guardrails, in order of how much they cost to get wrong. Activation publishes a public endpoint and deletion destroys a resource, and both are guarded verbs: `kilasflow workflow activate <workflowId>` (`activate-workflow`), `workflow deactivate`, `workflow delete`, the `credential` and `datastore` writes and `tenant delete` all refuse without `--yes`, exiting 3 with `confirmation_required`, and `kilasflow api <operation-id>` asks for the same confirmation when the operation is one of theirs. An import writes a workflow the instance then serves and has no verb, so `kilasflow api import-workflow` carries no guard of its own. An embed session can activate only when it carries `workflow:activate`, and can never delete, import or manage datastores at any scope — that is the design, not a bug to work around (the kilasflow-embedding skill). Ask the user before any of them, and pass `--yes` only on an explicit instruction from the user, never because `--json`, `--quiet` or a non-TTY made it convenient.

## Strong defaults

- Start from the validated example: `kilasflow skills show using-kilasflow-skills --reference EXAMPLE.md` is a connected two-node document and the five commands that save, run and read it back; every other skill's example is a larger version of it.
- Open every session with `kilasflow context`. It is one read-only call carrying the server, the identity, the workflows, the datastores and the node-type count, and it is local, so it works before you know anything else (internal/cli/context.go).
- Reach the whole API through `kilasflow api <operation-id>` (`api`) whenever a verb does not exist: `--path name=value` fills a placeholder, `--body @file.json` sends a body, `--header name=value` sends a header, and `--list` prints the ids the running server actually serves. It refuses what a scoped token refuses — a naming bypass, not an authority bypass.
- Expression before Code node. A value read or transformed with `{{ }}` is visible in the document and in every trace; a Code node hides it. Invoke the kilasflow-expressions skill before writing one.
- Validate before you save. `kilasflow workflow validate --file wf.json` (`validate-workflow-document`) checks a draft the way the API would, compiles it, and saves nothing; fix every diagnostic it prints, then `kilasflow workflow create --file wf.json`.
- Say which skills informed a write. `--skills-used kilasflow-workflow-lifecycle,kilasflow-expressions` on a mutating verb records the names on the revision it writes, so adoption is measurable from the history; name the skills you loaded in your report as well.
- Describe before configuring. `kilasflow node describe <type>` gives the definition the server will run, and `kilasflow node options <type> --property <key>` loads a field's values from the service that owns them; neither is guessable.
- A step you would build twice is a sub-workflow, and a workflow you would run by hand twice wants a trigger. Invoke the kilasflow-triggers skill.
- Make a retry safe before you retry. The server honours `Idempotency-Key` on `run-workflow` and on a datastore row write: the same key with the same request is answered with the first outcome, marked `Idempotent-Replayed: true`, and repeats no side effect; the same key with a different request is refused `409`. No verb carries the key today, so a retry-safe run is `kilasflow api run-workflow --path id=<workflowId> --header Idempotency-Key=<key> --body @input.json`, and a retry-safe write is the same header on `upsert-datastore-row` or `insert-datastore-row`. Without a key, a retry is a second execution.
- Watch what a run did before reporting it: `kilasflow run <workflowId> --wait` (`run-workflow`) to its terminal status, then the trace, then `kilasflow exec list --workflow <workflowId>` (`list-executions`) when you need the history.
- Name a resource by the id its own listing returns. `kilasflow workflow list` (`list-workflows`), `kilasflow credential list` (`list-credentials`) and `kilasflow datastore rows <datastoreId>` (`list-datastore-rows`) page with `--limit` and `--cursor`; a name is not an id and a truncated listing is not an absence.

## Decision tree

```
what is in front of you?
|
+-- the start of a session
|     -> kilasflow context
|        then the skill that matches the task, from the table below
|
+-- a workflow to create, edit, publish, activate, version or restore
|     -> kilasflow-workflow-lifecycle
|
+-- a node to configure, or a parameter name you are not certain of
|     -> kilasflow-node-configuration (kilasflow node describe <type> first)
|
+-- a value to read or transform between nodes
|     -> kilasflow-expressions
|
+-- something other than you must start the workflow
|     -> kilasflow-triggers
|
+-- it failed, produced the wrong output, or never started
|     -> kilasflow-debugging
|
+-- a failure must be caught, retried, alerting or answered to a caller
|     -> kilasflow-error-handling
|
+-- an AI agent, a chat, an LLM chain or answers from documents (RAG)
|     -> kilasflow-ai-agents
|
+-- items to batch or loop, a file or binary payload, one workflow calling another
|     -> kilasflow-flow-patterns
|
+-- a secret, a token, an API key or a database connection
|     -> kilasflow-credentials
|
+-- rows, columns, filters or CSV
|     -> kilasflow-datastore
|
+-- n8n in, or n8n out
|     -> kilasflow-import-export
|
+-- a node type of your own
|     -> kilasflow-node-packs
|
+-- customers, tenants, iframes or branding
|     -> kilasflow-embedding
|
+-- the installation itself: roles, readiness, migrations, backups
|     -> kilasflow-operations
|
+-- no verb for what you need
      -> kilasflow api <operation-id>   (kilasflow api --list enumerates them)
```

### The skills

| Skill | Load when |
| --- | --- |
| `kilasflow-workflow-lifecycle` | creating, editing, publishing, activating, versioning or restoring a workflow; "workflow", "publish", "activate", "version", "restore", "save" |
| `kilasflow-expressions` | a node parameter reads or transforms data: `{{ }}`, `$json`, `$node`, `$now`, Luxon, an expression error |
| `kilasflow-node-configuration` | configuring a node, or a parameter name, option source, port or `typeVersion` is in question: "node describe", "node options", "parameter", "load options" |
| `kilasflow-triggers` | the workflow must be started by something else: "webhook", "trigger", "schedule", "form", "activation" |
| `kilasflow-debugging` | a run failed, produced the wrong output or never started: "failed", "error", "wrong output", "trace" |
| `kilasflow-error-handling` | a failure must be caught, retried, alerted on or answered to an HTTP caller: "retry", "timeout", "error workflow", "respond" |
| `kilasflow-ai-agents` | building an agent, chat, LLM chain or RAG workflow: "agent", "AI", "LLM", "chat model", "tool", "memory", "RAG", "embeddings", "vector store" |
| `kilasflow-flow-patterns` | items must be batched or looped, a file is read or produced, or one workflow calls another: "loop", "batch", "binary", "file", "PDF", "sub-workflow", "execute workflow" |
| `kilasflow-credentials` | a secret, token or key is involved: "credential", "secret", "token", "auth", "api key" |
| `kilasflow-datastore` | reading or writing a table: "datastore", "table", "columns", "rows", "filter", "CSV" |
| `kilasflow-import-export` | migrating from n8n or exporting back: "n8n", "migration", "import", "export", "lossy" |
| `kilasflow-node-packs` | authoring, validating or installing a node type: "pack", "pack.json", "WASM", "sidecar" |
| `kilasflow-embedding` | a host product integrates KilasFlow: "embed", "tenant", "iframe", "API key", "branding" |
| `kilasflow-operations` | operating the installation: "deploy", "upgrade", "migrate", "backup", "ready", "tenant" |

### The command surface

<!-- generated:command-reference -->
```text
# every verb this binary implements, and the operation it drives.
# generated from `kilasflow help --json` by scripts/skills-command-reference;
# run `make generate-skills-command-reference` after the command tree changes.
kilasflow serve                     local                              run the API, worker and scheduler (the default when no verb is given)
kilasflow version                   local                              print this binary's version and, when a server is reachable, its version
kilasflow health                    get-health                         report that the process is up (does not check dependencies)
kilasflow ready                     get-ready                          report whether the instance can serve; exits 6 when it cannot
kilasflow help                      local                              list the verbs this binary implements
kilasflow context                   local                              read-only briefing for a first turn: server, identity, workflows, datastores, node types
kilasflow api                       local                              call any operation the running server serves, by operation id (--list to enumerate)
kilasflow auth login                local                              validate a token against the server and store it at 0600 in the configuration file
kilasflow auth logout               local                              remove the stored token, keeping the server URL
kilasflow auth whoami               get-me                             print the identity the configured credential resolves to
kilasflow workflow list             list-workflows                     list workflows, newest first (--limit, --cursor)
kilasflow workflow get              get-workflow                       read one workflow by id
kilasflow workflow create           create-workflow                    create a workflow from a canonical document (--file <path>|-)
kilasflow workflow validate         validate-workflow-document         check a draft document the way the API would, saving nothing (--file <path>|-)
kilasflow workflow duplicate        duplicate-workflow                 copy a workflow (--name <name> to rename the copy)
kilasflow workflow versions         list-workflow-versions             list a workflow's revisions (--limit, --cursor)
kilasflow workflow get-version      get-workflow-version               read one revision of a workflow, with its document
kilasflow workflow publish-events   list-workflow-publish-events       read a workflow's publish audit trail
kilasflow workflow export           export-workflow                    export a workflow as n8n JSON, with what could not be carried
kilasflow workflow diagnostics      workflow-diagnostics               read the import report stored with a revision
kilasflow workflow activate         activate-workflow [guarded]        activate a workflow's latest revision, publishing its endpoint (guarded)
kilasflow workflow deactivate       deactivate-workflow [guarded]      take a workflow's published endpoint offline (guarded)
kilasflow workflow delete           delete-workflow [guarded]          delete a workflow, retaining its audit trail (guarded)
kilasflow run                       run-workflow                       start a workflow run (--wait to watch it finish)
kilasflow exec list                 list-executions                    list executions, newest first (--workflow, --status, --trigger, --limit, --cursor)
kilasflow exec get                  get-execution                      read one execution, with its node-run trace
kilasflow exec cancel               cancel-execution                   request cancellation of a queued or running execution
kilasflow exec retry                retry-execution                    start a fresh execution of the same workflow, input and trigger as one that finished
kilasflow exec trace                stream-execution-events            collect an execution's events into one envelope, stopping at its outcome
kilasflow exec tail                 stream-execution-events            stream an execution's events as they arrive (one object per line; the documented exception)
kilasflow debug eval                eval-expression                    evaluate an expression against an execution's stored trace, as a node's parameters saw it (--execution <id>, --node <nodeId>, --run <n>, --item <n>)
kilasflow node list                 list-node-types                    list the node catalogue the server will run
kilasflow node describe             list-node-types                    print one node type's definition from the catalogue
kilasflow node options              load-node-property-options         load a property's selectable values from the service that owns them (--property, --version, --mode, --credential)
kilasflow credential list           list-credentials                   list stored credentials, without any secret value (--limit, --cursor)
kilasflow credential get            get-credential                     read one credential, without its secret value
kilasflow credential test           test-credential                    run the credential type's probe and report pass or fail
kilasflow credential create         create-credential [guarded]        store a credential from a JSON document (--file <path>|-, guarded)
kilasflow credential update         update-credential [guarded]        replace a stored credential from a JSON document (--file <path>|-, guarded)
kilasflow credential delete         delete-credential [guarded]        remove a stored credential (guarded)
kilasflow datastore list            list-datastores                    list datastores (--limit, --cursor)
kilasflow datastore get             get-datastore                      read one datastore and its columns
kilasflow datastore rows            list-datastore-rows                read one page of a datastore's rows (--limit, --cursor)
kilasflow datastore export          export-datastore-rows              export a datastore's rows as CSV (--out <path> writes a file; the default streams to stdout)
kilasflow datastore create          create-datastore [guarded]         create a data table; columns are added afterwards (guarded)
kilasflow datastore rename          rename-datastore [guarded]         rename a data table (<datastoreId> <newName>, guarded)
kilasflow datastore delete          delete-datastore [guarded]         delete a data table and every row it holds (guarded)
kilasflow datastore clear           clear-datastore [guarded]          delete every row of a data table, keeping its schema (guarded)
kilasflow datastore columns add     add-datastore-column [guarded]     append one column to a data table (<datastoreId> <name> --type <type>, guarded)
kilasflow datastore columns rename  rename-datastore-column [guarded]  rename one column of a data table (<datastoreId> <name> <newName>, guarded)
kilasflow datastore columns drop    delete-datastore-column [guarded]  drop one column of a data table and the values in it (guarded)
kilasflow schedule list             list-schedules                     list cron schedules (--limit, --cursor)
kilasflow tenant list               list-tenants                       list every tenant in this deployment (operator credential)
kilasflow tenant get                get-tenant                         read one tenant (operator credential)
kilasflow tenant users              list-tenant-users                  list a tenant's accounts, without any password hash (operator credential)
kilasflow tenant delete             delete-tenant [guarded]            delete a tenant and everything it owns (operator credential, guarded)
kilasflow pack validate             local                              check a pack directory or a pack.json the way the server would load it
kilasflow skills list               local                              list the skills this binary ships, with the situation each one is for
kilasflow skills show               local                              print one skill's SKILL.md, or one of its reference files (--reference <file>)
kilasflow skills install            local                              install the bundle into a harness directory (--target, --scope, --force, --dry-run)
kilasflow skills check              local                              compare an installed bundle with this binary's and exit non-zero on drift
kilasflow skills export             local                              write the bundle as the harness index (--format json) or a tarball (--format tar)
kilasflow mcp serve                 local                              serve the Model Context Protocol on stdin and stdout, one tool per verb
```
<!-- /generated:command-reference -->

### Protocol, in order

```
1. kilasflow context                     what exists, before you plan
2. load the skill the tree above names   before you act on that surface
3. read what you are about to change     workflow get, node describe, datastore get
4. change it                             workflow create --file, or kilasflow api <operation-id>
5. verify it                             re-read what you saved and check it came back
6. run it                                kilasflow run <workflowId> --wait
7. explain it                            kilasflow exec get <executionId>, then exec trace
8. report it                             what changed, what it cost, what you could not verify
```

## Anti-patterns

- "The workflow is simple, I will just build it" → the document shape, the connection kinds and the save path are a surface you have not read → invoke kilasflow-workflow-lifecycle and start from `kilasflow workflow get <workflowId>` or a canonical document.
- "I know this node's parameters" → the catalogue changes per version and the server ignores what a node does not declare → `kilasflow node describe <type>` first, then configure.
- "I will activate it so I can test it" → activation publishes a public endpoint and compiles the graph → test with `kilasflow run <workflowId> --wait` and read the trace; activate only when the user asked for it.
- "I will keep the key in a Set node" → the secret is then in the document, in every revision of it and in every export → invoke kilasflow-credentials; credentials are referenced by id.
- "Validation passed, so it is fine" → validation is structural, so an unknown node type or a full port is only found when the graph compiles or runs → verify by re-reading the saved document, then by running it.
- "The error is obvious, I will patch it" → a symptom has more than one cause, and the patch hides the evidence → invoke kilasflow-debugging and read the trace first.
- "A Code node is faster here" → it hides the transformation from the document and from the trace → invoke kilasflow-expressions first.
- "I will retry the run, it is a read" → no verb carries an idempotency key, so the retry is a second execution with the same effects as the first → make the step repeatable or key the call through `kilasflow api run-workflow --header Idempotency-Key=<key>`.
- "There is no verb for it, so it cannot be done" → the verbs are a convenience over the API, not the API → `kilasflow api --list` prints the operations the running server serves.
- "An embed session is broken because it cannot delete or import" → a session can never delete, import or manage datastores, at any scope, and activates only with `workflow:activate` → do those from the operator's own credential, and read references/SESSION_AUTHORITY.md in kilasflow-embedding.
- "The agent token has the scope, so the call will work" → an embed session is confined to the workflow and the resources of the revision it was minted from, and a scoped key is bounded by its own `scopes` → read the refusal: exit code 3 with `scope_denied` means drop the step, not retry it.

## Reference files

| File | Read when |
| --- | --- |
| EXAMPLE.md | you have not yet picked a skill and want a small document to save, run and read back |
