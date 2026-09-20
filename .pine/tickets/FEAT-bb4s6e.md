---
id: FEAT-bb4s6e
title: Skills drift gates G1-G5 and make skills-check
status: todo
priority: medium
labels:
    - agent
    - ci
    - testing
deps:
    - FEAT-4jns31
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5.7 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`), mirroring `sdk/test/operation-coverage.test.mjs`.

## Acceptance criteria

- [ ] G1 commands, G2 operations, G3 nodes, G4 expression roots run as ordinary Go tests in `internal/skills/skills_test.go`, so they run in `go test ./...` and cannot be skipped by forgetting a Makefile target.
- [ ] G5 freshness: `make skills-check` runs `kilasflow skills check` against a binary built from the commit, next to `sdk-check`, and CI runs it.
- [ ] The honesty test of §8 (every non-empty `kilasflow_not_shipped` has a matching section).
- [ ] Each gate is proven to fail: a test plants a bad command, operation id, node type and expression root and sees the gate reject it.
