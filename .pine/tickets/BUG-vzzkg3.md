---
id: BUG-vzzkg3
title: Board and docs claim three capabilities the code does not have
status: todo
priority: high
labels:
    - docs
    - platform
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T05:26:31Z"
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
