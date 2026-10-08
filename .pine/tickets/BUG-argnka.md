---
id: BUG-argnka
title: Credential resolution is nondeterministic when a node attaches several credentials, and field templating runs in several passes in random order
status: done
priority: low
labels:
    - credentials
    - engine
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-10-08T16:40:00Z"
---

# Description

- `ResolveNodeCredential` picks one of several attached credentials at random, because it iterates a map (internal/engine/authenticate.go:123-129).
- `expand` in internal/credentials/registry.go:288-295 substitutes in several passes in map order, so a field value containing `{{ other }}` can pull in another field.

# Acceptance Criteria
- [x] Resolution order is deterministic, following the node's declared credential order.
- [x] Field templating is a single pass, and a substituted value is never re-expanded.
- [x] Tests cover both.

# Notes

## Fix (2026-10-08)

- `ResolveNodeCredential` (internal/engine/authenticate.go) takes the first entry from `attachedCredentialTypes`. That list follows `ir.Definition.CredentialTypes`, which is the node definition's `Credentials` in declaration order. After those come any `RequiredCredentials` the list does not repeat, then any leftover attached keys in name order. The compiler refuses leftovers in a real workflow; they are sorted so a hand-built IR is deterministic too. Blank IDs are still skipped. For example, an HTTP Request node carrying both a header key and a bearer token now always signs with `httpHeaderAuth`, because it is declared before `httpBearerAuth`.
- The agent rebuilds connected HTTP Request Tool and MCP Client Tool nodes from their descriptors, and those rebuilt nodes had no compiled definition. They now get `CredentialTypes` from the registered definition (`declaredCredentialOrder` in nodes/ai.go), so a tool follows its declared order as well instead of falling back to name order.
- `expand` (internal/credentials/registry.go) is now one regexp pass (`fieldMarker`) over the template. It accepts the same `{{ key }}` and `{{key}}` forms as before, and a marker that names no field is left as written, as before. A substituted value is never scanned again. This covers the header, query, bearer and custom placements and the probe URL.
- Tests:
  - `TestANodeWithSeveralCredentialsResolvesInItsDeclaredOrder` (internal/engine) checks a node whose declared order runs against name order, and a node with no declaration. Each case runs 100 times so map order cannot make it pass. It was RED on the old code.
  - `TestAFieldValueIsNeverExpandedAsATemplate` (internal/credentials) uses `httpQueryAuth` with a value holding `{{ name }}`, a name holding `{{ value }}` (the case that put the secret into the parameter name), the compact form, a marker naming no field, and two fields naming each other. Each case also runs 100 times. It was RED on the old code (`token=token`).
- `go test` passes for credentials, engine, routing, nodes, webhook, nodepack and api. `go vet` is clean for credentials, engine and nodes.
