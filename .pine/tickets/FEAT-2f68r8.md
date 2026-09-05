---
id: FEAT-2f68r8
title: Open the credential type registry to node declarations
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:03:19Z"
updated: "2026-09-05T05:03:19Z"
---

## Scope

Credential types are a closed, unexported package-level map. `internal/credentials/credentials.go` declares `var definitions = map[string]Definition{…}` with six entries — `httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`, `postgres`, `mysql`, `sqlite` — and `Lookup` and `List` read it directly. There is no `Register`. Adding a credential type means editing that map, which means a node pack cannot bring its own. p3 needs `wahaApi` (base URL plus an `X-Api-Key` header) and `telegramApi` (a BotFather token); neither can exist today.

Three further gaps make this more than a missing function.

`node.Definition` cannot say which credential types it accepts. That knowledge lives only in the SPA, in `BY_NODE_TYPE` in `web/src/lib/workflow-editor/credentials.ts`, whose own comment concedes it is a presentation hint the server re-checks — except the server re-checks the *type and host scope of a credential that was chosen*, never which types the node should have been offered. A node with no entry there gets no credential picker at all.

Credential fields use a second, weaker property language. `credentials.Field` is `{Key, Label, Description, Required, Secret}` — no kinds, no options, no conditional visibility, no type options. A credential field cannot be a select, cannot be a password-masked string distinct from a secret one, and cannot be shown only for one authentication mode. Nodes got a rich property model; credentials got five booleans and strings.

And authentication is a hardcoded switch. `credentials.Apply` matches on the type ID and knows how to attach exactly three shapes of HTTP auth, returning `credential type %q cannot authenticate an HTTP request` for anything else. A registered `wahaApi` would hit that default and fail.

This ticket opens registration, unifies the field language, lets a node declare its requirements, and replaces the `Apply` switch with a declarative authentication descriptor plus a declarative test request.

## Acceptance criteria

- [ ] Credential types are registered into an explicit registry during composition rather than read from a package-level map, and the registry is immutable once the server starts serving.
- [ ] The six existing type IDs register with byte-identical IDs and field keys, so every stored credential row keeps resolving; a migration test loads a row of each type and reads it back.
- [ ] A credential type describes its fields in the same property language nodes use, including kinds, conditional visibility and password masking, and `Validate`, `Split` and `Redacted` keep working over that language with no secret ever returned after creation.
- [ ] `node.Definition` carries a list of credential requirements — type ID, required, and a display condition — and `/api/v1/node-types` returns them.
- [ ] A credential type declares how it authenticates a request declaratively (header, query parameter, basic, or bearer, with the value drawn from named fields); `Apply` resolves it from that descriptor instead of a type-ID switch, and an unregistered type still fails with a named error.
- [ ] A credential type may declare a test request; `POST /api/v1/credentials/{id}/test` runs it through `internal/safehttp` under the credential's own `AllowedDomains` and reports pass or fail without echoing any secret.
- [ ] Registering two credential types with the same ID fails at composition, and registering a type whose field keys collide fails with the offending key named.

## Implementation Plan

The import graph decides the shape, so settle it first. `internal/credentials` imports nothing internal today — it is a leaf. `internal/node` imports only `internal/workflow`, also a leaf. If credential fields are to reuse `node.PropertyDefinition` and `node.Definition` is to carry credential requirements, one of the two must not depend on the other. Recommend extracting the property language — `PropertyKind`, `PropertyDefinition`, `PropertyOption`, the visibility model and `TypeOptions` — into its own leaf package that both `internal/node` and `internal/credentials` import, and keeping the credential requirement on `node.Definition` as a plain type-ID string plus a condition. Naming a credential type by string is what n8n does too (`INodeCredentialDescription.name`), and it is what keeps the node catalogue from having to know the credential catalogue exists. The alternative — credentials importing node — works today but inverts the dependency the moment a credential type wants to reference a node.

Then build `credentials.Registry` in the shape of `node.Registry`: `NewRegistry`, `Register` refusing a duplicate ID, `Get`, `List` in stable order, cloned on the way out. Register the six built-ins in a `RegisterAll` beside `nodes.RegisterAll`, and wire it in `cmd/kilasflow/main.go` next to the existing node registration. `Lookup` and `List` are called from `internal/repository/credentials.go`, `internal/api/handlers/credentials.go`, `internal/engine/service.go` and `nodes/http.go`; each becomes a registry method call, and the registry has to be threaded to them rather than reached as a package global. That threading is most of the diff.

The authentication descriptor replaces the `Apply` switch. Model it on n8n's `authenticate` block: a placement (`header`, `query`, `basicAuth`, `bearer`), a name where the placement needs one, and a value template referencing field keys. The three HTTP types then express themselves in data, and `wahaApi` becomes `{header, "X-Api-Key", "{{ apiKey }}"}` with no Go change at all — which is the whole point. Keep `postgres`, `mysql` and `sqlite` outside it: they do not authenticate an HTTP request, so they declare no descriptor and `Apply` refuses them by absence rather than by a `default` branch, which is a better error anyway.

The test request follows n8n's `ICredentialTestRequest` — a request descriptor plus success rules over response code or body. Route it through `internal/safehttp` with the instance policy and the credential's `AllowsHost` check, exactly as an HTTP node would; a test that bypasses the SSRF policy would be a credential-shaped hole straight into the internal network.

One trap worth stating: `Redacted`, `Split` and `Validate` all key off `Field.Secret`. In the unified language that becomes a type option, and a field that is *masked in the UI* is not the same thing as a field that is *never returned by the API*. Keep them as two separate flags, not one, or a password-masked but readable field will start coming back as `••••••••` and users will overwrite real values with the placeholder.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-6.
- `internal/credentials/credentials.go` — the `definitions` map, `Field`, `Definition`, `Lookup`, `List`, `Validate`, `Redacted`, `Split`, `Apply`.
- `internal/node/registry.go` — the registry shape to mirror, and where credential requirements land on `Definition`.
- `internal/api/handlers/credentials.go` — `list-credential-types` and the CRUD operations; where the test operation is added.
- `internal/repository/credentials.go`, `internal/engine/service.go`, `nodes/http.go` — the current callers of the package-level lookup.
- `cmd/kilasflow/main.go` — composition, beside `nodes.RegisterAll` and `nodes.RegisterExecutors`.
- `web/src/lib/workflow-editor/credentials.ts` — the `BY_NODE_TYPE` map this makes redundant.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `INodeCredentialDescription`, `ICredentialType`, `ICredentialTestRequest`, `ICredentialsDisplayOptions`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 05, 16 — the credential picker inside the NDV with inline edit, and the credentials list. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
