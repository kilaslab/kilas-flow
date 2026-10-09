---
id: BUG-f85r5h
title: kilasflow node options sends no parameters, so a property whose options depend on other parameters cannot load
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T12:25:15Z"
---

# Description

`kilasflow node options <type> --property <name>` sends no node parameters to `POST /node-types/{type}/load-options`. A property whose options depend on other parameters (for example a chat model's model list, which needs `parameters.baseUrl` and a credential) therefore cannot be loaded through the verb; agents must fall back to `kilasflow api load-node-property-options` with a hand-written body.

Reported by the Track H2 coder in the 2026-10-09 parallel wave while writing the kilasflow-ai-agents skill.

# Steps to Reproduce

1. `kilasflow node options kilasflow.lmChatOpenAi --property model` (with a credential).

# Expected

The verb can pass the parameters (and credential) the loader depends on, e.g. `--param baseUrl=...`, `--credential <type>=<id>`, or `--parameters @file.json`, and prints the options.

# Actual

The loader gets no parameters and cannot answer.

# Acceptance Criteria
- [ ] `node options` accepts the dependent parameters and credential (flags documented in `--help` and cli.md, with a runnable example).
- [ ] A test proves the parameters reach the load-options request body.
- [ ] The AI skill uses the verb instead of the escape hatch where it can.


## Plan (2026-10-09)

Root cause. `runNodeOptions` (internal/cli/verbs_node.go:148) marshals a body of `property`, `version`, `mode` and `credentialId` only. The operation's body also takes `parameters` (internal/api/handlers/nodes.go `loadOptionsInput`), and that map is what `scopeFor` reads the loader's `dependsOn` keys from (nodes/ai.go:266 and :417 depend on `baseUrl`). So a property with dependencies always loads with an empty dependency set; the only way to send them was `kilasflow api load-node-property-options --body`. The credential half of the ticket already exists: `--credential <credentialId>` is sent as `credentialId`.

The change.
1. `node options` gets `--param key=value` (repeatable, collected with the CLI's existing `pairFlags`, the same flag shape `kilasflow api` uses for `--path/--query/--header`) and `--parameters <JSON object | @file | ->` (the shape `run --input` and `api --body` use, read with `readDocument`). Values from `--param` are strings; numbers, booleans and resource-locator objects go through `--parameters`. When both are given `--param` overrides a same-named key of `--parameters`; the same key repeated in `--param` is a usage error rather than a silent choice; `--parameters` that is not a JSON object is a usage error naming the flag. The body carries `parameters` only when something was given, as it does for the other optional fields.
2. `--credential` stays a bare credential id (the API takes a single `credentialId`; the loader's `credentialType` already says which type it must be, so the `type=id` shape the ticket floats would carry nothing the server reads and would break callers of today's flag). Its help text and the verb summary are made explicit.
3. Docs: reference/cli.md `kilasflow node` section documents both flags and gets a runnable example (the runner seeds `$WORKFLOW_ID`; the example loads `executeWorkflow`'s `workflowId` list, which depends on nothing, plus one with `--param` against a property that depends on a parameter and ends `# exit N` if it does not pass on the seeded server; I will check what the booted server answers before choosing). Then `make generate-skills-command-reference` for the router skill's command table, and the node-configuration skill text/LOAD_OPTIONS.md that say the verb cannot carry parameters. The ai-agents skill from the ticket is not on main, so the "skill uses the verb" criterion is met by updating the skills that exist (kilasflow-node-configuration) and noted in the report.
4. MCP needs no code: `pairFlags` is a non-Getter flag.Value, which the adapter already publishes as an array of strings (`mcpFlagList`), so the new tool schema gains `param` (array) and `parameters` (string) from the flag set.

Proof (test first).
- internal/cli/verbs_node_test.go: a recording stub of `/node-types/{type}/load-options` asserts the request body holds `parameters` with the `--param` strings, with a `--parameters` object, with `@file`, with the override order, with `--credential` beside them, and omits `parameters` when none were given; plus the three usage errors (duplicate `--param`, malformed pair, non-object `--parameters`).
- An end-to-end test against the real handler: a node type registered with an `http` loader that `dependsOn` a `baseUrl` parameter, served by an httptest upstream whose path/host proves the parameter arrived, reached through `kilasflow node options --param baseUrl=...` (not only a stub body), so "the parameters reach the load-options request" is proven through the server and not just on the wire.
- `make generate-skills-command-reference-check`, the cli.md example under `make build && make smoke-cli-examples`, and the existing tree/MCP tests (the schema gains two properties).
