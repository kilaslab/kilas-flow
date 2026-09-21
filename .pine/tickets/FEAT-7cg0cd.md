---
id: FEAT-7cg0cd
title: Run programmatic community nodes in a JavaScript sidecar
status: doing
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-8r9n21
    - FEAT-sp8cfm
    - FEAT-vvwpjw
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:11:19Z"
updated: "2026-09-20T07:42:30Z"
---

## Scope

This is the deferred decision from the V2 plan, and it stays deferred until p1 through p4 have landed. The research that shaped this roadmap found that across the 100 most-viewed n8n.io templates — 2,377 node instances — only 11 were third-party `n8n-nodes-*` packages: 0.46%. A JavaScript sidecar is the most expensive thing on the roadmap and buys the least coverage, which is why WAHA went native through the routing interpreter and the OpenAPI generator instead. It earns its place only for *programmatic* community nodes, the ones with a real `execute()` that a declarative pack cannot replicate.

The prerequisites are not technical, they are evidential. Until the p0 corpus can report `imported / activatable / executable` counts and the declarative pack path has been proven against WAHA and Telegram, nobody can say which programmatic nodes are actually needed. Picking this up before then means building a Node.js runtime for a demand that has not been measured.

The licence question must be re-opened before a line of code, not after. `packages/workflow/package.json` in the 2.34.0 reference checkout carries `"license": "LicenseRef-n8n-sustainable-use"`, and so does `packages/nodes-base`. KilasFlow's own LICENSE is Apache-2.0 and the product is white-label, multi-tenant and embedded — the configuration n8n's licensing FAQ names as not allowed. A community node package does not escape this: the owner's own `n8n-nodes-mitrachat` declares `n8n-workflow` as a `peerDependency` of `>=1.0.0` and a devDependency of `^2.16.0`, so loading it means loading SUL code into a process KilasFlow ships. Whether a clean-room reimplementation of the `n8n-workflow` runtime surface is viable, and whether the sidecar is distributed at all or only installed by an operator who accepts n8n's terms themselves, is the gate on this ticket.

The runtime is Node 24 LTS, not Bun, and that is settled. `isolated-vm@6.1.2` appears in n8n 2.34.0's `pnpm-lock.yaml` as a dependency of `packages/@n8n/expression-runtime`, `packages/cli` and `packages/nodes-base`; `n8n-workflow` depends on `@n8n/expression-runtime` as a workspace package, so anything importing `n8n-workflow` pulls in a native V8 C++ addon that cannot load on Bun's JavaScriptCore. Separately, `bun build --compile` statically links LGPL-2 JavaScriptCore into the produced binary, which is not a licence posture this project wants next to its Apache-2.0 distribution.

Shipping a sidecar also ends the single-binary promise. The runtime image today is `gcr.io/distroless/static-debian12:nonroot` with a single `CGO_ENABLED=0` Go binary and no shell; a sidecar means a second image or a second stage carrying a Node runtime and an npm dependency tree. That trade is part of what this ticket decides, not a detail to discover during implementation.

## Acceptance criteria

- [x] The licence position is recorded in `.pine/memory/licensing.md` before implementation starts, and states explicitly whether the sidecar is distributed by KilasFlow, installed by the operator, or built clean-room.
- [x] A programmatic community node package installed by an operator loads through its `package.json` `n8n` manifest (`n8nNodesApiVersion`, `nodes`, `credentials`) and appears in the node catalogue tagged `sidecar`, distinct from `builtin` and `pack`.
- [x] Host and sidecar speak NDJSON over a Unix domain socket. Nothing the protocol depends on is ever read from the child's stdout or stderr; a package that prints a startup banner does not corrupt a single message.
- [x] One sidecar process serves exactly one tenant, and a test proves a second tenant's execution never reaches a process holding the first tenant's decrypted credentials.
- [x] A sidecar that crashes, hangs, or exceeds its memory or wall-clock limit fails that node run with a diagnostic and leaves the rest of the execution and the host process intact.
- [x] Outbound HTTP from a community node is subject to the same SSRF policy and per-credential domain scoping as a native node, or the node is refused — the boundary is never silently wider than `internal/safehttp`.
- [x] The image and deployment consequences are documented: what the runtime image becomes, what an operator installs, and what a deployment that declines the sidecar loses.

## Implementation Plan

Do the licence work first and stop if it fails. Nothing below is worth writing against an unresolved distribution question.

The protocol is the second decision and the one that constrains everything else. Frame it as NDJSON over `SOCK_STREAM` on a Unix socket in the instance's data directory, with the socket path passed to the child by argument and the child writing nothing structural to its standard streams. Stdout is not a transport: a third-party package is free to log, and the first line of a logger's banner would desynchronise a stream-framed protocol permanently. Keep stdout and stderr connected to the host's logger as diagnostics only.

Model the sidecar as a source in the node registry rather than a special case in the engine. p2-7 makes `internal/node.Registry` composite and source-tagged; a sidecar contributes definitions the same way a generated pack does, and `nodes/executors.go` gains one executor that marshals `workflow.IRNode`, the resolved parameters and the input items across the socket. The engine should not know a node is remote.

Process lifetime is where the tenancy rule bites. One process per tenant means a pool keyed by tenant, an idle timeout, and a hard rule that a claimed execution for tenant B never reaches a process started for tenant A. The reason is that decrypted credentials cross the socket into third-party JavaScript: once they are in that address space, process isolation is the only isolation left. Write the test that asserts this before the pool, not after.

The unresolved design choice worth stating: whether outbound HTTP from a community node goes out from the sidecar directly or is proxied back to the host and issued through `internal/safehttp`. Proxying back is slower and requires a host-callable request channel over the same socket, but it is the only option that preserves the SSRF policy and per-credential `AllowedDomains` that V1 established. Recommend proxying back, and refusing to run a package that reaches the network by any other route.

## References

- Roadmap plan, p8 section, entry V2-p8-2: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the "What research changed about the original idea" section, for the 0.46% figure and the licence boundary.
- PRD: `gflow-prd-v1.md` §4 (arbitrary n8n npm community nodes and the full JavaScript Code Node are out of scope for V1), §64 ("n8n npm compatibility runner", "plugin SDK design").
- Reference checkout (read-only, never vendored): `/Users/izzadev/projects/mitrachat/n8n/pnpm-lock.yaml` (`isolated-vm@6.1.2` under `packages/@n8n/expression-runtime`, `packages/cli`, `packages/nodes-base`), `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/package.json` (`LicenseRef-n8n-sustainable-use`, `@n8n/expression-runtime` dependency).
- Owner's community package (read-only): `/Users/izzadev/projects/mitrachat/mitrachat-orpc-input-fix/packages/n8n-nodes-mitrachat/package.json` — the `n8n` manifest key and the `n8n-workflow` peer dependency this loader must satisfy.
- Code: `Dockerfile` (distroless static runtime image, `CGO_ENABLED=0`), `internal/safehttp`, `internal/node/registry.go`, `nodes/executors.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 17 — the install-by-npm-package-name dialog and its explicit unverified-code risk checkbox. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (2):
  - `f2699725` — chore(pine): point every roadmap citation at the in-repo roadmap
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .env.example                                       |   186 +
 .github/actions/js-toolchain/action.yml            |    49 +
 .github/workflows/ci.yml                           |   429 +
 .github/workflows/release.yml                      |   157 +
 .gitignore                                         |    13 +
 .pine/CHECKPOINT.md                                |   148 +
 .pine/MEMORY.md                                    |     9 +
 .pine/memory/code-node.md                          |    74 +
 .pine/memory/docker.md                             |     9 +
 .pine/memory/licensing.md                          |    13 +
 .pine/memory/live-databases.md                     |    40 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/memory/persistence.md                        |    14 +
 .pine/memory/web-editor.md                         |    10 +
 .pine/roadmap.md                                   |  1174 ++
 .pine/tickets/BUG-9s3htg.md                        |   170 +
 .pine/tickets/BUG-br7ggc.md                        |   189 +
 .pine/tickets/BUG-v6tdjr.md                        |   349 +
 .pine/tickets/BUG-xmcm8x.md                        |   152 +
 .pine/tickets/EPIC-m42s3g.md                       |    79 +
 .pine/tickets/FEAT-0556ck.md                       |   729 ++
 .pine/tickets/FEAT-096vs9.md                       |   831 ++
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |   539 +
 .pine/tickets/FEAT-1500sp.md                       |   212 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    73 +
 .pine/tickets/FEAT-27km39.md                       |   730 ++
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |   461 +
 .pine/tickets/FEAT-347egc.md                       |   854 ++
 .pine/tickets/FEAT-3taswf.md                       |   786 ++
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |   438 +
 .pine/tickets/FEAT-48hreg.md                       |   894 ++
 .pine/tickets/FEAT-4d0bje.md                       |   904 ++
 .pine/tickets/FEAT-53fht8.md                       |   120 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fhj6p.md                       |    69 +
 .pine/tickets/FEAT-5fv8gf.md                       |   226 +
 .pine/tickets/FEAT-5kfctc.md                       |   208 +
 .pine/tickets/FEAT-5kv1jq.md                       |   119 +
 .pine/tickets/FEAT-5mvech.md                       |   332 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-5z37xh.md                       |   756 ++
 .pine/tickets/FEAT-68zzqs.md                       |   438 +
 .pine/tickets/FEAT-6vfn3s.md                       |   395 +
 .pine/tickets/FEAT-7cg0cd.md                       |    94 +
 .pine/tickets/FEAT-7tgasa.md                       |   252 +
 .pine/tickets/FEAT-8qyfh1.md                       |   486 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   142 +
 .pine/tickets/FEAT-9555xz.md                       |    59 +
 .pine/tickets/FEAT-96p7m3.md                       |   830 ++
 .pine/tickets/FEAT-9dqn7d.md                       |   422 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a7p1b2.md                       |   136 +
 .pine/tickets/FEAT-a94c8y.md                       |   931 ++
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   114 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |   163 +
 .pine/tickets/FEAT-az620p.md                       |   482 +
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
 .pine/tickets/FEAT-bscygc.md                       |   713 ++
 .pine/tickets/FEAT-c2a081.md                       |   891 ++
 .pine/tickets/FEAT-cgm1y3.md                       |   830 ++
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-cpdp8y.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   146 +
 .pine/tickets/FEAT-cwz4ac.md                       |   763 ++
 .pine/tickets/FEAT-cx3hq1.md                       |   712 ++
 .pine/tickets/FEAT-czbzs6.md                       |   717 ++
 .pine/tickets/FEAT-ddzk2k.md                       |   114 +
 .pine/tickets/FEAT-de8d4c.md                       |    71 +
 .pine/tickets/FEAT-ed6wdy.md                       |    66 +
 .pine/tickets/FEAT-ej0468.md                       |   874 ++
 .pine/tickets/FEAT-frvez8.md                       |    70 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |   196 +
 .pine/tickets/FEAT-gg85se.md                       |   702 ++
 .pine/tickets/FEAT-gjzgkd.md                       |   118 +
 .pine/tickets/FEAT-gvn62x.md                       |   197 +
 .pine/tickets/FEAT-gxppx1.md                       |   902 ++
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |   838 ++
 .pine/tickets/FEAT-jq84xk.md                       |   790 ++
 .pine/tickets/FEAT-jwhdsy.md                       |   445 +
 .pine/tickets/FEAT-k3fmj1.md                       |   142 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |   897 ++
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    95 +
 .pine/tickets/FEAT-kwxxd0.md                       |   141 +
 .pine/tickets/FEAT-m94hhx.md                       |   265 +
 .pine/tickets/FEAT-mvegj5.md                       |   112 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   458 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nc6z9r.md                       |    68 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   921 ++
 .pine/tickets/FEAT-nrfz6m.md                       |   197 +
 .pine/tickets/FEAT-nxxbs5.md                       |   213 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-pnbt4z.md                       |    91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   834 ++
 .pine/tickets/FEAT-q81bq4.md                       |   481 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |   136 +
 .pine/tickets/FEAT-r6xhnp.md                       |   856 ++
 .pine/tickets/FEAT-rj17xj.md                       |   100 +
 .pine/tickets/FEAT-sar60r.md                       |   125 +
 .pine/tickets/FEAT-sbnejr.md                       |   834 ++
 .pine/tickets/FEAT-sdjdh2.md                       |    82 +
 .pine/tickets/FEAT-sfy1tq.md                       |   139 +
 .pine/tickets/FEAT-snxxny.md                       |   409 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
 .pine/tickets/FEAT-ss44d9.md                       |   875 ++
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |   433 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |   886 ++
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |   327 +
 .pine/tickets/FEAT-xr7ga9.md                       |   841 ++
 .pine/tickets/FEAT-xx6p22.md                       |   117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   103 +
 .pine/tickets/FEAT-ykyfbd.md                       |    72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   749 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   124 +
 .pine/tickets/FEAT-za118x.md                       |   711 ++
 .pine/tickets/FEAT-zmfsjd.md                       |   146 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |   384 +
 Dockerfile                                         |    54 +-
 Makefile                                           |   251 +-
 README.md                                          |   276 +-
 cmd/kilasflow/main.go                              |   510 +-
 cmd/kilasflow/main_test.go                         |    99 +-
 cmd/nodepackgen/generate.go                        |   576 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   175 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
 compose.build.yaml                                 |    35 +
 compose.postgres.yaml                              |    73 +
 compose.yaml                                       |   103 +
 config.example.yaml                                |   293 +-
 docker-compose.yml                                 |    41 -
 docs/.gitignore                                    |     6 +
 docs/astro.config.mjs                              |   130 +
 docs/package.json                                  |    20 +
 docs/plugins/base-links.mjs                        |    57 +
 docs/pnpm-lock.yaml                                |  3807 +++++++
 docs/src/components/ThemeProvider.astro            |    59 +
 docs/src/components/ThemeSelect.astro              |    79 +
 docs/src/content.config.ts                         |    11 +
 docs/src/content/docs/404.md                       |    21 +
 docs/src/content/docs/concepts/architecture.md     |   163 +
 docs/src/content/docs/concepts/credentials.md      |   223 +
 docs/src/content/docs/concepts/execution-model.md  |   335 +
 docs/src/content/docs/concepts/expressions.md      |   210 +
 .../src/content/docs/concepts/items-and-lineage.md |   174 +
 docs/src/content/docs/concepts/node-registry.md    |   319 +
 .../src/content/docs/concepts/safety-boundaries.md |   283 +
 .../content/docs/concepts/tenancy-and-embedding.md |   305 +
 docs/src/content/docs/concepts/webhooks.md         |   203 +
 docs/src/content/docs/contributing.md              |    89 +
 docs/src/content/docs/guides/embedding.md          |    49 +
 docs/src/content/docs/guides/n8n-migration.md      |   674 ++
 docs/src/content/docs/guides/node-authoring.md     |    41 +
 docs/src/content/docs/index.mdx                    |    59 +
 .../docs/operate/configuration-reference.md        |   667 ++
 docs/src/content/docs/operate/configuration.md     |    71 +
 docs/src/content/docs/operate/deployment.md        |   101 +
 docs/src/content/docs/operate/security.md          |   112 +
 docs/src/content/docs/operate/upgrades.md          |    69 +
 docs/src/content/docs/reference/api-contract.md    |   306 +
 docs/src/content/docs/reference/api.md             |    41 +
 docs/src/content/docs/reference/api/auth.md        |   129 +
 docs/src/content/docs/reference/api/credentials.md |   168 +
 docs/src/content/docs/reference/api/embed.md       |    29 +
 docs/src/content/docs/reference/api/errors.md      |    36 +
 docs/src/content/docs/reference/api/events.md      |    40 +
 docs/src/content/docs/reference/api/executions.md  |   102 +
 docs/src/content/docs/reference/api/interop.md     |    51 +
 docs/src/content/docs/reference/api/nodes.md       |   111 +
 docs/src/content/docs/reference/api/schedules.md   |    88 +
 docs/src/content/docs/reference/api/system.md      |    42 +
 docs/src/content/docs/reference/api/webhooks.md    |    36 +
 docs/src/content/docs/reference/api/workflows.md   |   288 +
 .../content/docs/reference/expression-grammar.md   |   183 +
 docs/src/content/docs/reference/node-packs.md      |    36 +
 docs/src/content/docs/start/first-workflow.md      |    40 +
 docs/src/content/docs/start/install.md             |   271 +
 docs/src/content/docs/start/what-kilasflow-is.md   |    84 +
 docs/src/styles/kilasflow.css                      |   138 +
 docs/tsconfig.json                                 |     5 +
 e2e/.gitignore                                     |     2 +
 e2e/fixtures.ts                                    |    40 +
 e2e/fixtures/waha-migration.ts                     |   210 +
 e2e/global-setup.ts                                |    27 +
 e2e/helpers/seed.ts                                |   174 +
 e2e/helpers/server.ts                              |   142 +
 e2e/helpers/stub.ts                                |    98 +
 e2e/package.json                                   |    14 +
 e2e/playwright.config.ts                           |    32 +
 e2e/pnpm-lock.yaml                                 |    57 +
 e2e/tests/node-coverage.spec.ts                    |   835 ++
 e2e/tests/smoke.spec.ts                            |   108 +
 e2e/tests/waha-migration.spec.ts                   |   279 +
 executions-narrow.png                              |   Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |    32 +
 go.mod                                             |     9 +-
 go.sum                                             |    37 +-
 internal/ai/agent.go                               |   146 +-
 internal/ai/agent_output_test.go                   |   185 +
 internal/ai/ai.go                                  |    50 +-
 internal/ai/ai_test.go                             |   213 +
 internal/ai/fromai.go                              |   548 +
 internal/ai/fromai_test.go                         |   139 +
 internal/ai/maf/doc.go                             |    15 +-
 internal/ai/maf/runtime.go                         |   144 +
 internal/ai/maf/runtime_test.go                    |   131 +
 internal/ai/memory.go                              |   164 +-
 internal/ai/openai.go                              |   100 +-
 internal/ai/openai_test.go                         |   156 +
 internal/ai/outputschema.go                        |   414 +
 internal/api/auth_test.go                          |   668 ++
 internal/api/credentials_test.go                   |   401 +
 internal/api/embed_test.go                         |    61 +-
 internal/api/handlers/auth.go                      |   380 +
 internal/api/handlers/credentials.go               |   339 +
 internal/api/handlers/executions.go                |    41 +-
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   468 +-
 internal/api/handlers/tenants.go                   |    46 +
 internal/api/handlers/workflows.go                 |   303 +-
 internal/api/handlers/workflows_delete_test.go     |   111 +
 internal/api/middleware/auth.go                    |   173 +
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/node_types_test.go                    |   424 +
 internal/api/routes.go                             |    60 +-
 internal/api/server.go                             |    59 +-
 internal/api/workflow_history_test.go              |   188 +
 internal/api/workflows_test.go                     |   211 +-
 internal/auth/auth.go                              |    73 +
 internal/auth/auth_test.go                         |   311 +
 internal/auth/keys.go                              |   197 +
 internal/auth/session.go                           |   274 +
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/conditions/conditions.go                  |   542 +
 internal/conditions/conditions_test.go             |   231 +
 internal/conditions/doc.go                         |    26 +
 internal/config/config.go                          |   531 +-
 internal/config/config_test.go                     |   325 +
 internal/credentials/builtin.go                    |   189 +
 internal/credentials/credentials.go                |   113 +-
 internal/credentials/credentials_test.go           |   244 +
 internal/credentials/registry.go                   |   346 +
 internal/database/database.go                      |    71 +-
 internal/database/database_test.go                 |    91 +-
 internal/database/migrate.go                       |   648 ++
 internal/database/migrate_test.go                  |   887 ++
 internal/database/prefix_test.go                   |   371 +
 internal/datastore/doc.go                          |    31 +
 internal/datastore/engine.go                       |   421 +
 internal/datastore/engine_test.go                  |   628 ++
 internal/datastore/fleet.go                        |   163 +
 internal/datastore/fleet_test.go                   |   117 +
 internal/datastore/idents.go                       |   206 +
 internal/datastore/idents_test.go                  |   215 +
 internal/datastore/model.go                        |    51 +
 internal/datetime/datetime_test.go                 |   134 +
 internal/datetime/doc.go                           |    15 +
 internal/datetime/format.go                        |   195 +
 internal/datetime/parse.go                         |   108 +
 internal/embed/embed.go                            |     2 +
 internal/engine/authenticate.go                    |   104 +
 internal/engine/runner.go                          |   907 +-
 internal/engine/runner_test.go                     |  1229 +-
 internal/engine/service.go                         |   384 +-
 internal/engine/service_test.go                    |    55 +-
 internal/engine/subworkflow_test.go                |   329 +
 internal/engine/worker_test.go                     |     9 +-
 internal/execution/records.go                      |    67 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    91 +-
 internal/expression/expression.go                  |   350 +-
 internal/expression/expression_test.go             |   374 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/guardrails/shell_injection_test.go        |    70 +
 internal/interop/n8n/corpus/BASELINE.md            |   105 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   436 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   613 +
 internal/interop/n8n/export_test.go                |    25 +
 internal/interop/n8n/n8n.go                        |  1354 ++-
 internal/interop/n8n/n8n_test.go                   |  3335 +++++-
 internal/interop/n8n/parameters.go                 |  2712 ++++-
 internal/interop/n8n/sqlfidelity_test.go           |   442 +
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   110 +
 internal/loadoptions/loadoptions.go                |   412 +
 internal/loadoptions/loadoptions_test.go           |   453 +
 internal/loadoptions/schema.go                     |    68 +
 internal/loadoptions/sql.go                        |   287 +
 internal/loadoptions/sql_test.go                   |   350 +
 internal/loadoptions/workflows.go                  |    64 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   771 +-
 internal/node/registry_test.go                     |   932 +-
 internal/nodepack/loaddir.go                       |   155 +
 internal/nodepack/loaddir_test.go                  |   336 +
 internal/nodepack/nodepack.go                      |   432 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/locator_test.go                  |   116 +
 internal/property/mapper.go                        |   322 +
 internal/property/mapper_test.go                   |   196 +
 internal/property/property.go                      |   459 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/auth.go                        |   330 +
 internal/repository/auth_test.go                   |   322 +
 internal/repository/credentials.go                 |   196 +-
 internal/repository/execution_retention.go         |   180 +
 internal/repository/execution_retention_test.go    |   396 +
 internal/repository/executions.go                  |   282 +-
 internal/repository/models.go                      |   332 +-
 internal/repository/models_test.go                 |    28 +-
 internal/repository/postgres_execution_test.go     |   213 +
 internal/repository/prefix_test.go                 |    80 +
 internal/repository/schedules.go                   |   145 +-
 internal/repository/table_names_test.go            |    50 +
 internal/repository/webhooks.go                    |   257 +-
 internal/repository/workflow_history.go            |   446 +
 internal/repository/workflow_history_test.go       |   613 +
 internal/repository/workflows.go                   |   182 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   412 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/runcode/doc.go                            |    37 +-
 internal/runcode/runcode.go                        |    96 +-
 internal/runcode/runcode_test.go                   |   184 +-
 internal/safehttp/safehttp.go                      |   139 +-
 internal/safehttp/safehttp_test.go                 |   193 +
 internal/scheduler/extract.go                      |    81 +
 internal/scheduler/item.go                         |    71 +
 internal/scheduler/rule.go                         |   321 +
 internal/scheduler/rule_test.go                    |   278 +
 internal/scheduler/scheduler.go                    |   147 +-
 internal/scheduler/scheduler_test.go               |   196 +-
 internal/sqlbuild/dialect.go                       |   256 +
 internal/sqlbuild/sqlbuild.go                      |   392 +
 internal/sqlbuild/sqlbuild_test.go                 |   650 ++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |     2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |     2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |     2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |     2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |     2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |     2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |     2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |     2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |     2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |     2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |     2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |     2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |     2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |     2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |     2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |     2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |     2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |     2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |     2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |     2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |     1 +
 .../testdata/mysql/delete_drop_cascade.sql         |     1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |     2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |     1 +
 .../testdata/mysql/delete_truncate_restart.sql     |     1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |     2 +
 .../testdata/mysql/insert_skip_conflict.sql        |     2 +
 internal/sqlbuild/testdata/mysql/select.sql        |     3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |     2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |     3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |     3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |     2 +
 internal/sqlbuild/testdata/mysql/update.sql        |     2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |     2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |     2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |     1 +
 .../testdata/postgres/delete_drop_cascade.sql      |     1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |     2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |     1 +
 .../testdata/postgres/delete_truncate_restart.sql  |     1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |     3 +
 .../testdata/postgres/insert_skip_conflict.sql     |     3 +
 internal/sqlbuild/testdata/postgres/select.sql     |     3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |     2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |     3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |     3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |     2 +
 internal/sqlbuild/testdata/postgres/update.sql     |     3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |     3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |     3 +
 internal/sqlguard/admit.go                         |   245 +
 internal/sqlguard/attack_test.go                   |   344 +
 internal/sqlguard/dialect.go                       |   260 +
 internal/sqlguard/doc.go                           |    53 +
 internal/sqlguard/sqlguard.go                      |   443 +
 internal/sqlguard/sqlguard_test.go                 |   338 +
 internal/sqlnode/export_test.go                    |    11 +
 internal/sqlnode/guard_test.go                     |   126 +
 internal/sqlnode/introspect.go                     |   240 +
 internal/sqlnode/policy_test.go                    |   243 +
 internal/sqlnode/sqlnode.go                        |   905 +-
 internal/sqlnode/sqlnode_test.go                   |   106 +
 internal/web/dist/index.html                       |    38 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   396 +-
 internal/webhook/webhook_test.go                   |   652 +-
 internal/workflow/compiler.go                      |   506 +-
 internal/workflow/compiler_test.go                 |   215 +
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/lifecycle.go                     |    51 +
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 migrations/.gitkeep                                |     0
 migrations/embed.go                                |    27 +
 migrations/postgres/000001_baseline.down.sql       |    23 +
 migrations/postgres/000001_baseline.up.sql         |   192 +
 .../postgres/000002_workflow_history.down.sql      |    11 +
 migrations/postgres/000002_workflow_history.up.sql |    30 +
 migrations/postgres/000003_identity.down.sql       |    15 +
 migrations/postgres/000003_identity.up.sql         |    75 +
 .../postgres/000004_execution_indexes.down.sql     |     5 +
 .../postgres/000004_execution_indexes.up.sql       |    26 +
 migrations/postgres/000005_datastores.down.sql     |    10 +
 migrations/postgres/000005_datastores.up.sql       |    48 +
 migrations/sqlite/000001_baseline.down.sql         |    22 +
 migrations/sqlite/000001_baseline.up.sql           |   185 +
 migrations/sqlite/000002_workflow_history.down.sql |    11 +
 migrations/sqlite/000002_workflow_history.up.sql   |    29 +
 migrations/sqlite/000003_identity.down.sql         |    14 +
 migrations/sqlite/000003_identity.up.sql           |    73 +
 .../sqlite/000004_execution_indexes.down.sql       |     5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |    21 +
 migrations/sqlite/000005_datastores.down.sql       |    10 +
 migrations/sqlite/000005_datastores.up.sql         |    47 +
 nodes/ai.go                                        |  2785 ++++-
 nodes/ai_ollama_test.go                            |   413 +
 nodes/ai_test.go                                   |  1463 ++-
 nodes/ai_tools_test.go                             |   518 +
 nodes/annotation.go                                |    62 +
 nodes/apostrophe_live_test.go                      |    43 +
 nodes/assignments.go                               |   180 +
 nodes/bindings_test.go                             |   136 +
 nodes/code.go                                      |   106 +-
 nodes/code_test.go                                 |   155 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   222 +-
 nodes/database.go                                  |   331 +-
 nodes/database_test.go                             |   757 +-
 nodes/datetime.go                                  |   408 +
 nodes/datetime_test.go                             |   274 +
 nodes/executors.go                                 |   642 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   197 +-
 nodes/http_test.go                                 |   231 +-
 nodes/jscode.go                                    |   172 +
 nodes/jscode_test.go                               |   100 +
 nodes/loop.go                                      |   245 +
 nodes/mysql_v2.go                                  |   199 +
 nodes/mysql_v2_test.go                             |   181 +
 nodes/postgres_v2.go                               |   633 ++
 nodes/postgres_v2_test.go                          |   231 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/sql_options.go                               |   569 +
 nodes/sql_options_live_test.go                     |   594 +
 nodes/sql_options_test.go                          |   257 +
 nodes/sqlite_attach_test.go                        |   161 +
 nodes/subworkflow.go                               |   275 +
 nodes/telegram.go                                  |   393 +
 nodes/telegram_download.go                         |   243 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   610 +
 nodes/testdata/n8n_chat_model_options.json         |    38 +
 nodes/testdata/n8n_sql_options.json                |    17 +
 nodes/transform.go                                 |   745 ++
 nodes/transform_test.go                            |   315 +
 nodes/unsupported.go                               |   116 +-
 nodes/wait.go                                      |   230 +
 nodes/webhook.go                                   |   302 +-
 packs/telegram/README.md                           |    40 +
 packs/telegram/pack.json                           |  1119 ++
 packs/telegram/telegram.go                         |    58 +
 packs/telegram/telegram_test.go                    |   466 +
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/pack-trigger-202409.json                |   124 +
 packs/waha/pack-trigger-202502.json                |   130 +
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1196 ++
 pkg/sdk/.gitkeep                                   |     0
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/config-reference.go                        |   402 +
 scripts/config-reference_test.go                   |    87 +
 scripts/corpus-sync.sh                             |   225 +
 scripts/docker-tags.sh                             |    84 +
 scripts/e2e-stub.mjs                               |    50 +
 scripts/generate-api-reference.mjs                 |   394 +
 scripts/smoke-docker.sh                            |    13 +-
 scripts/smoke-postgres.sh                          |    43 +-
 sdk/README.md                                      |   150 +-
 sdk/examples/host-page/README.md                   |    26 +-
 sdk/examples/host-page/index.html                  |    31 +-
 sdk/examples/host-page/server.mjs                  |    31 +-
 sdk/package.json                                   |    22 +-
 sdk/scripts/dump-openapi.mjs                       |    13 +
 sdk/src/browser.ts                                 |   120 +-
 sdk/src/generated/models.ts                        |  1637 ++-
 sdk/src/http.ts                                    |    31 +-
 sdk/src/server.ts                                  |   304 +-
 sdk/src/version.ts                                 |    15 +-
 sdk/test/browser.test.ts                           |   121 +
 sdk/test/operation-coverage.test.mjs               |   138 +
 sdk/test/operations.test.ts                        |   220 +
 sdk/test/server.test.ts                            |    90 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 web/src/lib/api/generated/auth/auth.ts             |   752 ++
 .../lib/api/generated/credentials/credentials.ts   |   199 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |    20 +
 .../lib/api/generated/models/activationNotice.ts   |    13 +
 .../lib/api/generated/models/activationResource.ts |    23 +
 .../models/{unsupported.ts => assignment.ts}       |     9 +-
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |    17 +
 .../models/createStreamTicketInputBody.ts          |    17 +
 .../api/generated/models/createdAPIKeyResource.ts  |    18 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    18 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     4 +
 .../lib/api/generated/models/executionSummary.ts   |     4 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 web/src/lib/api/generated/models/field.ts          |     1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    48 +-
 .../api/generated/models/listAPIKeysOutputBody.ts  |    15 +
 .../generated/models/listWorkflowVersionsParams.ts |    20 +
 .../api/generated/models/loadOptionsInputBody.ts   |    22 +
 .../models/loadOptionsInputBodyParameters.ts       |     9 +
 .../api/generated/models/loadOptionsResource.ts    |    17 +
 .../lib/api/generated/models/loadSchemaResource.ts |    17 +
 web/src/lib/api/generated/models/loginInputBody.ts |    24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |    21 +
 web/src/lib/api/generated/models/node.ts           |     1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |    16 +
 .../api/generated/models/nodeCodexSubcategories.ts |     9 +
 .../api/generated/models/{lossy.ts => nodeIcon.ts} |     7 +-
 web/src/lib/api/generated/models/option.ts         |    12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |    21 +
 web/src/lib/api/generated/models/port.ts           |     9 +-
 .../lib/api/generated/models/principalResource.ts  |    24 +
 .../lib/api/generated/models/propertyDefinition.ts |    19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 web/src/lib/api/generated/models/propertyMode.ts   |    19 +
 .../generated/models/publishVersionInputBody.ts    |    17 +
 .../generated/models/resourceMapperDeclaration.ts  |    15 +
 .../api/generated/models/streamTicketResource.ts   |    16 +
 .../api/generated/models/testCredentialResource.ts |    17 +
 .../lib/api/generated/models/testPayloadBody.ts    |    22 +
 .../api/generated/models/testPayloadBodyFields.ts  |    12 +
 web/src/lib/api/generated/models/typeOptions.ts    |    17 +
 web/src/lib/api/generated/models/visibility.ts     |    15 +
 .../lib/api/generated/models/webhookDeclaration.ts |    15 +
 .../api/generated/models/webhookRouteResource.ts   |    14 +
 .../models/workflowPublishEventResource.ts         |    19 +
 .../models/workflowPublishEventResourceAction.ts   |    16 +
 .../models/workflowVersionListResource.ts          |    17 +
 .../models/workflowVersionSummaryResource.ts       |    23 +
 web/src/lib/api/generated/nodes/nodes.ts           |   443 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |   317 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   113 +-
 web/src/lib/api/http.test.ts                       |    79 +-
 web/src/lib/api/http.ts                            |    23 +
 .../lib/components/dashboard/list-states.svelte    |   136 +
 web/src/lib/components/ui/table/index.ts           |    28 +
 web/src/lib/components/ui/table/table-body.svelte  |    15 +
 .../lib/components/ui/table/table-caption.svelte   |    20 +
 web/src/lib/components/ui/table/table-cell.svelte  |    15 +
 .../lib/components/ui/table/table-footer.svelte    |    20 +
 web/src/lib/components/ui/table/table-head.svelte  |    15 +
 .../lib/components/ui/table/table-header.svelte    |    20 +
 web/src/lib/components/ui/table/table-row.svelte   |    15 +
 web/src/lib/components/ui/table/table.svelte       |    17 +
 .../workflow-editor/activation-notices.svelte      |   120 +
 .../components/workflow-editor/canvas-node.svelte  |    45 +-
 .../workflow-editor/execution-canvas-node.svelte   |    20 +-
 .../components/workflow-editor/node-icon.svelte    |    10 +-
 .../components/workflow-editor/node-picker.svelte  |     8 +-
 .../workflow-editor/properties-panel.svelte        |    61 +-
 .../workflow-editor/property-field.svelte          |   586 +-
 .../workflow-editor/version-panel.svelte           |   509 +
 .../workflow-editor/workflow-editor.svelte         |   290 +-
 web/src/lib/dashboard/cursor-page.test.ts          |    71 +
 web/src/lib/dashboard/cursor-page.ts               |    64 +
 web/src/lib/dashboard/list-state.test.ts           |    65 +
 web/src/lib/dashboard/list-state.ts                |    69 +
 web/src/lib/dashboard/request-guard.test.ts        |    55 +
 web/src/lib/dashboard/request-guard.ts             |    44 +
 web/src/lib/embed/embed-editor.svelte              |    50 +-
 web/src/lib/embed/session.svelte.ts                |    22 +-
 web/src/lib/embed/session.test.ts                  |    35 +-
 web/src/lib/workflow-editor/activation.test.ts     |   130 +
 web/src/lib/workflow-editor/activation.ts          |   108 +
 web/src/lib/workflow-editor/assignments.test.ts    |    82 +
 web/src/lib/workflow-editor/assignments.ts         |    89 +
 web/src/lib/workflow-editor/collection.test.ts     |   131 +
 web/src/lib/workflow-editor/collection.ts          |   148 +
 web/src/lib/workflow-editor/conditions.test.ts     |    82 +
 web/src/lib/workflow-editor/conditions.ts          |    80 +
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |    14 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |   131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |    75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |   273 +
 web/src/lib/workflow-editor/history-diff.ts        |   Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   226 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |   112 +
 web/src/lib/workflow-editor/resource-locator.ts    |   100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |   121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |   111 +
 .../lib/workflow-editor/version-history.test.ts    |   191 +
 web/src/lib/workflow-editor/version-history.ts     |   156 +
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   199 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   130 +-
 .../app/workflows/[id]/export-dialog.svelte        |   118 +
 .../app/workflows/diagnostics-section.svelte       |   120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |   204 +
 .../(dashboard)/app/workflows/import-report.svelte |   126 +
 .../routes/(dashboard)/credentials/+page.svelte    |    54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |   190 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    33 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    54 +-
 web/src/routes/+page.svelte                        |     8 +-
 811 files changed, 174796 insertions(+), 2747 deletions(-)
```

## Reopened 2026-09-20

Closed `done` with every acceptance criterion unticked. `sidecar/` implements the protocol, the
per-tenant pool and the host-enforced limits, and is tested — but nothing in the product uses
it:

- no production import (`grep kilaslab/kilas-flow/sidecar` outside `./sidecar/` returns nothing),
- no config section, so an operator cannot enable it,
- `node.SourceSidecar` has no `RegisterFrom` call site, so no sidecar node can appear in the
  catalogue,
- no `engine.Executor` adapter and no egress proxy, which the protocol's deny-by-default
  host-call rule requires before any outbound HTTP can happen.

Reopened to `todo` by `BUG-vzzkg3`. The licence position in `.pine/memory/licensing.md` is the
first criterion and is the decision that gates the rest.

## Implementation notes

### Stage 1 of 4 — process boundary (commit `worktree-omp-FEAT-7cg0cd`)

Scope: `sidecar/` only, Go, no JS runner yet. The other three stages are not started.

**What changed.** The pool's process boundary was hardened before any product wiring:

- `sidecar/process.go` (new): `ProcessSpec` + `NewProcessSpawn`. Listen-before-start (the listener exists before the child runs, so a child that dials immediately cannot lose the race); an explicit environment allowlist (`LANG`, `TZ` — the credential master key and database DSN never cross); Node's permission model (`--permission` plus one `--allow-fs-read` grant per allowed path, no other `--allow-*`); script and read paths resolved through `filepath.EvalSymlinks` before the grant is built (Node refuses to start when a granted path traverses a symlink); process group (`Setpgid`), `WaitDelay`, a `Diagnose` that classifies the wait status (SIGABRT → `sidecar-memory-limit`, SIGKILL → container-memory diagnostic, exit N → status N), and `CheckNode` (Node ≥ 24). `DefaultSpawn` is now a thin `NewProcessSpawn` wrapper.
- `sidecar/procattr_unix.go` / `procattr_other.go`, `memory*.go`: the unix-only process-group/kill/exit-classification and the RSS watchdog (Linux `/proc/<pid>/statm`, macOS `ps -o rss=`), so `GOOS=windows go build ./sidecar/` still compiles.
- `sidecar/sidecar.go`: 1-slot per-tenant semaphore honouring its own context; single-flight cold start; identity-aware `evictProc` (a stale run holding a dead P1 can no longer evict a healthy P2); atomic `lastUsed` set at run start and end; `CloseIdle` snapshots candidates under the pool lock and never waits on a process while holding it, skipping busy processes; context cancel/deadline mapped to `sidecar-cancelled` / `sidecar-timeout` with `CallError.Unwrap`; `MaxProcesses` evicts LRU-idle then waits min(ctx, SpawnTimeout) before `sidecar-busy`; `payloadBytes` charges items **and** outputs; `NewPool` raises `MaxFrameBytes` above `MaxOutputBytes` so the output bound is reachable; host calls served sequentially with `MaxHostCalls`; new codes `sidecar-memory-limit`, `sidecar-cancelled`, `sidecar-busy`; child-controlled strings truncated (message 2 KiB, frame type 64 B).
- `sidecar/protocol.go`: `NodeVersion`, `ParamsByItem`, `Credentials`, `Context` on execute; `Binary`/`PairedItem` on `Item`; `Outputs`/`Catalogue` on the terminal frame; `Result.Outputs`; `HostHandler` with the `http.request` / `http.response` / `http.error` frames.
- `sidecar/discover.go` (new): `Discover` runs a process in the describe role (empty tenant, unreachable from `Execute`) and caps the catalogue at `MaxCatalogueBytes`.
- `sidecar/logwriter.go` (new): line-splitting, per-line truncation, a per-process budget and one "output truncated" line, always draining (`return len(p), nil`).
- `sidecar/sidecartest/sidecartest.go` (new): `Node(t)` skips with a named reason when Node is absent/too old, and fails instead when `KILASFLOW_TEST_REQUIRE_NODE=1`.
- `sidecar/fixture/*.js` (new, node builtins only): `env`, `perm`, `forged`, `hang`, `crash`, `heap`, `rss`, `dial`.
- `sidecar/doc.go`: protocol, host calls, multi-output, the env/permission posture, the limits, and the honest statement that the JS guard and Node's permission model are defence in depth, not a sandbox against malicious code.

**Verified.**

- `go test -race -count=1 ./sidecar/...` → `ok ...sidecar 13.116s` (macOS, Node v24.16.0).
- `gofmt -l sidecar/` → empty; `go vet ./...` → clean; `go build ./...` → clean.
- `go test -count=1 ./internal/guardrails/...` → `ok` (after `git add sidecar/`, since guardrails scan `git ls-files`).
- `GOOS=windows go build ./sidecar/` → ok; `GOOS=linux go vet ./sidecar/...` → ok.
- Linux lane: `docker run --rm -v "$PWD":/src -w /src kf-7cg0cd-linux go test -race -count=1 ./sidecar/...` → `ok ...sidecar 7.572s`. Image is `golang:1.27-bookworm` plus `node:24-bookworm-slim`'s `node` binary (Node v24.20.0); this is the only lane that exercises the `/proc/statm` watchdog, the `Setpgid` group kill and `WaitDelay`. (Node 24.20 behaves the same as the verified 24.16; the plan's "only 24.16 verified" note is superseded for 24.20.)
- Node absent: `env PATH="$(dirname "$(command -v go)"):/usr/bin:/bin" go test -count=1 ./sidecar/...` → `ok`, the ten real-Node tests `SKIP` with "node is not on PATH; install Node 24…". With `KILASFLOW_TEST_REQUIRE_NODE=1` the same command FAILs those ten tests with the same message, so CI cannot skip silently.

**Mutation matrix (evidence the new recorder is stronger than the old test).** Each mutation was applied to `sidecar.go`, the tests run, and the file reverted to green.

| Mutation | `TestSecondTenantNeverReachesFirstTenantsProcess` (old) | new (a) `TestEachProcessOnlyEverSeesItsOwnTenantsBytes` | new (b) concurrent (c) evicted (d) exact |
|---|---|---|---|
| M1: key `pool.procs` by a constant in `procFor` | FAIL — `spawns = 1, want 2` | FAIL — `spawns = 1, want 2` | (b) FAIL `spawns=1 want 8`; (c) FAIL; (d) FAIL |
| M2: warm hit returns the most recently spawned process | PASS (each tenant runs once) | FAIL — `process started for "tenant-b" received a frame carrying tenant "tenant-a"` and `received tenant-a's secret` | (b) FAIL; (c),(d) PASS |

M1 alone does not show the new recorder is stronger because the old test also fails on its own `spawns==2` assertion; M2 is the discriminating case: the old test's self-reported `frame.Tenant` looks correct while the per-process recorder sees the misroute.

**Pre-fix failures reproduced for the pool defects** (mutations reverted to green afterwards): removing single-flight → `TestConcurrentColdStartsForOneTenantSpawnOnce` FAILs `spawns = 25, want 1` and `TestConcurrentTenantsNeverCrossProcesses` FAILs `spawns = 11, want 8`; restoring tenant-keyed eviction → `TestEvictIsIdentityAware` FAILs (`evicting a stale process removed the healthy replacement from the pool`).

**Deviations.**
- `TestOversizeFrameFailsTheRun`'s payload was raised from 2 MiB to 5 MiB: `NewPool` now raises `MaxFrameBytes` to sit above `MaxOutputBytes` (required so the 4 MiB output bound is reachable), so 2 MiB no longer exceeds the frame bound. The assertion and the property it pins are unchanged.
- `TestFixtureEchoRunsHeadless` now discovers Node through `sidecartest.Node` so `KILASFLOW_TEST_REQUIRE_NODE=1` fails it instead of skipping; no assertion changed.
- `TestChildEnvironmentIsAllowlisted` tolerates exactly `__CF_USER_TEXT_ENCODING` on darwin only, the one key the OS injects even with an empty `Env`.

**Not done (later stages).** No `runner.cjs`, no manifest/package loading, no `internal/sidecarnode`, no `nodes/` adapter, no safehttp egress proxy, no config key, no `main.go` wiring, no `node.SourceSidecar` registration, no docs/CHANGELOG/CI, no migration (000020 stays unused). Acceptance criteria 2, 6 and 7 are therefore still unticked. Host calls are served when a `HostHandler` is supplied, which is the seam Stage 3's safehttp proxy plugs into; until then the default denies.

### Stage 2 of 4 — clean-room runner and community-package fixtures (commit `worktree-omp-FEAT-7cg0cd`)

Scope: `sidecar/` only — the embedded runner, the fixture packages and the tests. No config, no
`main.go`, no `nodes/`, no `internal/sidecarnode`; those are stages 3 and 4.

**What changed.**

- `sidecar/runner/runner.cjs` (new, 967 lines): the clean-room runner, embedded with
  `//go:embed`. Node built-ins only (`node:net`, `node:fs`, `node:path`), no dependency of its own,
  written from the protocol in `sidecar/protocol.go` and the usage shape of the owner's own MIT
  package. Startup order is dial (before hardening, so the host socket exists) → install the guard
  → load packages → serve frames. `socket.on('close')` exits 0, so an idle orphan cannot outlive
  the host.
  - **Guard**: replaces `net.Socket.prototype.connect`, `net.connect`/`createConnection`,
    `net.Server.prototype.listen`, every function on `dns`, `dns.promises` and both `Resolver`
    prototypes, `dgram.createSocket` and `dgram.Socket.prototype.{send,connect,bind}`, and
    `fetch`/`WebSocket`/`EventSource`, and replaces `process.kill`/`process._kill` so any pid other
    than its own is refused. Every attempt is recorded; a run in which anything was recorded fails
    with `network-refused` or `process-refused` **even when the package swallowed the exception**.
  - **Manifest loading**: `n8nNodesApiVersion` must be 1; every `nodes`/`credentials` path must
    stay inside the package both lexically and after `realpath` (so a symlink cannot escape);
    a file that fails to load is a *file* error that excludes only that node
    (`missing-module` names the module and says to install the peer dependency), while a manifest
    defect, a missing directory, an `ERR_ACCESS_DENIED`, an `ERR_DLOPEN_DISABLED` or a network
    attempt during load is *fatal* and refuses the package. Classes are picked by file basename
    (`Foo.node.js` → `Foo`), instantiated, and keyed by `(description.name, version)` — including
    one name at several versions; a duplicate pair is excluded with a named error.
  - **Execute**: `getInputData`, `getNodeParameter` (per-item via `paramsByItem`, dotted paths,
    throw or fallback), `getCredentials`, `getNode`, `getWorkflow`, `getExecutionId`, `getMode`,
    `getTimezone`, `continueOnFail`, `logger`, and `helpers.{httpRequest,returnJsonArray,constructExecutionMetaData}`.
    Unimplemented members throw `KF_UNSUPPORTED` naming themselves (through a Proxy that exempts
    symbols and `then`/`toJSON`/`inspect` probes, so awaiting or logging the surface cannot throw);
    `httpRequestWithAuthentication`/`requestWithAuthentication`/`request` are named explicitly.
    `httpRequest` forwards `method/url/headers/qs/body/json/timeout/returnFullResponse/ignoreHttpStatusErrors`
    to the host as an `http.request` host call and throws `KF_UNSUPPORTED` naming **any** other
    option (`auth`, `proxy`, `skipSslCertificateValidation`, `encoding`, `form`, …) instead of
    ignoring it. Output is normalised to `[[{json,pairedItem?}]]`; an input or output item carrying
    binary data is refused. A frame whose tenant is not this process's is refused (defence in
    depth), as is an unknown `(name, version)` pair, with a message saying the packages on disk may
    have changed since boot.
- `sidecar/runner.go` (new): `RunnerContent` (bytes + digest), `ExtractRunner(runtimeDir)`
  (content-addressed `runner-<sha256[:12]>.cjs`, dir 0700, file 0600, temp+rename, idempotent,
  symlink-resolved), `RunnerConfig`, `RunnerSpawn` (role from the tenant; argv carries
  `--role/--tenant/--socket/--packages-dir/--package=…`; `ReadPaths` is the resolved packages
  directory) and `failingSpawn` for a configuration that cannot work.
- `sidecar/sidecartest/sidecartest.go`: `PackagesDir(t)` (runtime.Caller, like the fixture test).
- `sidecar/testdata/packages/` (new, flat, no `node_modules/` because `.gitignore` ignores it):
  `kf-fixture-nodes` (Greet with every property shape incl. a hidden property and an
  options-with-`loadOptionsMethod` field, Relay using `helpers.httpRequest` exactly as the owner's
  nodes do, `FixtureApi` credential with `apiKey`/`baseUrl`), `kf-fixture-versions` (one node name
  at versions 1 and 2 in two files, plus a file requiring the absent peer `kf-absent-peer`),
  `kf-fixture-hostile` (19 modes: the seven network routes with and without a swallow, `signal`,
  `child-process`, `addon`, `fs-outside`, `env`, `crash`, `hang`, `heap`, `buffer`, and the three
  unsupported-shape probes), `kf-fixture-badload`, `kf-fixture-netload`, `kf-fixture-badmanifest`
  (api version 2, `../escape.js`, and a symlink escaping into the sibling fixture package),
  `kf-fixture-unsupported` (trigger node, `ai_tool` input, `resourceLocator`, numeric option values,
  unknown credential type). Every manifest is hand-written CommonJS in the shape `tsc` emits and
  declares **no** dependency block.
- `sidecar/runner_test.go` (new, 22 tests): the plan's list, plus the extraction, the unusable-config
  diagnostics, the runtime-directory plumbing, the crash/hang/heap/buffer table and a gated
  `TestOwnersCommunityPackageLoads`. The http helper is exercised against a Go fake `HostHandler` —
  safehttp is stage 3's.

**Verified** (Node v24.16.0 on macOS unless stated):

- `go test -race -count=1 ./sidecar/...` → `ok … sidecar 19.3s`.
- Linux/glibc lane, `docker run --rm -v "$PWD":/src -w /src kf-7cg0cd-linux go test -race -count=1 ./sidecar/...`
  → `ok … sidecar 12.7s` (Node v24.20.0; exercises the `/proc/statm` watchdog and the darwin/linux
  poll interval difference).
- `gofmt -l sidecar/` → empty; `go vet ./sidecar/...` → clean; `go vet ./...` → clean;
  `go build ./...` → clean; `GOOS=windows go build ./sidecar/` and `GOOS=linux go vet ./sidecar/...` → clean.
- `node --check sidecar/runner/runner.cjs` → clean.
- `go test -count=1 ./internal/guardrails/...` → `ok` (run after `git add sidecar/`, because the
  licence scan reads `git ls-files`). No new dependency, Go or npm: the runner is stdlib-only
  JavaScript, the fixtures declare no dependency block, and the symlink fixture is committed as a
  symlink (mode 120000).
- Node absent: `env PATH="$(dirname "$(command -v go)"):/usr/bin:/bin" go test -count=1 ./sidecar/...`
  → `ok`, every real-Node test SKIPs with "node is not on PATH; install Node 24 to run the sidecar
  tests". With `KILASFLOW_TEST_REQUIRE_NODE=1` the same command FAILs (26 failures) with the same
  message, so CI cannot skip silently.

**Mutation evidence** (each mutation applied to `runner.cjs`/`runner.go`, tests run, file reverted and re-run green):

| Mutation | Test | Result |
|---|---|---|
| M-A: `installNetworkGuard()` commented out | `TestDirectNetworkAttemptFailsTheRunEvenWhenSwallowed` | FAIL — `error = <nil>, want *CallError`: with no guard the package really opens the socket and the run "succeeds" |
| M-B: file-level load failure marked `fatal` | `TestPerFileLoadFailureExcludesOnlyThatNode` | FAIL — `Severity:fatal, want a file-severity missing-module` |
| M-C: manifest `n8nNodesApiVersion` check disabled | `TestManifestApiVersionAndPathEscapeAreRefused` | FAIL — `no "manifest-api-version" error among [path-escape symlink-escape]` |
| M-D: `RuntimeDir` dropped from the `ProcessSpec` | `TestRunnerSpawnPutsTheSocketInTheRuntimeDir` | FAIL — socket landed in `/var/folders/…/T/kflow-sidecar-…` instead of the configured directory |

**Independent proof against the real compiled package** (plan stage 2 D). Source read-only from the
owner's package, copied to the scratchpad and compiled there with
`tsc --noCheck --skipLibCheck --module commonjs --target es2022 --moduleResolution node10 --esModuleInterop --outDir dist`
(typescript 5.9.3 installed in the scratchpad; nothing of it enters this repository). Then:
`KILASFLOW_TEST_COMMUNITY_PACKAGES_DIR=/tmp/kf7cg0cd-scratch/community go test -count=1 -run TestOwnersCommunityPackageLoads ./sidecar/`
→ PASS. The catalogue it produced:

```
package n8n-nodes-mitrachat@0.2.0: 9 nodes, 1 credentials, 1 errors
  node mitraChatAgent v1 … v2, mitraChatContact v1 … v2, mitraChatConversation v1,
       mitraChatToolResponse v1, mitraChatSendMessage v1, mitraChatSendTyping v1   (execute=true)
  node mitraChatProviderTrigger v1 execute=false unsupported=[webhook] group=[trigger]
  credential mitraChatApi (MitraChat API) fields=apiKey,baseUrl
  error[file] missing-module dist/nodes/MitraChatWebhookTrigger/MitraChatWebhookTrigger.node.js:
       this file needs the module "n8n-workflow", which is not installed: the operator must
       install the package's peer dependency beside it
```

That is 8 convertible definitions and 2 exclusions (the trigger-group file and the file whose peer
is missing) — the plan's prediction, reached without any fixture written by the same hand as the
runner. `mitraChatAgent` and `mitraChatContact` each appear at v1 and v2 under one name, exactly as
C11 described.

**Black-box re-run of C15** (never reading or copying any of it): inside the local n8n image,
`docker run --rm --entrypoint node -w /usr/local/lib/node_modules/n8n docker.n8n.io/n8nio/n8n:latest
--permission --allow-fs-read=/usr/local/lib/node_modules -e "…require('n8n-workflow')…"` →
`loaded n8n-workflow ok; exports: 422`, `native addons in require cache: 0`, and a follow-up
`require('isolated-vm')` fails with `ERR_ACCESS_DENIED` (fs read outside the grant), so
`n8n-workflow` does not dlopen an addon at require time. The grant has to cover the resolved pnpm
store path *and* the package's top-level symlink, the same symlink lesson as C7a.

**Deviations.**

- The hostile fixture's signal mode calls `process.kill(victimPid, 'SIGTERM')` on a victim the *test*
  starts (and asserts is still alive) rather than `process.ppid`: a real signal attempt with real
  evidence, without a test that kills the harness if the guard is broken.
- The `heap` mode allocates arrays of short strings, not `'x'.repeat(65536)` blobs: measured,
  a 256 MB array of `String.repeat` blobs does **not** abort under `--max-old-space-size=64`,
  while the array form does (SIGABRT, exit 134 → `sidecar-memory-limit`).
- The `buffer` mode holds 192 MB across ~2 s instead of allocating and returning: the RSS watchdog
  polls every 250 ms on darwin and 200 ms on linux, so a fast allocation can finish between two
  samples (it did, intermittently, before this change).
- `kf-fixture-badmanifest` carries all three manifest defects at once and the runner reports all
  three (`manifest-api-version`, `path-escape`, `symlink-escape`) in one pass, instead of three
  near-identical packages.
- `--role` is parsed and logged, but what enforces describe-versus-run is the tenant: a describe
  process is started with an empty tenant, so `Pool.Execute` can never route a run to it and the
  runner refuses any execute frame whose tenant is not its own.

**What this stage does and does not prove for the acceptance criteria.** Criterion 2's loading
clause is proven end to end — an operator-installed package loads through its `n8n` manifest and the
catalogue carries its nodes and credentials, including the real package above — but the criterion
also requires those nodes to appear in the *node catalogue tagged `sidecar`*, which needs
`sidecarnode.Load`/`RegisterFrom` and is stage 3, so criterion 2 stays unticked. Criterion 6's clause
about outbound HTTP is partly proven: the runner can only reach HTTP through the host-call channel,
and every direct route fails the run; the SSRF policy, the per-credential domain scoping and the
refusal of a package that reaches the network another way are `internal/safehttp`'s (stage 3), so
criterion 6 stays unticked too. Criteria 4 and 5 gain evidence (a banner plus a *forged* protocol
frame on stdout still cannot desynchronise the socket; crash/hang/heap/buffer fail only that run
through the runner rather than through a bare script) and stay ticked.

**Not done (later stages).** No `internal/sidecarnode`, no `nodes/` executor adapter, no safehttp
egress proxy, no config section, no `main.go` wiring, no `node.SourceSidecar` registration, no
docs/CHANGELOG/CI, no migration (000020 stays unused).

### Stage 3 of 4 — conversion, `source=sidecar` registration, the executor adapter and the safehttp egress proxy (commit `worktree-omp-FEAT-7cg0cd`)

Scope: `internal/sidecarnode` (new), `nodes/sidecar.go` and `nodes/sidecar_egress.go` (new), one test in
`internal/api`, the `defaultRegistry` comment in `internal/credentials/registry.go`, and two additive
changes in `sidecar/` (a coded host-call refusal). **No config, no `main.go`, no docs, no CHANGELOG, no
CI, no Dockerfile, no migration** — those are stage 4.

**What changed.**

- `internal/sidecarnode/catalogue.go` (new): the package/nodes/credentials/errors shapes the runner's
  `describe` answer carries, plus `ExecutorID = "sidecar.node"` and the severity split (`file` vs
  `fatal`).
- `internal/sidecarnode/convert.go` (new): `Convert(pkg) ([]Converted, []credentials.Type, []Exclusion, error)`.
  Type is `sidecar.<slug(package)>.<slug(nodeName)>` (both halves slugged so the type is a safe URL path
  segment for `/node-types/{type}/icon`); group mapped onto the closed set with a trigger **excluded**;
  exactly one main input and ≥1 main output required; the port name `error` is reserved and refused;
  property kinds string/number/boolean/options/multiOptions/json/dateTime/notice/collection/
  fixedCollection map across, `options` with a `loadOptionsMethod` becomes a **string** with a note
  saying why, `'={{ … }}'` defaults become the `{mode:"expression",…}` marker, `displayOptions.show|hide`
  becomes `property.Visibility` (sorted keys, `@version` passed through for the registry to validate),
  hidden properties go to `NodeRef.HiddenDefaults` instead of the editor; every other shape (resource
  locator, mapper, filter, non-string option values, non-main connections, a parameter named after a
  shared setting, an unknown credential type, no `execute()`) is an exclusion with a named reason. Each
  built definition is trial-registered into a scratch `node.NewRegistry()` so the registry's **own**
  message is the exclusion reason.
- `internal/sidecarnode/load.go` (new): `Load(ctx, LoadDeps{Spawn, Limits, Definitions, Credentials,
  SharedSettings, Log}) (*Index, error)`. Per-file load errors become exclusions (Warn-logged, and the
  caller can read them from `Index.Exclusions()`), a `fatal` package error refuses the boot, and only a
  real-catalogue collision (same type+version, or a credential type already registered) fails —
  naming both packages. `Index` maps `(type, version)` to a `NodeRef` (package, dispatch name, dispatch
  version, hidden defaults, declared credential types) and deliberately does not copy the property list.
- `nodes/sidecar.go` (new): `SidecarExecutorID` (=`sidecarnode.ExecutorID`), the `SidecarRunner`
  interface (`*sidecar.Pool`), `NewSidecarExecutor(runner, index, catalog, policy, log)`,
  `RegisterSidecarExecutor(...)` (by the `nodes/routing.go` pattern, not in `RegisterExecutors`) and
  `SidecarSharedSettings()` (the one-line wrapper over `sharedSettings()` that stage 4 hands to `Load`).
  `Execute` refuses an unindexed node and a tenantless run before anything is sent, refuses binary input
  and output items, fills defaults **then** resolves expressions **then** drops parameters the node does
  not currently show, resolves only the credential types the node declares (skipping blank IDs, sorted,
  type-checked), sends `ParamsByItem` aligned with the items plus the run context, and maps the answer
  onto `len(Outputs)` minus the error port the engine owns (extra non-empty streams refused, missing
  streams padded empty, `Paired` left nil for the engine's positional inference). Child errors are
  rebuilt with every credential value of ≥4 characters scrubbed, keeping the `*CallError` type; a run
  whose context ended returns `ctx.Err()`.
- `nodes/sidecar_egress.go` (new): the `HostHandler` over **one** `safehttp.NewClient(policy)` built at
  construction: parse → `policy.CheckURL` (before the dial) → every held credential's `AllowsHost(host)`
  → `safehttp.WithCredentialScope` with the same conjunction so a redirect outside the scope stops the
  chain → `min(policy.Timeout, timeoutMs)` under the run's context → `policy.ReadBody` with a truncation
  answered as code `response-too-large` → hop-by-hop request headers stripped → response headers
  lower-cased, one string per name. Refusals are logged at Warn with the tenant and the node.
- `sidecar/protocol.go` + `sidecar/sidecar.go` (additive): `HostCallCoder` (an error that names its own
  `http.error` code, defaulting to `http-error`), so the size limit is a code the package can act on
  rather than a sentence. No existing behaviour or assertion changed.
- `internal/credentials/registry.go`: the `defaultRegistry` comment now says `Default()` is a
  `*Registry` singleton whose map is unsynchronised, and that composition may extend it once at boot
  before any goroutine reads it.
- Tests, all new: `internal/sidecarnode/convert_test.go` (mapping, the exclusion table by shape, type-ID
  safety, credential secrecy, shared-setting collision, reserved error port, dry-run refusal,
  versioned/multi-version nodes), `internal/sidecarnode/load_test.go` (per-file vs fatal policy, both
  collision kinds, missing catalogues, exact `Index.Get`, and the real-Node
  `TestLoadRegistersSidecarSourceAndCredentialTypes`), `nodes/sidecar_test.go` (marshalling, the
  parameter pipeline, the adapter-level two-tenant isolation proof with a per-process recorder and
  exactly two processes, undeclared credentials, binary, scrubbing, no tenant, output mapping, the
  error-port arithmetic, the context error), `nodes/sidecar_egress_internal_test.go` (handler-level
  policy: the credential intersection, redirect scope, hop-by-hop stripping, the response cap, the
  host allowlist, unusable URLs, refusal scrubbing), `nodes/sidecar_egress_test.go` (the real runner and
  the Relay fixture through an `httptest` server granted by `AllowedPrivateEndpoints`),
  `nodes/sidecar_engine_test.go` (the engine-level proofs), `internal/api/node_types_sidecar_test.go`.

**Verified** (Node v24.16.0 on macOS unless stated).

- `go test -race -count=1 ./sidecar/... ./internal/sidecarnode/... ./nodes/... ./internal/credentials/...`
  → `ok` (sidecar 21.3s, sidecarnode 2.1s, nodes 280.8s, credentials 1.7s).
- `go test -race -count=1 ./internal/api/` → `ok … 66.3s`.
- `go test -count=1 ./internal/guardrails/...` → `ok` (after `git add`, the scan reads `git ls-files`).
- `test -z "$(gofmt -l . | grep -v '^web/')"` → prints nothing; `go vet ./...` → clean; `go build ./...` → clean;
  `GOOS=windows go build ./sidecar/ ./internal/sidecarnode/` → clean; `GOOS=linux go vet ./sidecar/... ./nodes/...` → clean.
- Linux/glibc lane (`kf-7cg0cd-linux` = golang:1.27-bookworm + node 24, Node v24.20.0):
  `docker run --rm -v "$PWD":/src -w /src kf-7cg0cd-linux go test -race -count=1 ./sidecar/... ./internal/sidecarnode/... ./nodes/...`
  → `ok` (sidecar 17.4s, sidecarnode 1.2s, nodes 213.2s) — this is the lane that exercises the
  `/proc` watchdog and the group kill as CI will.
- Node off PATH (`env PATH="$(dirname "$(command -v go)"):/usr/bin:/bin"`): every real-Node test SKIPs
  with "node is not on PATH; install Node 24 to run the sidecar tests" — including the new
  `TestLoadRegistersSidecarSourceAndCredentialTypes`, `TestEgress*`, `TestSidecarCrash*`,
  `TestSidecarErrorBranch*`, `TestHungNode*`, `TestEveryLoaded*`, while the pure-Go tests (the
  handler-level egress tests among them) still PASS. With `KILASFLOW_TEST_REQUIRE_NODE=1` the same
  command FAILs those tests with the same message, so CI cannot skip silently.
- Wide run `go test -race -count=1 ./...` → all packages of this change green; four failures elsewhere,
  all load-sensitive and none in a package this stage touched:
  `internal/engine TestExpiredWaitsResolveOnTheirOwnDeadline` (passes alone: `ok … 3.0s` — and main's own
  tip is `BUG-w8h3km`, "the wait-service tests flake under load"),
  `internal/api/handlers TestLoginRefusesASprayFromOneAddress` (passes alone: `ok … 51.0s`,
  `auth_test.go:162: no refusal within 30 attempts` in the loaded run),
  `internal/runcode` (package timeout at 600s under load; on a clean detached checkout of main the same
  package passes alone in 61s), and
  `pkg/sdk TestExamplePackRunsUnderWazero` (`code exceeded its 10s time limit`) — **reproduced on a clean
  detached checkout of main** (`/private/tmp/kf-wave1/main-check` @ 72d0200:
  `--- FAIL: TestExamplePackRunsUnderWazero (115.20s) … code exceeded its 10s time limit`), so it is
  environmental (wazero JIT under `-race` on a loaded machine), not this change.

**Mutation evidence** (each mutation applied to the new code, the named test run, the file restored from
a copy and the suite re-run green):

| Mutation | Test | Result |
|---|---|---|
| MA: `nodes/sidecar.go` — visibility filter disabled | `TestExecutorDropsNonVisibleParameters` | FAIL — `attachmentFilename = "stale.txt", want a parameter the node does not show dropped` |
| MB: `nodes/sidecar.go` — `sidecarExpectedPorts` always returns the declared arity | `TestExecutorReturnsNoErrorPortStream` | FAIL — `output streams = 3, want 2: the error port is the runner's` |
| MC: `internal/sidecarnode/convert.go` — shared-setting key check disabled | `TestConvertExcludesAParameterThatCollidesWithASharedSetting` | FAIL — `Convert() registered 1 definitions for a node whose parameter collides with a shared setting` |
| MD: `internal/sidecarnode/load.go` — `fatal` treated as a file error | `TestLoadRefusesAPackageWithAFatalError` | FAIL — `Load() error = nil, want the package refused` |
| ME: `nodes/sidecar.go` — declared-credential check disabled | `TestExecutorRefusesUndeclaredCredentialTypes` | FAIL — `credential "cred-2" does not exist, want it to name the type the node does not declare` |

**Deviations and honest limits.**

- The plan's credential bullet says `credentials.Type{… ExecutorID: ExecutorID}`; `credentials.Type`
  has no `ExecutorID` field (credential types are pure data — only `node.Definition.ExecutorID` exists),
  so nothing is set there. The node definitions carry `sidecarnode.ExecutorID`.
- `Convert` slugs the **node name** as well as the package name. The plan only slugged the package; a
  package whose description name is not an identifier would otherwise produce a type the icon route
  cannot address (`TestTypeIDIsURLSafeAndNeverInTheBuiltinNamespace`).
- The plan's `fmt.Errorf("node %q: %w", ir.Name, callErr)` is implemented as a **scrubbed copy** of the
  `*CallError` (Detail and ChildMessage scrubbed) wrapped with `%w`: wrapping the original would leave
  the unscrubbed text reachable through `Unwrap`, which is the one thing the scrubbing exists to stop.
- Two additive lines in `sidecar/` (a `HostCallCoder` interface and the pool honouring it) were needed
  to carry `response-too-large` as a code instead of the generic `http-error`; the plan listed no
  `sidecar/` change for this stage. Stage 2's tests are unchanged and still green.
- What v1 does not map, all presentation: a node's icon, subtitle, codex, and a credential class's
  `documentationUrl`. Documented on `Convert`.
- Honest limit, unchanged from stage 2: the JS guard and Node's permission model are defence in depth,
  not a sandbox against malicious code; and all allowlisted packages share one tenant process, so one
  package can read another's inputs for the same tenant.

**What this stage does and does not prove for the acceptance criteria.** Criterion 2 is ticked: a package
on disk loads through its `n8n` manifest, its nodes register with `Source == sidecar`, and
`GET /api/v1/node-types` serves them beside `builtin` and `pack` entries. Criterion 6 is ticked: a
community node's outbound HTTP is issued by the host through one `safehttp` client under the deployment's
policy, the credential's `AllowedDomains` and the same conjunction on redirects, and every direct network
route already fails the run in the runner. Criterion 7 (docs) stays unticked — that is stage 4, which
also owes the config section, the composition-root wiring, `config.example.yaml`, the regenerated
configuration reference, the new operate page, the CHANGELOG bullet and the CI/Docker recipe.

### Stage 4 of 4 — product wiring: config, composition root, availability, docs, CI and a Linux Docker recipe

**What changed.**

- `internal/config/config.go`: a `Sidecar` section (one word, so `KILASFLOW_SIDECAR_*` maps to
  `sidecar.*`) on `Config` after `Packs`. Defaults equal `sidecar.DefaultLimits` (a test pins them).
  `validateSidecar` runs only when `enabled`: `packages_dir`, `runtime_dir` and at least one package are
  required, every package name must match the npm grammar (refuses `..`, absolute paths and any
  separator), durations and output are positive, `max_heap_mb >= 16`, `max_processes >= 1`,
  `max_rss_mb >= 0` (0 disables the watchdog, as documented), and a non-unix host is refused at boot.
- `config.example.yaml` and `docs/src/content/docs/operate/configuration-reference.md`: REGENERATED
  with `make generate-config-reference`, never hand-edited.
- `cmd/kilasflow/sidecar.go` (new): `setupSidecar` returns `(nil, nil)` when disabled — no Node probe,
  no runtime directory, no process. Enabled: probe Node ≥ 24, `Abs`+`EvalSymlinks` the packages
  directory (Node's permission model refuses a grant that traverses a symlink), extract the runner,
  build the pool from the limits, run `sidecarnode.Load` (a fatal package error or a real collision
  refuses the boot naming the package; per-file failures are Warns), register the sidecar executor,
  sweep idle processes every `max(idle/4, 10s)`, and expose `Close` and `EvictTenant` (the latter for
  FEAT-fpqvwx's purge path). `Availability()` is the cheap surface: a `stat` of the resolved node path
  plus a TTL-cached (30 s) `CheckNode` verdict, so a catalogue request never spawns `node --version`.
- `cmd/kilasflow/main.go`: one call after `nodepack.LoadDir` (before the registry is shared and before
  anything reads `credentials.Default()`, which the community credential types extend), a
  `defer sidecarRuntime.Close()` that runs after `runtime.Drain`, and `NodeAvailability: availability`
  merging the Code-node report with the sidecar report.
- `sidecar/process.go`: a child that never dialled back now carries its exit status via `classifyExit`
  and points at the sidecar log lines above it [C26].
- `sidecar/runner.go` + `sidecar/logwriter.go` (additive): `RunnerConfig.Wrapper` passes the
  documented `sidecar.wrapper` argv through to `ProcessSpec.Wrapper`; `logWriter` gained a mutex,
  because a pool shares one `NewLogWriter` across every child and os/exec copies each child's streams
  on its own goroutine, so `Write` is called concurrently.
- Docs: new `operate/javascript-sidecar.md` (sidebar order 6) with the image/deployment consequences,
  the trust model and an explicit not-a-boundary list, the failure-code table and the Docker recipe;
  the `sidecar` row in `concepts/node-registry.md`; a caveat section in
  `concepts/safety-boundaries.md`; a paragraph in `operate/deployment.md`; a CHANGELOG bullet.
- CI: `- uses: ./.github/actions/js-toolchain` before the Go tests step (Node 24 from `devbox.json`)
  and `KILASFLOW_TEST_REQUIRE_NODE: "1"` in that step's env.

**Verified.**

- `KILASFLOW_TEST_REQUIRE_NODE=1 go test -race -count=1 ./sidecar/... ./internal/sidecarnode/... ./nodes/... ./internal/config/... ./cmd/kilasflow/...`
  → ok (sidecar 32.6 s, sidecarnode 2.2 s, nodes 345.8 s, config 2.2 s, cmd/kilasflow 7.6 s) — the
  real-Node tests ran rather than skipped.
- Linux/glibc lane (`kf-7cg0cd-linux`, Node v24.20.0):
  `docker run --rm -v "$PWD":/src -w /src kf-7cg0cd-linux go test -race -count=1 ./sidecar/... ./internal/sidecarnode/... ./nodes/...`
  → ok (28.2 s / 1.3 s / 306.6 s) — the `/proc` watchdog, group kill and `WaitDelay` as CI runs them.
- `go vet ./...` and `go build ./...` clean; `gofmt -l .` (minus `web/`) prints nothing;
  `GOOS=windows go build ./sidecar/ ./internal/sidecarnode/` clean;
  `GOOS=linux go vet ./sidecar/... ./nodes/... ./cmd/kilasflow/...` clean.
- Node off PATH (`env PATH="$(dirname "$(command -v go)"):/usr/bin:/bin"`): every real-Node test SKIPs
  with "node is not on PATH; install Node 24 to run the sidecar tests", the pure-Go tests still pass;
  with `KILASFLOW_TEST_REQUIRE_NODE=1` the same command FAILs. Same behaviour for the new
  `cmd/kilasflow` tests.
- `make generate-config-reference && make generate-config-reference-check` green; `go test ./scripts/...`
  green; `make coordinates-check` green; `make docs-build` → "All internal links are valid" (44 pages).
- Docker recipe (built by hand, not in CI; it is documented in the new page): cross-compiled
  `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`, `FROM node:24-slim`, the binary, `kf-fixture-nodes` under
  `/opt/sidecar/node_modules`, a writable data dir, non-root. Booted:
  `JavaScript sidecar started node=v24.21.0 packages=[kf-fixture-nodes] nodes=2 excluded=0`, then
  `http server listening`; `GET /api/v1/node-types` returned 61 definitions, 2 with
  `source=sidecar` (`sidecar.kf-fixture-nodes.fixtureGreet@1`, `…fixtureRelay@1`). Booted with
  `KILASFLOW_SIDECAR_ENABLED=false`: no sidecar log line, a `/proc` scan found no `node` process, and
  the catalogue returned 59 definitions with 0 sidecar entries. Both containers were removed.
- The CI step itself cannot be exercised locally (GitHub-hosted runner); the first CI run is its test.

**Mutation evidence (stage 4).**

| Mutation | Test | Result |
|---|---|---|
| M-4a: `setupSidecar` ignores `enabled` | `TestSetupSidecarDisabledChangesNothing` | FAIL — `sidecar.packages_dir: no packages directory is configured` |
| M-4b: `nodeStatus` probes instead of trusting the cached verdict | `TestSidecarAvailabilityReportsAnOutageWithoutSpawningPerRequest` | FAIL — `checkNode ran 10 times behind the cached verdict, want 0` |
| M-4c: the `logWriter` mutex removed | `TestLogWriterIsSafeForConcurrentChildren` (`-race`) | FAIL — `WARNING: DATA RACE` in `logWriter.Write` |

**Pre-existing flake, not this change.** The wider scoped run produced one `internal/engine` failure,
`TestResumeOfAPerItemSuspendProcessesEveryItem` (`approval request has expired: the deadline is in the
past`), a wall-clock wait deadline. It passes alone (`ok … 2.5 s`) and 3× on a clean archive of `main`
(`5328524`, `git archive` into `/tmp`, `go test -race -count=3`) — the load-sensitive wait-service
flake already on the board (BUG-w8h3km).

**Deviations and honest limits.**

- Three additive `sidecar/` fixes were needed that the plan's stage-4 file list did not name:
  `RunnerConfig.Wrapper` (the documented `sidecar.wrapper` key was otherwise accepted and silently
  dropped), the spawn exit status [C26], and the log-writer mutex (the pool shares one writer).
- `Validate` also requires `runtime_dir` non-empty when enabled (not in the plan's list): an empty
  value would have extracted the runner into the process working directory.
- The wide `go test -race -count=1 ./...` was started but did not finish before hand-off; every package
  this change touches, plus every package in the plan's gate list, passes above (one pre-existing
  `internal/engine` flake dismissed, above).
- **Sentences for BUG-vzzkg3's pages** (not edited here): in
  `docs/src/content/docs/guides/community-nodes.md`, "Deny by default" says "until that proxy exists, a
  node that reaches past the boundary has its run refused" — the proxy now exists, so it should say the
  HTTP is proxied back through `internal/safehttp` with per-credential `allowedDomains` and only the
  other routes rest on the guard; "What the deployment looks like" names `sidecar/` and
  `fixture/echo.js` as the host reference, which is now understated (`sidecar/runner/runner.cjs`,
  `internal/sidecarnode`, `nodes/sidecar.go`, the `sidecar.*` keys, and a link to the new
  `operate/javascript-sidecar.md`); the "Three rules" list could note that all allowlisted packages
  share one process per tenant. `concepts/tenancy-and-embedding.md` has no stale sentence.
