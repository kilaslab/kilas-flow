---
id: FEAT-x5qqpm
title: 'Agent skills bundle v1: the domain skills, generated index, honest ''Not shipped yet'' sections'
status: todo
priority: medium
labels:
    - agent
    - docs
deps:
    - FEAT-bp59m4
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): `skills/<name>/SKILL.md` with `references/*.md`, frontmatter per §5.2, body conventions per §5.3, and the 12 domain skills of the §5.4 inventory (the router skill is its own ticket). Each skill states the CLI verb first and the HTTP operation second.

## Acceptance criteria

- [ ] The 12 domain skills exist with the frontmatter contract of §5.2 (`name`, `description`, `kilasflow_skills_version`, `kilasflow_commands`, `kilasflow_operations`, `kilasflow_nodes`, `kilasflow_expression_roots`, `kilasflow_not_shipped`).
- [ ] `skills/index.json` is generated, not hand-written, and a test fails when it is stale.
- [ ] Every skill with a non-empty `kilasflow_not_shipped` carries a `## Not shipped yet` section naming each entry (§5.8: WASM packs, sidecar, agent tokens, tenant deletion, idempotency — updated to whatever has shipped by the time this lands).
- [ ] No skill instructs an agent to put a secret in a workflow document, CLI argument or chat.
