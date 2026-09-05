---
id: FEAT-qfr9xe
title: Enforce required credentials at compile time
status: done
priority: medium
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T11:13:34Z"
updated: "2026-09-05T11:57:24Z"
---

## Scope

`node.Definition.Credentials` carries a `Required` flag and nothing reads it. `internal/node/registry.go` serves it to the editor, `web/src/lib/workflow-editor/credentials.ts` renders `requiresCredential` from it, and the compiler never looks — so a node that cannot possibly work without a credential compiles, activates, and fails at its first outbound call.

The failure is visible in the corpus. `waha-templates/restart-server-at-midnight` compiles with no WAHA credential attached and dies at run time with `request target is not allowed: scheme "" is not supported`, because the pack builds its base URL from `{{ $credentials.baseUrl }}` and that resolved to nothing. The message names a URL scheme; the actual problem is a missing credential, and nothing in the chain says so.

The database nodes do not have this problem because each one's `Validate` says `a postgres credential is required` by hand. A generated pack has no `Validate` and cannot have one — that is the whole point of a pack being data — so the check has to move to where the declaration already lives.

## Acceptance criteria

- [x] A node whose definition declares a required credential and has none attached fails compilation, with an error naming the credential type rather than a downstream symptom.
- [x] The error is a `workflow.ValidationError` with a node id and a field path, so the editor can point at the credential picker.
- [x] A node whose credential requirement is optional still compiles with none attached.
- [x] The database nodes' hand-written checks are removed in the same change, or the same node reports the same thing twice.
- [x] The corpus baseline is regenerated and the movement explained: a workflow that used to compile and fail at run time now fails to compile, which is a better answer and a lower number.

## Outcome

`workflow.NodeDefinition` gained `RequiredCredentials`, filled by the registry from each `CredentialRequirement`'s own `Required` flag, and the compiler checks it beside the required-parameter check so both produce the same shape of error — `ErrorRequiredConfig`, a node id, and the path `/nodes/N/credentials/<type>` for the editor to point at.

An attached-but-*empty* reference is not attached. That is not pedantry: an imported node arrives with its foreign credential id deliberately dropped, so treating an empty string as a credential would let exactly the nodes this check exists for straight through.

The database nodes' hand-written check is gone. `validateDatabaseConfiguration` used to say `a postgres credential is required` itself, which would now be the same thing reported twice in two wordings — and its test moved to asking the compiler, since that is where the rule now lives.

### The corpus went down, on purpose

| | Before | After |
| --- | ---: | ---: |
| Activatable | 13 / 39 | **12 / 39** |
| Runnable | 3 / 39 | 3 / 39 |

`waha-templates/restart-server-at-midnight` used to compile, activate, and die at its first outbound call with `request target is not allowed: scheme "" is not supported` — because the pack builds its base URL from the credential and the credential was not there. It now fails to compile with `node "…" requires a wahaApi credential`. That is one fewer activatable workflow and a strictly better answer: the old one named a URL scheme, and the actual problem was a missing credential nobody had mentioned.

The compiler's own tests moved into `internal/workflow` with a stub catalogue for this, so a compiler rule can be stated without a real node happening to exercise it — and so that changing a real node cannot quietly change what a compiler test proves.

## Implementation Plan

`workflow.NodeDefinition` needs the requirement, the way it already carries `RequiredParameters`. Add the declared credential types with their required flag, populate them in `node.Registry.Lookup`, and check in the compiler beside the required-parameter check so the two produce the same shape of error.

The trap is an imported workflow. An n8n import deliberately drops the foreign credential id and reports it, so every imported node with a credential arrives unbound — and this change turns that from "activates and fails later" into "does not activate". That is the correct outcome and it will move the corpus numbers down; say so in the baseline rather than softening the check.

## References

- `internal/node/registry.go` — `CredentialRequirement`, and `Lookup`, which builds `workflow.NodeDefinition`.
- `internal/workflow/compiler.go` — `NodeDefinition.RequiredFor` and where required parameters are checked.
- `nodes/database.go` — the hand-written `a postgres credential is required` this replaces.
- `.pine/tickets/FEAT-sp8cfm.md` — the corpus row that exposed it.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `ab8c2614` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `a7c2fb9a` — feat(interop): map WAHA workflows through the n8n importer
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-3taswf.md                       |   67 ++
 .pine/tickets/FEAT-53fht8.md                       |   60 ++
 .pine/tickets/FEAT-6vfn3s.md                       |  354 ++++++-
 .pine/tickets/FEAT-7tgasa.md                       |   61 ++
 .pine/tickets/FEAT-bscygc.md                       |   62 ++
 .pine/tickets/FEAT-jq84xk.md                       |   67 ++
 .pine/tickets/FEAT-m94hhx.md                       |   60 ++
 .pine/tickets/FEAT-nc6z9r.md                       |   68 ++
 .pine/tickets/FEAT-pnbt4z.md                       |   91 ++
 .pine/tickets/FEAT-qfr9xe.md                       |   58 +
 .pine/tickets/FEAT-sp8cfm.md                       |  359 ++++++-
 .pine/tickets/FEAT-xqqjqv.md                       |  281 ++++-
 .pine/tickets/FEAT-yx0qt6.md                       |   71 ++
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +++++-
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |   32 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/credentials/builtin.go                    |   23 +
 internal/credentials/credentials.go                |    7 +
 internal/credentials/credentials_test.go           |    1 +
 internal/credentials/registry.go                   |   30 +-
 internal/engine/runner.go                          |   30 +-
 internal/engine/runner_test.go                     |   87 ++
 internal/interop/n8n/corpus/BASELINE.md            |   36 +-
 internal/interop/n8n/corpus/baseline.json          |  113 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   64 +-
 internal/interop/n8n/n8n.go                        |  289 ++++-
 internal/interop/n8n/n8n_test.go                   |  503 ++++++++-
 internal/interop/n8n/parameters.go                 |  171 ++-
 internal/node/registry.go                          |   46 +-
 internal/node/registry_test.go                     |   81 +-
 internal/property/property.go                      |   94 +-
 internal/routing/executor.go                       |  216 +++-
 internal/routing/request.go                        |   34 +-
 internal/routing/response.go                       |    5 +
 internal/routing/routing.go                        |   29 +-
 internal/webhook/shape.go                          |   29 +
 internal/webhook/webhook.go                        |   64 +-
 internal/workflow/compiler.go                      |   30 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/assignments.go                               |  180 ++++
 nodes/bindings_test.go                             |    7 +-
 nodes/core.go                                      |    6 +-
 nodes/database.go                                  |   10 +-
 nodes/database_test.go                             |   49 +-
 nodes/executors.go                                 |  115 +-
 nodes/executors_test.go                            |  216 ++++
 nodes/telegram.go                                  |  393 +++++++
 nodes/telegram_download.go                         |  243 +++++
 nodes/telegram_lifecycle.go                        |  423 ++++++++
 nodes/telegram_test.go                             |  610 +++++++++++
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++++++++++++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 ++++++++
 packs/waha/waha_test.go                            |   68 +-
 sdk/src/generated/models.ts                        |  150 +--
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 web/src/lib/api/generated/models/index.ts          |    1 +
 .../lib/api/generated/models/propertyDefinition.ts |    3 +
 .../workflow-editor/property-field.svelte          |   67 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 ++
 web/src/lib/workflow-editor/assignments.ts         |   89 ++
 web/src/lib/workflow-editor/node-visual.ts         |    2 +
 65 files changed, 8107 insertions(+), 371 deletions(-)
```
