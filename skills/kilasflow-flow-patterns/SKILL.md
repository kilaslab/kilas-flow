---
name: kilasflow-flow-patterns
description: Use when items must be batched or looped, a file or binary payload is read or produced, or one workflow calls another. Triggers on "loop", "batch", "binary", "file", "PDF", "sub-workflow", "execute workflow".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow node describe
  - kilasflow workflow validate
  - kilasflow workflow create
  - kilasflow workflow activate
  - kilasflow run
  - kilasflow exec get
  - kilasflow exec list
kilasflow_operations:
  - list-node-types
  - validate-workflow-document
  - create-workflow
  - activate-workflow
  - run-workflow
  - get-execution
  - list-executions
kilasflow_nodes:
  - kilasflow.loop
  - kilasflow.splitOut
  - kilasflow.aggregate
  - kilasflow.httpRequest
  - kilasflow.extractFromFile
  - kilasflow.executeWorkflow
  - kilasflow.executeWorkflowTrigger
kilasflow_expression_roots:
  - $json
kilasflow_not_shipped:
  - 'No OCR: kilasflow.extractFromFile reads a PDF text layer only, so a scanned PDF yields empty text'
---

## Non-negotiables

1. Every node already runs once per incoming item, so a Set, an HTTP Request or a datastore node after a node that returned fifty items runs on fifty items with no loop at all. Add `kilasflow.loop` only for batching or pagination: a body that must see several items at once, or a call that has to be made in groups.
2. A loop is declared and bounded. The loop node's `loop` output carries the current batch into the body, the body's last node is wired back into the loop's own `main` input, and whatever should happen once afterwards hangs off `done`. `maxIterations` fails the run rather than letting a loop that never converges report success, and any cycle that does not close on a loop node is refused as an invalid topology.
3. An item carries binary metadata, never the bytes (docs: concepts/items-and-lineage). A file is a reference — `id`, `fileName`, `mediaType`, `size` — under a binary property name, and a node reads it by that name. Do not look for content in `json`, in a trace or in `exec get`; the bytes are written and read only by the nodes that handle files.
4. A child workflow begins with `kilasflow.executeWorkflowTrigger`, and the parent names it by id in `workflowId`, stored as the resource locator `{"__rl": true, "mode": "id", "value": "<childWorkflowId>"}` (docs: concepts/execution-model). The child must be active when the parent runs: a sub-workflow run is pinned to the called workflow's active revision, and activation is the user's decision.

## Strong defaults

- Start from the validated examples: `kilasflow skills show kilasflow-flow-patterns --reference EXAMPLE.md` is a loop over batches, `--reference BINARY_DATA.md` downloads a file and reads its text, and `--reference SUB_WORKFLOWS.md` is a parent and a child, each accepted by `kilasflow workflow validate`.
- Turn a list inside one item into items with `kilasflow.splitOut` (`fieldToSplitOut`), and collapse many items into one list with `kilasflow.aggregate`. A node that works per item never sees the elements of an array until they are split out.
- In a loop, keep the batch small enough for the slowest call in the body and set `maxIterations` to a number the data cannot exceed. The trace tells a loop from a retry: a loop leaves one node run per pass with an increasing `runIndex`, a retry leaves a higher `attempt` of the same run.
- Download a file with `kilasflow.httpRequest` and `responseFormat: "file"`, which stores the body under the property named by `outputPropertyName` (default `data`) and leaves `json` empty. Read it with `kilasflow.extractFromFile`, choosing the operation that matches the file: `pdf`, `text`, `json`, `csv`, `xlsx` or `binaryToProperty`, with `binaryPropertyName` naming the property and `destinationKey` the field the text lands in.
- Call a sub-workflow with `kilasflow.executeWorkflow`. `itemsPerCall` is `allItems` (one run with the whole batch) or `eachItem`; `mode: "each"` waits and returns the child's items, `fireAndForget` lets the incoming items pass through; `inputFields` is a key/value object of what to send, expressions allowed, and empty sends the incoming items unchanged.
- Declare what a child expects on its trigger: `inputSource: "fields"` with `workflowInputs.values` rows of `name` and `type`. They document and check the call without dropping any field a caller sends.
- Prove a pair before anyone activates the parent: `kilasflow workflow validate --file child.json` (`validate-workflow-document`) and the same for the parent, `kilasflow workflow create --file child.json` (`create-workflow`) and the same for the parent, then ask the user to activate the child with `kilasflow workflow activate <childWorkflowId> --yes` (`activate-workflow`), then `kilasflow run <parentWorkflowId> --wait` (`run-workflow`). A failure inside the child is read in the child's own execution: `kilasflow exec list --workflow <childWorkflowId>` (`list-executions`), then `kilasflow exec get <executionId>` (`get-execution`).
- Read the node you are about to configure instead of this list: `kilasflow node describe kilasflow.loop`, `kilasflow.extractFromFile` and `kilasflow.executeWorkflow` print the parameters, defaults and options the server will run (`list-node-types`).

## Decision tree

```
what is the data doing?
|
+-- many items, each needs the same steps
|     -> nothing: nodes already run per item (non-negotiable 1)
|
+-- a list field must become items, or items a list
|     -> kilasflow.splitOut / kilasflow.aggregate
|
+-- the body must see items in groups, or pages are fetched until done
|     -> kilasflow.loop: body from `loop`, wired back into the loop, then `done`
|        start from references/EXAMPLE.md
|
+-- a file comes in or goes out
|     -> kilasflow.httpRequest (responseFormat file) -> kilasflow.extractFromFile
|        references/BINARY_DATA.md
|
+-- a step is reused, or a part of the graph deserves its own history
|     -> a child workflow (executeWorkflowTrigger) called by kilasflow.executeWorkflow
|        references/SUB_WORKFLOWS.md
|
+-- the loop never ends, or the run fails at maxIterations
|     -> the body is not wired back, or the data is larger than maxIterations allows:
|        read the trace's runIndex values
|
+-- the parent fails calling the child
      -> is the child active? kilasflow exec list --workflow <childWorkflowId>
```

## Not shipped yet

- No OCR: kilasflow.extractFromFile reads a PDF text layer only, so a scanned PDF yields empty text. Run the file through an OCR service with kilasflow.httpRequest first, or read the text from the source system instead.

## Anti-patterns

- "I need a loop to process each item" → every node already runs per item, and a loop adds a back edge, a batch size and an iteration cap to maintain → delete the loop and let the nodes run on the items.
- "I will wire the body's output to an earlier node to repeat it" → a cycle that does not close on a loop node is refused as an invalid topology → use `kilasflow.loop` and close the body onto its `main` input.
- "The loop's second output is the result" → `loop` is the current batch going into the body, and `done` is what the body returned, once, at the end → connect the work that follows to `done`.
- "I will read the file's content from `$json`" → `json` holds fields and the bytes are not in the item at all → extract the text with `kilasflow.extractFromFile` and read `{{ $json.text }}`.
- "The PDF came back empty, so the node is broken" → a scanned PDF has no text layer and the node does not run OCR → check the file, not the node.
- "The parent is saved, so it will run" → the child is only called if it is active, and a saved child fails the calling node → ask the user to activate the child first.
- "I will name the child by its title" → the call is by id, stored as a resource locator → read the id from the create answer or `kilasflow workflow list`.
- "The child failed, so the parent's trace will say why" → the child is its own execution with a `parentExecutionId` → read the child's execution.

## Reference files

| File | Read when |
| --- | --- |
| EXAMPLE.md | you need to batch items or paginate and want a validated loop with its back edge and its `done` branch |
| BINARY_DATA.md | a file is downloaded, read or produced, and you need the reference an item carries and the Extract operations |
| SUB_WORKFLOWS.md | one workflow calls another and you need the child's trigger, the parent's call and the activation order |
