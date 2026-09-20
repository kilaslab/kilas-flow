---
id: FEAT-3taswf
title: Publish @kilasflow/sdk to npm
status: doing
priority: high
labels:
    - sdk
    - release
deps:
    - FEAT-yx0qt6
    - FEAT-53fht8
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:51:49Z"
updated: "2026-09-20T07:42:30Z"
---

## Scope

`@kilasflow/sdk` is a finished package that no one can install. It has three export subpaths (`.`, `./server`, `./browser`) with `types` and `import` conditions on each, a `files` array naming `dist` and `README.md`, a build (`tsc -p tsconfig.build.json`), a typecheck, a vitest suite, and a runnable example under `examples/host-page`. It is not marked `private`. Everything about the manifest says "intended for publication".

Nothing publishes it, and several things would have to be true first.

`sdk/dist/` is gitignored, so the only artifact the `files` array names does not exist in a clone. The root `Makefile` has no SDK target of any kind — no build, no test, no typecheck, no generate — so the package is built only by somebody who already knows to run `pnpm --dir sdk build` by hand. A host wanting to use it today must vendor the source and build it themselves, which is why every integration is currently a copy.

The manifest is also incomplete in ways npm surfaces to anyone looking at the package page. There is no `repository`, no `homepage`, no `bugs`, and no `keywords`, so a published package would carry no link back to the project it belongs to. There is no `publishConfig`. And `"license": "MIT"` contradicts the repository's Apache-2.0 `LICENSE` — a discrepancy V2-p10-4 resolves, and one that becomes an irreversible published claim the first time this ticket runs.

The version is the other unresolved half. `sdk/src/version.ts` hardcodes `SDK_VERSION = '0.1.0'` and `package.json` says `0.1.0`; two literals that must agree, with nothing checking that they do. `sdk/README.md` documents a policy — semver for the package surface, `API_VERSION` for the path — that nothing enforces, and no release has ever exercised it.

The example is worth fixing at the same time, because it is the first thing an evaluator runs. `sdk/examples/host-page/server.mjs` is a complete demonstration — backend mints a session, page mounts the editor, page receives execution events — and it necessarily runs against a KilasFlow the reader has built from source. Once V2-p10-2 publishes an image, the example can run against a pulled container instead, which turns a half-hour evaluation into a two-command one.

## Acceptance criteria

- [ ] `npm install @kilasflow/sdk` in an empty project resolves, and importing all three subpaths type-checks under `moduleResolution: bundler` and under `node16`.
- [x] The published tarball contains `dist` and `README.md` and nothing else — no source, no tests, no `.tmp`, no generated intermediate. Read as `dist` plus `README.md`, `CHANGELOG.md`, `LICENSE` and the manifest (criterion 6 requires the changelog to ship; Apache-2.0 requires the licence text). Proved by `make sdk-package-check`.
- [x] `package.json` and `src/version.ts` cannot disagree: a check fails the build when `SDK_VERSION` and the manifest version differ. `make sdk-version-check` (already on main) and `sdk/test/version.test.mjs` both fail on drift; re-proved this stage with SDK_VERSION alone changed to 0.1.1.
- [x] The manifest declares `repository`, `homepage`, `bugs` and a licence that matches the repository's, per the decision recorded in V2-p10-4. All four are in `sdk/package.json`; `sdk/test/release.test.mjs` asserts them under the canonical source read out of `scripts/check-coordinates.sh`, and `scripts/check-coordinates.sh` check 3 fails if any of the three URLs moves; `sdk/LICENSE` now ships byte-identical to the repository `LICENSE`.
- [ ] Publishing is driven by a git tag through the V2-p10-1 pipeline, with npm provenance attested, and no publish is possible from a working tree that is not that tag.
- [x] A `CHANGELOG.md` records each release and states, per entry, whether it is additive, a fix, or breaking, in the vocabulary V2-p10-4 defines. `sdk/test/release.test.mjs` (`changelog`) fails on a missing, duplicate, empty or unlabelled entry; this stage reconciled the 0.1.0 entry against the README's operation table group by group and removed a bullet describing a change to something never published.
- [x] The Makefile gains SDK targets so building, testing, typechecking and regenerating the package are the same commands on a laptop and in the pipeline. `sdk-check`, `sdk-test`, `sdk-build`, `sdk-version-check`, `sdk-package-check`, `sdk-example-check` and the aggregate `sdk-release-check`; `release-workflow.test.mjs` ties `release.yml`'s sdk job to the `sdk-release-check` recipe and ties `ci.yml`'s sdk job to that recipe minus `generate-types-check`, which `ci.yml`'s drift job runs — removing `make sdk-example-check` from either workflow, or `make generate-types-check` from the drift job, fails it. `release.yml` runs all seven targets; `ci.yml` runs six in the sdk job and the seventh, the one that needs a binary built from the tree, in the drift job.
- [ ] `examples/host-page` runs against a published container image and a published package with no checkout of this repository, and its README says so.

## Implementation Plan

Sequence this after V2-p10-2, not merely alongside it. The example's value depends on a pullable image, and the version-stamping story is the same story in both tickets: a published npm package that says `0.1.0` while the server it targets says `21056a1-dirty` teaches a consumer that neither number means anything.

Keep `tsc` as the build. There is no bundler and there does not need to be one — the package is ESM-only, has no runtime dependencies, and ships declarations. Adding a bundler here would be work with no beneficiary.

Two mechanical traps in publishing an ESM-only package with export conditions. First, `moduleResolution: node16` in a consumer resolves the `types` condition per subpath, so all three subpaths need their declarations emitted and present; a `dist` that builds `index.d.ts` but not `server.d.ts` fails only for consumers on that setting, which is not the setting the author will be testing on. Verify by installing the packed tarball into a scratch project under both resolutions rather than by trusting the build. Second, `files: ["dist", "README.md"]` silently excludes the licence file; npm includes `LICENSE` automatically when it is at the package root, and this package root is `sdk/`, which has none.

For provenance, npm requires the publish to run from a public CI workflow with an OIDC token. That is free once V2-p10-1 exists and is very awkward to add later, so do it in the same change.

The version-agreement check is three lines and prevents a class of bug that is otherwise only found by a consumer: read `package.json`, read `SDK_VERSION`, compare, fail. Put it in the test suite so it runs without ceremony.

One decision to settle rather than default into: whether the first published version is `0.1.0` or `0.x` continues until the API contract from V2-p10-4 is considered stable. Recommend staying on `0.x` until then and saying so in the README, because semver's pre-1.0 allowance is the honest description of a package whose server surface is still being reshaped by p1 through p9 — and claiming `1.0.0` before `FEAT-ddzk2k` lands would promise a stable authentication story that does not exist.

## References

- Roadmap plan, p10 section, entry V2-p10-8: `.pine/roadmap.md`.
- `sdk/package.json` — the three export subpaths, `files`, the missing `repository`/`homepage`/`bugs`, and the MIT declaration.
- `sdk/src/version.ts` — `SDK_VERSION`, the literal that must agree with the manifest.
- `sdk/README.md` — the `## Versioning` section and the entry-point table.
- `sdk/tsconfig.build.json` and the `build` script — the whole build.
- `.gitignore` — the rule that keeps `sdk/dist/` out of the repository.
- `Makefile` — where the missing SDK targets belong, beside the existing web targets.
- `sdk/examples/host-page/server.mjs` and its README — the example to repoint at a published image.
- `.pine/tickets/FEAT-bscygc.md` — V2-p10-4, which resolves the licence and defines the release vocabulary this ticket uses.

## Batch-4 progress (SDKRelease, 2026-09-06)

Implemented, verified, not closed (main verifies and closes).

- Manifest: `repository` (+`directory: sdk`), `homepage`, `bugs`, `keywords`,
  `publishConfig {access public, provenance true}`, `prepack: tsc -p tsconfig.build.json`.
  `files` restored to `["dist", "README.md", "CHANGELOG.md"]` after an edit
  dropped it (caught by `npm pack --dry-run`: 15 files, dist+README+CHANGELOG+package.json, no source/tests).
- `sdk/CHANGELOG.md` (new): 0.1.0 entry, additive/fix vocabulary. Stays 0.x per plan.
- Release workflow: `sdk` job on `sdk-v*` tags (namespaces disjoint from `v*`;
  `if: startsWith(github.ref, 'refs/tags/sdk-v')`, `id-token: write` for
  provenance, make gates + tag==manifest check + `npm publish --dry-run` proof
  before real `npm publish --provenance --access public`). No NPM_TOKEN (OIDC trusted publishing).
- Version agreement: `test/version.test.mjs` (new, .mjs because tsconfig has no
  node types — same reason as operation-coverage gate); `make sdk-version-check` green.
- Example: `host-page/package.json` (new, pins 0.1.0), README runs against
  `ghcr.io/kilaslabs/kilasflow:vX.Y.Z` + published package, no checkout.
- Proof (all local, never published): pack installs into scratch project, all
  three subpaths import + run (node), typecheck under bundler AND nodenext;
  example boots against packed tarball (page 200, SDK dist 200, ticket route validates).
- NOT done (out of SDKRelease ownership, for main): Makefile SDK targets
  already exist (no change needed); dashboard `event-stream.svelte.ts`
  handshake is web/ (untouched).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `053214bc` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `c38dcdcf` — feat(nodes): add the assignment collection kind and move Set onto it
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  429 +++
 .github/workflows/release.yml                      |  157 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    7 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/licensing.md                          |    1 +
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-v6tdjr.md                        |  349 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/FEAT-0556ck.md                       |  729 ++++
 .pine/tickets/FEAT-096vs9.md                       |  784 +++-
 .pine/tickets/FEAT-0f87fn.md                       |    2 +-
 .pine/tickets/FEAT-12s0e5.md                       |  494 ++-
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |    5 +
 .pine/tickets/FEAT-27km39.md                       |  730 ++++
 .pine/tickets/FEAT-2f68r8.md                       |    2 +-
 .pine/tickets/FEAT-2phs15.md                       |  413 ++-
 .pine/tickets/FEAT-347egc.md                       |  806 ++++-
 .pine/tickets/FEAT-3taswf.md                       |   91 +
 .pine/tickets/FEAT-45tfmh.md                       |  390 +-
 .pine/tickets/FEAT-48hreg.md                       |   34 +-
 .pine/tickets/FEAT-4d0bje.md                       |  862 ++++-
 .pine/tickets/FEAT-53fht8.md                       |  120 +
 .pine/tickets/FEAT-55v09k.md                       |    2 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |  189 +-
 .pine/tickets/FEAT-5kfctc.md                       |  162 +-
 .pine/tickets/FEAT-5kv1jq.md                       |    3 +-
 .pine/tickets/FEAT-5mvech.md                       |  332 ++
 .pine/tickets/FEAT-5rvtzc.md                       |    2 +-
 .pine/tickets/FEAT-5s1w0t.md                       |    2 +-
 .pine/tickets/FEAT-5z37xh.md                       |  756 ++++
 .pine/tickets/FEAT-68zzqs.md                       |  391 +-
 .pine/tickets/FEAT-6vfn3s.md                       |    2 +-
 .pine/tickets/FEAT-7cg0cd.md                       |   40 +-
 .pine/tickets/FEAT-7tgasa.md                       |  252 ++
 .pine/tickets/FEAT-8qyfh1.md                       |  455 ++-
 .pine/tickets/FEAT-8r9n21.md                       |    2 +-
 .pine/tickets/FEAT-91as16.md                       |    3 +-
 .pine/tickets/FEAT-9555xz.md                       |    3 +-
 .pine/tickets/FEAT-96p7m3.md                       |  784 +++-
 .pine/tickets/FEAT-9dqn7d.md                       |  374 +-
 .pine/tickets/FEAT-9knk67.md                       |    2 +-
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |   50 +-
 .pine/tickets/FEAT-adzn0a.md                       |    2 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |    2 +-
 .pine/tickets/FEAT-bscygc.md                       |  713 ++++
 .pine/tickets/FEAT-c2a081.md                       |   17 +-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |   80 +
 .pine/tickets/FEAT-cx3hq1.md                       |  712 ++++
 .pine/tickets/FEAT-czbzs6.md                       |  717 ++++
 .pine/tickets/FEAT-ddzk2k.md                       |   75 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-ej0468.md                       |  826 ++++-
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  152 +-
 .pine/tickets/FEAT-gg85se.md                       |  702 ++++
 .pine/tickets/FEAT-gjzgkd.md                       |   65 +-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |   37 +-
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |  788 +++-
 .pine/tickets/FEAT-jq84xk.md                       |   95 +
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |   16 +-
 .pine/tickets/FEAT-knpfqf.md                       |   45 +-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  265 ++
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n5fdz3.md                       |  409 ++-
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nrfg6e.md                       |   58 +-
 .pine/tickets/FEAT-nrfz6m.md                       |  151 +-
 .pine/tickets/FEAT-nxxbs5.md                       |  213 ++
 .pine/tickets/FEAT-pd3p6x.md                       |    2 +-
 .pine/tickets/FEAT-ptyh9w.md                       |  789 +++-
 .pine/tickets/FEAT-q81bq4.md                       |  447 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |    2 +-
 .pine/tickets/FEAT-qe6wb8.md                       |    2 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  113 +-
 .pine/tickets/FEAT-r6xhnp.md                       |  808 ++++-
 .pine/tickets/FEAT-rj17xj.md                       |   42 +-
 .pine/tickets/FEAT-sar60r.md                       |    3 +-
 .pine/tickets/FEAT-sbnejr.md                       |  789 +++-
 .pine/tickets/FEAT-sdjdh2.md                       |   65 +-
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  361 +-
 .pine/tickets/FEAT-sp8cfm.md                       |    2 +-
 .pine/tickets/FEAT-ss44d9.md                       |  812 ++++-
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |    2 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |    2 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   25 +-
 .pine/tickets/FEAT-xqqjqv.md                       |  281 +-
 .pine/tickets/FEAT-xr7ga9.md                       |  841 +++++
 .pine/tickets/FEAT-xx6p22.md                       |   93 +-
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |  749 ++++
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |  711 ++++
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |    2 +-
 .pine/tickets/FEAT-ztxs5p.md                       |    2 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  232 +-
 README.md                                          |  243 +-
 cmd/kilasflow/main.go                              |  405 ++-
 cmd/kilasflow/main_test.go                         |   99 +-
 cmd/nodepackgen/main.go                            |   12 +-
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   73 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  300 +-
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 ++++++++++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  223 ++
 docs/src/content/docs/concepts/execution-model.md  |  335 ++
 docs/src/content/docs/concepts/expressions.md      |  210 ++
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  319 ++
 .../src/content/docs/concepts/safety-boundaries.md |  283 ++
 .../content/docs/concepts/tenancy-and-embedding.md |  305 ++
 docs/src/content/docs/concepts/webhooks.md         |  203 ++
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/embedding.md          |   49 +
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |   41 +
 docs/src/content/docs/index.mdx                    |   59 +
 .../docs/operate/configuration-reference.md        |  667 ++++
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  101 +
 docs/src/content/docs/operate/security.md          |  112 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  306 ++
 docs/src/content/docs/reference/api.md             |   41 +
 docs/src/content/docs/reference/api/auth.md        |  129 +
 docs/src/content/docs/reference/api/credentials.md |  168 +
 docs/src/content/docs/reference/api/embed.md       |   29 +
 docs/src/content/docs/reference/api/errors.md      |   36 +
 docs/src/content/docs/reference/api/events.md      |   40 +
 docs/src/content/docs/reference/api/executions.md  |  102 +
 docs/src/content/docs/reference/api/interop.md     |   51 +
 docs/src/content/docs/reference/api/nodes.md       |  111 +
 docs/src/content/docs/reference/api/schedules.md   |   88 +
 docs/src/content/docs/reference/api/system.md      |   42 +
 docs/src/content/docs/reference/api/webhooks.md    |   36 +
 docs/src/content/docs/reference/api/workflows.md   |  288 ++
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |   36 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 e2e/.gitignore                                     |    2 +
 e2e/fixtures.ts                                    |   40 +
 e2e/fixtures/waha-migration.ts                     |  210 ++
 e2e/global-setup.ts                                |   27 +
 e2e/helpers/seed.ts                                |  174 +
 e2e/helpers/server.ts                              |  142 +
 e2e/helpers/stub.ts                                |   98 +
 e2e/package.json                                   |   14 +
 e2e/playwright.config.ts                           |   32 +
 e2e/pnpm-lock.yaml                                 |   57 +
 e2e/tests/node-coverage.spec.ts                    |  835 +++++
 e2e/tests/smoke.spec.ts                            |  108 +
 e2e/tests/waha-migration.spec.ts                   |  279 ++
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 go.mod                                             |    9 +-
 go.sum                                             |   37 +-
 internal/ai/agent.go                               |  146 +-
 internal/ai/agent_output_test.go                   |  185 +
 internal/ai/ai.go                                  |   50 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/fromai.go                              |  548 +++
 internal/ai/fromai_test.go                         |  139 +
 internal/ai/maf/doc.go                             |   15 +-
 internal/ai/maf/runtime.go                         |  144 +
 internal/ai/maf/runtime_test.go                    |  131 +
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  414 +++
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 +++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  284 +-
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/nodes.go                     |  288 +-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  186 +-
 internal/api/handlers/workflows_delete_test.go     |  111 +
 internal/api/middleware/auth.go                    |  173 +
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   54 +-
 internal/api/server.go                             |   46 +-
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  138 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  311 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  274 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  500 ++-
 internal/config/config_test.go                     |  325 ++
 internal/credentials/builtin.go                    |   36 +
 internal/credentials/credentials.go                |   15 +-
 internal/credentials/credentials_test.go           |    2 +
 internal/credentials/registry.go                   |   14 +-
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  887 +++++
 internal/database/prefix_test.go                   |  371 ++
 internal/datastore/doc.go                          |   31 +
 internal/datastore/engine.go                       |  421 +++
 internal/datastore/engine_test.go                  |  628 ++++
 internal/datastore/fleet.go                        |  163 +
 internal/datastore/fleet_test.go                   |  117 +
 internal/datastore/idents.go                       |  206 ++
 internal/datastore/idents_test.go                  |  215 ++
 internal/datastore/model.go                        |   51 +
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/authenticate.go                    |   13 +-
 internal/engine/runner.go                          |   49 +
 internal/engine/service.go                         |  303 +-
 internal/engine/service_test.go                    |    8 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   14 +-
 internal/expression/doc.go                         |   19 +-
 internal/expression/expression.go                  |   22 +
 internal/expression/expression_test.go             |   65 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   41 +-
 internal/interop/n8n/corpus/baseline.json          |  102 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   43 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  472 ++-
 internal/interop/n8n/n8n_test.go                   | 1978 +++++++++-
 internal/interop/n8n/parameters.go                 | 2802 ++++++++++++--
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  110 +
 internal/loadoptions/loadoptions.go                |   61 +-
 internal/loadoptions/loadoptions_test.go           |   99 +
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/registry.go                          |  143 +-
 internal/node/registry_test.go                     |  328 +-
 internal/nodepack/loaddir.go                       |  155 +
 internal/nodepack/loaddir_test.go                  |  336 ++
 internal/nodepack/nodepack.go                      |   16 +-
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  283 +-
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/credentials.go                 |  196 +-
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  234 +-
 internal/repository/models.go                      |  237 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/prefix_test.go                 |   80 +
 internal/repository/schedules.go                   |  145 +-
 internal/repository/table_names_test.go            |   50 +
 internal/repository/workflow_history.go            |  446 +++
 internal/repository/workflow_history_test.go       |  613 ++++
 internal/repository/workflows.go                   |  182 +-
 internal/routing/request.go                        |    6 +-
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  184 +-
 internal/safehttp/safehttp.go                      |  139 +-
 internal/safehttp/safehttp_test.go                 |  193 +
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  155 +-
 internal/sqlbuild/dialect.go                       |  256 ++
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  650 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 ++
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 ++
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 +++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/policy_test.go                    |  243 ++
 internal/sqlnode/sqlnode.go                        |  905 ++++-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/web/dist/index.html                       |   38 +-
 internal/webhook/webhook.go                        |   74 +
 internal/webhook/webhook_test.go                   |  179 +-
 internal/workflow/compiler.go                      |  167 +-
 internal/workflow/compiler_test.go                 |  215 ++
 internal/workflow/lifecycle.go                     |   51 +
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/postgres/000005_datastores.down.sql     |   10 +
 migrations/postgres/000005_datastores.up.sql       |   48 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 migrations/sqlite/000005_datastores.down.sql       |   10 +
 migrations/sqlite/000005_datastores.up.sql         |   47 +
 nodes/ai.go                                        | 2723 +++++++++++++-
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/ai_tools_test.go                             |  518 +++
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |    7 +
 nodes/code.go                                      |  114 +-
 nodes/code_test.go                                 |   91 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  174 +-
 nodes/database.go                                  |  317 +-
 nodes/database_test.go                             |  751 +++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  604 +++-
 nodes/executors_test.go                            |  263 ++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |    4 +-
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/postgres_v2.go                               |  633 ++++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/sql_options.go                               |  569 +++
 nodes/sql_options_live_test.go                     |  594 +++
 nodes/sql_options_test.go                          |  257 ++
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |   14 -
 nodes/telegram_download.go                         |   40 +-
 nodes/telegram_test.go                             |    6 -
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/wait.go                                      |  230 ++
 nodes/webhook.go                                   |  257 +-
 packs/waha/waha_test.go                            |    5 +-
 pkg/sdk/.gitkeep                                   |    0
 scripts/config-reference.go                        |  402 +++
 scripts/config-reference_test.go                   |   87 +
 scripts/docker-tags.sh                             |   84 +
 scripts/e2e-stub.mjs                               |   50 +
 scripts/generate-api-reference.mjs                 |  394 ++
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/README.md                                      |  150 +-
 sdk/examples/host-page/README.md                   |   26 +-
 sdk/examples/host-page/index.html                  |   31 +-
 sdk/examples/host-page/server.mjs                  |   31 +-
 sdk/package.json                                   |   22 +-
 sdk/scripts/dump-openapi.mjs                       |   13 +
 sdk/src/browser.ts                                 |  120 +-
 sdk/src/generated/models.ts                        | 1102 +++++-
 sdk/src/http.ts                                    |   31 +-
 sdk/src/server.ts                                  |  304 +-
 sdk/src/version.ts                                 |   15 +-
 sdk/test/browser.test.ts                           |  121 +
 sdk/test/operation-coverage.test.mjs               |  138 +
 sdk/test/operations.test.ts                        |  220 ++
 sdk/test/server.test.ts                            |   90 +-
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  105 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 web/src/lib/api/generated/models/definition.ts     |    1 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   21 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |    2 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |    8 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |    3 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  103 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  314 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  113 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   23 +
 .../lib/components/dashboard/list-states.svelte    |  136 +
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../components/workflow-editor/canvas-node.svelte  |   12 +
 .../workflow-editor/properties-panel.svelte        |   27 +-
 .../workflow-editor/property-field.svelte          |  472 ++-
 .../workflow-editor/version-panel.svelte           |  509 +++
 .../workflow-editor/workflow-editor.svelte         |  286 +-
 web/src/lib/dashboard/cursor-page.test.ts          |   71 +
 web/src/lib/dashboard/cursor-page.ts               |   64 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/embed/embed-editor.svelte              |   50 +-
 web/src/lib/embed/session.svelte.ts                |   22 +-
 web/src/lib/embed/session.test.ts                  |   35 +-
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/document.ts            |   10 +-
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.ts         |   18 +
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.ts          |   20 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  113 +-
 .../app/workflows/[id]/export-dialog.svelte        |  118 +
 .../app/workflows/diagnostics-section.svelte       |  120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |  204 ++
 .../(dashboard)/app/workflows/import-report.svelte |  126 +
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  190 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    7 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 682 files changed, 99916 insertions(+), 2322 deletions(-)
```

## Reopened 2026-09-20

Closed `done` with every acceptance criterion unticked, and the deliverable does not exist:
`npm view @kilasflow/sdk version` answers `E404 Not Found` against the live registry, and
`git tag` is empty, so the tag-driven publish this ticket describes has never run.

Reopened to `todo` by the board reconciliation ticket `BUG-vzzkg3`. The remaining work is the
publish itself (and whatever of the manifest criteria is still unmet — the Makefile targets and
`publishConfig` have since landed).

## Implementation notes

### Stage 1 of 3, 2026-09-20 — package integrity

Stage 1 of the approved 3-stage plan. Nothing was published, tagged or pushed: no
`npm publish` (not even `--dry-run` is needed by this stage), no `git tag`, no `git push`,
no `gh` write call. No Go source, SQL, migration, web/ or docs page changed, so the Go
gates below are sanity checks on an untouched tree.

What changed:

- `sdk/LICENSE` (new) — byte copy of the repository's Apache-2.0 `LICENSE`; `cmp` clean.
  npm auto-includes a root `LICENSE` only when the package root is the repository root,
  and this package root is `sdk/`, so without this file the published tarball would carry
  no licence text.
- `sdk/package.json` — `files` is `["dist","README.md","CHANGELOG.md","LICENSE"]`; new
  `clean` script (`node -e` removing `dist`); `build` is now
  `npm run clean && tsc -p tsconfig.build.json`; `prepack` is `npm run build` (it was a
  bare `tsc`, which shipped whatever stale files sat in `dist`).
- `sdk/scripts/lib/release.mjs` (new) — the release rules as pure functions:
  `readCanonicalSource`, `parseSdkTag`, `distTagFor`, `publishContextProblems`,
  `changelogProblems`, `packProblems`, `manifestProblems`. No I/O. Stage 2's
  `scripts/release.mjs` CLI and the tests both call these, so the rules that gate a publish
  exist once.
- `sdk/scripts/lib/pack.mjs` (new) — `run()` (promisified `execFile` whose rejection
  carries stdout and stderr) and `packSdk()`, the single place the SDK is packed.
- `sdk/scripts/check-package.mjs` (new) — the consumer check. Pack, assert the file list,
  install into a scratch project outside the repository, typecheck under `bundler`,
  `node16` and `nodenext`, run a fourth pass under `nodenext` with `lib: ["ES2022"]` and
  `skipLibCheck: true` (the README's escape hatch, made executable), then import all three
  subpaths at runtime. Flags: `--tarball <path>` (skip packing, for mutation proofs),
  `--registry [spec]` (install from the registry; default `@kilasflow/sdk@<manifest
  version>`, `KILASFLOW_SDK_SPEC` read when the flag is absent), `--keep`. Every phase runs
  even after an earlier one failed and all failures print before exit 1, so the file-list
  layer cannot mask the typecheck layer; the install is the only prerequisite and the
  phases that need it are reported as not run rather than allowed to cascade.
- `sdk/test/release.test.mjs` (new) — 33 tests over those rules (tag grammar, publish
  context, changelog vocabulary, tarball allowlist, manifest coordinates, `LICENSE`
  byte-equality, the example's version pin).
- `Makefile` — `SDK_SPEC ?=`; `sdk-package-check`; `sdk-verify-published` (target-specific
  `export KILASFLOW_SDK_SPEC = $(SDK_SPEC)`, recipe reads `"$$KILASFLOW_SDK_SPEC"` — no make
  variable interpolated into a shell line); `sdk-release-check`, which calls `$(MAKE)` on
  `sdk-check`, `sdk-test`, `sdk-build`, `sdk-version-check`, `generate-types-check`,
  `sdk-package-check` in that order (serial on purpose: `sdk-build` removes `dist` first, so
  a parallel run could pack a half-written tree). `clean` also removes `sdk/dist` and
  `sdk/.tmp`. Every new target carries a `## ` help string.
- `scripts/check-coordinates.sh` — check 3: the canonical source under the `repository.url`,
  `homepage` and `bugs` keys of `sdk/package.json`, matched as fixed strings tied to their
  JSON keys so a URL under an unrelated key cannot satisfy the check.
- `.github/workflows/ci.yml` — the `sdk` job gains the pinned `actions/setup-go`
  (operation-coverage.test.mjs reaches `go build ./cmd/kilasflow` through
  scripts/dump-openapi.mjs and only passed on the runner's preinstalled Go), the
  "No Go here" comment is corrected, and the job runs `make sdk-package-check`.
- `CONTRIBUTING.md` — the checks table gains `make sdk-package-check` and
  `make sdk-release-check`; it had no SDK rows at all.

Not edited on purpose: `e2e/fixtures/epic-external.ts` (re-checked, see below);
`sdk/examples/reference-host/*` (its static-route traversal is reported, not fixed here);
docs pages owned by BUG-vzzkg3.

How it was verified (commands and real outcomes):

1. Failing test first: `cd sdk && pnpm exec vitest run test/release.test.mjs` before
   `lib/release.mjs` existed → `Error: Cannot find module '../scripts/lib/release.mjs'`,
   "no tests". After implementing → 33 passed.
2. Clean build: planted `sdk/dist/stale.js`, then `make sdk-build` → the stale file is gone
   and `dist` holds exactly the 12 compiled files (`index`, `server`, `browser`, `http`,
   `version` and `generated/models`, each `.js` + `.d.ts`).
3. `make sdk-package-check` → exit 0; the printed list is 16 files — `package.json`,
   `README.md`, `CHANGELOG.md`, `LICENSE` and 12 under `dist/` — with no source, no tests,
   no `.tmp`, no map, no dotfile. `npm install --offline <tarball>` into the scratch project
   succeeded (so the package genuinely needs nothing else), all four typechecks passed, and
   the runtime phase imported all three subpaths and matched `SDK_VERSION` to the installed
   manifest.
4. Mutation (a), SDK_VERSION drift: `sdk/src/version.ts` → `0.1.1` while the manifest stays
   `0.1.0`. `make sdk-version-check` → exit 2,
   `sdk/package.json version (0.1.0) != SDK_VERSION (0.1.1); bump both together`.
   `pnpm exec vitest run test/version.test.mjs test/release.test.mjs` → 1 failed,
   `version.test.mjs` ("SDK_VERSION mirrors the manifest version"). Honest detail:
   `release.test.mjs` stayed green in that run — it asserts no SDK_VERSION rule, which lives
   in `version.test.mjs` and `sdk-version-check`. Reverted.
5. Mutation (b), `rm sdk/LICENSE` → `release.test.mjs` fails on
   "ships a LICENSE byte-identical to the repository LICENSE", and `sdk-package-check` prints
   `FAIL tarball file list` / `missing from tarball: LICENSE` while the install and all four
   typechecks still pass — the two layers fail independently, which is the point of
   collecting every phase. Reverted (`cp LICENSE sdk/LICENSE`, `cmp` clean).
6. Mutation (c), `"src"` added to `files` → `sdk-package-check` fails naming all six
   `src/*.ts` as `unexpected file in tarball`, everything else green. Reverted.
7. Mutation (d), tarball with `dist/server.d.ts` removed (pack, `tar -xzf`, `rm`,
   `tar -czf`, all in a temp directory outside the repository) then
   `node scripts/check-package.mjs --tarball <path>` → 5 failures: the file list names
   `missing from tarball: dist/server.d.ts`, and all four typechecks fail with TS7016
   ("Could not find a declaration file for module './server.js'") and TS2305 ("has no
   exported member 'KilasFlowClient'"), while the runtime import still passes.
8. Mutation (d2), `dist/server.d.ts` replaced by `export {}` (file list unchanged) → the
   file list passes and only the typecheck matrix fails, TS2305 in all four passes.
9. Mutation (e), the ticket's own node16 trap: `export * from './version.js'` changed to
   `'./version'` in `sdk/src/index.ts`. `make sdk-check` → exit 0 and `make sdk-build` →
   exit 0 (both resolve with `bundler`, which is exactly the gap), while
   `make sdk-package-check` fails: TS2835 under `node16` and `nodenext` ("Relative import
   paths need explicit file extensions in ECMAScript imports … Did you mean './version.js'?"),
   `bundler` still green, and the runtime import fails with
   `ERR_MODULE_NOT_FOUND … dist/version`. This is the evidence that the new gate catches what
   the existing gates cannot. Reverted, and `make sdk-check` / `make sdk-package-check` are
   green again.
10. `make sdk-verify-published` → fails today with a real registry answer, so the network was
    available and this is a true 404 rather than a resolver error:
    `npm error code E404 … GET https://registry.npmjs.org/@kilasflow%2fsdk - Not found`.
    The registry half of criterion 1 stays unticked for that reason.
11. `sh scripts/check-coordinates.sh` → green. With `repository.url` temporarily pointing at
    another owner → exit 1,
    `check-coordinates: sdk/package.json repository.url is not git+https://github.com/kilaslab/kilas-flow.git`.
    Reverted → green.
12. e2e reconciliation, because `prepack` now runs a cleaning build inside the fixture's
    `npm pack`. From `/tmp`:
    `npm pack <abs sdk path> --pack-destination <tmp> --json` → 16 files including
    `dist/server.js` and `LICENSE` and no `src/` (the fixture's own three assertions).
    The Playwright proof-4 spec was **not run**: `e2e/node_modules` is not installed in this
    worktree. Instead the fixture's `scaffoldExternalApp()` — the exact code proof 4 calls —
    was executed directly (`node --input-type=module -e "… await import('./e2e/fixtures/epic-external.ts')"`,
    Node 24 strips the types) and passed end to end: pack, the shape assertions,
    `npm install` of the tarball into a scratch project, and a dynamic import of the
    installed `dist/server.js` exporting `KilasFlowClient` and `datastoreFilter`.
13. `make sdk-release-check` → green end to end (sdk-check, sdk-test, sdk-build,
    sdk-version-check, generate-types-check, sdk-package-check), exit 0.
14. Go gates on a tree with no Go change: `go vet ./...` exit 0; `go build ./...` exit 0;
    `go test -race ./internal/guardrails/...` → ok (4.055s), which is also what enforces the
    no-home-directory-paths rule over tracked `*.mjs`/`*.sh`/`Makefile`/`*.yml`;
    `gofmt -l` not applicable (no Go file changed). No config change, so
    `./scripts/...` and the config-reference targets were not run; no API surface change, so
    `generate-api-check`/`generate-api-reference-check` are unaffected.

Criteria ticked by this stage: 2 (with the reading stated on the criterion line), 3 and 4.
Still unticked, with reasons: 1 and 8 need a published package (and an image for 8);
5 needs a real tag run for the provenance attestation, and the `prepublishOnly` guard
stage 2 adds is a foot-gun guard, not a boundary, because `npm publish <tarball>` runs no
lifecycle scripts; 6 and 7 are stages 2 and 3 (6 rewrites the CHANGELOG entry, 7's
release-job parity test is stage 2).

### Stage 2 of 3, 2026-09-20 — release pipeline

Stage 2 of the approved 3-stage plan. Nothing was published, tagged or pushed: no real
`npm publish` (only `--dry-run`), no `git tag`, no `git push`, no `gh` write call. No Go
source, SQL, migration, web/ or docs page changed, so the Go gates below are sanity
checks on an untouched tree.

What changed:

- `sdk/scripts/release.mjs` (new) — the release CLI, ESM, no new runtime dependency:
  `guard` (refuse a publish whose context is not a push of the matching `sdk-v` tag),
  `facts <tag>` (every non-registry release rule in one step; prints only `version=` and
  `dist_tag=` for `$GITHUB_OUTPUT`), `npm-version` (refuse npm older than 11.5.1, before
  the build rather than as a late OIDC failure). Reads and exits; the rules stay pure
  functions in `lib/release.mjs`.
- `sdk/scripts/lib/release.mjs` — one new pure function, `npmSupportsTrustedPublishing`,
  compared field by field (`11.10.0` is newer than `11.5.1` and sorts before it).
- `sdk/package.json` — `"prepublishOnly": "node scripts/release.mjs guard"`; `yaml@2.9.0`
  as a devDependency (ISC; already in `sdk/pnpm-lock.yaml` transitively, lockfile diff is
  three lines, ships in no artifact — `files` is an allowlist).
- `sdk/test/release-workflow.test.mjs` (new) — 12 tests that PARSE `release.yml` with
  `yaml` (its prose comments contain the very tokens asserted) plus the `Makefile`.
- `sdk/test/release.test.mjs` — 15 more tests: the `npmSupportsTrustedPublishing` table
  and the CLI as a subprocess (`process.execPath` is spawned directly so the child's env
  can be exactly set; an "empty environment" that inherited PATH would not be empty).
- `.github/workflows/release.yml` — header now names both namespaces and points at
  `sdk/RELEASING.md`; the trigger comment no longer claims each job runs only on its own
  namespace; the image job gains
  `if: startsWith(github.ref, 'refs/tags/v')`; the sdk job gains `persist-credentials:
  false`, pinned `actions/setup-go`, `cache: ''` on the js-toolchain, the `npm-version`
  and `facts` steps, one step per `sdk-release-check` target, and a publish step whose
  token lives only in its own env behind a `BOOTSTRAP ONLY` `if`.
- `.github/actions/js-toolchain/action.yml` — a `cache` input (default `pnpm`) wired into
  `setup-node`, so a release build restores no cache while CI is unchanged.
- `sdk/RELEASING.md` (new) — what a release is, the one-time owner setup (bootstrap token
  requirements, `npm trust`, tag ruleset), the per-release steps, post-publish
  verification, the post-first-publish flip list, and optional hardening.

The image-job `if` is in scope because this ticket's own `sdk-v*` tag is what started that
job: its `scripts/docker-tags.sh` rejects the tag, but that failure sits inside a `$(...)`
argument in the `docker-release` recipe, so only buildx refusing an untagged push stopped
a push from a job holding `packages: write` and `id-token: write`.

How it was verified (commands and real outcomes):

1. Failing test first: `pnpm exec vitest run test/release-workflow.test.mjs` before the
   workflow edits → 5 failed for the right reasons (image job had no `if`; publish had no
   `--tag`; no `NPM_TOKEN` anywhere; the old "Tag agrees with the manifest" step is not a
   make/CLI step; the job's make list was missing `generate-types-check` and
   `sdk-package-check`). After the edits → 12 passed.
2. `pnpm exec vitest run test/release.test.mjs` before `release.mjs` existed → 14 failed
   (`npmSupportsTrustedPublishing is not a function`; every CLI test: "Cannot find module
   .../scripts/release.mjs"). After implementing → 48 passed.
3. Mutation (a), image job `if` deleted → "the image job only runs for v* tags" failed.
   Reverted.
4. Mutation (b), a second `npm publish --provenance` added to the image job → "there is
   exactly one non-dry-run npm publish" failed (and the `--tag` test, because the second
   publish became the first match). Reverted.
5. Mutation (c), `run: echo ${{ github.ref_name }}` added to the image job → "the tag
   reaches shell steps through env, never interpolated into a run line" failed. This is
   also the parsed-vs-text point: the file's comments name `github.ref_name` and the test
   still passes, because it reads only `run` values. Reverted.
6. Mutation (d), `- run: make generate-types-check` deleted from the sdk job → "runs
   exactly the make targets that sdk-release-check names, in the same order" failed with
   the five-target list against the Makefile's six. Reverted.
7. Mutation (e), `guard`'s `if (problems.length > 0) fail(...)` changed to `if (false)` →
   all five guard tests failed. Reverted.
8. Mutation (f), the `version !== manifest.version` push removed from `facts` → "facts
   exits 1 for a tag that is not this version" failed. Reverted.
9. `cd sdk && pnpm install --frozen-lockfile` → "Lockfile is up to date"; the `yaml`
   devDependency is consistent.
10. `node sdk/scripts/release.mjs npm-version` → `npm 11.13.0 supports trusted publishing`.
11. `make sdk-release-check` → exit 0 end to end (`sdk-check`, `sdk-test`, `sdk-build`,
    `sdk-version-check`, `generate-types-check`, `sdk-package-check`); the tarball list is
    16 files and the four typechecks plus the runtime import all pass.
12. The hook, safely: `cd sdk && npm run prepublishOnly` → exit 1, four reasons and
    "publishing happens only from the release workflow on a push of the matching sdk-v
    tag". `npm publish --dry-run` → exit 0, 16 files, "npm warn This command requires you
    to be logged in ... (dry-run)" (the guard did not change it). Never a real publish.
13. The bootstrap token branch, locally: with a fake token in a temp userconfig,
    `NPM_CONFIG_USERCONFIG=<tmp> npm publish --dry-run --ignore-scripts --tag latest` still
    exits 0 and no longer prints the "requires you to be logged in" warning — so the
    workflow's `if [ -n "$NPM_TOKEN" ]` block authenticates.
14. The workflow's `facts` step simulated with `GITHUB_OUTPUT` set → the file holds exactly
    `version=0.1.0` and `dist_tag=latest`; `sdk-v9.9.9` is refused on stderr.
15. `ruby -ryaml -e 'ARGV.each{|f| YAML.load_file(f)}'` on `release.yml`, `ci.yml` and
    `js-toolchain/action.yml` → all three parse.
16. e2e reconciliation, because `prepack` runs on `npm pack`: from `/tmp`,
    `npm pack <abs sdk path> --pack-destination <tmp> --json` → 16 files, `dist/server.js`
    and `dist/index.d.ts` present, no `src/`. (`prepublishOnly` does not run on `npm pack`,
    so the fixture is unaffected by this stage.) The Playwright proof-4 spec was not run
    (`e2e/node_modules` is not installed in this worktree); stage 1 executed
    `scaffoldExternalApp()` directly and this stage changed nothing it calls.
17. `sh scripts/check-coordinates.sh` → green.
18. `make sdk-verify-published` → exit 2 with a real registry answer, not a resolver
    error: `npm error code E404 ... GET https://registry.npmjs.org/@kilasflow%2fsdk - Not
    found`. Criterion 1 stays unticked for that reason.
19. Go gates on a tree with no Go change: `go vet ./...` exit 0; `go build ./...` exit 0;
    `go test -race ./internal/guardrails/...` ok (1.833s) — which is also what enforces the
    no-home-directory-path rule over the new `*.mjs`/`*.yml`, and the licence boundary over
    the new `yaml` devDependency. `gofmt -l` not applicable (no Go file changed).

Criteria ticked by this stage: none. The stage adds the pipeline hardening, the guard and
the release facts, but each criterion's tick is owned elsewhere: criterion 7's
pipeline-parity proof is now in place (the parity test and the "nothing but make targets
after install" test pass, and `make sdk-release-check` is green), yet stage 3 appends
`sdk-example-check` to the same list and ticks 6 and 7 after its own proofs; criteria 1
and 8 need a published package and image; criterion 5 needs a real tag run, and the guard
added here is a foot-gun guard, not a boundary — `npm publish <tarball>` runs no lifecycle
scripts, so the real controls are the registry-side trusted publisher and a tag ruleset,
both owner-only and both in `sdk/RELEASING.md`. `status` was not touched.

### Stage 3 of 3, 2026-09-20 — shipped prose, the host-page example, and close-out

Stage 3 of the approved 3-stage plan. Nothing was published, tagged or pushed: no
`npm publish` (only `--dry-run`), no `git tag`, no `git push`, no `gh` write call. No Go
source, SQL, migration, web/ or docs page changed, so the Go gates below are sanity checks
on an untouched tree.

What changed:

- `sdk/README.md` — `## Install` rewritten timelessly (leads with
  `npm install @kilasflow/sdk`, then the pinning and provenance sentence); a
  `### Requirements` section stating the type requirements per entry point exactly as
  verified (`/server` needs `lib DOM` or `@types/node`; the root and `/browser` need
  `lib DOM` even with `@types/node`; `skipLibCheck` silences all of it; a Node-only project
  imports `/server`); a `### Unreleased changes` section (build main from a checkout and
  `npm pack`); the relative `../docs/…/api-contract.md` link is now the absolute GitHub URL
  because the README renders on the npm page; the `## Example` section points at the
  no-checkout path in `examples/host-page/README.md`. The time-bound "not on npm yet —
  `npm view` answers 404" paragraph and the `pnpm add file:` recipe are gone: the tarball is
  immutable and must read correctly on both sides of the first publish.
- `sdk/CHANGELOG.md` — the 0.1.0 **Additive** bullet reconciled group by group against the
  README's operation table: it now names datastores, tenants and accounts (the operator
  surface) and workflow diagnostics, which the package ships and the old bullet omitted.
  The **Fix** bullet about the manifest licence was removed — it described a change to
  something never published, and the licence is enforced by tests, not the changelog. Kept
  minimal for an easy merge with in-flight appends to the same entry.
- `sdk/examples/host-page/README.md` — rewritten as the published shape first ("no checkout
  of this repository needed"): `docker run ghcr.io/kilaslab/kilasflow:v0.1.0` with auth on
  (`KILASFLOW_AUTH_ENABLED`, signing key, operator key shaped `kfa1_<12 hex>_<url-safe
  base64>`, bootstrap email/password, embed signing key, allowed origin), the authenticated
  `POST /api/v1/tenants/default/api-keys` mint whose `token` field is the host key (`kfa1_`,
  not the old `kfa1.` typo), copying the three files out and running `npm install` +
  `npm start`, the pin sentence, the honest authorization sentence, and a clearly delimited
  "Before the first release is published" block to delete after the first publish.
- `sdk/examples/host-page/server.mjs` — the static route's path traversal fixed: the path is
  parsed with `new URL(request.url, origin).pathname` (which also drops query strings), the
  decoded remainder is `resolve()`d against a fixed `sdkDist`, anything not under
  `sdkDist + sep` is a 404, a malformed percent-escape is a 404, and a missing file is a 404
  instead of a 500 that crashed the process (`ERR_HTTP_HEADERS_SENT`).
- `sdk/scripts/check-example.mjs` (new) + `make sdk-example-check` — packs the SDK, copies
  the example into a scratch directory outside the repository, installs the tarball, starts
  a stub KilasFlow (`node:http`, records the `Authorization` header) and the example
  backend, and asserts: `/` is 200 and mounts the editor; `dist/browser.js` and `dist/http.js`
  are served; `POST /api/embed-session` returns a token and the stub saw
  `Authorization: Bearer <key>`; `/api/stream-ticket` without `executionId` is 400; and the
  traversal probes are 404, never 200/500, with no API key in the body. Traversal uses raw
  `node:http` requests because `fetch` normalises `..%2f` before it leaves the process.
  Children are killed by PID; no name-based kill.
- `Makefile` — `sdk-example-check`, appended to the `sdk-release-check` recipe.
- `.github/workflows/release.yml` and `.github/workflows/ci.yml` — `make sdk-example-check`
  appended to the sdk job in each, in the same order, so the Stage 2 parity test stays green.
- `CHANGELOG.md` (root) — one `### Added` and one `### Security` bullet under `[Unreleased]`.

How it was verified (commands and real outcomes):

1. Failing test first: `cd sdk && pnpm exec vitest run test/release.test.mjs` → 1 failed,
   `README.md says "not on npm" about the registry` (the sentence at the old README line
   264). After the README/CHANGELOG rewrite → 50 passed.
2. Failing test first for the traversal: `node scripts/check-example.mjs` with the OLD
   `server.mjs` → phases 1-6 ok, then `FAIL the static route refuses to serve outside the
   SDK dist directory / socket hang up`. After the fix → all 8 phases ok, exit 0.
3. The traverse reconstruction against HEAD's `server.mjs`, to record the actual defect:
   `curl --path-as-is .../dist/..%2f..%2f..%2f..%2fserver.mjs` → **200** and the body was
   `server.mjs` (`grep -c KilasFlowClient` = 2), and
   `.../dist/..%2f..%2f..%2fpackage.json` → the process crashed with
   `ERR_HTTP_HEADERS_SENT` ("Empty reply from server"), because the old route called
   `writeHead(200)` before `readFile`. Both are 404 with the fix.
4. `make sdk-release-check` → exit 0 end to end: `sdk-check`, `sdk-test`, `sdk-build`,
   `sdk-version-check`, `generate-types-check`, `sdk-package-check`, `sdk-example-check`;
   the tarball list is 16 files and every `check-example` phase is ok.
5. Mutation, the new workflow step: `- run: make sdk-example-check` deleted from
   `release.yml` → `release-workflow.test.mjs` failed, `actual` missing `sdk-example-check`
   against the Makefile's seven targets. Reverted.
6. Real-binary proof (manual, all processes killed by PID): built
   `go build -o <tmp>/kilasflow ./cmd/kilasflow`, ran it in an empty temp cwd with
   `KILASFLOW_SERVER_PORT=18099` and the step-1 environment, minted a default-tenant key with
   the operator key, copied the example out, installed the packed tarball, and started it:
   `POST /api/embed-session` returned a `kfe1.` token against the real server,
   `GET /api/stream-ticket?executionId=abc` answered `execution not found` (so the
   authenticated request reached the server), `curl --path-as-is` gave **404** for
   `..%2f..%2f..%2f..%2fserver.mjs`, `..%2f..%2f..%2fpackage.json` and a missing dist file,
   and a normal `dist/browser.js` still served **200**. The boot log printed the four
   misleading `configuration key matches nothing and was ignored` WARNs for
   `auth.operator_key`, `auth.signing_key`, `bootstrap.password` and `embed.signing_key` —
   a separate config bug (the env names collide with the override scheme), reported not
   fixed. The published-image half was not run: no image exists.
7. `make sdk-verify-published` → exit 2 with a real registry answer, not a resolver error:
   `npm error code E404 … GET https://registry.npmjs.org/@kilasflow%2fsdk - Not found`.
8. `cd sdk && npm run prepublishOnly` → exit 1 (four reasons; "publishing happens only from
   the release workflow on a push of the matching sdk-v tag"); `npm publish --dry-run` →
   exit 0. Never a real publish.
9. `sh scripts/check-coordinates.sh` → green; `ruby -ryaml -e 'ARGV.each{|f|
   YAML.load_file(f)}'` on `release.yml`, `ci.yml` and `js-toolchain/action.yml` → all parse.
10. e2e fixture pack check from `/tmp`: `npm pack <abs sdk path> --pack-destination <tmp>
    --json` → 16 files, `dist/server.js` present, no `src/`, `LICENSE` present (the fixture's
    own assertions). The Playwright proof-4 spec was **not run**: `e2e/node_modules` is not
    installed in this worktree.
11. Go gates on a tree with no Go change: `gofmt -l` on changed Go files → nothing;
    `go vet ./...` exit 0; `go build ./...` exit 0; `go test -race ./internal/guardrails/...`
    → ok. No config change, so `./scripts/...` and the config-reference targets were not run;
    no API operation/header/schema change, so `generate-api-check`/
    `generate-api-reference-check` are unaffected (`generate-types-check` ran inside
    `sdk-release-check`).

Reconciliation recorded (plan step B): the 0.1.0 entry's first **Additive** bullet was
checked against the README's operation table group by group. Missing and added: Datastores;
Tenants and accounts (the operator surface, added by BUG-rpkjpy commit 35a32bf); workflow
diagnostics. Already present: workflows and versions, executions, credentials, API keys
("Auth and keys"), schedules, node catalogue, interop, embed ("Embed"), system. The removed
**Fix** bullet described the manifest licence, which is now enforced by
`release.test.mjs`/`sdk-version-check` rather than announced as a release change.

Criteria ticked by this stage: 6 (changelog vocabulary and the reconciled entry) and 7
(the Makefile targets and the laptop/pipeline parity, re-proved by the mutation in step 5).
Criteria 2, 3 and 4 were ticked by stage 1; criterion 2's reading is stated on its line.

Still unticked, with reasons:
- **1** needs the package to resolve from the public registry: `make sdk-verify-published`
  answers a real `E404` today, and this run must not publish. The tarball half is proved by
  `make sdk-package-check` (three resolutions + runtime import).
- **5** needs a real `sdk-vX.Y.Z` tag run for the provenance attestation; nothing was
  tagged. The guard added in stage 2 is a foot-gun guard, not a boundary: `npm publish
  <tarball>` runs no lifecycle scripts. The real controls are the registry-side trusted
  publisher and a tag ruleset, both owner-only and both in `sdk/RELEASING.md`.
- **8** needs a published image and package to run against. The README now says the
  no-checkout commands, and the example is proved to run against the packed tarball and a
  real binary, but the registry half cannot be exercised without publishing.

Owner-only steps (not done here, listed in `sdk/RELEASING.md`): confirm the `@kilasflow`
scope, create the bootstrap token, push `sdk-v0.1.0`, configure `npm trust`, lock down and
delete the secret, add the tag ruleset. The four misleading boot WARNs and the identical
`..%2f` static-route flaw at `sdk/examples/reference-host/server.mjs:74-76` (not in the
tarball, no proof harness here) are reported as separate findings, not fixed by this stage.
`status` was not touched.

### Review round 1 (fixer), 2026-09-20 — nine low findings

The three-lens review of the branch found no high or medium finding and nine low ones, all
of one family: a shipped or contributor-facing statement that no longer matched the code.
Two were behavioural — the example's second crash mode and a workflow test that could not
fail for its own name. All nine are closed in one commit; `status` was not touched and
nothing was published, tagged or pushed.

1. **The host-page page route crashed on a missing `index.html`**
   (`sdk/examples/host-page/server.mjs`). The `/` route wrote its 200 before
   `await readFile`, so a failed read reached a catch that wrote a second set of headers:
   `ERR_HTTP_HEADERS_SENT` out of an async handler, an unhandled rejection, a dead example
   and an empty reply instead of a 404/500. It now reads before writing headers, as the
   static route does, and the catch answers only when `!response.headersSent`.
   `sdk/scripts/check-example.mjs` gained a step that removes the copied `index.html` and
   asserts `GET /` answers 500, the child is still alive and `dist/browser.js` still answers
   200. Proof: with the old handler shape planted back, the check fails
   (`FAIL GET / with index.html missing…: socket hang up`, 7 ok, exit 1); with the fix it is
   8/8 ok, exit 0.
2. **The shipped README undercounted the surface** (`sdk/README.md`): "all 73" → "all 74"
   and the Workflows row gained `listWorkflowWebhooks`, both read off a fresh
   `node sdk/scripts/dump-openapi.mjs <tmp>/openapi.json` dump — 56 paths, 74 operations,
   74 unique operation ids, every path under `/api/v1`.
3. **`CONTRIBUTING.md`'s `sdk-release-check` row listed six targets**: now the seven, in
   recipe order, and `make sdk-example-check` has a row of its own describing what it proves.
4. **`sdk/RELEASING.md` claimed `sdk-verify-published` runs "the same checks" as
   `sdk-package-check`**. Chose the **narrowing** option rather than making the registry path
   pack and assert the file list: the sentence now says exactly what runs (four typechecks
   plus the runtime import) and that the tarball file list — which is asserted against the
   tarball `npm pack` builds in this tree — is not asserted on the registry path. The
   registry-side assertion would have been an unverifiable-against-real-npm code path in a
   release gate that is expected to 404 until the first publish.
5. **`.github/workflows/ci.yml` sdk-job comment said "nothing here builds one"** two lines
   under the `setup-go` step added because `make sdk-test` builds and boots the binary. The
   false clause is deleted; the comment now says only that `generate-types-check` needs a
   binary built from this tree and lives in the drift job with the Go toolchain that job
   already has.
6. **`sdk/test/release-workflow.test.mjs`'s `it('installs dependencies before anything else
   runs')` could not fail for its own name** — it asserted only that the install step exists,
   while its sibling slices from `installIndex + 1` and so shrinks instead of failing. It now
   asserts that no make/release-CLI/publish step precedes the install, sharing one predicate
   with the sibling so the two are exact complements. Proof: moving `Install SDK dependencies`
   to the end of `release.yml`'s sdk job fails it with 10 preceding steps listed; before the
   change that same mutation left the suite green.
7. **Criterion 7 claimed `release.yml` and `ci.yml` run the same SDK gate list** — false:
   `ci.yml`'s sdk job runs six of the seven and `generate-types-check` lives in its drift job.
   Chose the **test** option as well as correcting the sentence: `release-workflow.test.mjs`
   now also parses `ci.yml` and asserts the sdk job's make steps equal the `sdk-release-check`
   recipe minus `generate-types-check`, in order, plus that every recipe target is run
   somewhere in `ci.yml`. Proofs: removing `make sdk-example-check` from the ci sdk job fails
   both new tests; removing `make generate-types-check` from the drift job fails the second;
   removing `make sdk-example-check` from `release.yml`'s sdk job fails the release parity
   test.
8. **The corrected README claim is now checked rather than merely fixed**: a new assertion in
   `sdk/test/operation-coverage.test.mjs` requires the shipped README to name every method the
   coverage map covers and to state that map's size. Proofs: dropping `listWorkflowWebhooks`
   from the row fails it; putting the total back to 73 fails it.
9. `CHANGELOG.md`'s `[Unreleased]` security bullet gained the page-route crash (the finding's
   own family: prose that did not mention the fixed half of the same file).

Gates re-run on this tree, all green:

- `make sdk-release-check` → exit 0; sdk-test `Test Files 8 passed (8)`, `Tests 146 passed
  (146)`; `tarball file list`, all four typechecks and the runtime import ok; the example
  check 8/8 ok including the new missing-`index.html` step.
- `node sdk/scripts/check-example.mjs` (standalone) → 8/8 ok, exit 0.
- `sh scripts/check-coordinates.sh` → green, exit 0.
- `go test -count=1 ./internal/guardrails/...` → `ok`, exit 0; `go vet ./...` → exit 0;
  `gofmt -l` on the changed Go files → nothing (no Go change in this round).
- Mutation runs listed per finding above: each new assertion was watched fail on the mutation
  and pass on the restored tree, and every mutated file was restored from a copy taken before
  the mutation.

### Review round 1 delta (fixer 2), 2026-09-20 — the `headersSent` branch left the request open

The delta review confirmed the nine fixes above and that every new assertion bites, and found
one low defect in the round's own error path: `if (!response.headersSent)` wrote nothing,
logged nothing and never ended the response, so a route that threw after its headers were out
left the request open and the error invisible. A partial revert of the read-before-write order
— the very bug round 1 fixed — turned that crash into a silent hang: the reviewer planted the
old order and `node sdk/scripts/check-example.mjs` stalled to its 120 s timeout (exit 124)
instead of failing in a few seconds, because `rawRequest` had no deadline.

Both halves are fixed, in `sdk/examples/host-page/server.mjs` and `sdk/scripts/check-example.mjs`:

- the `headersSent` branch now logs the error and terminates the response —
  `console.error(error); response.destroy();`. Headers already sent means no status can be
  corrected, but the connection must not be left open; the branch returns explicitly so the
  two cases cannot fall through into each other.
- `rawRequest` carries a 10 s deadline (`rawRequestTimeoutMs`) that destroys the request and
  rejects with `no response for <METHOD> <PATH> within <n>ms: the example left it open`, and a
  request error is reported as `<METHOD> <PATH> failed: <cause>`, so a failure names the
  request that hung or reset rather than the bare `socket hang up`.
- the example's stderr is forwarded to the check's stderr as `example: …`, so the error the
  example logged in that branch sits next to the step that failed instead of being swallowed
  by the pipe.

Proof, all local: nothing was published, tagged or pushed. Two mutations of the tree, each run
against a copy of the file taken before the mutation and restored from that copy afterwards:

| plant | before this delta | after |
| --- | --- | --- |
| old order (`writeHead` before `readFile`) with the new destroy branch | 120 s harness timeout, exit 124 | `FAIL GET / with index.html missing answers 500 and the example stays up` / `GET / failed: socket hang up`, 12.5 s, exit 1 |
| old order with `response.destroy()` removed — the true silent hang | hangs, nothing ever answered | `FAIL …` / `no response for GET / within 10000ms: the example left it open`, 23.1 s, exit 1 |

Both planted runs also printed the example's own `ENOENT` trace, which only became visible
because stderr is now forwarded. Restored tree: `node sdk/scripts/check-example.mjs` 8/8 ok,
exit 0.

Gates re-run on this tree, all green:

- `make sdk-release-check` → exit 0 (sdk-check, sdk-test, sdk-build, sdk-version-check,
  generate-types-check, sdk-package-check, sdk-example-check 8/8 ok).
- `sh scripts/check-coordinates.sh` → green, exit 0.
- `go test -count=1 ./internal/guardrails/...` → `ok`, exit 0; no Go file was touched, so
  there was nothing for `gofmt -l` or `go vet` to see.

No acceptance criterion changed state in this round, and `status` was not touched.
