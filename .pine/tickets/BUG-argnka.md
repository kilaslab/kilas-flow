---
id: BUG-argnka
title: Credential resolution is nondeterministic when a node attaches several credentials, and field templating runs in several passes in random order
status: todo
priority: low
labels:
    - credentials
    - engine
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

- `ResolveNodeCredential` picks one of several attached credentials at random, because it iterates a map (internal/engine/authenticate.go:123-129).
- `expand` in internal/credentials/registry.go:288-295 substitutes in several passes in map order, so a field value containing `{{ other }}` can pull in another field.

# Acceptance Criteria
- [ ] Resolution order is deterministic, following the node's declared credential order.
- [ ] Field templating is a single pass, and a substituted value is never re-expanded.
- [ ] Tests cover both.
