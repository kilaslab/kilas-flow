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
