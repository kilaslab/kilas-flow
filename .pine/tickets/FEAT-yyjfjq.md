---
id: FEAT-yyjfjq
title: Record the n8n licence boundary as project memory
status: done
priority: high
labels:
    - reference
    - guardrails
parent: EPIC-m42s3g
phase: p0
created: "2026-09-05T05:04:25Z"
updated: "2026-09-05T05:04:25Z"
---

## Scope

V2's central technique is reading n8n closely and reimplementing it in Go. That technique has one failure mode that would be expensive and quiet: somebody, months from now and under time pressure, copies a TypeScript file "just as a starting point", or adds `n8n-workflow` to a manifest to get a type definition, or drops a template JSON into `testdata` because it was the fastest fixture to hand. Each of those is a licence violation, none of them looks like one in a diff, and by the time anyone notices the code has been shipped.

The boundary is not vague and can be written down precisely, so it should be. At the reference checkout's commit, `n8n-workflow`, `n8n-core`, `n8n-nodes-base`, the `n8n` CLI package, `@n8n/n8n-nodes-langchain`, `@n8n/node-cli` and `@n8n/eslint-plugin-community-nodes` every one declares `"license": "LicenseRef-n8n-sustainable-use"` in its `package.json`. The root `LICENSE.md` is the Sustainable Use License v1.0 and additionally carves out every source file with `.ee.` in its filename or `.ee` in a directory name, which is not under the SUL at all and requires an n8n Enterprise License per `LICENSE_EE.md`. KilasFlow's own root `LICENSE` is Apache-2.0 and the product is white-label, multi-tenant and embedded, which is the configuration n8n's licensing FAQ names as not permitted. There is no reading of the SUL under which this repository can carry n8n source or depend on an n8n package.

What V2 does copy is the interoperability format, and the distinction has to be stated as sharply as the prohibition or the rule will be applied too widely and stall the work. Connection-type strings such as `ai_languageModel`, the workflow JSON document shape, the `.node.json` codex conventions, the `resource`/`operation` naming rules a generated pack must reproduce, `typeVersion` semantics, and the exact update list a Telegram Trigger offers are facts about an interchange format that a second implementation must match in order to interoperate at all. Reading them from the reference checkout and reimplementing them in Go is what every compatible implementation of every format does. Copying the code that implements them is not.

Two third-party licences differ from n8n's and must be recorded next to it, because they are the ones that will actually come up. `@devlikeapro/n8n-nodes-waha` is MIT, which is why p0-1 vendors its two `openapi.json` documents with the upstream notice — the only third-party bytes V2 puts in this repository. `github.com/devlikeapro/waha-n8n-templates`, the source of the official WAHA template corpus, carries no licence file at all and the GitHub API reports `license: null`, which is stricter than the SUL, not looser: those templates are never committed.

Today the place this belongs is empty. `.pine/memory/` has no files, `.pine/learnings/` has none, and `.pine/MEMORY.md` holds a single Log line about the project's naming. The repository's own practice already shows the boundary being kept — `.gitignore` excludes `design-refs/` with a comment explaining that n8n UI screenshots are third-party product UI kept locally and never committed — but that precedent lives in a comment on an ignore rule, where nobody will find it in time.

## Acceptance criteria

- [x] `.pine/memory/licensing.md` exists, written through `pine learn --to memory/licensing.md` rather than by hand, and `pine learn show memory/licensing.md` renders it.
- [x] It states the three prohibitions in a form an agent can apply without further reading: no n8n source in this repository, no n8n package in any manifest, no n8n bytes in any shipped artifact — and no contact with n8n.
- [x] It names the licences by their declared identifiers (`LicenseRef-n8n-sustainable-use`, the `.ee` Enterprise carve-out) and the n8n version they were read at, so a future reader can tell whether the facts are stale.
- [x] It separates format facts, which may be reimplemented, from code, which may not, with at least two concrete examples of each.
- [x] It records the two third-party exceptions: `@devlikeapro/n8n-nodes-waha` is MIT and its OpenAPI documents are vendored with the notice; `devlikeapro/waha-n8n-templates` is unlicensed and is never committed.
- [x] It states that KilasFlow's own `LICENSE` stays Apache-2.0, and names the reference-checkout paths as read-only material that is never a build input.
- [x] A repeatable check proves the boundary holds and fails if it stops holding, rather than depending on anyone remembering the rule.
- [x] The rule reaches an agent that starts a session with `pine context` — verified against real output, with a one-line pointer added to `.pine/MEMORY.md` if topic files turn out not to be inlined there.

## Implementation Plan

Write the memory entry first and keep it short. A guardrail that runs to two pages gets skimmed; this one needs to survive being read in five seconds by an agent about to add a dependency. Lead with the three prohibitions, then the format-versus-code distinction with examples, then the two third-party exceptions, then the version and date the licence facts were read at. `pine learn` suggests a destination by default, so force it with `--to memory/licensing.md`; the plan settles that this is project memory, not machine-wide (`-g`), because it is a fact about this repository's licence position and not about the author's habits.

Then add the check, because memory is advisory and the failure this ticket exists to prevent is somebody not reading it. The cheapest honest check is a Go test — `internal/…/licence_boundary_test.go` or similar — that reads `go.mod` and every `package.json` in the tree (root, `web/`, `sdk/`) and fails if any dependency name matches `n8n`. It runs inside `go test ./...`, which every ticket already runs, and it costs nothing. Today it passes: `go.mod` contains no `n8n` line and no manifest references one. The alternative, a `make lint` grep step, is weaker because a contributor can land a change without running `make lint` and CI would have to be taught about it separately. Recommend the Go test.

The trap is scope creep in the opposite direction: do not write a check that greps the whole tree for the string `n8n`. Fourteen tracked files legitimately contain it — `internal/interop/n8n/n8n.go`, `parameters.go`, `n8n_test.go`, `internal/api/handlers/interop.go`, `internal/workflow/doc.go`, `nodes/doc.go`, the generated `sdk/src/generated/models.ts` and `web/src/lib/api/generated/…` models, `web/src/lib/workflow-editor/document.ts`, `README.md`, `gflow-prd-v1.md` and `.gitignore` — because interoperating with a format means naming it. A check that fires on those is a check that gets deleted. Match dependency names in manifests, nothing else.

One decision remains: whether the memory entry also names the clean-room question that p8-2 defers. The JS sidecar is the one part of the roadmap where the boundary is genuinely contested, since running third-party community nodes means executing SUL code in a process this project ships. Recommend a single sentence in the memory file pointing at that ticket and stating that the question is reopened there and nowhere else — enough that an agent reading the guardrail does not treat p8-2 as a licence bug, and not so much that the guardrail becomes an argument.

## References

- Roadmap plan, p0 section, entry V2-p0-3: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the "Licence posture" row of the locked-decisions table.
- Reference checkout `$KILASFLOW_N8N_REFERENCE` at `40dfa42` (n8n 2.34.0): `LICENSE.md` (Sustainable Use License v1.0 plus the `.ee` carve-out), `LICENSE_EE.md`, and the `license` field of `packages/{workflow,core,nodes-base,cli}/package.json` and `packages/@n8n/{nodes-langchain,node-cli,eslint-plugin-community-nodes}/package.json`.
- `github.com/devlikeapro/n8n-nodes-waha` — MIT, version 2025.2.9. `github.com/devlikeapro/waha-n8n-templates` — no licence file, `license: null`.
- This repository: root `LICENSE` (Apache-2.0), `.gitignore` (the `design-refs/` exclusion and its comment), `.pine/MEMORY.md`, `AGENTS.md` learnings rules.
- Sibling tickets: the reference-checkout and vendoring ticket in this phase, and the deferred JS sidecar ticket in p8.

## Outcome

`.pine/memory/licensing.md` was written through `pine learn --to
memory/licensing.md` in five entries and renders under `pine learn show
memory/licensing.md`. It leads with the three prohibitions plus the
no-contact rule, then the licence identifiers and the version they were read
at, then the format-versus-code distinction, then the two third-party
exceptions, then the Apache-2.0 position and the single sentence pointing at
FEAT-7cg0cd as the only place the sidecar question is reopened.

Every licence fact was re-verified against the reference checkout rather than
copied from the ticket: all seven packages (`workflow`, `core`, `nodes-base`,
`cli`, `@n8n/nodes-langchain`, `@n8n/node-cli`,
`@n8n/eslint-plugin-community-nodes`) declare
`LicenseRef-n8n-sustainable-use`, and the `.ee` carve-out is at LICENSE.md
lines 6-9.

The check is `internal/guardrails`, six tests inside the ordinary `go test
./...`:

- `TestNoN8NDependencyInGoModule` — no n8n module in `go.mod`.
- `TestNoN8NDependencyInNodeManifests` — no n8n package in any tracked
  `package.json`, discovered rather than listed so a manifest added later is
  covered automatically.
- `TestReferenceCheckoutIsNeverABuildInput` — no tracked build input reaches
  outside the repository for bytes.
- `TestVendoredThirdPartyCarriesItsLicence` — every `third_party/<dir>` keeps
  a `LICENSE` and `PROVENANCE.md` beside its bytes.
- `TestForbiddenModuleRequiresDetectsN8N` and
  `TestForbiddenDependencyMatchesNamesNotProse` — proof the matcher fires.

The trap the ticket named was avoided: the check matches **dependency names**,
never file contents. Seventy-nine tracked files legitimately contain "n8n"
because interoperating with a format means naming it, and
`TestForbiddenDependencyMatchesNamesNotProse` pins that — `connection8nine`
contains the substring `n8n` and must not match.

Three things the ticket did not anticipate:

1. **The content scan needed to be scoped to tracked files.** A filesystem
   walk flagged `web/static/vendor/scalar.js`, an untracked 3.7 MB vendor
   bundle whose `/home/user/...` strings are documentation examples. The
   boundary is about what enters the repository, so discovery is `git
   ls-files`; the test skips with an explanatory message outside a git work
   tree.
2. **The checker tripped its own check**, because it has to name the patterns
   it hunts for. The markers are stored as fragments joined at run time and
   `internal/guardrails/` is exempt from the content scan, which is documented
   in the file rather than left to be rediscovered.
3. **The go.mod check cannot be proved by editing go.mod.** An unresolvable
   `require` stops the module loading, so `go test` never reaches the
   assertion and the check appears to pass — the probe was silently useless.
   The parse is therefore a pure function over the file's bytes with a fixture
   test beside it.

All four checks were then demonstrated failing on a real violation — an
`n8n-workflow` dependency staged into `sdk/package.json`, a tracked Go file
naming the reference checkout, and a `third_party/probe/` with no notice — and
passing again once reverted.

The reference-checkout rule is stated portably rather than as one machine's
path: an absolute path into any home directory is a build-input violation on
any machine, which catches the licence case and is worth enforcing regardless.

`pine context` was checked against real output and **does** inline
`memory/licensing.md` in full, so the conditional pointer in the acceptance
criteria was not required; a one-line pointer was added to `.pine/MEMORY.md`
anyway for anyone reading that file on its own.
