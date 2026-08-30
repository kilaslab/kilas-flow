---
id: FEAT-6yef51
title: Build server-defined node registry and generic property metadata
status: todo
priority: critical
labels:
    - nodes
    - registry
    - schema
deps:
    - FEAT-kk9h5y
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:03Z"
updated: "2026-08-29T15:40:03Z"
---

## Scope

Create the node registry used by both compiler/runtime and editor. Node metadata defines type/version/category, typed input/output ports, required configuration, generic shared settings, and dynamic property forms. It must remain the sole node catalogue.

## Acceptance criteria

- A registered node definition includes stable type/version, display name/description/category, input/output ports and connection type, parameter schema, shared node settings, and executor binding.
- `GET /api/v1/node-types` returns a versioned, documented catalogue suitable for a client-side picker/form renderer; no frontend-only duplicate node catalogue is introduced.
- Validation identifies unknown versions, invalid port references, invalid required fields, and incompatible `main`, `ai_languageModel`, `ai_memory`, and `ai_tool` connections.
- Manual Trigger, Set, IF, and Merge are registered with the ports necessary for the first graph slice; IF exposes distinct labelled true/false outputs.
- Registry and API tests prove deterministic registration order and stable metadata serialization.

## References

- PRD: §§21–24 and API §36 Nodes.
- Design reference: `02-canvas-agentic-workflow.png`, `07-node-picker-triggers.png`, `08-node-picker-categories.png`, `09-node-picker-search-results.png`, `04-ndv-node-settings-tab.png`.

## Relevant documentation

- Current Svelte Flow connection/handle APIs are documented at https://svelteflow.dev/learn/customization/handles and https://svelteflow.dev/examples/interaction/validation. Use `find-docs` again if installed package versions or APIs differ.

## Relevant skills

- `pine` — maintain registry decisions.
- `find-docs` — mandatory for Svelte Flow or a validation-library API.
- `test-driven-development`, `verification-before-completion`.
