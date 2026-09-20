---
id: FEAT-edxxj7
title: 'Release + docs + dead code: 0 releases, wrong namespace, contradicting docs, placeholder, CLI'
status: doing
priority: medium
labels:
    - dx
    - docs
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T14:24:35Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 8 finding(s) from dims: find:build-health-dx, find:unfinished-work, find:web-frontend-code.

---
### Nothing has ever been released (0 tags, no image, SDK returns 404 on npm) and the release config targets a namespace the repo token likely cannot push to [find:unfinished-work] (medium/unfinished) · area: distribution · confidence: high

FEAT-53fht8 (images) and FEAT-3taswf (npm SDK) are marked done, but no release tag exists, ghcr holds nothing, and @kilasflow/sdk is not on npm. The SDK README still says `npm install @kilasflow/sdk`. The epic's proof 4 (docker run the published image + npm install) cannot be performed.

Evidence: `git tag` returns 0 tags. compose.yaml:30-45 says 'no version tag has ever been pushed ... ghcr holds nothing'. `npm view @kilasflow/sdk version` returns E404. sdk/README.md:256-262 has '## Install npm install @kilasflow/sdk ... Releases are cut from sdk-vX.Y.Z tags', and docs/guides/embedding.md:147 imports '@kilasflow/sdk/browser'. docs/operate/deployment.md:21 uses `kilasflow:latest`, which does not exist and which the compose file warns against. FEAT-53fht8 lines 29 and 36 are unchecked ('not met: nothing is published yet'). The remote is git@github.com:kilaslab/kilas-flow (github.com/kilaslabs/* redirects there with 301), but Makefile:19 sets IMAGE ?= ghcr.io/kilaslabs/kilasflow and :22 SOURCE_URL ?= https://github.com/kilaslabs/k-flow. release.yml pushes with the repo's GITHUB_TOKEN, so the first tag release will probably be refused (my inference; not run). Five doc links point at 

Impact: Customers cannot install from an image or npm. The quickstart requires building locally. FEAT-5fhj6p's capstone is blocked partly on this.

Suggested fix: Fix IMAGE/SOURCE_URL to the actual owner (or add the org), cut v0.x and sdk-v0.1.0 tags, and verify the pushes. Until then, mark the SDK README install as 'pnpm pack from source'. Reopen FEAT-53fht8 and FEAT-3taswf.

Files: Makefile, .github/workflows/release.yml, compose.yaml, sdk/README.md

Existing tickets: FEAT-53fht8, FEAT-3taswf, FEAT-5fhj6p

---
### Docs of record contradict the running product on auth, tenancy, waits, API size, node counts and importer fidelity [find:unfinished-work] (medium/docs) · area: docs · confidence: high

Several 'what works / what is not there yet' statements are stale in both directions: features documented as missing now exist, and generated references miss a third of the API.

Evidence: docs/start/what-kilasflow-is.md:39 says '39 node types' (live: 46 builtin + 3 pack distinct types). Its :52 says 'There is no authentication on the API' and :58 says 'Multi-tenancy is ... not enforced ... exactly one tenant', but auth.enabled, /api/v1/auth/* and api-keys exist. Its :65-68 says 'Executions are not suspended to storage', but a live 2-minute wait goes to status waiting with a resumeUrl. docs/concepts/execution-model.md:303-313 claims a Wait over one minute ends with execution.timeout, which is false now. docs/reference/api.md:20 and api-contract.md:41 say '46 operations ... 9 groups' and 'no operation requires authentication' (generated in commit 7382bd5). Live /api/openapi.json has 63 operations in 13 tags. None of the 17 Datastore operations has a page under docs/reference/api/. README.md:90 says '35 operations'. docs/concepts/node-registry.md:223-224 says '36 builtin / 3

Impact: Evaluators and operators get wrong expectations. The datastore API is undiscoverable in the docs site.

Suggested fix: Regenerate the API reference (make generate-api-reference) and add a CI check that it matches the binary's OpenAPI. Rewrite what-kilasflow-is and the execution-model 'not yet' sections. Compute counts at build time. Publish the top-100 template numbers in the migration guide.

Files: docs/src/content/docs/start/what-kilasflow-is.md, docs/src/content/docs/concepts/execution-model.md, docs/src/content/docs/reference/api.md, docs/src/content/docs/reference/api-contract.md

Existing tickets: FEAT-sfy1tq, FEAT-za118x, FEAT-zmfsjd

---
### Committed SPA placeholder internal/web/dist/index.html was overwritten with real build output that references gitignored hashed chunks [find:unfinished-work] (medium/dx) · area: build / embed · confidence: high

The committed index.html should be the 'SPA not built' placeholder so that `go build` on a fresh clone works. Commits 75d0441 and bf802ab committed a real SvelteKit index.html that references /_app/immutable/*.js files, which are gitignored. A fresh clone built without `make build-web` serves a blank editor, and every web build dirties the git tree.

Evidence: The .gitignore comment says only dist/.gitkeep and dist/index.html are committed placeholders; internal/web/embed.go:22-23 says the same. `git show 75d0441 -- internal/web/dist/index.html` replaces '<p>SPA not built. Run make build-web.</p>' with a page loading /_app/immutable/entry/start.DxsqTFM7.js. At HEAD it loads start.4yiz8tni.js. `git ls-files internal/web/dist` lists only .gitkeep and index.html. The SPA fallback (embed.go:52-56) serves index.html for the missing JS URLs, which causes MIME errors.

Impact: Contributors and CI jobs that run go build without the web step get a silently broken editor instead of the explanatory placeholder. Agents keep committing build output.

Suggested fix: Restore the one-line placeholder. Add a CI/pre-commit check that the committed dist/index.html equals the placeholder, or generate the placeholder at build time.

Files: internal/web/dist/index.html, internal/web/embed.go, .gitignore

---
### `kilasflow pack ...` silently boots a full server; positional args are ignored and the promised pack subcommand does not exist [find:unfinished-work] (low/dx) · area: CLI / pack toolchain · confidence: high

FEAT-cwz4ac promised a `pack` subcommand group in the binary. The tooling actually lives in a separate `nodepackgen` binary, which the Docker image does not ship. `kilasflow pack validate ./dir` gives no error: it starts the server with the default config.yaml and ./data.

Evidence: Running `kilasflow pack` started a long-running server process that had to be stopped. cmd/kilasflow never reads flag.Args() or os.Args. `kilasflow -h` lists only -config, -role, -version and -worker-id. FEAT-cwz4ac criterion 31 ('The binary gains a `pack` subcommand group') is unchecked. docs/guides/node-authoring.md:3,10 says 'check it with pack validate'. The Dockerfile builds and copies only /app/kilasflow (lines 59, 84).

Impact: Operators following the pack guide may accidentally start a second server against ./data, and image users have no validator or checksum tool.

Suggested fix: Reject unexpected positional arguments with a usage error. Add `kilasflow pack init|validate|checksum` backed by the nodepackgen code, or ship nodepackgen in the image and fix the docs.

Files: cmd/kilasflow/main.go, cmd/nodepackgen, Dockerfile, docs/src/content/docs/guides/node-authoring.md

Existing tickets: FEAT-cwz4ac

---
### Dead or stale half-wired code misleads readers, and CI has no unused-code lint [find:unfinished-work] (low/dx) · area: codebase hygiene · confidence: high

Several packages and comments describe features that are not wired or that behave differently now. No CI lint catches unused functions, which is how the retention and $env regressions went unnoticed.

Evidence: engine.WaitRegistry (internal/engine/approval.go:193-310) is used only by tests, and its comment says a restart drops suspended runs, which is false. internal/ai/maf/runtime.go plus the go.mod dependency github.com/microsoft/agent-framework-go v0.1.0: doc.go says 'adopt as an optional runtime', but nothing imports it and no config selects it (FEAT-ej0468). internal/datastore/fleet.go NewFleetRunner/VersionSpread are never started, so FEAT-gxppx1's criterion 'Readiness reports the fleet's version spread' is not wired. Stale comments: nodes/wait.go:29-43 (worker-slot rationale), internal/scheduler/doc.go ('Milestone 2+ / V1 single process'). nodes/{ai,core,database,http,webhook} are empty directories holding only .gitkeep. .github/workflows/ci.yml:5 runs only go vet, gofmt and svelte-check. 14 done tickets (EPIC-3844bz children, the editor-UX tickets, BUG-xmcm8x, BUG-v6tdjr) still carry th

Impact: Readers and agents trust wrong comments (the BUG-v6tdjr class recurs), and removed wiring is not caught in review.

Suggested fix: Delete or wire the code. Add staticcheck/golangci-lint with U1000 to CI. Require real acceptance criteria before `pine close`.

Files: internal/engine/approval.go, internal/ai/maf/runtime.go, internal/datastore/fleet.go, nodes/wait.go

Existing tickets: BUG-v6tdjr, FEAT-ej0468, FEAT-gxppx1

---
### The committed web API client is out of date for the embed-session models; the CI generate-api-check would fail on main [find:web-frontend-code] (low/dx) · area: generated client (orval) · confidence: high

Ran orval 8.27 with the repo's config into a scratch directory against the current server's OpenAPI. Operations are identical, but models/embedSessionBody.ts and embedSessionResource.ts differ. The server makes workflowId optional and adds datastoreId ('exactly one of') and the datastore:read/write scopes. The committed web client still requires workflowId and has no datastoreId. The SDK was regenerated in FEAT-1c70nt; web was not.

Evidence: Scratch output: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/web-frontend-code/orval/src/lib/api/generated compared with web/src/lib/api/generated/models. internal/api/handlers/embed.go:60-61. Makefile:215 and .github/workflows/ci.yml:184 run generate-api-check.

n8n behavior: Not applicable.

Impact: Low today (the SPA does not create embed sessions), but it shows the drift gate is not enforced.

Suggested fix: Run pnpm generate:api in web and commit the result. Make sure the CI job is required for merge.

Files: /Users/izzadev/projects/k-flow/web/src/lib/api/generated/models/embedSessionBody.ts, /Users/izzadev/projects/k-flow/web/src/lib/api/generated/models/embedSessionResource.ts

Existing tickets: FEAT-1c70nt

---
### No i18n infrastructure: all UI copy, plurals and sentences are inline English and lang is fixed to 'en' (matters for the white-label embed) [find:web-frontend-code] (low/unfinished) · area: frontend architecture · confidence: high

There is no message catalog or i18n library. Copy is inline in .svelte files and in lib helpers (version-history.ts, activation.ts, execution.ts statusLabel). Plurals are hand-rolled, app.html has lang='en', and embed branding has no locale field.

Evidence: grep for i18n/paraglide/$t in web/src finds only test files; app.html:2 has <html lang="en">; hand-rolled plurals at execution-canvas.svelte:59 and version-panel.svelte:390; the EmbedBranding type in session.svelte.ts has no locale.

n8n behavior: n8n's editor UI uses an i18n layer with locale files.

Impact: White-label hosts, including the Indonesian market the product name suggests, cannot localize the embedded editor, and retrofitting gets harder as copy grows.

Suggested fix: Adopt a message catalog (for example paraglide) now, add `locale` to embed branding, and set document lang from it.

Files: /Users/izzadev/projects/k-flow/web/src/app.html, /Users/izzadev/projects/k-flow/web/src/lib/embed/session.svelte.ts

---
### All published coordinates point at an unowned GitHub namespace "kilaslabs" (the real repo is kilaslab/kilas-flow): repojacking and image-squatting risk [find:build-health-dx] (high/security) · area: release/distribution metadata · confidence: high

The Go module path, GHCR image name, OCI source label, SDK package metadata, README and install docs all use github.com/kilaslabs/... or ghcr.io/kilaslabs/.... The actual repository is github.com/kilaslab/kilas-flow, and the "kilaslabs" account/org name is currently unclaimed. Today these URLs work only through GitHub rename redirects, which stop the moment anyone registers the name.

Evidence: `git remote -v` -> git@github.com:kilaslab/kilas-flow.git. Checked 2026-09-19: github.com/kilaslab -> 200; github.com/kilaslabs -> 404; api.github.com/users/kilaslabs -> 404. github.com/kilaslabs/kilas-flow and github.com/kilaslabs/k-flow both 301 -> kilaslab/kilas-flow. References: go.mod `module github.com/kilaslabs/kilas-flow` (233 .go files); docs guides/community-nodes.md tells node authors to import github.com/kilaslabs/kilas-flow/pkg/sdk; Makefile:19 IMAGE ?= ghcr.io/kilaslabs/kilasflow; Makefile:22 and Dockerfile:98 source .../kilaslabs/k-flow; compose.yaml:31,41; .env.example:44; README.md:5; sdk/package.json repository/homepage/bugs; docs start/install.md:12 `git clone https://github.com/kilaslabs/k-flow`; docs astro.config.mjs:59, index.mdx, 404.md; sdk/examples/host-page/README.md:22. Three spellings are in use.

Impact: Whoever registers "kilaslabs" could serve `go get` for the module path to community-node authors, publish the ghcr.io/kilaslabs/kilasflow image that the docs tell operators to pin, and receive clones from the install guide. `make docker-release` also pushes to a namespace the project does not control.

Suggested fix: Claim the "kilaslabs" org now (or move the repo there). Otherwise switch the module path, IMAGE, SOURCE_URL, docs and SDK metadata to the real owner in one sweep. Add a CI grep guard that enforces the canonical coordinates.

Files: go.mod, Makefile, Dockerfile, compose.yaml

## Acceptance criteria

- [ ] Nothing has ever been released (0 tags, no image, SDK returns 404 on npm) and the release config targets a nam
- [ ] Docs of record contradict the running product on auth, tenancy, waits, API size, node counts and importer fide
- [ ] Committed SPA placeholder internal/web/dist/index.html was overwritten with real build output that references 
- [ ] `kilasflow pack ...` silently boots a full server; positional args are ignored and the promised pack subcomman
- [ ] Dead or stale half-wired code misleads readers, and CI has no unused-code lint
- [ ] The committed web API client is out of date for the embed-session models; the CI generate-api-check would fail
- [ ] No i18n infrastructure: all UI copy, plurals and sentences are inline English and lang is fixed to 'en' (matte
- [ ] All published coordinates point at an unowned GitHub namespace "kilaslabs" (the real repo is kilaslab/kilas-fl
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-20 (DXOps2)
- Release/distribution coordinates moved to the real owner `kilaslab`: Makefile IMAGE/SOURCE_URL, Dockerfile OCI source
  label, compose.yaml, .env.example, README.md, docs (install/404/index/what-is/community-nodes), sdk/package.json,
  release.yml comment, sdk/examples. New `make coordinates-check` (scripts/check-coordinates.sh, wired into CI lint)
  fails on the unowned `kilaslabs` namespace and asserts the canonical image/source values; negative-tested both ways.
  The Go module path is deliberately excepted — deferred to BUG-341sxn (created, linked below) because a 233-file
  rename needs a quiet tree.
- Nothing is published: sdk/README.md now says so and gives the checkout install path (`pnpm build` + `file:` dep);
  guides/embedding.md says the same where the `@kilasflow/sdk/browser` import appears;
  sdk/examples/reference-host/README.md takes the operator-surface fence (POST /tenants, /tenants/{id}/users,
  /tenants/{id}/api-keys, KILASFLOW_AUTH_* env names, kfa1_<prefix>_<secret> key shape) and its package.json depends
  on the SDK by path; host-page/README.md no longer claims a published image or package; operate/deployment.md's
  `kilasflow:latest` now names `make docker` as the thing that creates it.
- `kilasflow pack …` is gone as a claim: the binary refuses stray arguments (BUG-8sb0jw), docs/guides/node-authoring.md
  names `nodepackgen` and says how to get it, and the Dockerfile builds and ships /app/nodepackgen so image users have
  the validator and checksum tool.
- Dead/stale half-wired code: internal/engine/approval.go's WaitRegistry comment now says it is test-only and that the
  durable path is wait_service.go; internal/datastore/fleet.go's FleetRunner comment now says nothing starts it and
  FEAT-gxppx1's readiness criterion is unmet. Deletion (the other half of "delete or wire") is recorded as the
  follow-up — EngineCore confirms neither file is in any slice's contract this wave, and deleting production code in a
  package another agent is mid-landing was not a call to take unilaterally. Hub sent to AINodes2 (internal/ai/maf +
  agent-framework-go dependency) and EngineWaits (nodes/wait.go:29-43, internal/scheduler/doc.go).
- No unused-code linter added to CI, deliberately: a staticcheck U1000 gate needs a clean baseline, and the audit's own
  dead-code list lives in slices that have not landed their deletions. Recorded here rather than added red.
- i18n (the finding's largest item) moved to FEAT-15k49d: a message catalog plus plurals plus a locale on the embed
  branding type is a rewrite of the frontend copy layer, not a hygiene commit.
- Deferred: BUG-341sxn (Go module path rename off kilaslabs — quiet tree required), FEAT-15k49d (i18n).

- API reference regenerated and drift-gated: scripts/generate-api-reference.mjs now maps the datastore (17) and
  tenant/account (9) operations into two new contract groups, so `make generate-api-reference` succeeds instead of
  failing with "operations without a contract group" and the datastore API is discoverable at
  /reference/api/datastores/ (pages autogenerate into the sidebar). The generator's auth sentence is derived from the
  served document (root `security` present = auth enforced) rather than hard-coded "no operation requires
  authentication", and generated frontmatter descriptions are JSON-quoted so a blurb containing a colon cannot break the
  YAML parse. Verified: `make generate-api-reference-check` passes (14 pages fresh, 72 operations) and
  `cd docs && pnpm build` passes (43 pages, links validated). reference/api-contract.md now states the live count and
  points at the generated reference for the groups it does not duplicate.
