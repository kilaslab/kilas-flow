---
name: kilasflow-fixture
description: Use when a fixture is exercised. Triggers on "fixture".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow run
kilasflow_operations:
  - run-workflow
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped: []
---


## Non-negotiables

- Run `kilasflow run <workflowId>` and read the operation `run-workflow` back.

## Decision tree

```
is this a fixture?
  yes -> run it
  no  -> say so
```

## Strong defaults

- Prefer the documented verb over a guessed path.

## Anti-patterns

- Editing a fixture without running it.
