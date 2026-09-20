---
id: FEAT-4jns31
title: 'Embed the skills bundle and add the skills verbs: list, show, install, check, export'
status: todo
priority: medium
labels:
    - agent
    - cli
deps:
    - FEAT-x5qqpm
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5.6 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the bundle is embedded with `//go:embed` (the way `internal/web` embeds the SPA) so `kilasflow skills install` works from the distroless image with no checkout.

## Acceptance criteria

- [ ] `skills list|show|install|check|export` per §5.6 and the CLI surface block of FEAT-mha6a0.
- [ ] Install targets `claude`, `codex`, `agents` and `dir:<path>`, scopes `user` and `project`; a round-trip test installs into a temp directory and asserts the file layout.
- [ ] `skills check` compares the installed bundle's version stamp with the binary; mutating a stamp makes it report drift and exit non-zero.
- [ ] Works from the shipped image (no filesystem beyond the install target).
