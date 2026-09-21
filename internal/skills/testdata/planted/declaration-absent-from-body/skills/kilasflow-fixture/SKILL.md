---
name: kilasflow-fixture
description: Use when a fixture is exercised. Triggers on "fixture".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow run
kilasflow_operations:
  - run-workflow
  - get-workflow
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped: []
---


## Non-negotiables

- Run `kilasflow run <workflowId>` and read the operation `run-workflow` back.

## Strong defaults

- Prefer the documented verb over a guessed path.

## Decision tree

```
is this a fixture?
  yes -> run it
  no  -> say so
```

## Anti-patterns

- Editing a fixture without running it.
