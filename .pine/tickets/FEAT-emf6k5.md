---
id: FEAT-emf6k5
title: Per-tenant node visibility so a host can ship its own nodes to one customer
status: done
priority: medium
labels:
    - registry
    - packs
    - platform
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T11:16:11Z"
---

## Problem

A SaaS host that ships its own nodes (its CRM nodes, its WhatsApp nodes) publishes them to
every tenant of the deployment. There is no per-tenant dimension in the node catalogue, so
"custom node milik MyCRM" is not expressible.

## Evidence

- The registry key is `{nodeType, version}` with no tenant dimension
  (`internal/node/registry.go:238`); `RegisterFrom` takes a source but no scope.
- Directory packs are installed at composition, before the registry is shared:
  `internal/nodepack/loaddir.go` (`LoadDir`) and `DirDeps` carry no tenant.
- The catalogue endpoint returns everything: `internal/api/handlers/nodes.go:127` (`List`).
- The only availability concept is deployment-wide: `api.Deps.NodeAvailability` returns a map
  of node type → reason (`internal/api/server.go:108-111`), stamped per deployment.
- Embed sessions can read the whole catalogue (`/node-types` is allowed on `workflow:read` in
  `internal/api/middleware/embed.go`), so one host's packs are visible to every tenant's
  editor.

## Acceptance criteria

- [x] A pack can declare the tenants it is visible to (or an operator can scope an installed
      pack to a set of tenants), and a node outside that set is invisible in `GET /node-types`
      for that tenant.
- [x] The compiler refuses a document that references a node type invisible to the tenant
      being compiled, with a diagnostic that distinguishes "unknown type" from "not available
      to you".
- [x] Execution refuses a run whose document references an invisible type, so a workflow
      copied between tenants cannot smuggle one in.
- [x] An unscoped pack keeps today's behaviour (visible everywhere), so nothing existing
      changes.
- [x] Tests cover: two tenants, one scoped pack, catalogue and run behaviour for each.

## Out of scope

Runtime install/hot-reload of packs, and per-tenant pack upload over HTTP. Both are separate
decisions.

## Implementation notes

### Stage 1 — the visibility model (registry, compiler, pack manifest, config, boot)

This stage builds the model and the declaration surfaces. It adds no HTTP behaviour and no
engine enforcement: a Stage-1 deployment accepts and logs scopes that only Stage 2 enforces,
so this commit must not be released on its own. No migration (000018 stays unused), no new
dependency (stdlib only), no SQL touched.

The design the later stages build on, fixed here:

- **The unit is the node TYPE, not `{type, version}`.** Every registered version of a type
  shares one scope, because a per-version scope would let a workflow written against version 1
  of a scoped type resolve downward to a version the tenant was never given. Registering a
  second version with a different scope is refused, naming the type and both versions (the
  candidate versions are iterated in ascending order, so the message is identical on every
  boot).
- **Declaration** is a pack manifest field `visibleTo`, an array of tenant IDs, surfaced as
  `node.Definition.VisibleTo []string` tagged `json:"-"`, so the catalogue never discloses which
  tenants a node is reserved for. Absent means unscoped; a present but empty list is refused at
  both the loader and the validator, because it would read either as "nobody" or as
  "everybody".
- **Operator override** is the config key `packs.visible_to` (env `KILASFLOW_PACKS_VISIBLE_TO`,
  comma-separated), entries written `"<node type>=<tenant id>"`. A list of strings rather than
  a YAML map, because koanf's "." key delimiter would split a `pack.telegram` map key. An entry
  REPLACES the manifest's set for that type, so an operator can widen or narrow; there is no
  wildcard. An override that names an unregistered or built-in type refuses the boot (fail
  closed), and so does a malformed entry, reported as `packs.visible_to[N]`.
- **An unscoped registry pays nothing.** `Registry.scopes` is created on the first write, so the
  hot path adds one `len(scopes)==0` check and, for a registry that scopes something, one map
  lookup by type. `Resolve` and the definitions map are untouched.
- **Built-ins can never be scoped**, because the engine names some of them (the sub-workflow
  trigger, the error trigger). The door is closed at both entrances: `Register` refuses a
  built-in carrying `VisibleTo`, and `ScopeTo`/`ApplyVisibility` refuses a type registered with
  `SourceBuiltin` or in the `kilasflow.*` namespace.
- **The tenant grammar is looser than the admin create-tenant pattern** on purpose
  (`node.CheckTenantID`: non-empty, ≤128 bytes, valid UTF-8, no whitespace or control
  characters, none of the `,` and `=` the config grammar reserves), because
  `auth.bootstrap_tenant` is not validated by that pattern.
- **The override must be configured identically on every process role.** Composition runs before
  the role is chosen, so a worker gets the same scopes as the API; a worker started without the
  key is the fail-open case, and the config comment and the generated reference say so.

What changed:

- `internal/node/visibility.go` (new): `CheckTenantID`, `normaliseTenants`, the scope index
  (`VisibleTo`, `Scoped`, `Scopes`, `indexScope`, `checkScopeAgrees`), the tenant-aware reads
  (`ListFor`, `ResolveFor`, `ForTenant`), the operator writes (`ScopeTo`, `ApplyVisibility`,
  all-or-nothing: every entry is validated before any is applied) and `TenantView`
  (`Lookup`, `HasType`, `Restricted`). `TenantView` deliberately does not implement
  `workflow.TenantScoper`, so a narrowed view can never be re-widened.
- `internal/node/registry.go`: `Definition.VisibleTo` (tagged `json:"-"`), the lazily created
  `scopes` index, normalisation and the built-in refusal in `register()`, the one-scope-per-type
  check after the duplicate-key check, and the `VisibleTo` copy in `cloneDefinition`.
- `internal/workflow/catalog_scope.go` (new): `RestrictedCatalog`, `TenantScoper` and
  `CatalogFor`, the one place "which nodes may this tenant use" is decided.
- `internal/workflow/compiler.go`: `ErrorNodeNotAvailable` (`node.not_available`), and the
  not-found branch asks `RestrictedCatalog` FIRST, so a withheld type no longer reports as
  simply unregistered. The message names no other tenant. `WorkflowValidationIssue.Code` is a
  plain string in OpenAPI, so no schema change follows.
- `internal/nodepack/nodepack.go`: `Pack.VisibleTo` (`json:"visibleTo,omitempty"`), the
  empty-list refusal and the (copied) `Definition.VisibleTo` in `Load`, placed before the
  trigger/action split so trigger packs are covered too.
- `internal/nodepack/validate.go`: `visibleTo` joins `packKeys`, plus per-entry issues at
  `$.visibleTo[i]` (through `node.CheckTenantID`) and `$.visibleTo` for the empty list.
- `internal/config/packs_visibility.go` (new): `Packs.VisibilityGrants()` (splits on the first
  `=`, trims, groups by type) and `validateVisibleTo`; `internal/config/config.go` gets the field
  and one `Validate()` line. The default stays nil, so the generated reference renders `[]`.
- `cmd/kilasflow/main.go`: one append-style block after the `LoadDir` block — `VisibilityGrants`,
  `ApplyVisibility` (failure names the key) and one boot log line per scoped type listing its
  tenants, in sorted type order, so a typo is visible to an operator. The comment states the
  block must stay after the last node registration.

Verification (all from the worktree `wf_d1e04be5-af7-31`, all green):

- `gofmt -l` on the changed Go files: prints nothing.
- `go vet ./...`: clean. `go build ./...`: clean.
- `go test -race -count=1 ./internal/node/... ./internal/workflow/... ./internal/nodepack/...
  ./internal/config/... ./packs/... ./cmd/... ./scripts/... ./internal/guardrails/...`: all ok.
  The new tests were written first and watched fail for the right reason (missing
  `Pack.VisibleTo`, missing `Packs.VisibleTo`/`VisibilityGrants`), then pass. No existing test
  was weakened, skipped or deleted.
- `make generate-config-reference && make generate-config-reference-check`: the check is clean;
  `config.example.yaml` and the configuration reference carry `packs.visible_to`.
- Boot, refusing (built binary on a free port, scratch sqlite DSN, no handlers involved):
  `packs.visible_to: ["pack.nope=acme"]` exits 1 with
  `apply packs.visible_to: node type "pack.nope" is not registered, so it cannot be scoped to
  tenants`; `["kilasflow.set=acme"]` exits 1 naming the built-in; `["pack.telegram=a b"]` exits 1
  with `tenant ID "a b" must not contain whitespace or control characters`. Boot accepting:
  `["pack.telegram=acme", "pack.waha=acme", "pack.wahaTrigger=acme"]` boots and logs one
  `node type is scoped to tenants` line per type with `tenants=[acme]`.
- `BenchmarkRegistryLookup` (`internal/node/registry_bench_test.go`, new) with
  `-benchmem -count=10` and again with `-count=15 -cpu=1`: allocs/op and B/op are EQUAL within
  each raw/tenant pair — builtin 37 allocs/8880 B, unscoped 7 allocs/832 B, scoped-visible
  8 allocs/848 B — and a hidden lookup is 0 allocs/0 B, because the view refuses before it
  resolves and clones anything. Raw before/after medians are context only: raw/pack 5762 ns
  before, and the tenant view's ns/op is NOT measurable on this machine, where about a dozen
  worktrees build at once (two runs of the same tree gave +12.8% and −39.2% for the same pair).
  The load-independent signal is the allocs/B equality.

Left for the later stages (not claimed here): the four `/node-types` operations and the icon
route, the engine compile sites and the worker gate, the rule-based guardrail test over every
`workflow.Compile` site, the docs page and the fixes to the three copies of "the catalogue
carries no tenant data". Acceptance criterion 2 is proven at the compiler level (the diagnostic
is asserted) but no production compile site passes a tenant-scoped catalogue yet, so it is not
ticked. Criterion 4 is ticked: unscoped is bit-for-bit today's behaviour, asserted by
`TestAnUnscopedNodeIsVisibleToEveryTenant`, the nil/empty normalisation, the empty
`packs.visible_to` default, the unchanged existing suites and a boot with no such key.

### Stage 2 — enforcement at every entry point (catalogue, compiler, worker, import)

This stage makes the model true everywhere a caller can list nodes or start a run. No
migration, no new dependency (stdlib only), no SQL touched.

The shape it settles:

- **One engine compile site.** `Service.compile(record, document)` is
  `workflow.Compile(document, workflow.CatalogFor(service.catalog, record.TenantID))`. It replaces
  the two engine sites (`runWithStack`, `resumeRun`). `runOnce`, `resume`, the sub-workflow call
  and the error-workflow call all end in one of those two, so the worker is the authoritative gate
  for manual, webhook, schedule, sub-workflow, error-workflow and resumed runs: a record that
  reached the queue without a scoped compile fails at claim with the not-available message and
  zero node runs.
- **The catalogue narrows itself.** `List` serves `ListFor(caller)` with
  `Cache-Control: private, no-cache`; `Icon` and `declaredProperty` resolve through
  `ResolveFor(caller, ...)`, so `load-options` and `load-schema` answer 404 exactly as they do for
  an unregistered type — the 404 text is unchanged, which is what keeps a hidden type from being
  an existence oracle. A scoped type's icon is served `private, max-age=86400, immutable`. An
  embed session needs no middleware change: `PrincipalTenants` already resolves it to its tenant.
- **A refused node no longer blames its own wires.** `internal/workflow/compiler.go` records the
  nodes whose type the catalogue refused and skips their connections in the topology pass. Before
  this, the connection issue sorted first, so the single sentence a worker records read
  "connection must reference registered source and target nodes" — a wiring mistake that does not
  exist, because the endpoints are registered and it is the tenant that may not use one.
- **A rule-based guardrail, not a site list.** `internal/guardrails/compile_scope_test.go` walks
  the repository (skipping tests, generated trees and vendored code), resolves the file's local
  name for the workflow import so an alias cannot evade it, and requires: every `workflow.Compile`
  to take `workflow.CatalogFor(...)` (or, inside `internal/repository`, a `workflow.Catalog`
  parameter of the enclosing function, whose callers the next rule then holds); the set of
  repository declarations taking a catalogue to equal `{Activate, PublishVersion, publish,
  QueueManualLatest}`; every non-test call to a pinned name to pass `workflow.CatalogFor(...)` at
  the argument index derived from the pinned declaration. A correctly scoped new call site needs
  no edit here, which is what FEAT-ew46cb (validate, run --revision, exec retry) will rely on.
- **Import and export take the tenant's view**, so an imported document cannot preserve a type the
  tenant cannot see as a resolved node.
- **The stale claim is fixed where this commit falsifies it**: the comment in
  `internal/api/middleware/embed.go`, the string in `scripts/generate-api-reference.mjs` and the
  row in `docs/src/content/docs/reference/api-contract.md` no longer say the catalogue carries no
  tenant data.

What changed:

- `internal/engine/service.go` (the `compile` helper, `runWithStack`), `internal/engine/wait_service.go`
  (`resumeRun`).
- `internal/api/handlers/nodes.go` (per-tenant `List`/`Icon`/`declaredProperty`, the response
  header, four operation descriptions), `internal/api/handlers/workflows.go` (PublishVersion,
  Activate, Run), `internal/api/handlers/interop.go` (Import, Export),
  `internal/api/middleware/embed.go` (comment only).
- `internal/workflow/compiler.go` (the refused-node set and the connection skip).
- New tests: `internal/engine/tenant_visibility_test.go` (five tests: manual/activation,
  worker-past-the-API, webhook and schedule, sub-workflow, resumed run),
  `internal/api/node_visibility_test.go` (seven tests over two real tenants with real API keys and
  embed tokens), `internal/guardrails/compile_scope_test.go`, plus
  `TestARefusedNodeTypeDoesNotBlameItsConnections` in `internal/workflow/compiler_visibility_test.go`.
- Regenerated (never hand-edited): `web/src/lib/api/generated/nodes/nodes.ts`,
  `sdk/src/generated/models.ts`, `docs/src/content/docs/reference/api/nodes.md`. The diff is the
  four operation descriptions and the embed line; no schema changed, so the generated types differ
  only in doc comments.

Verification (all from the worktree `wf_d1e04be5-af7-31`):

- TDD: every new test was written first and watched fail for the right reason — the five engine
  tests failed with `status = "succeeded", want failed` (the engine still compiled with the raw
  registry); the API tests failed with the scoped node listed for globex, the icon served 200 to
  globex, no `Cache-Control` on the catalogue, and activation/run answering 200/202 instead of 422;
  `TestARefusedNodeTypeDoesNotBlameItsConnections` failed with the connection message as the first
  issue. No existing test was weakened, skipped or deleted.
- Guardrail probes (each reverted): reverting the engine compile to `service.catalog`, handing
  `handler.catalog` to `QueueManualLatest`, adding a repository function that takes a catalogue,
  and adding an *aliased* import of the workflow package with an unscoped compile — all four fail
  the guardrail with the addressed message.
- `gofmt -l` on the changed and new Go files: prints nothing. `go vet ./...`: clean.
  `go build ./...`: clean.
- `go test -count=1 ./...` (full, non-race): passes.
- `go test -race -count=1 ./internal/node/... ./internal/workflow/... ./internal/nodepack/...
  ./internal/config/... ./internal/engine/... ./internal/api/... ./internal/repository/...
  ./internal/interop/... ./internal/webhook/... ./packs/... ./cmd/... ./internal/guardrails/...
  ./scripts/...`: every package passes except `internal/api/handlers`, which fails
  `TestLoginRefusesASprayFromOneAddress` ("no refusal within 30 attempts"). That is a pre-existing,
  load-induced flake in an auth test this ticket does not touch: the same test fails identically on
  a detached checkout of stage 1's commit `0b8cadd` (base worktree, same command, 141s to the same
  message), and the test's own comment says a slow machine spends long enough per attempt for the
  bucket to refill. The two `50 ms`-deadline wait tests
  (`TestResumeOfAPerItemSuspendProcessesEveryItem`, `TestExpiredWaitsResolveOnTheirOwnDeadline`)
  flake the same way: they fail intermittently at `0b8cadd` too (5 of 8 runs, same
  "the deadline is in the past" message) and pass in isolation on this tree. Both are reported, not
  hidden, and neither was weakened.
- PostgreSQL regression (scratch `pgvector/pgvector:pg17`, unique name, `-p 0:5432`, removed after):
  `KILASFLOW_TEST_POSTGRES_DSN=... go test -race -p 1 ./internal/engine/... ./internal/repository/...`
  — `internal/repository` passes (88s); `internal/engine` reports only the same 50 ms-deadline
  flake above, and the PG-gated engine tests (multiprocess, scheduler, cancel, drain, suspend/resume,
  per-item and approval resume, all the `...OnPostgres` cases) pass with it excluded. The new tests
  are dialect-neutral and need no PostgreSQL variant of their own.
- `BenchmarkRegistryLookup -benchmem -count=10`: allocs/op are EQUAL within each raw/tenant pair
  (builtin 37, unscoped 7, scoped-visible 8) and a hidden lookup is 0 allocs, matching stage 1's
  numbers; ns/op is not measurable on this machine (about a dozen worktrees building at once).
- Composition smoke, real binary on a free port with a scratch sqlite DSN (auth off, so the caller
  is tenant `default`): `packs.visible_to: ["pack.telegram=acme"]` boots and `GET /api/v1/node-types`
  lists `pack.waha` and `pack.wahaTrigger` but not `pack.telegram`; switching the entry to
  `pack.telegram=default` and restarting brings it back. The icon route is deliberately not
  asserted there — `pack.telegram`'s icon is `builtin:send`, so it 404s with or without scoping;
  icon behaviour is proven by the API test, which registers a served `IconLight` asset.
- `make generate-api generate-types generate-api-reference`, then `make generate-api-check
  generate-types-check generate-api-reference-check sdk-check sdk-test sdk-version-check`: clean
  (SDK 81 tests pass). `cd web && pnpm check` (1517 files, 0 errors) and `pnpm test` (514 tests).
  Fresh-worktree installs ran first for web/, sdk/ and docs/.

Left for stage 3 (not claimed here): the new guide, the docs pages, the CHANGELOG entries and the
two stale "no per-tenant configuration" sentences in
`docs/src/content/docs/operate/security.md:139` and
`docs/src/content/docs/start/what-kilasflow-is.md:88`. Neither page is one of the two that
BUG-vzzkg3 owns (`guides/community-nodes.md`, `concepts/tenancy-and-embedding.md`), so this ticket
may edit them; the one vzzkg3-owned sentence this change falsifies sits in
`concepts/tenancy-and-embedding.md` and is recorded in stage 3's note instead of edited.

### Stage 3 — Documentation, remaining stale-claim fixes, changelog and ticket write-back

This stage adds no code: it documents the behaviour stages 1-2 built, corrects the two remaining
"no per-tenant configuration" sentences the change falsified — both in pages this ticket owns
(`operate/security.md:139`, `start/what-kilasflow-is.md:88`), not in the two BUG-vzzkg3-owned pages
— records the change in the changelog, and closes the ticket file. The third falsified sentence
lies in a page RULES.md reserves for BUG-vzzkg3, so it is named below rather than edited.

What changed, by file:

- `docs/src/content/docs/guides/tenant-scoped-nodes.md` (new): what the scope solves; the
  `visibleTo` manifest fragment (absent = everyone, empty refused, regenerate `pack.sha256`); the
  `packs.visible_to` / `KILASFLOW_PACKS_VISIBLE_TO` override (`type=tenant`, one entry per node
  type so WAHA needs `pack.waha` and `pack.wahaTrigger`, replace-not-merge, no wildcard, identical
  on every process role because the worker is the gate); where tenant IDs come from
  (`POST /api/v1/tenants`, `default` when auth is off); what a scoped-away tenant sees (catalogue,
  icon and loaders answer as for an unregistered type, embed sessions included); the
  `node.unknown_type` / `node.unknown_version` / `node.not_available` table; narrowing a scope
  later (bindings survive, deliveries 202 then fail at claim, waiting runs fail on resume);
  boot-time refusals; what this is not; and a curl verification recipe.
- `docs/src/content/docs/reference/node-packs.md`: a `visibleTo` row after `credentialType`, a
  "Per-tenant visibility" paragraph under "Install and distribution", and the verbatim
  unknown-field diagnostic corrected to the new `packKeys` order.
- `docs/src/content/docs/concepts/node-registry.md`: a "Visibility per tenant" section before
  "Boot-time checks" (per-type scope, `ForTenant`, `node.not_available`, composition-time-only,
  one map lookup on the hot path; `ApplyVisibility` is named as the one `main.go` call site and
  `ScopeTo` as its single-type convenience wrapper, whose only consumers today are tests), a
  `packs.visible_to` refusal bullet in "Boot-time checks", and `internal/node/visibility.go` plus
  `internal/workflow/catalog_scope.go` added to "Source".
- `docs/src/content/docs/concepts/execution-model.md`: the `node.not_available` row after
  `node.unknown_version`.
- `docs/src/content/docs/guides/node-authoring.md`: one sentence pointing at the new guide.
- `docs/src/content/docs/operate/security.md` and `docs/src/content/docs/start/what-kilasflow-is.md`:
  the "no per-tenant configuration" sentence in each now reads "no per-tenant configuration beyond
  which node types it may see (set in the operator's configuration, not stored on the tenant)".
- `docs/src/content/docs/concepts/tenancy-and-embedding.md:267-268` (**BUG-vzzkg3-owned; NOT edited
  here**, per RULES.md): "There is no per-tenant configuration and no per-tenant quota." is
  falsified once this feature lands. Intended wording, matching the two sentences fixed above:
  "no per-tenant configuration beyond which node types it may see (set in the operator's
  configuration, not stored on the tenant) and no per-tenant quota". Recorded here because RULES.md
  forbids this ticket from editing that page and requires the sentence to be listed instead;
  verified still present in the tree at HEAD.
- `CHANGELOG.md`: one `Added` bullet (the field, the key and the diagnostic) and one `Changed`
  bullet (the narrowed operations and `Cache-Control: private, no-cache`).
- `.pine/tickets/FEAT-emf6k5.md`: this write-back.

Design decisions (recap of Stage 1, unchanged by this stage): the unit is the node TYPE, not
`{type, version}`; the declaration is manifest `visibleTo` surfaced as `Definition.VisibleTo`
tagged `json:"-"`; the override is `packs.visible_to` as a list of `type=tenant` strings with
replace semantics and no wildcard; an unscoped registry pays nothing (`len(scopes)==0`); built-ins
can never be scoped; the tenant grammar is looser than the admin pattern because
`auth.bootstrap_tenant` is not validated by it; and the override must be identical on every process
role.

Benchmark (unchanged by this stage, re-stated for the record): `BenchmarkRegistryLookup
-benchmem -count=10` gives equal allocs/op/B within each raw/tenant pair (builtin 37 allocs/8880 B,
unscoped 7/832, scoped-visible 8/848; a hidden lookup is 0 allocs/0 B because the view refuses
before it resolves). Raw before/after ns/op medians are context only and are NOT evidence on this
machine — about a dozen worktrees build at once, and two runs of the same tree differed by +12.8%
and −39.2% for the same pair. The load-independent signal is the allocs/B equality.

Gate commands run and outcomes (stages 1-2, recorded at the time): `gofmt -l` on changed Go files
(clean); `go vet ./...` and `go build ./...` (clean); `go test -count=1 ./...` (full non-race pass);
`go test -race -count=1` across the touched packages plus `./internal/guardrails/... ./scripts/...`
(all pass except the two pre-existing load-induced flakes, proven on stage 1's commit `0b8cadd`:
`internal/api/handlers` login-spray and the `internal/engine` 50 ms-deadline wait tests;
`internal/repository` clean); PostgreSQL regression on a scratch `pgvector/pgvector:pg17`
(`-p 0:5432`, host port **32788**, container `kf-pg-emf6k5`, removed after) — `internal/repository`
passes, `internal/engine` reports only the same 50 ms-deadline flake (PG-gated engine tests pass
with it excluded); `make generate-config-reference` and `generate-config-reference-check` (clean);
`make generate-api generate-types generate-api-reference` then their `-check` variants plus
`sdk-check sdk-test sdk-version-check` (clean, SDK 81 tests); `cd web && pnpm check` (1517 files, 0
errors) and `pnpm test` (514 tests); and two boot smokes of the real binary with a scratch sqlite
DSN and a free port — the stage 1 refusals (`pack.nope=acme`, `kilasflow.set=acme`, `pack.telegram=a b`)
and the stage 2 hide-and-restore of `pack.telegram` (catalogue only; the port was chosen at runtime
and not retained). Stage 3 re-ran the catalogue smoke to pin a port: on **127.0.0.1:61414**,
`packs.visible_to: ['pack.telegram=acme']` hid `pack.telegram` from the `default` caller while
`pack.waha`/`pack.wahaTrigger` stayed, the response carried `Cache-Control: private, no-cache`, no
`visibleTo` leaked, and the boot log printed `msg="node type is scoped to tenants"
type=pack.telegram tenants=[acme]`; switching the entry to `pack.telegram=default` and restarting
brought it back.

Gate commands run for stage 3 (the only files this stage touches are docs, the changelog and this
ticket, so the Go gates do not apply): `make coordinates-check` (clean); `make docs-build` with
`docs/` dependencies installed (43 pages built, starlight-links-validator: "All internal links are
valid"); `make generate-api-reference-check` (14 pages fresh, 74 operations) and
`make generate-config-reference-check` (clean). Because no Go file changed, the Go, web and SDK
gates were not re-run in this stage; their last recorded outcomes are the stage 1-2 runs above.

Coverage notes the plan asked to record: there is no retry or replay entry point in this tree — the
executions handler exposes list, get, cancel and stream only — so a retry cannot be tested
separately; a retry must queue through `ClaimNext`, which is gated by `Service.compile`, and the
rule-based guardrail forces FEAT-ew46cb's exec retry, validate and `run --revision` through
`workflow.CatalogFor`. The error-workflow path shares `invokeWorkflow` → `runWithStack` with the
sub-workflow path and is therefore covered by that choke point plus the guardrail, not by a
dedicated test. No migration (000018 stays unused), no new dependency (stdlib only) and no
PostgreSQL-dialect-specific code were needed; the PostgreSQL run was regression insurance for the
claim path, not a dialect variant of the new tests.

Honest limits: drafts can hold an invisible type until they are compiled; credential types
(`GET /credential-types`) are deployment-wide and not scoped; a process started without
`packs.visible_to` fails open for that process, so every process role must be given the same value;
narrowing a scope does not re-evaluate existing bindings, so triggered and waiting runs fail at
claim; a typo in a tenant ID is not checked against the identity store (the boot log is the
mitigation); and `node.not_available` is the one deliberate existence disclosure, since the
catalogue deliberately answers as if the type were unregistered.

### Review round 1 — the two conventions findings (fix commit)

The three-lens review found nothing under correctness or acceptance (the correctness lens ran a
live composition proof: a scoped node hidden from the catalogue, activate/run refused with
`node.not_available`, icons and load-options 404, nothing queued). The conventions lens reported
two low accuracy defects in the committed record, both fixed in the commit that adds this note. No
Go file changed, so no behaviour changed.

- **Ticket accounting (this file).** Stage 2's hand-off and stage 3's intro both called the two
  stale "no per-tenant configuration" sentences "BUG-vzzkg3-owned", but they live in
  `docs/src/content/docs/operate/security.md` and `docs/src/content/docs/start/what-kilasflow-is.md`,
  and neither page is one of the two RULES.md reserves for BUG-vzzkg3
  (`guides/community-nodes.md`, `concepts/tenancy-and-embedding.md`); the sentence that really is in
  a vzzkg3-owned page and is falsified by this change —
  `docs/src/content/docs/concepts/tenancy-and-embedding.md:267-268`, "There is no per-tenant
  configuration and no per-tenant quota." — was named nowhere in the committed file. Both phrasings
  are corrected above and the sentence and its intended wording are now recorded (see the stage 3
  bullet list). The page itself was not touched: `git diff --name-only` for this commit lists only
  `.pine/tickets/FEAT-emf6k5.md` and `docs/src/content/docs/concepts/node-registry.md`, and
  `git grep` at HEAD still finds the old sentence at `tenancy-and-embedding.md:268`.
- **`ScopeTo` call sites.** `docs/src/content/docs/concepts/node-registry.md:315-321` claimed
  `ScopeTo` and `ApplyVisibility` are both "called once from `main.go`". The only production call
  site is `ApplyVisibility` (`cmd/kilasflow/main.go:313`); `grep -rn '\.ScopeTo(' --include=*.go .`
  finds callers only in `internal/node/visibility_test.go` (10 sites) and
  `internal/engine/tenant_visibility_test.go:343`. **Chose to reword, not to delete `ScopeTo`**: it
  is a three-line forwarder to `ApplyVisibility` (`internal/node/visibility.go:276-278`), so the
  validation, all-or-nothing application and built-in refusal run through exactly the same code;
  deleting it would rewrite 11 test call sites from `ScopeTo("pack.x", tenants)` into one-entry map
  literals without removing a distinct path, which is churn, not simplification. The sentence now
  names `ApplyVisibility` as the single `main.go` entry point and `ScopeTo` as the single-type
  convenience wrapper whose only consumers today are tests.

Gates re-run for this fix commit (docs page plus this ticket only; no Go, web, SDK or config file
changed): `gofmt -l` on changed Go files (none; also clean on `internal/node/visibility.go`),
`go vet ./...` (clean), `go build ./...` (clean), `go test -race -count=1 ./internal/node/...
./internal/workflow/... ./internal/guardrails/...` (all three packages ok), and `make docs-build`
(44 pages built, starlight-links-validator: "All internal links are valid"; stage 3's note recorded
43 for the same command, a difference this commit does not introduce, since it adds no page — the
only docs file it touches is `concepts/node-registry.md`).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `b3809c15` — FEAT-emf6k5: scope node types to tenants, with packs.visible_to and node.not_available
  - `b3c9f3f9` — chore(pine): start the board — the open tickets move to doing before the parallel wave
  - `f8156140` — chore(pine): record the ticket board — the new tickets, memory entries and their notes
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |   1 +
 .pine/memory/embedding.md                          |   8 +
 .pine/tickets/BUG-fng4m2.md                        |  88 +++
 .pine/tickets/BUG-fvdz46.md                        | 400 +++++++++++
 .pine/tickets/BUG-p3t7yq.md                        |  30 +
 .pine/tickets/BUG-t9j2ek.md                        | 452 ++++++++++++
 .pine/tickets/BUG-vzzkg3.md                        |  54 ++
 .pine/tickets/BUG-w8h3km.md                        | 123 ++++
 .pine/tickets/BUG-xmr673.md                        |  46 ++
 .pine/tickets/EPIC-bkj6yf.md                       |  46 ++
 .pine/tickets/EPIC-m0bne8.md                       |  57 ++
 .pine/tickets/EPIC-r0yg5q.md                       |  27 +
 .pine/tickets/FEAT-3taswf.md                       |  14 +-
 .pine/tickets/FEAT-48hreg.md                       |  20 +-
 .pine/tickets/FEAT-4jns31.md                       |  26 +
 .pine/tickets/FEAT-4ve1bq.md                       |  67 ++
 .pine/tickets/FEAT-77rveq.md                       |  73 ++
 .pine/tickets/FEAT-7cg0cd.md                       |  20 +-
 .pine/tickets/FEAT-8mymac.md                       |   4 +-
 .pine/tickets/FEAT-bb4s6e.md                       |  27 +
 .pine/tickets/FEAT-bp59m4.md                       |  32 +
 .pine/tickets/FEAT-c72set.md                       |  26 +
 .pine/tickets/FEAT-cwmw90.md                       | 513 ++++++++++++-
 .pine/tickets/FEAT-emf6k5.md                       | 423 +++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |  26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  57 ++
 .pine/tickets/FEAT-g07pj8.md                       |  67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |  52 ++
 .pine/tickets/FEAT-m4d2y1.md                       |  30 +
 .pine/tickets/FEAT-mha6a0.md                       | 259 +++++++
 .pine/tickets/FEAT-p77zr3.md                       |  67 ++
 .pine/tickets/FEAT-qdedm0.md                       |   4 +-
 .pine/tickets/FEAT-x5qqpm.md                       |  26 +
 .pine/tickets/FEAT-yxwyav.md                       |  26 +
 CHANGELOG.md                                       |  43 ++
 README.md                                          |   9 +-
 cmd/kilasflow/embed_issuer.go                      |  18 +
 cmd/kilasflow/embed_issuer_test.go                 | 134 ++++
 cmd/kilasflow/main.go                              |  77 +-
 cmd/kilasflow/webhook_wiring_test.go               | 138 ++++
 config.example.yaml                                |  53 +-
 docs/src/content/docs/concepts/credentials.md      |  21 +-
 docs/src/content/docs/concepts/execution-model.md  |   1 +
 docs/src/content/docs/concepts/node-registry.md    |  40 ++
 docs/src/content/docs/concepts/webhooks.md         | 107 ++-
 docs/src/content/docs/guides/embedding.md          |  32 +-
 docs/src/content/docs/guides/node-authoring.md     |   6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md | 183 +++++
 .../docs/operate/configuration-reference.md        |  85 ++-
 docs/src/content/docs/operate/security.md          |  23 +-
 docs/src/content/docs/operate/upgrades.md          |  10 +
 docs/src/content/docs/reference/api-contract.md    |   7 +-
 docs/src/content/docs/reference/api.md             |   4 +-
 docs/src/content/docs/reference/api/nodes.md       |  16 +-
 docs/src/content/docs/reference/api/workflows.md   |  21 +
 docs/src/content/docs/reference/node-packs.md      |  15 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  11 +-
 .../specs/2026-09-20-agent-surface-design.md       | 519 ++++++++++++++
 e2e/fixtures/live-backend.ts                       |  12 +-
 e2e/helpers/stub.ts                                |   8 +
 e2e/tests/live-backend-api.spec.ts                 |  52 +-
 e2e/tests/live-backend-http-auth.spec.ts           |  94 +++
 e2e/tests/live-backend-webhook.spec.ts             | 117 ++-
 go.mod                                             |   1 +
 go.sum                                             |   2 +
 internal/api/embed_defaults_test.go                | 150 ++++
 internal/api/handlers/auth_test.go                 |  63 +-
 internal/api/handlers/interop.go                   |   6 +-
 internal/api/handlers/nodes.go                     |  76 +-
 internal/api/handlers/workflows.go                 |  81 ++-
 internal/api/middleware/embed.go                   |   3 +-
 internal/api/middleware/loginlimit.go              |  13 +
 internal/api/middleware/loginlimit_test.go         |   3 +-
 internal/api/node_visibility_test.go               | 544 ++++++++++++++
 internal/api/workflows_test.go                     |  71 +-
 internal/config/config.go                          |  79 +-
 internal/config/embed_branding_test.go             | 192 +++++
 internal/config/embed_validate.go                  |  34 +
 internal/config/packs_visibility.go                |  83 +++
 internal/config/packs_visibility_test.go           | 168 +++++
 internal/config/webhook_require_auth_test.go       |  50 ++
 internal/credentials/builtin.go                    |  68 ++
 internal/credentials/credentials_test.go           |  58 ++
 internal/credentials/registry.go                   |  63 ++
 internal/database/webhook_route_backfill_test.go   | 491 +++++++++++++
 internal/embed/embed.go                            | 121 +++-
 internal/embed/embed_branding_test.go              | 164 +++++
 internal/embed/embed_lifetime_test.go              | 146 ++++
 internal/engine/export_test.go                     |  20 +
 internal/engine/service.go                         |  30 +-
 internal/engine/tenant_visibility_test.go          | 379 ++++++++++
 internal/engine/wait_service.go                    |  47 +-
 internal/engine/wait_service_test.go               | 219 +++++-
 internal/guardrails/compile_scope_test.go          | 440 ++++++++++++
 internal/interop/n8n/parameters.go                 |   3 +-
 internal/node/registry.go                          |  31 +
 internal/node/registry_bench_test.go               | 112 +++
 internal/node/visibility.go                        | 347 +++++++++
 internal/node/visibility_test.go                   | 796 +++++++++++++++++++++
 internal/nodepack/nodepack.go                      |  18 +
 internal/nodepack/trigger.go                       |   8 +
 internal/nodepack/trigger_require_auth_test.go     |  43 ++
 internal/nodepack/validate.go                      |  13 +-
 internal/nodepack/visibility_test.go               | 262 +++++++
 internal/repository/models.go                      |  10 +-
 internal/repository/webhooks.go                    |  79 +-
 internal/repository/webhooks_test.go               | 184 ++++-
 internal/webhook/jwt.go                            | 144 ++++
 internal/webhook/jwt_test.go                       | 212 ++++++
 internal/webhook/require_auth.go                   |  74 ++
 internal/webhook/require_auth_test.go              | 367 ++++++++++
 internal/webhook/route_label_test.go               | 172 +++++
 internal/webhook/shape.go                          |  27 +-
 internal/webhook/shape_test.go                     |  30 +
 internal/webhook/webhook.go                        |  83 ++-
 internal/webhook/webhook_test.go                   | 194 ++++-
 internal/workflow/catalog_scope.go                 |  39 +
 internal/workflow/compiler.go                      |  24 +-
 internal/workflow/compiler_visibility_test.go      | 280 ++++++++
 .../000014_webhook_route_backfill.down.sql         |  14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |  62 ++
 .../sqlite/000014_webhook_route_backfill.down.sql  |  14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |  58 ++
 nodes/core.go                                      |   5 +
 nodes/error_workflow.go                            |   4 +
 nodes/http.go                                      |   2 +
 nodes/presentation_test.go                         |  35 +
 nodes/webhook.go                                   |  18 +-
 scripts/generate-api-reference.mjs                 |   4 +-
 sdk/src/generated/models.ts                        |  60 +-
 sdk/src/server.ts                                  |  10 +
 sdk/test/operation-coverage.test.mjs               |   1 +
 .../generated/models/executionNodeRunResource.ts   |   1 +
 web/src/lib/api/generated/nodes/nodes.ts           |   8 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  97 +++
 135 files changed, 12495 insertions(+), 362 deletions(-)
```
