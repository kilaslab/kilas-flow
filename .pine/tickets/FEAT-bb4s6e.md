---
id: FEAT-bb4s6e
title: Skills drift gates G1-G5 and make skills-check
status: doing
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
updated: "2026-09-21T02:55:33Z"
---

## Scope

Design §5.7 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`), mirroring `sdk/test/operation-coverage.test.mjs`.

## Acceptance criteria

- [x] G1 commands, G2 operations, G3 nodes, G4 expression roots run as ordinary Go tests in
      `internal/skills/skills_test.go`, so they run in `go test ./...` and cannot be skipped by
      forgetting a Makefile target.
      `TestG1DeclaredCommandsResolve`, `TestG1FencedCommandsResolve`, `TestG2OperationsExist`,
      `TestG3NodeTypesExist`, `TestG4ExpressionRootsExist`, over `skills.LoadBundle()` (the bundle
      the binary carries). No build tag, no Makefile dependency: `go test -count=1 ./internal/skills/...`
      runs all five. Nothing is a hand-maintained list — G1 reads the `help --json` document
      `cli.Run` prints from the real registry, G2 reads the OpenAPI document of an in-process
      server (`api.NewServer`), G3 calls `nodes.RegisterAll` into a fresh `node.Registry`, G4
      reads `expression.Roots()`. 96 declared commands and 130 fenced commands examined in the
      bundle today; the fenced gate refuses to pass unless it examined at least 100 of them
      (`fencedCommandFloor`), so an extractor that stops matching fails instead of passing empty.
- [x] G5 freshness: `make skills-check` runs `kilasflow skills check` against a binary built from
      the commit, next to `sdk-check`, and CI runs it.
      `make skills-check` (Makefile, in the skills cluster) builds the binary, then runs
      `go test -count=1 ./internal/skills/...` (G1–G4, so a skill naming a verb the binary does not
      implement fails this target) and installs the binary's own bundle into a `mktemp -d` scratch
      directory, checking that copy back. Green on this tree: 29 files, `ok: true`, exit 0. Wired
      into `.github/workflows/ci.yml`'s lint job as a "Skills bundle" step beside `make lint`.
- [x] The honesty test of §8 (every non-empty `kilasflow_not_shipped` has a matching section).
      Already end to end in `internal/skills/check.go`'s not-shipped rule and proven by two
      planted fixtures (`testdata/planted/not-shipped-missing-section`, `.../not-shipped-unnamed-entry`,
      both asserted in `TestCheckRejectsPlantedViolations`); no second implementation was written.
      `TestEmbeddedBundlePassesItsOwnChecker` runs `skills.Check` over the embedded bundle, which
      is the artifact the other four gates read, so the claim an agent loads is the claim the
      frontmatter makes.
- [x] Each gate is proven to fail: a test plants a bad command, operation id, node type and
      expression root and sees the gate reject it.
      `TestGatesRefusePlantedDeclarations` (table-driven, one case per gate, each document parsed
      by `skills.Parse` so the fixture also proves the frontmatter contract accepts it), plus an
      out-of-band plant of the same four values in the real bundle — each gate named the planted
      value and failed, and `git status --porcelain skills/` was empty after the restore.

## Implementation notes

**The gates live in an external test package, and that is the one architectural decision here.**
`internal/skills` is a leaf on purpose — it reads markdown and JSON and reaches nothing else, so a
distroless image can install the bundle with no checkout — and none of the four product surfaces
is reachable from it. `skills_test` is therefore `package skills_test`, which is where the two are
allowed to meet: it imports `internal/cli`, `internal/api`, `nodes` and `internal/expression`, all
of them test-only. `internal/cli/openapi_contract_test.go` already takes the same allowance for
`internal/api`, and `internal/cli/doc.go` states the rule the allowance is an exception to. No
production package gained a dependency.

**G1's command tree comes from `help --json`, driven in-process.** The registry is unexported —
which is why `scripts/skills-command-reference` reads the same document — and `cli.Run(cli.Env{…})`
is the composition `cmd/kilasflow` executes, so the gate reads the registry the binary would run
rather than a copy of the verb list. Doing it in-process rather than by building a binary keeps
the gate an ordinary test: no toolchain, no subprocess, ~20ms.

**G1's fenced half is the strict one, and it is strict on purpose.** Every word `kilasflow` in a
fence that is followed by a word must resolve to a verb, with exactly one exemption: a value
another program was given (a word after a flag, or the word after such a value — the upgrade notes'
`pg_dump -U kilasflow kilasflow`, where the database user and the database name are spelled like
the binary). Resolving only the longest *prefix* keeps the verb as the claim: `kilasflow workflow
get <workflowId> to confirm it was saved` resolves on `workflow get`, and everything after it is
the invocation's own arguments, which the CLI judges when it runs. `kilasflow workflow frobnicate`
fails because no path in the tree is the family alone. The asymmetry in the exemption is
deliberate: a skipped occurrence would hide a renamed verb silently, while an occurrence read as a
command that is really prose fails loudly and names the line.

**G2 reads the published document, not the source.** `internal/api/handlers/` writes
`OperationID:` literals inside `huma.Operation` values, which are not reachable without either a
text scan or a booted server; the OpenAPI document of `api.NewServer` with a stub `DB` is the
surface the product actually publishes, costs one `httptest` server, and is the same composition
`internal/cli/openapi_contract_test.go` uses. 76 operation ids today.

**`make skills-check` has two halves because the drift has two shapes.** The round trip (install
into a scratch directory, check it back) is the half only a real binary can prove, and it is what
design §5.7 calls G5. The test half is there because the acceptance criterion for this target is
"fails when a skill names a verb that does not exist", and `skills check` cannot see that — it
compares bytes, and bytes written from the bundle always match it. The scratch directory is
outside the checkout on purpose: checking `.agents/skills` would report whatever a developer's
last install left there.

## Files

```
 .github/workflows/ci.yml            | lint job: make skills-check, beside make lint
 Makefile                            | skills-check
 internal/skills/skills_test.go      | G1-G4, the planted-declaration proofs, §8 over the bundle
 .pine/tickets/FEAT-bb4s6e.md        | this ticket
```

## Evidence

- `go test -count=1 ./internal/skills/...` — ok (five gate tests, the planted table, the checker
  over the embedded bundle).
- `make skills-check` — `ok … internal/skills`, then `{"ok":true, …, "files":29,"drift":[]}`,
  exit 0; with `kilasflow-credentials/SKILL.md`'s fence changed to `kilasflow datastore lst` —
  `--- FAIL: TestG1FencedCommandsResolve … names no verb this binary implements`, exit 2;
  `git checkout -- skills/` → `git status --porcelain skills/` empty → green again.
- Planted per gate, each restored: `kilasflow credential lst` (G1 declared), `kilasflow datastore
  lst` in a fence (G1 fenced), `list-credentialz` (G2), `kilasflow.httpRequestt` (G3), `$jsom` (G4)
  — each gate failed naming the value, `git status --porcelain skills/` empty afterwards.
- Tree-driven, not list-driven: a scratch verb added to `registry()` made a fence naming it pass,
  and removing it from the registry made the same fence fail.
- `gofmt -l internal/skills/` — silent; `go build ./... && go vet ./...` — clean.
- `make generate-skills-index-check` — `13 skills, up to date`, exit 0;
  `make generate-skills-command-reference-check` — `58 verbs, up to date`, exit 0.
- Drift half out of band: after mutating one installed file, `skills check` exits 1 with
  `error.code = "skills_drift"` naming the file.

## Open question for the landing review

`make generate-skills-index-check` and `make generate-skills-command-reference-check` are wired
into no CI job (they are the two gates over the bundle's generated half, both green here). This
ticket's criterion named `skills-check` only, so they were left alone; the drift job — which
already runs the API client, SDK types, docs and config reference checks — is where they belong if
they should run per push.
