---
id: FEAT-bp59m4
title: 'Agent CLI: command tree, api escape hatch, output contract, config and read-only verbs'
status: todo
priority: medium
labels:
    - agent
    - api
    - cli
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §4 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`). `kilasflow <verb>` becomes the agent surface while `kilasflow` with no subcommand keeps meaning "serve" (the container entrypoint and every Compose file depend on it; `-config`/`-version` keep working).

## Acceptance criteria

- [ ] Command tree per §4.2 for phase 1: `context`, `health`, `version`, `auth`, the read-only verbs, and the run/observe verbs that need no new API operation.
- [ ] `kilasflow api <operation-id>` reaches every operation id in `docs/src/content/docs/reference/api-contract.md` (§4.3), with a test that walks the contract.
- [ ] Output contract (§4.4): `--json` envelope stable and documented; exit codes per §4.5, including a distinguishable code for a refused scope.
- [ ] Config and token handling per §4.6: precedence, storage, and that a token is never printed or accepted on argv where avoidable.
- [ ] Guardrails per §4.7: guarded verbs refuse without `--yes`.
- [ ] `bin/kflow` (a stale build of the server binary) is removed; no `kflow` alias is added.
- [ ] `make smoke-cli` drives context → workflow create → run --wait → exec trace against a booted server and asserts exit codes and the envelope.

## Out of scope

Scoped tokens (phase 2), debug primitives that need new operations (phase 3).
