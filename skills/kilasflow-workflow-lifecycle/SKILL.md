---
name: kilasflow-workflow-lifecycle
description: Use when creating, editing, publishing, activating, versioning or restoring a workflow, or when a workflow will not save, run or behave as expected. Triggers on "workflow", "publish", "activate", "version", "restore", "save".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow workflow list
  - kilasflow workflow get
  - kilasflow workflow create
  - kilasflow workflow versions
  - kilasflow workflow get-version
  - kilasflow workflow export
  - kilasflow workflow diagnostics
  - kilasflow workflow publish-events
  - kilasflow run
  - kilasflow exec list
  - kilasflow exec get
  - kilasflow context
  - kilasflow api
kilasflow_operations:
  - list-workflows
  - get-workflow
  - create-workflow
  - update-workflow
  - import-workflow
  - export-workflow
  - workflow-diagnostics
  - list-workflow-versions
  - get-workflow-version
  - restore-workflow-version
  - list-workflow-publish-events
  - activate-workflow
  - deactivate-workflow
  - run-workflow
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No activation verb: activation and deactivation are the activate-workflow and deactivate-workflow operations through the escape hatch'
  - 'No workflow update, import or restore verb: those operations are served and reached through the escape hatch'
  - 'No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet'
---

## Non-negotiables

1. Read before editing. `kilasflow workflow get <workflowId>` (`get-workflow`) is the only thing that says what is stored. An update written from memory of what you sent is an update against a document that does not exist.
2. A 2xx from create or update is not verification. Re-read with `kilasflow workflow get <workflowId>` and check that the connections and parameters you meant to save came back. Draft validation is structural: it says nothing about a graph that runs.
3. Never activate to test. Activation publishes a public endpoint and is reached today as `kilasflow api activate-workflow --path id=<workflowId>` (`activate-workflow`). Ask the user first, and roll back with `kilasflow api deactivate-workflow --path id=<workflowId>` (`deactivate-workflow`).
4. Never guess a node parameter. If a parameter's name or options are not in front of you, read them from the server (the node-configuration skill) rather than recalling them.

## Strong defaults

- Begin a session with `kilasflow context`: server, identity, workflows, datastores and node types in one read-only call.
- Find a workflow with `kilasflow workflow list` (`list-workflows`, `--limit` and `--cursor`); the listing is summaries, so the document still comes from `get-workflow`.
- Author the canonical document the server stores: `schemaVersion: 1` with `name`, `nodes`, `connections` and `settings`. Every node carries `id`, `name`, `type`, `typeVersion` and `position`; every connection carries `id`, `kind`, `source` and `target`, each endpoint naming `nodeId` and `port` (internal/workflow/document.go).
- Spell a connection kind the way the document does: thirteen channels, `ai_` prefixed, lowerCamel after the prefix — `ai_languageModel`, never `ai_language_model` (internal/workflow/document.go).
- Create with `kilasflow workflow create --file <path>|-` (`create-workflow`). The body must not choose the workflow id: the server assigns it on create, and update takes it from the path (internal/api/handlers/workflows.go).
- Anything without a verb is one call away. `kilasflow api <operation-id>` (`api`) reaches every operation the running server serves, with `--path name=value` for placeholders, `--body @file.json` for a body and `--list` to enumerate the surface.
- Read the history with `kilasflow workflow versions <workflowId>` (`list-workflow-versions`) and one revision with `kilasflow workflow get-version <workflowId> <versionId>` (`get-workflow-version`). A revision is immutable and carries the document that ran.
- Publish audit and diagnostics are reads: `kilasflow workflow publish-events <workflowId>` (`list-workflow-publish-events`) records published, unpublished and restored acts, and `kilasflow workflow diagnostics <workflowId>` (`workflow-diagnostics`) returns the import report stored with a revision.
- Export with `kilasflow workflow export <workflowId> --format n8n` (`export-workflow`), which reports what could not be carried; import through the escape hatch with `kilasflow api import-workflow --body @wf.json` (`import-workflow`).
- To watch what a workflow did after running it: `kilasflow run <workflowId> --wait` (`run-workflow`, `--input` for manual input), then `kilasflow exec list --workflow <workflowId>` (`list-executions`) to find the run and `kilasflow exec get <executionId>` (`get-execution`) for its trace.
- A retry is safe only when it carries a key. The server honours `Idempotency-Key` on `run-workflow` and on a datastore row write: a repeat with the same key and the same request is answered with the first request's outcome, marked `Idempotent-Replayed: true`, and repeats no side effect. No verb carries the key yet, so a retry-safe run is `kilasflow api run-workflow --path id=<workflowId> --header Idempotency-Key=<key> --body @input.json`; the same key with a different request is refused `409`. Without a key, a retry is a second execution.

## Decision tree

```
what are you doing?
|
+-- creating
|     -> kilasflow workflow create --file wf.json
|        then kilasflow workflow get <workflowId> and check the graph came back
|
+-- editing
|     -> kilasflow workflow get <workflowId> first
|        then kilasflow api update-workflow --path id=<workflowId> \
|               --header If-Match=<versionId> --body @wf.json
|        a base version that is no longer the latest is refused 409: re-read, re-apply, save again
|
+-- publishing / activating
|     -> kilasflow api activate-workflow --path id=<workflowId>
|        it publishes a public endpoint AND compiles the latest revision,
|        so a graph that cannot compile is refused here
|
+-- running it yourself
|     -> kilasflow run <workflowId> --wait
|        then kilasflow exec get <executionId> and kilasflow exec trace <executionId>
|
+-- refused with "workflow draft is invalid"
|     -> a structural fault in the document: see references/VALIDATION_CHECKLIST.md
|
+-- saves but will not activate or run
      -> a compile fault: the codes in references/VALIDATION_CHECKLIST.md say which
```

## Not shipped yet

- No activation verb: activation and deactivation are the activate-workflow and deactivate-workflow operations through the escape hatch — `kilasflow workflow create` and friends exist, but there is no `workflow activate`, `workflow deactivate` or `workflow delete`, so activation is `kilasflow api activate-workflow --path id=<workflowId>` and rollback is `kilasflow api deactivate-workflow --path id=<workflowId>`. There is no `--yes` guard to lean on either, so ask the user before you activate.
- No workflow update, import or restore verb: those operations are served and reached through the escape hatch — `update-workflow`, `import-workflow` and `restore-workflow-version` are all served and reachable with `kilasflow api <operation-id>`, `--path` and `--body @file.json`. There is also no separate validate verb: validation happens on save, and executability is proved by activation or by a run. `workflow duplicate` and `run --revision` do not exist either; duplicate by reading a version and creating a new workflow from it.
- No idempotency flag: the server honours Idempotency-Key on a run and on a row write, but no verb carries the key yet — `kilasflow run` has no key flag, so a retry-safe run goes through `kilasflow api run-workflow --path id=<workflowId> --header Idempotency-Key=<key> --body @input.json`. The same key with a different request is refused `409`; a replayed answer is marked `Idempotent-Replayed: true`.

## Anti-patterns

- "I edited from memory" → the save dropped the connections you forgot → `kilasflow workflow get <workflowId>` first, change the document you read back.
- "I'll activate it so I can test it" → activation publishes a public endpoint and compiles the whole graph → run it with `kilasflow run <workflowId> --wait` and read `kilasflow exec trace <executionId>` instead; activate only when the user asked for it.
- "The run failed, so the workflow is broken" → usually one node's input or credential is wrong, not the graph → `kilasflow exec get <executionId>`, then `kilasflow exec trace <executionId>`, and read which node failed first (the debugging skill).
- "The save returned 2xx, so the workflow is fine" → draft validation is structural, so an unknown node type or a full port is only found when the graph is compiled → activate or run to compile, and read the problem's codes.
- "I'll guess the parameter name" → the server refuses unknown configuration or ignores a parameter the node does not declare → describe the node type first.
- "My save failed with 409" → someone else's save moved the latest revision → re-read the workflow, re-apply your change to the new revision, save again.
- "I renamed the node, so the graph is cleaner" → a rename breaks every expression that names it → see references/NAMING_AND_DESCRIPTIONS.md before renaming.

## Reference files

| File | Read when |
| --- | --- |
| VALIDATION_CHECKLIST.md | a create, update, activation or run was refused and you need to know what the refusal means and how to fix it |
| NAMING_AND_DESCRIPTIONS.md | you are naming node instances, node types or descriptions, or renaming a node another node's expression references |
