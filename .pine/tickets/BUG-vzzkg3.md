---
id: BUG-vzzkg3
title: Board and docs claim three capabilities the code does not have
status: done
priority: high
labels:
    - docs
    - platform
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-21T00:31:53Z"
---

## Problem

Three tickets are `status: done` with **every acceptance criterion still unticked**, and the
documentation was written against them, so both the board and the docs currently tell the next
reader (human or agent) that these paths work.

## Evidence

- `FEAT-3taswf` (Publish `@kilasflow/sdk` to npm) — body criteria all `- [ ]`. Live check
  2026-09-20: `npm view @kilasflow/sdk version` → `E404 Not Found`. `git tag` count is 0.
- `FEAT-48hreg` (native community module SDK on WebAssembly) — body criteria all `- [ ]`.
  The guest side exists (`pkg/sdk`), the host side does not: `internal/nodepack/nodepack.go:38`
  has no module field on `Pack`, `internal/nodepack/loaddir.go:6-8` still describes WASM packs
  as future work, and `internal/runcode` grants the guest no host functions.
- `FEAT-7cg0cd` (JavaScript sidecar) — the protocol and per-tenant pool exist in `sidecar/`,
  but the package is imported by no production code (grep `kilaslab/kilas-flow/sidecar`
  outside `./sidecar/` returns nothing), there is no config key for it, and
  `node.SourceSidecar` has no `RegisterFrom` call site.
- `docs/src/content/docs/guides/community-nodes.md:28,46` presents the WASM pack build as a
  working path; `:77-83` presents the sidecar as something an operator configures.
- `docs/src/content/docs/concepts/tenancy-and-embedding.md:270` states "Nothing creates a
  tenant through the API. No operation exists for it." — contradicted by
  `POST /api/v1/tenants` (`internal/api/handlers/admin.go:208`) and by the generated
  reference at `docs/src/content/docs/reference/api/tenants.md`.
- `FEAT-77rveq` and `FEAT-cwmw90` are duplicate open tickets for the same deliverable
  (`GET /workflows/{id}/webhooks`).

## Acceptance criteria

- [x] `FEAT-3taswf`, `FEAT-48hreg` and `FEAT-7cg0cd` are either reopened with criteria that
      match what actually shipped, or re-scoped in place with a comment naming what remains.
      Proved in stage 1: `FEAT-48hreg` and `FEAT-7cg0cd` are `status: doing`, each carrying a
      `## Reopened 2026-09-20` section that names what remains, so neither needed an edit;
      `FEAT-3taswf` was `status: done` with criteria 1, 5 and 8 unticked and its reopened note
      already deleted by the re-close commit, so it was re-scoped in place with a
      `## Re-scoped 2026-09-20` note naming each unproven criterion and why it cannot be proved
      from inside this repository.
- [x] `community-nodes.md` states plainly which parts of the WASM and sidecar stories are not
      shipped, or the pages are removed until they are.
      Proved in stage 2: the page now carries a `## What is not shipped yet` section naming the
      four verified gaps, the two path sections are scoped to what exists (guest SDK; sidecar
      library), and no sentence asserts an operator path the stage's greps show absent.
      `make docs-build` passes (44 pages, "All internal links are valid.").
- [x] `concepts/tenancy-and-embedding.md` matches the code on tenant provisioning.
      Proved in stage 3: the false paragraph at `:270` ("Nothing creates a tenant through the
      API. No operation exists for it.") is replaced by one that names `POST /api/v1/tenants`
      (201 + `Location`, 409 on duplicate), the tenant-users and tenant-api-key operations, and
      the operator-credential requirement — each matching `internal/api/handlers/admin.go`
      (create-tenant registration at `:209`, `CreateTenant` at `:301`, `operatorOnly` gate at
      `:187`) and the generated `reference/api/tenants.md`. The other page sections it touches
      were verified sentence by sentence; `grep -rn "Nothing creates a tenant\|No operation
      exists for it" docs/src/content/docs/` now returns nothing, and `make docs-build` passes
      with all internal links valid.
- [x] The duplicate webhook-URL tickets are merged into one.
      Proved in stage 1 by verification only, because the merge had already happened before this
      ticket started: `FEAT-77rveq` and `FEAT-cwmw90` are both `status: done`, each carrying a
      `## Duplicate 2026-09-20` note, and `list-workflow-webhooks` has exactly one registration
      (`internal/api/handlers/workflows.go:349`) and exactly one generated page
      (`docs/src/content/docs/reference/api/workflows.md:74`). No second open ticket and no
      second registration exists, so there was nothing left to merge.
- [x] `pine doctor` is clean afterwards.
      Proved in stage 3 after all ticket-file edits: `pine doctor` prints "✓ config.json and
      board.json are valid" / "✓ no problems found" (exit 0).

## Out of scope

Building the WASM host ABI or wiring the sidecar. This ticket only makes the board and the
docs tell the truth about them.

## Implementation notes

Stage 1 of 4 (board annotation and verification), 2026-09-20, worktree
`.claude/worktrees/omp-BUG-vzzkg3`, branch `worktree-omp-BUG-vzzkg3`.

### What changed

- `.pine/tickets/FEAT-3taswf.md` — appended a `## Re-scoped 2026-09-20` section after the final
  Work Evidence code block. Frontmatter untouched: the ticket stays `status: done`, because
  workspace rule 4 reserves a status flip for the orchestrator.
- `.pine/tickets/BUG-vzzkg3.md` — criteria 1 and 4 ticked with their proof; these notes.

### What did not change, and why

- `FEAT-48hreg` and `FEAT-7cg0cd` were left untouched: both already carry the reopened note the
  criterion asks for and both are `status: doing`. No docs page was edited in this stage; the two
  pages this ticket owns are stages 2 and 3.

### How it was verified (commands run and outcomes)

- `pine show FEAT-48hreg` → `status: doing`, plus a `## Reopened 2026-09-20` section naming the
  four host-side gaps: `Pack` has no module field, `internal/nodepack/loaddir.go:6-8` still
  describes WASM packs as future work, `internal/runcode` grants the guest no host module, and no
  production code builds a `runcode.Artifact` from a pack.
- `pine show FEAT-7cg0cd` → `status: doing`, plus a `## Reopened 2026-09-20` section naming: no
  production import of `github.com/kilaslab/kilas-flow/sidecar` outside `./sidecar/`, no config
  section, no `RegisterFrom(node.SourceSidecar)` call site, no `engine.Executor` adapter and no
  egress proxy.
- `pine show FEAT-3taswf` → `status: done` with five of eight criteria ticked; criteria 1, 5 and 8
  are `- [ ]` and no reopened section is present (commit `5658304` had removed it). That is the
  state the appended note describes.
- `npm view @kilasflow/sdk version` → `npm error code E404`, `404 Not Found - GET
  https://registry.npmjs.org/@kilasflow%2fsdk`.
- `git tag | wc -l` → `0`. Together with `.github/workflows/release.yml`'s sdk job — gated on
  `startsWith(github.ref, 'refs/tags/sdk-v')`, running every `make sdk-release-check` target
  (`sdk-check`, `sdk-test`, `sdk-build`, `sdk-version-check`, `generate-types-check`,
  `sdk-package-check`, `sdk-example-check`) before `npm publish --provenance --access public` —
  this is what makes the note's "it has never run" and "provenance can only be attested by a
  publish from that workflow" claims checkable rather than asserted.
- `grep -n list-workflow-webhooks` over `internal/` → exactly one hit,
  `internal/api/handlers/workflows.go:349` (`OperationID: "list-workflow-webhooks"`, `GET
  /workflows/{id}/webhooks`).
- `grep -n list-workflow-webhooks docs/src/content/docs/reference/api/workflows.md` → one hit,
  line 74 (`## List a workflow's webhook URLs (list-workflow-webhooks)`).

### Gates (stage 1)

No Go source, config key, API operation, SDK surface or migration was touched, so only the gates
that can be affected were run. `docs/node_modules` does not exist in a fresh worktree, so
`pnpm install --frozen-lockfile` was run in `docs/` first (exit 0, 10.7s).

- `make docs-build` → pass. 44 pages built, "All internal links are valid."
- `pine doctor` → pass, "✓ config.json and board.json are valid" / "✓ no problems found" (exit 0),
  run against the final edited ticket files.
- `make coordinates-check` → pass, "every published coordinate names ghcr.io/kilaslab/kilasflow's
  owner".
- `go test ./internal/guardrails/...` → pass, `ok github.com/kilaslab/kilas-flow/internal/guardrails
  1.406s`.

`gofmt -l`, `go vet ./...`, `go build ./...`, the web `pnpm` gates, the SDK generator targets and
the SQL-dialect gates do not apply: no Go, web, SDK or migration file changed.

### Stage 2 of 4 (community-nodes.md honesty), 2026-09-20, same worktree/branch

#### What changed

- `docs/src/content/docs/guides/community-nodes.md` — rewritten where it overclaimed. The two
  path sections are now scoped to what exists: "Path one" documents the author-side guest SDK
  (`pkg/sdk`: `Main`, `Handle`, `Item`, `example/echo`) and says the host half does not load
  packs; "Path two" documents `sidecar/` as a written-and-tested library and says no production
  code imports it. A new `## What is not shipped yet` section names the concrete gaps. The
  `description` frontmatter and the guest-SDK table row were corrected; `## What the deployment
  looks like` was retitled in substance to describe what deploys today (`packs.dir`; no `.wasm`
  story) and what a wired sidecar would cost. The Licence position section is unchanged.
- Removed as unverifiable-against-the-tree: the claim that a parsed pack format is "manifest plus
  `.wasm` modules", the implication that a pack runs on the host with per-call limits enforced,
  and the description of "without a sidecar configured, sidecar-tagged runs fail with a
  diagnostic" as a deployment behaviour (the `Pool` behaviour is real, but nothing constructs the
  pool in the server).
- `.pine/tickets/BUG-vzzkg3.md` — criterion 2 ticked with its proof; these notes. Status untouched.

#### What did not change, and why

- `FEAT-48hreg` and `FEAT-7cg0cd` are still in flight (`status: doing`), so the page states the
  tree as it stands and adds no sentence a future landing would make true; the section closes
  with "It will grow the operator-facing sections when the host halves land." No host half of
  either path was built (out of scope).

#### How it was verified (commands run and outcomes)

- `grep -n Module internal/nodepack/nodepack.go` → no match. `Pack` carries type, version,
  display name, description, category, icon/colour, subtitle, documentation URL, credential type,
  `VisibleTo`, request defaults, trigger, parameters, resources and provenance — no module or
  artifact field.
- `sed -n '1,20p' internal/nodepack/loaddir.go` → line 7 still reads "the WASM packs of
  FEAT-48hreg reuse this loader by adding a module kind beside the manifest" (future tense). The
  shipped unit is `pack.json` (`ManifestName`) plus `pack.sha256` (`ChecksumName`).
- `grep -rn "WithHostModule\|HostModule" internal/runcode` → no match. `internal/runcode/doc.go`
  and `runcode.go` (`wazero.NewModuleConfig()` with stdin/stdout/stderr and the two clocks; "No
  WithFS, no WithEnv, no WithArgs") confirm the guest gets no host interface.
- `grep -rn "kilaslab/kilas-flow/sidecar" --include=*.go . | grep -v '^./sidecar/'` outside tests
  → no match. The package is imported only by its own tests.
- `grep -rn -i sidecar internal/config/config.go config.example.yaml` → no match. Config has a
  `packs` section (`packs.dir` / `packs.visible_to`) and no sidecar section.
- `grep -rn "RegisterFrom(node.SourceSidecar" --include=*.go internal nodes cmd` → only
  `internal/node/registry_test.go:741`; no production call site. `SourceSidecar` is declared at
  `internal/node/registry.go:158`.
- Verified what does exist so the page names it accurately: `pkg/sdk/sdk.go` (`Main`, `Handle`,
  `Item`, `Version = "v1"`), `pkg/sdk/example/echo/main.go`, `sidecar/{doc,protocol,sidecar}.go`
  (`Pool` keyed by tenant, `CallError` codes incl. `host-call-denied`/`frame-too-large`, `Limits`
  with `Timeout`/`MaxFrameBytes`/`MaxOutputBytes`/`NodeMaxHeapMB`), `sidecar/fixture/echo.js`
  (prints a startup banner to stdout deliberately), and `cmd/kilasflow/main.go:291`
  (`nodepack.LoadDir` for directory packs).

#### Gates (stage 2)

`docs/node_modules` was already present; `pnpm install --frozen-lockfile` in `docs/` reported
"Already up to date" (exit 0).

- `make docs-build` → pass. 44 pages, "All internal links are valid."
- `pine doctor` → pass, "✓ config.json and board.json are valid" / "✓ no problems found" (exit 0),
  run against the final edited ticket file.
- `make coordinates-check` → pass, "every published coordinate names ghcr.io/kilaslab/kilasflow's
  owner".
- `go test -count=1 ./internal/guardrails/...` → pass,
  `ok github.com/kilaslab/kilas-flow/internal/guardrails 0.795s`.

No Go, web, SDK, config or migration file changed, so `gofmt -l` / `go vet ./...` / `go build
./...` / the web `pnpm` gates / the SDK generator targets / the SQL-dialect gates do not apply.

### Stage 3 of 4 (tenancy-and-embedding.md matches tenant provisioning), 2026-09-20, same worktree/branch

#### What changed

- `docs/src/content/docs/concepts/tenancy-and-embedding.md` — the "Multi-tenancy today" paragraph
  at `:270` was replaced. Its lead sentence ("**Nothing creates a tenant through the API.** No
  operation exists for it.") was false: `POST /api/v1/tenants` exists and is operator-gated. The
  replacement names the three provisioning operations (create tenant, create first account, mint
  a key for the tenant), their status codes (`201` + `Location`, `409` on a duplicate ID), and
  the operator-credential requirement (an API key scoped to the operator tenant; customer keys
  and sessions are refused). The three legacy paths (migration seed, `auth.bootstrap_tenant` boot
  ensure, direct row insert) are kept, and the workflow/credential-will-carry-any-string note is
  unchanged because it is still true.
- `## API operations` — added the operator surface (`GET`/`POST /api/v1/tenants`, the account
  operations under `/api/v1/tenants/{id}/users...`, and `POST /api/v1/tenants/{id}/api-keys`)
  with a link to the generated `/reference/api/tenants/` page.
- `## Source` — added `internal/api/handlers/admin.go` (the operator provisioning surface and its
  `operatorOnly` gate) beside `internal/api/handlers/tenants.go`.
- `.pine/tickets/BUG-vzzkg3.md` — criteria 3 and 5 ticked with their proof; these notes. Status
  untouched.

#### Surrounding claims re-verified before editing (not just the fixed sentence)

- `internal/api/handlers/admin.go` — `create-tenant` registered at `:209` (the ticket's `:208` is
  one line stale), `func (handler *Admin) CreateTenant` at `:301`; `CreateTenant` returns
  `huma.Error409Conflict` on `repository.ErrAlreadyExists` and sets `Location:
  handler.tenantPath(tenant.ID)` with `Status: http.StatusCreated`; the `operatorOnly` gate
  (`:187`) accepts only `auth.KindAPIKey` with `principal.TenantID == repository.OperatorTenantID`
  and returns `403` otherwise. `create-tenant-user` (`:232`) and `create-tenant-api-key`
  (`:270`) are registered the same way.
- `docs/src/content/docs/reference/api/tenants.md` (generated) — documents each of those
  operations with the same statuses and the operator-credential note, so the page and the
  reference agree.
- `migrations/{sqlite,postgres}/000003_identity.up.sql` — `tenants(id, name, created_at,
  updated_at)`; `users` and `api_keys` carry `fk_*_tenant ... ON DELETE RESTRICT`; the seed
  inserts `default` plus every distinct `tenant_id` from `workflows` and `credentials`.
  `workflows`/`credentials` are in `000001_baseline` with a plain `tenant_id` column and no FK to
  `tenants`, confirming the page's "will happily carry any tenant string".
- `cmd/kilasflow/main.go:939-951` — `bootstrapIdentity` calls `store.EnsureTenant(ctx, tenantID,
  tenantID)` with `cfg.BootstrapTenant` on every boot, plus the operator tenant, confirming the
  "ensured on every boot" sentence.
- `internal/api/handlers/tenants.go` — `PrincipalTenants.Resolve` checks an embed session first,
  then a principal, then the fallback, matching the page's precedence section.
- Endpoints named in `## API operations` all exist: `/embed-sessions` (`internal/api/handlers/
  embed.go:80`), `/auth/login|logout|me` and `/api-keys` GET/POST (`internal/api/handlers/
  auth.go:235-267`), `/stream-tickets` (`:283`).

#### Gates (stage 3)

`docs/node_modules` was present; no `pnpm install` was needed.

- `make docs-build` → pass. Astro built 44 pages, pagefind indexed 44 HTML files, and
  starlight-links-validator printed "All internal links are valid." (including the new
  `/reference/api/tenants/` link).
- `pine doctor` → pass, "✓ config.json and board.json are valid" / "✓ no problems found" (exit 0),
  run after the ticket-file edits.
- `make coordinates-check` → pass, "every published coordinate names ghcr.io/kilaslab/kilasflow's
  owner".
- `go test -count=1 ./internal/guardrails/...` → pass,
  `ok github.com/kilaslab/kilas-flow/internal/guardrails 0.636s`.
- `grep -rn "Nothing creates a tenant\|No operation exists for it" docs/src/content/docs/` → no
  match (exit 1), so the old claim is gone repo-wide in docs/.

No Go, web, SDK, config or migration file changed, so `gofmt -l` / `go vet ./...` / `go build
./...` / the web `pnpm` gates / the SDK generator targets / the SQL-dialect gates do not apply.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `180b4f52` — BUG-vzzkg3: the board and the docs tell the truth about what shipped
  - `f8156140` — chore(pine): record the ticket board — the new tickets, memory entries and their notes
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/ci.yml                           |   25 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/MEMORY.md                                    |    1 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-fng4m2.md                        |   88 ++
 .pine/tickets/BUG-fvdz46.md                        |  400 +++++++
 .pine/tickets/BUG-p3t7yq.md                        |   30 +
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++++++++
 .pine/tickets/BUG-vzzkg3.md                        |  277 +++++
 .pine/tickets/BUG-w8h3km.md                        |  123 ++
 .pine/tickets/BUG-xmr673.md                        |  737 ++++++++++++
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-3taswf.md                       | 1199 +++++++++++++++-----
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 ++
 .pine/tickets/FEAT-77rveq.md                       |   73 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-8mymac.md                       |    4 +-
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 +++++++
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  513 ++++++++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  936 +++++++++++++++
 .pine/tickets/FEAT-g07pj8.md                       |   67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |  750 ++++++++++++
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++++
 .pine/tickets/FEAT-p77zr3.md                       |   67 ++
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++++++++++++++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |   98 +-
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   53 +
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +++
 cmd/kilasflow/fleet.go                             |   92 ++
 cmd/kilasflow/fleet_test.go                        |  395 +++++++
 cmd/kilasflow/idempotency_test.go                  |  225 ++++
 cmd/kilasflow/main.go                              |  198 +++-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +++
 config.example.yaml                                |   67 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   40 +
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |  107 +-
 docs/src/content/docs/guides/community-nodes.md    |  131 ++-
 docs/src/content/docs/guides/embedding.md          |   32 +-
 docs/src/content/docs/guides/idempotency.md        |  197 ++++
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +++
 .../docs/operate/configuration-reference.md        |  111 +-
 docs/src/content/docs/operate/deployment.md        |   10 +-
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 ++++
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   34 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/datastores.md  |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   24 +-
 docs/src/content/docs/reference/cli.md             |  628 ++++++++++
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 +++++++++
 e2e/fixtures/live-backend.ts                       |   12 +-
 e2e/helpers/stub.ts                                |    8 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 +++
 e2e/tests/live-backend-api.spec.ts                 |   52 +-
 e2e/tests/live-backend-http-auth.spec.ts           |   94 ++
 e2e/tests/live-backend-webhook.spec.ts             |  117 +-
 go.mod                                             |    2 +
 go.sum                                             |    4 +
 internal/api/cors_test.go                          |    4 +-
 internal/api/embed_defaults_test.go                |  150 +++
 internal/api/handlers/admin.go                     |  154 +++
 internal/api/handlers/admin_admin_test.go          |  263 ++++-
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  183 ++-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/interop.go                   |    6 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 ++-
 internal/api/handlers/workflows.go                 |  166 ++-
 internal/api/idempotency_test.go                   | 1004 ++++++++++++++++
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |    3 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/node_visibility_test.go               |  544 +++++++++
 internal/api/ready_fleet_test.go                   |  358 ++++++
 internal/api/routes.go                             |   16 +-
 internal/api/server.go                             |   13 +-
 internal/api/tenant_delete_test.go                 |  386 +++++++
 internal/api/workflows_test.go                     |   71 +-
 internal/binary/binary.go                          |  117 ++
 internal/binary/binary_test.go                     |  217 ++++
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 +++++++
 internal/cli/cli_test.go                           |  248 ++++
 internal/cli/client.go                             |  397 +++++++
 internal/cli/client_test.go                        |  320 ++++++
 internal/cli/command.go                            |  139 +++
 internal/cli/command_test.go                       |  302 +++++
 internal/cli/config.go                             |  258 +++++
 internal/cli/config_test.go                        |  749 ++++++++++++
 internal/cli/context.go                            |  318 ++++++
 internal/cli/context_test.go                       |  236 ++++
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 ++
 internal/cli/exit_test.go                          |  101 ++
 internal/cli/flags.go                              |   84 ++
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 ++++
 internal/cli/openapi.go                            |  202 ++++
 internal/cli/openapi_contract_test.go              |  411 +++++++
 internal/cli/output.go                             |  116 ++
 internal/cli/output_test.go                        |  246 ++++
 internal/cli/sse.go                                |  151 +++
 internal/cli/sse_test.go                           |  149 +++
 internal/cli/verbs_api.go                          |  315 +++++
 internal/cli/verbs_api_test.go                     |  820 +++++++++++++
 internal/cli/verbs_auth.go                         |  289 +++++
 internal/cli/verbs_credential.go                   |  146 +++
 internal/cli/verbs_credential_test.go              |  119 ++
 internal/cli/verbs_datastore.go                    |  209 ++++
 internal/cli/verbs_datastore_test.go               |  215 ++++
 internal/cli/verbs_exec.go                         |  413 +++++++
 internal/cli/verbs_exec_test.go                    |  436 +++++++
 internal/cli/verbs_node.go                         |  292 +++++
 internal/cli/verbs_node_test.go                    |  196 ++++
 internal/cli/verbs_pack.go                         |  143 +++
 internal/cli/verbs_pack_test.go                    |  195 ++++
 internal/cli/verbs_run.go                          |  257 +++++
 internal/cli/verbs_run_test.go                     |  314 +++++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +++++
 internal/cli/verbs_system_test.go                  |  182 +++
 internal/cli/verbs_tenant.go                       |   86 ++
 internal/cli/verbs_tenant_test.go                  |  116 ++
 internal/cli/verbs_workflow.go                     |  592 ++++++++++
 internal/cli/verbs_workflow_test.go                |  425 +++++++
 internal/config/config.go                          |  160 ++-
 internal/config/config_test.go                     |   82 ++
 internal/config/embed_branding_test.go             |  192 ++++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 ++
 internal/config/packs_visibility_test.go           |  168 +++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   68 ++
 internal/credentials/credentials_test.go           |   58 +
 internal/credentials/registry.go                   |   63 +
 internal/database/migrate_test.go                  |   17 +
 internal/database/tenant_columns_test.go           |  309 +++++
 internal/database/webhook_route_backfill_test.go   |  491 ++++++++
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +++
 internal/datastore/concurrency.go                  |    5 +-
 internal/datastore/doc.go                          |    4 +-
 internal/datastore/engine.go                       |   27 +-
 internal/datastore/engine_test.go                  |   53 +-
 internal/datastore/fleet.go                        |  364 +++++-
 internal/datastore/fleet_engine_test.go            |  711 ++++++++++++
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  243 +++-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |   25 +-
 internal/embed/embed.go                            |  121 +-
 internal/embed/embed_branding_test.go              |  164 +++
 internal/embed/embed_lifetime_test.go              |  146 +++
 internal/engine/export_test.go                     |   20 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 +++++++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +++-
 internal/guardrails/compile_scope_test.go          |  440 +++++++
 internal/idempotency/hash.go                       |   64 ++
 internal/idempotency/hash_test.go                  |  142 +++
 internal/idempotency/idempotency.go                |  432 +++++++
 internal/idempotency/idempotency_test.go           | 1120 ++++++++++++++++++
 internal/idempotency/sweeper.go                    |   94 ++
 internal/idempotency/sweeper_test.go               |  146 +++
 internal/interop/n8n/parameters.go                 |    3 +-
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 ++
 internal/node/visibility.go                        |  347 ++++++
 internal/node/visibility_test.go                   |  796 +++++++++++++
 internal/nodepack/nodepack.go                      |   18 +
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   13 +-
 internal/nodepack/visibility_test.go               |  262 +++++
 internal/repository/idempotency.go                 |  360 ++++++
 internal/repository/idempotency_test.go            |  615 ++++++++++
 internal/repository/models.go                      |   23 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +++++
 internal/repository/tenant_rows_test.go            |  420 +++++++
 internal/repository/webhooks.go                    |  124 +-
 internal/repository/webhooks_delivery_test.go      |  147 +++
 internal/repository/webhooks_test.go               |  184 ++-
 internal/repository/workflows.go                   |   22 +-
 internal/tenantpurge/completeness_test.go          |  368 ++++++
 internal/tenantpurge/doc.go                        |  120 ++
 internal/tenantpurge/docs_test.go                  |  115 ++
 internal/tenantpurge/harness_test.go               |  614 ++++++++++
 internal/tenantpurge/purge.go                      |  412 +++++++
 internal/tenantpurge/purge_test.go                 |  507 +++++++++
 internal/webhook/jwt.go                            |  144 +++
 internal/webhook/jwt_test.go                       |  212 ++++
 internal/webhook/require_auth.go                   |   74 ++
 internal/webhook/require_auth_test.go              |  367 ++++++
 internal/webhook/route_label_test.go               |  172 +++
 internal/webhook/shape.go                          |   27 +-
 internal/webhook/shape_test.go                     |   30 +
 internal/webhook/webhook.go                        |   87 +-
 internal/webhook/webhook_test.go                   |  194 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +++++
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/core.go                                      |    5 +
 nodes/error_workflow.go                            |    4 +
 nodes/http.go                                      |    2 +
 nodes/presentation_test.go                         |   35 +
 nodes/webhook.go                                   |   18 +-
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   19 +-
 scripts/smoke-cli.sh                               |  228 ++++
 sdk/CHANGELOG.md                                   |   23 +-
 sdk/LICENSE                                        |  202 ++++
 sdk/README.md                                      |   94 +-
 sdk/RELEASING.md                                   |  188 +++
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++++++
 sdk/scripts/check-package.mjs                      |  315 +++++
 sdk/scripts/lib/pack.mjs                           |   77 ++
 sdk/scripts/lib/release.mjs                        |  266 +++++
 sdk/scripts/release.mjs                            |  149 +++
 sdk/src/generated/models.ts                        |  197 +++-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  146 ++-
 sdk/test/operation-coverage.test.mjs               |   23 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 ++++
 sdk/test/release.test.mjs                          |  390 +++++++
 sdk/test/server.test.ts                            |  108 ++
 web/src/lib/api/generated/admin/admin.ts           |   94 ++
 .../api/generated/datastore-rows/datastore-rows.ts |    4 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../generated/models/executionNodeRunResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |    6 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   97 ++
 web/src/lib/dashboard/cursor-page.test.ts          |  285 ++++-
 web/src/lib/dashboard/cursor-page.ts               |   92 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 ++-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   21 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |    9 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  143 ++-
 307 files changed, 46949 insertions(+), 1111 deletions(-)
```
