---
id: FEAT-qfr9xe
title: Enforce required credentials at compile time
status: todo
priority: medium
created: "2026-09-05T11:13:34Z"
updated: "2026-09-05T11:13:34Z"
parent: EPIC-m42s3g
phase: p4
---

## Scope

`node.Definition.Credentials` carries a `Required` flag and nothing reads it. `internal/node/registry.go` serves it to the editor, `web/src/lib/workflow-editor/credentials.ts` renders `requiresCredential` from it, and the compiler never looks — so a node that cannot possibly work without a credential compiles, activates, and fails at its first outbound call.

The failure is visible in the corpus. `waha-templates/restart-server-at-midnight` compiles with no WAHA credential attached and dies at run time with `request target is not allowed: scheme "" is not supported`, because the pack builds its base URL from `{{ $credentials.baseUrl }}` and that resolved to nothing. The message names a URL scheme; the actual problem is a missing credential, and nothing in the chain says so.

The database nodes do not have this problem because each one's `Validate` says `a postgres credential is required` by hand. A generated pack has no `Validate` and cannot have one — that is the whole point of a pack being data — so the check has to move to where the declaration already lives.

## Acceptance criteria

- [ ] A node whose definition declares a required credential and has none attached fails compilation, with an error naming the credential type rather than a downstream symptom.
- [ ] The error is a `workflow.ValidationError` with a node id and a field path, so the editor can point at the credential picker.
- [ ] A node whose credential requirement is optional still compiles with none attached.
- [ ] The database nodes' hand-written checks are removed in the same change, or the same node reports the same thing twice.
- [ ] The corpus baseline is regenerated and the movement explained: a workflow that used to compile and fail at run time now fails to compile, which is a better answer and a lower number.

## Implementation Plan

`workflow.NodeDefinition` needs the requirement, the way it already carries `RequiredParameters`. Add the declared credential types with their required flag, populate them in `node.Registry.Lookup`, and check in the compiler beside the required-parameter check so the two produce the same shape of error.

The trap is an imported workflow. An n8n import deliberately drops the foreign credential id and reports it, so every imported node with a credential arrives unbound — and this change turns that from "activates and fails later" into "does not activate". That is the correct outcome and it will move the corpus numbers down; say so in the baseline rather than softening the check.

## References

- `internal/node/registry.go` — `CredentialRequirement`, and `Lookup`, which builds `workflow.NodeDefinition`.
- `internal/workflow/compiler.go` — `NodeDefinition.RequiredFor` and where required parameters are checked.
- `nodes/database.go` — the hand-written `a postgres credential is required` this replaces.
- `.pine/tickets/FEAT-sp8cfm.md` — the corpus row that exposed it.
