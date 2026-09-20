---
id: FEAT-edxxj7
title: 'Release + docs + dead code: 0 releases, wrong namespace, contradicting docs, placeholder, CLI'
status: done
priority: medium
labels:
    - dx
    - docs
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:06Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (8):
  - `0825505b` — FEAT-edxxj7: the served document declares both credentials and the public exceptions — docs
  - `e9bcc6ae` — BUG-8sb0jw FEAT-edxxj7: record testing state with per-finding remainders
  - `84b97839` — FEAT-edxxj7: regenerate the API reference, map the datastore and tenant groups, derive the auth sentence — docs/reference
  - `36390984` — FEAT-edxxj7: correct what-works pages against the running product — docs
  - `2445d653` — FEAT-edxxj7: scheduler package doc stops claiming a single-process-only design — ops/docs
  - `0a567d21` — FEAT-edxxj7: move published coordinates to the real owner, honest SDK/pack install docs, correct dead-code comments — release/docs
  - `2930a34c` — SecurityDx: progress notes — xf1wqm+s0wy50 landed, safehttp partial, rest doing
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 +++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  524 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  630 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 ++++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  639 +++++
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  617 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  752 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 +++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 75553 insertions(+), 4725 deletions(-)
```
