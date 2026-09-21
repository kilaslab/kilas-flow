---
name: kilasflow-import-export
description: Use when migrating n8n workflows into KilasFlow, exporting a workflow back to n8n JSON, or reading the unsupported and Lossy[] report a translation produced. Triggers on "n8n", "migration", "import", "export", "lossy", "diagnostics".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow workflow export
  - kilasflow workflow diagnostics
  - kilasflow workflow versions
  - kilasflow workflow get
  - kilasflow api
kilasflow_operations:
  - export-workflow
  - import-workflow
  - workflow-diagnostics
  - get-workflow
  - list-workflow-versions
kilasflow_nodes:
  - kilasflow.unsupported
  - kilasflow.foreignCode
  - kilasflow.code
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped:
  - 'No workflow update, import or restore verb: those operations are served and reached through the escape hatch'
---

## Non-negotiables

1. Read the report before you trust the translation. An import answers with `unsupported[]` and a *blocking* issue means the workflow cannot run as imported; the same report is stored with the revision it created, so it is still there tomorrow (internal/api/handlers/interop.go).
2. Read the draft back. `kilasflow workflow get <workflowId>` (`get-workflow`) is what says what is stored; an import is a save through the ordinary draft path, with the same validation and the same revision history as any other save (internal/api/handlers/interop.go).
3. Never activate or run an imported workflow as a way of testing it. A node the importer could not map becomes `kilasflow.unsupported` and an n8n Code node becomes `kilasflow.foreignCode`; both refuse to compile, so the only thing a run proves is what the diagnostics already said (internal/interop/n8n/n8n.go, nodes/unsupported.go, nodes/jscode.go).
4. Export sends the latest revision, not the draft you are editing. `kilasflow workflow export <workflowId> --format n8n` (`export-workflow`) converts the stored latest revision; an unsaved editor change is not in the file (internal/api/handlers/interop.go, `Export`).

## Strong defaults

- Export with `kilasflow workflow export <workflowId> --format n8n` (`export-workflow`). `--format` is the verb's only flag — there is no `--out` — and the response carries `format`, the n8n `workflow` document, `lossy[]` and `supportedMappings[]` (internal/cli/verbs_workflow.go, internal/api/handlers/interop.go).
- When you need the file itself, take it through the escape hatch's own `--out`: `kilasflow api export-workflow --path id=<workflowId> --query format=n8n --out workflow.json` writes the body 0600, and `--out -` streams it (internal/cli/verbs_api.go). Any format other than `n8n` is a 422.
- Import with `kilasflow api import-workflow --body @workflow.json` (`import-workflow`, `POST /workflows/import`). The body is `{"format": "n8n", "workflow": <the n8n JSON>, "name": "<optional override>"}`; the answer is 201 with a `Location`, the saved workflow, `unsupported[]` and `webhooks[]` — the public addresses its triggers will answer on once activated (internal/api/handlers/interop.go).
- Read the stored report with `kilasflow workflow diagnostics <workflowId>` (`workflow-diagnostics`) and `--version-id` for an older revision; a revision id comes from `kilasflow workflow versions <workflowId>` (`list-workflow-versions`). The report is per revision, and an empty `source` is what separates "never imported" from "imported cleanly" (internal/api/handlers/interop.go).
- Read `supportedMappings[]` from an export instead of recalling a table: it is the advertised subset, `n8n-nodes-base.x ↔ kilasflow.y`, written down and tested rather than derived (`SupportedMappings` in internal/interop/n8n/n8n.go).
- Read every issue by its severity: `blocking` (the workflow cannot run as imported), `lossy` (carried differently, the workflow still activates) and `dropped` (not carried at all) (`IssueSeverity` in internal/interop/n8n/n8n.go). references/MAPPING_LIMITS.md says what each severity costs you.
- Assume a node type outside the mapping table is a placeholder, not an import failure: it imports as `kilasflow.unsupported` at one of four arities, keeps the whole source node in a capsule, renders where the original node was, and exports back as the node it came from (internal/interop/n8n/n8n.go).
- Treat an imported Code node as work to do, not data lost: it becomes `kilasflow.foreignCode`, keeps `jsCode` or `pythonCode` verbatim, never translates, and names a replacement — the native nodes, or `kilasflow.code`, the Go Code node that does run under a time and memory limit (nodes/jscode.go, nodes/code.go, docs/src/content/docs/guides/n8n-migration.md).
- Expressions cross differently in one place: n8n marks an expression with a leading `=`, KilasFlow marks one with `{"mode":"expression","value":"…"}`, and the `{{ … }}` interpolation inside is the same, so `{{ $json.userId }}` survives the trip (internal/expression/expression.go, docs/src/content/docs/guides/n8n-migration.md).
- Fix a blocking issue at its node, then re-run the workflow, then export again: replacing the placeholder is the fix, and the report is what says whether the replacement was complete (internal/interop/n8n/n8n.go).

## Decision tree

```
what are you doing?
|
+-- bringing an n8n workflow in
|     -> kilasflow api import-workflow --body @workflow.json
|        read unsupported[] in the response
|        then kilasflow workflow get <workflowId> and check the graph
|
+-- "what did the import drop, and is it still true?"
|     -> kilasflow workflow diagnostics <workflowId>
|        --version-id <versionId> for the revision the import created
|
+-- sending a workflow back to n8n
|     -> kilasflow workflow export <workflowId> --format n8n
|        read lossy[] before handing the file over
|
+-- the n8n JSON as a file
|     -> kilasflow api export-workflow --path id=<workflowId> \
|              --query format=n8n --out workflow.json
|
+-- it will not activate or run
|     -> a blocking issue survived: replace every kilasflow.unsupported
|        placeholder and every kilasflow.foreignCode Code node
|
+-- the export reports a lossy node
      -> references/MAPPING_LIMITS.md: severity, cause and what survives
```

## Not shipped yet

- No workflow update, import or restore verb: those operations are served and reached through the escape hatch — `import-workflow`, `update-workflow` and `restore-workflow-version` are all served, so call them with `kilasflow api <operation-id>` and `--path`/`--body`; an import is how a workflow arrives as a new draft, and a new revision is what an edit produces.

## Anti-patterns

- "The POST returned 201, so the workflow is ready" → 201 means the draft saved, and the nodes named as blocking make it un-runnable → read `unsupported[]`, then the stored report with `kilasflow workflow diagnostics <workflowId>`.
- "The report is empty in the editor, so the import was clean" → the report belongs to a revision, and the newest revision of a workflow somebody has since edited carries none → read the revision the import created, by `--version-id` from `kilasflow workflow versions`.
- "The Code node will be translated" → it is carried verbatim and never translated, so the workflow cannot compile → replace it with the native node the diagnostic names, or rewrite the body in `kilasflow.code`.
- "I'll just activate it and see" → an unsupported placeholder and a foreign Code node refuse compilation, and activation also publishes a public endpoint → fix the blocking issues first, then run the workflow and read its trace.
- "It exported, so n8n will run it" → a KilasFlow-only node goes out under its own type, which n8n refuses, and credential references are not exported at all → reattach credentials in n8n and replace the unrecognised nodes there.
- "The mapping table in the docs is the answer" → the subset is a claim the server can state for itself, and it changes as mappings land → read `supportedMappings[]` from the export in front of you.
- "Rounding to a pinned typeVersion is harmless" → the exporter writes the version whose parameter shape it translated, and a node authored at another version comes back with that difference → read the lossy entry, and check the node in n8n after the file lands.
- "I'll import the file twice to be sure" → the second import saves a second workflow, not a second revision of the first → import once and read diagnostics; the report is kept per revision, so nothing is lost by not repeating yourself.

## Reference files

| File | Read when |
| --- | --- |
| MAPPING_LIMITS.md | you are reading a lossy or unsupported report, or you need what an n8n construct maps to and what it costs |
