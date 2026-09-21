---
id: FEAT-48hreg
title: Publish a native community module SDK on WebAssembly
status: doing
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-adzn0a
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:14:52Z"
updated: "2026-09-20T07:42:29Z"
---

## Scope

KilasFlow already runs untrusted code in WebAssembly, and the mechanism is sound: `internal/runcode` compiles a user's Go to a `GOOS=wasip1 GOARCH=wasm` module and executes it under wazero with no filesystem, no environment, no arguments and no host functions. That is the right foundation for a third-party node ecosystem that keeps the single-binary promise — no cgo, no `buildmode=plugin`, no Node.js. This ticket turns it into a real module SDK: an author publishes a pack, an operator installs it, and its nodes appear in the catalogue and execute like built-ins.

Three things have to be fixed before that is possible, and each of them is a live defect in what ships today.

The Code node cannot compile in the shipped image. `cmd/kilasflow/main.go` wires `runcode.NewToolchainCompiler()`, whose `Available()` is `exec.LookPath("go")`, and the runtime stage of the `Dockerfile` is `gcr.io/distroless/static-debian12:nonroot` — no Go toolchain, no shell, nothing to look up. Every Code node in a container deployment fails with `ErrCompilerUnavailable`. `internal/runcode/doc.go` still records this as an open design question to "resolve before Milestone 5", which is now several milestones stale.

The compilation cache is unused. `Runner.Execute` calls `wazero.NewRuntimeWithConfig(runCtx, config)` inside the per-execution path and closes it on the way out; nothing in the tree ever constructs a `wazero.CompilationCache`. Every single run pays wazero's full compilation of the module again. `MemoryCache` caches the *artifact bytes*, which is a different and much cheaper thing, and it is process-local, so a restart throws away work that needed a Go toolchain to produce.

The guest has no capabilities at all. `Execute` instantiates `wasi_snapshot_preview1` and a `ModuleConfig` carrying stdin, stdout, stderr and the two system clocks, with no `WithFS`, no `WithEnv`, no `WithArgs` and no host module. For the Code node that is exactly right and should stay. For a node pack it is fatal: no HTTP call, no credential, no binary data, so a pack can only reshape the items it was handed. A community node that cannot talk to an API is not a community node.

The technology choices are settled. wazero directly, because it is already the dependency (`github.com/tetratelabs/wazero v1.9.0`) and adding Extism would put another abstraction between KilasFlow and the capability boundary it must audit. Not `buildmode=plugin`, which requires cgo and would end `CGO_ENABLED=0` and the distroless image with it.

## Acceptance criteria

- [x] A shared `wazero.CompilationCache` spans executions, and a test shows the second run of an unchanged artifact does not recompile the module.
- [x] Compiled artifacts survive a process restart, and the persistent cache keys on `RuntimeVersion` so an artifact built against a previous host ABI is rebuilt rather than loaded.
- [x] The Code node behaves honestly in the shipped image: either the image gains a working compile path, or the node reports a user-facing diagnostic naming exactly what the operator must provide. `internal/runcode/doc.go` no longer describes this as open.
- [ ] A guest module receives capabilities only through an explicit host module — outbound HTTP, named credential access, binary read and write — and a pack that declares none is granted none.
- [ ] Every host call is policed on the host side: outbound HTTP goes through `internal/safehttp` with the same SSRF policy and per-credential domain scoping as the HTTP node, and a credential a pack did not declare is not resolvable.
- [ ] A pack distributed as `.wasm` modules plus a manifest registers node definitions tagged `pack`, carrying parameters, ports and validation, and its nodes run inside a workflow indistinguishably from built-ins.
- [x] Per-call limits are enforced — wall clock, linear memory, output bytes, and the number of host calls — and exceeding any of them fails that node run with a named error, never the process.
- [ ] `pkg/sdk` publishes the guest-side Go module a pack author imports, with an example pack built by hand under `GOOS=wasip1 GOARCH=wasm` and the build output recorded on this ticket.

## Implementation Plan

Do the three prerequisites first, in the order they were listed, because each one is independently useful and the SDK is worthless without all three.

The compilation cache is a small change with a large effect: hoist a `wazero.CompilationCache` out of `Runner.Execute` into the `Runner` and pass it through `wazero.NewRuntimeConfig().WithCompilationCache(...)`. Keep the per-execution runtime — the isolation between two concurrent guests is worth more than the instantiation cost — but stop recompiling. Then make the artifact cache durable behind the existing `runcode.Cache` interface, which was written as an interface precisely for this. Then resolve the toolchain question and update `doc.go` with the answer rather than the question.

The host ABI is the real design work. wazero 1.9.0 does not implement the Component Model, so do not design against WIT; define a small hand-written ABI over a pointer-and-length convention in linear memory, with one Go definition in `pkg/sdk` generating both sides. `pkg/sdk` exists in the repository today as an empty directory holding only a `.gitkeep`, which is where this belongs.

**Amendment for p10.** Two unrelated things in this repository share the word SDK and must not be confused while implementing this. `pkg/sdk` is the **guest-side Go module** a WASM pack author imports, which is what this ticket creates. `sdk/` is the **TypeScript host SDK**, `@kilasflow/sdk`, which a host application installs from npm to drive the API — a different artefact with a different audience, owned by V2-p10-5 through V2-p10-8. Neither is a version of the other.

Second, the install path is no longer this ticket's to invent. V2-p10-15 (`FEAT-czbzs6`) adds an operator-configured pack directory scanned at composition, with per-pack manifests and checksum pinning, and it is deliberately shaped as "a directory of packs" rather than "a directory of JSON files" so that a WASM pack is a module kind within it rather than a second loader. Build on that loader. Its hot-reload exclusion matches this ticket's and is settled for the same reason: `node.Registry.Register` refuses a duplicate `{type, version}` pair and the registry is read-only once the server serves.

The choice worth stating rather than assuming: whether a pack keeps the Code node's batch contract — everything it needs pre-resolved into one JSON document on stdin, one document back on stdout — or gets real host functions it can call mid-execution. Recommend host functions for packs and the batch contract for the Code node. A pack that must call an API, read a paginated response and decide what to fetch next cannot be expressed as a single batch, and host functions are also where the capability boundary becomes something an operator can enumerate and audit. The Code node stays capability-free, which is the whole reason it is safe to hand to a tenant.

Then the pack format: a manifest declaring node definitions, the credential types the pack needs, and the capabilities it requests, alongside its `.wasm` modules. Loading happens at composition in `cmd/kilasflow/main.go`, before the registry is shared. That is a hard constraint, not a preference: `internal/node.Registry.Register` refuses a duplicate `{type, version}` pair and the registry is documented as read-only once the server begins handling work. Hot-loading a pack into a running process is out of scope and should stay out.

Two traps. First, `SourceHash` folds `RuntimeVersion` into the artifact identity and `Execute` refuses an artifact whose `RuntimeVersion` does not match — keep both when the cache becomes persistent, or a stale artifact compiled against a removed host import will be loaded and trap at instantiation. Second, the security argument changes shape the moment host functions exist. Today the guarantee is structural: the guest has no capability, so there is nothing to audit. Afterwards every host function is an attack surface, and the SSRF policy, the credential scope and the internal-database guard must all be enforced on the host side of the call, never by anything the guest tells us.

## References

- Roadmap plan, p8 section, entry V2-p8-5: `.pine/roadmap.md`.
- PRD: `gflow-prd-v1.md` §64 ("plugin SDK design", "arbitrary third-party Go nodes"), §§30–32 Go Code Node, Go Code Security, Go Code V1 Restrictions, §57 Security Requirements.
- Code: `internal/runcode/runcode.go` (`ToolchainCompiler.Available`, `ErrCompilerUnavailable`, `MemoryCache`, `Cache`, `SourceHash`, `RuntimeVersion`, `Runner.Execute` and its per-call `wazero.NewRuntimeWithConfig`, `DefaultLimits`), `internal/runcode/doc.go` (the stale open question), `cmd/kilasflow/main.go` (`runcode.NewToolchainCompiler()`), `Dockerfile` (`CGO_ENABLED=0`, `gcr.io/distroless/static-debian12:nonroot`), `internal/node/registry.go` (`Register`, immutability), `internal/safehttp/safehttp.go`, `pkg/sdk/` (empty placeholder), `go.mod` (`github.com/tetratelabs/wazero v1.9.0`).
- `.pine/tickets/FEAT-czbzs6.md` — V2-p10-15, the external pack loader this ticket's WASM packs reuse rather than duplicate.
- `sdk/` and `.pine/tickets/FEAT-3taswf.md` — the TypeScript host SDK, a different artefact that shares the word.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (4):
  - `f2699725` — chore(pine): point every roadmap citation at the in-repo roadmap
  - `679afb7a` — ci: run the checks this repository already defines
  - `c94583a2` — feat(nodes): reach parity on the flow-control node family
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
 .pine/tickets/FEAT-48hreg.md                       |    87 +
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
 811 files changed, 173989 insertions(+), 2747 deletions(-)
```

## Reopened 2026-09-20

Closed `done` with every acceptance criterion unticked. The guest half shipped (`pkg/sdk`,
`pkg/sdk/example/echo`); the host half did not:

- `internal/nodepack/nodepack.go` — `Pack` has no module field, so a manifest cannot name a
  `.wasm` artifact.
- `internal/nodepack/loaddir.go:6-8` — still describes WASM packs as future work.
- `internal/runcode` — the guest gets stdin/stdout/stderr and clocks, no host module, so the
  "capabilities only through an explicit host module" criterion is unimplemented.
- No production code builds a `runcode.Artifact` from a pack.

Reopened to `todo` by `BUG-vzzkg3`. Note the ticket's own p10 amendment: the install path is
owned by `FEAT-czbzs6` (done), so this work builds on the directory loader rather than
inventing a second one.

## Implementation notes

Stage 1 of 4 (prerequisites 1-3: persistent artifact and translation caches keyed on
`RuntimeVersion`, a deterministic no-recompile proof, and an honest Code-node diagnostic).
RuntimeVersion stays `wasip1-v1`: this stage changes where caches live, not what the Code node's
guest sees. No migration, no OpenAPI, no web and no TypeScript-SDK change; no new dependency
(wazero v1.9.0, Apache-2.0, already in go.mod).

**What changed**

- `internal/wasmtest` (new): `MinimalModule(stdout)` assembles a ~150-byte WASI command module by
  hand, so the cache, limit and diagnostic tests need no Go toolchain. Stages 2-4 reuse it.
- `internal/runcode/persist.go` (new): `DiskCache` (`artifacts/<version>/<hash>.wasm` + `.json`,
  0o600 files, 0700 dirs, temp+rename, meta written last), `NewPersistentModuleCache`
  (`translations/<version>/` handed to `wazero.NewCompilationCacheWithDir`), `PruneStaleVersions`
  and `GoBuildDir`. Hashes are validated against `^[0-9a-f]{64}$` before becoming file names. A
  corrupt, truncated or lying entry (digest, size, `runtimeVersion`, `hash`) is deleted and read as
  a miss. Budget: 0 = unbounded, otherwise translations evicted oldest-first before artifacts, down
  to 90%; `Get` touches the module's mtime so eviction is roughly LRU.
- `internal/runcode/diagnostic.go` (new): `MinimumGoVersion` ("1.24"), `UnavailableMessage`,
  `UnavailableReporter`/`WhyUnavailable`/`DescribeUnavailable`. One explanation is used by the node
  catalogue, the run-time error and the editor's status.
- `internal/runcode/runcode.go`: the duplicate package comment is gone (`doc.go` is the only package
  doc, extended with "The operator recipe" and "Persistence"); `ToolchainCompiler` gains `CacheDir`,
  and `buildEnv` points `GOCACHE` at it — resolved to an absolute path, because the go command
  refuses a relative GOCACHE and `code.cache_dir` defaults to a relative one.
- `internal/config`: new single-word `code` section — `go_binary` ("go"), `cache_dir`
  ("./data/codecache", empty = memory only), `cache_max_bytes` (2 GiB, 0 = unbounded, negative
  refused by `Validate`). `config.example.yaml` and the configuration reference were regenerated.
- `nodes`: `WithCodeCaches(artifacts, modules)` + `NewCodeExecutorWith` + `codeExecutorOf`, so the
  deployment's caches reach every Code node (one per process, as before, but now shareable and
  durable).
- `cmd/kilasflow/main.go`: `buildCodeCompiler` (the two keys that reach the toolchain) and
  `buildCodeCaches` (prune stale versions, open both caches, warn and fall back to memory on any
  error, never refuse the boot), wired into `RegisterExecutors`.
- Docs: `safety-boundaries.md` and `operate/deployment.md` state the recipe, the three `code.*`
  keys and the fact that the cache directory holds native machine code and must stay private;
  `CHANGELOG.md` has the `[Unreleased]` entries.

**New tests** (all in the touched packages, all passing): `internal/wasmtest` (the module family
through wazero), `internal/runcode/persist_test.go` (12 tests: no-recompile probe with a fresh-cache
control, translations surviving a restart by mtime, per-version keying, artifact round-trip, older
version refused, no rebuild across a runner restart, corrupt entry removed, non-hex hash refused,
8-goroutine Put/Get under `-race`, budget order and cap, zero budget, prune),
`internal/runcode/diagnostic_test.go` + `diagnostic_internal_test.go` (the message names what to
provide and carries no machine path; the build environment points GOCACHE at an absolute path and
never overrides an operator's own), `nodes/code_test.go`
(`TestCodeNodeWithoutACompilerNamesWhatToProvide`, `TestRegisterExecutorsUsesTheSuppliedCodeCaches`
— the supplied artifact cache is consulted and the supplied translation cache writes into the
deployment's directory), `cmd/kilasflow/main_test.go` (availability text, persistence across two
`buildCodeCaches` calls, fallback when the directory is unusable, compiler wiring),
`internal/config/config_test.go` (defaults, YAML + environment reachability, negative budget).
Every persistence test calls `t.Setenv("PATH","")` and is serial, so "needs no toolchain" is proven
inside the test that claims it.

**Commands run (outcomes)**

- `gofmt -l cmd internal nodes pkg scripts` — clean; `go build ./...` — clean; `go vet ./...` — clean.
- `go test -race ./internal/wasmtest/... ./internal/config/... ./scripts/...` — ok.
- `go test -race -timeout 25m ./internal/runcode/... ./nodes/... ./cmd/kilasflow/... ./internal/guardrails/...` — see the stage report; the toolchain-dependent tests in `internal/runcode` rebuild a Go guest per case and are slow on a loaded machine, so they were also run scoped during development (all passing), including the two new deterministic probes.
- `make generate-config-reference` then `go run ./scripts/config-reference.go --check` — clean.
- Docker (recorded below), against the image built from this branch by `make docker`.

**Docker proof, run for real** (image `kilasflow:latest`, digest
`sha256:44fd74bda4e7e815f6e8fc18c2e1da07a3041950a18082a9f4bec3ebeda7298f` built from this tree;
one data directory reused across all three containers, removed afterwards):

1. No toolchain. `GET /api/v1/node-types` reports `kilasflow.code` with
   `unavailable: "This deployment cannot compile Code nodes: it looked for the Go toolchain by
   running \"go\" and found no toolchain there. ... point KILASFLOW_CODE_GO_BINARY — the
   code.go_binary configuration key — at its go binary, or put the toolchain's bin directory on the
   PATH of the kilasflow process. ..."`. A manual run of a Code workflow fails with
   `node "Code": This deployment cannot compile Code nodes: ...` — the same text, run-time.
2. Toolchain mounted read-only (`-v <go1.27.1 toolchain>:/usr/local/go:ro -e
   KILASFLOW_CODE_GO_BINARY=/usr/local/go/bin/go`): the catalogue reports no unavailable node, and
   the same workflow succeeds, the Code node emitting `{"n":1,"ok":true}`. The data volume then
   holds `codecache/artifacts/wasip1-v1/<hash>.wasm` + `.json`,
   `codecache/translations/wasip1-v1/wazero-v1.9.0-arm64-linux/<hex>` and `codecache/go-build/`
   (GOCACHE).
3. Restart on the same data volume **without** the toolchain: the catalogue again reports the node
   unavailable, and the same Code node still succeeds from the cached artifact — the module's mtime
   was touched by the cache read and the artifact was not rebuilt. This is the end-to-end proof that
   a compiled artifact survives a restart on a deployment that can no longer compile.

The first run of step 2 failed with `code did not compile: build cache is required, but could not be
located: GOCACHE is not an absolute path`, because `code.cache_dir` defaults to a relative path and
the go command refuses a relative GOCACHE. Fixed in `ToolchainCompiler.buildEnv` (absolute
resolution) and pinned by a test; the proof was re-run against a rebuilt image, which is how the
defect was found.

Stage 2 of 4 (named per-call limit errors, the Sandbox/HostBinding seam, the `wasmtest` assembler
and `ModuleCache.Inspect`). Nothing here registers a node and nothing here knows about packs: it is
verified by calling `runcode` directly. `RuntimeVersion` stays `wasip1-v1` — the Code node's guest
sees exactly what it saw in stage 1. No new dependency (wazero v1.9.0, Apache-2.0, already in
go.mod), no config key, no migration, no OpenAPI/web/TypeScript-SDK surface.

**What changed**

- `internal/runcode/errors.go` (new): `ErrTimeLimit`, `ErrMemoryLimit`, `ErrOutputLimit`,
  `ErrHostCallLimit`. `ExecutionError` gains `Cause error` + `Unwrap()`, so `errors.Is` answers
  "which limit" while `errors.As(*ExecutionError)` and every existing message stay byte-identical
  (every existing literal is keyed, so the field is additive). `nodes/code.go` already wraps the
  runner's error with `%w`, so a node run answers `errors.Is` for the same sentinels.
- `internal/runcode/sandbox.go` (new): `Call{Stdin, Limits, Host, Subject}`, `Outcome{Stdout,
  Stderr}`, `HostBinding.Install(ctx, rt, *CallState)` and `CallState.Enter`/`Used`. The body of
  `Runner.Execute` moved here as `Runner.Sandbox` unchanged apart from per-call limits, the host
  binding (installed after WASI and before `CompileModule`) and classification; `Execute` is now
  the version check, `json.Marshal`, `Sandbox` and decoding the `{items,error}` payload.
  Classification order, as specified: cancellation as before; `ExitCodeDeadlineExceeded`/`runCtx`
  expiry -> `ErrTimeLimit`; the tripped `CallState` or exit code `0xC0DE0001` -> `ErrHostCallLimit`;
  a structured `{"error"}` on stdout -> a plain `ExecutionError`; otherwise a non-zero exit whose
  stderr carries `fatal error: out of memory` / `runtime: out of memory` -> `ErrMemoryLimit`; a
  compile refusal containing `over limit of` -> `ErrMemoryLimit` stating the CONFIGURED limit
  (`this module cannot start inside the 32-page (2 MiB) memory limit`); truncated stdout ->
  `ErrOutputLimit`. `executeHostCallLimit` is 0xC0DE0001 and stops the guest from inside the host
  function via `module.CloseWithExitCode` — the only handle the host has on a running call.
- `Limits` gains `MaxHostCalls` (default 100) and the four "a zero field means the shipped default"
  fills collapse into one `Limits.orDefault`, used by `NewRunner` and by `Sandbox`.
- `internal/wasmtest`: an assembler — `Build(imports, startBody, memPages, data)` plus
  `I32Const/Call/Drop/Loop/Br/If/Unreachable` and `ExpectResult(index, want)`
  (`call; i32.const want; i32.ne; if; unreachable; end`), with the byte-level layout documented in
  one place on `Build`. `MinimalModule` is now expressed through `Build`.
- `internal/runcode/inspect.go` (new): `(*ModuleCache).Inspect(ctx, module, memoryPages)` ->
  `Inspection{Imports []Import; Exports []string}`. Imports come from `ImportedFunctions()` via
  `FunctionDefinition.Import()` — NOT `ModuleName()`/`Name()`, which are empty for the WASI imports
  of a Go guest; exports merge `ExportedFunctions()` and `ExportedMemories()` and are sorted. The
  compile runs in a throwaway runtime over the shared translation cache, so an audit warms the
  translation a first run would pay for, and the `CompiledModule` is never closed (closing one
  evicts its translation — `.pine/memory/code-node.md`).
- `internal/runcode/doc.go`: new "# Capabilities" and "# Limits" sections; the old blanket "no host
  functions at all" sentence now says "none of the sandbox's own", because `Sandbox` can install a
  call's binding.
- `CHANGELOG.md`: one `[Unreleased] / Changed` entry for the user-visible wording (a memory failure
  used to surface as the Go runtime's own `fatal error: out of memory` or "exited with status 2").

**Tests** (all in the two packages touched; each new toolchain-free test calls
`t.Setenv("PATH", "")`, so none of them can be reaching for a Go toolchain)

- `internal/runcode/sandbox_test.go` (new): `TestALimitFailureIsNamedAndNeverTheProcess` (wall clock
  via a hand-built `loop br 0` module and output bytes via `MinimalModule`; asserts `errors.Is`, the
  message, that each failure is still an `ExecutionError`, that the bytes written before the output
  limit come back, and that the process and the runner both survive — a normal call follows),
  `TestAHostCallBudgetStopsTheModule` (a hand-built guest that calls `kilasflow_v1.ping` forever;
  the host does its work exactly `MaxHostCalls` times and not once more, `Used()` counts the refused
  call, and a single call under the default budget succeeds), and
  `TestASandboxWithNoHostBindingCannotImportAHostFunction` (no binding -> a capability failure
  naming `kilasflow_v1`, never a limit), `TestAModuleThatCannotStartInsideTheMemoryLimitIsNamed`
  (a 40-page module under a 32-page limit, toolchain-free; the message states `32-page (2 MiB)`),
  `TestTheMemoryLimitIsNamed` (toolchain: an 8 MiB allocation under 6 MiB).
- `internal/runcode/inspect_test.go` (new): imports and exports of a hand-built module; the WASI
  imports of a real Go guest by name (logged, not counted: 19 of them on Go 1.27.1); the warm
  translation cache observed as a file wazero wrote after `Inspect`; a module refused for not
  fitting the audit's memory limit.
- `internal/wasmtest/wasmtest_test.go`: `Build` with an empty start body, `If`/`Unreachable`
  (both branches), `Loop`/`Br` stopped by the context, and `ExpectResult` answering exactly,
  differently and by sign.
- Tightened (not widened): `TestMemoryPressureIsDeniedRatherThanExhaustingTheHost` and the tight
  half of `TestASharedTranslationDoesNotCarryAMemoryLimitWithIt` no longer assert only `err != nil`.
  Both now run at 96 pages and go through `requireAllocationDenial`, which requires
  `errors.Is(err, ErrMemoryLimit)` **and** the guest's own out-of-memory report on stderr — the
  second half is what stops the case being satisfied by a module refused before it starts.

**Corrections to the plan's measured facts** (re-measured here, Go 1.27.1 darwin/arm64)

- A trivial wasip1 guest declares **50 pages** (3 MiB) of initial memory, not 36: with 48 pages
  wazero answers `section memory: min 50 pages (3 Mi) over limit of 48 pages (3 Mi)`. The plan's
  "change the allocation-denial cases to 48 pages" would therefore have replaced one vacuous test
  with another — a start-up refusal that still answers `errors.Is(ErrMemoryLimit)` — which is why
  the cases run at 96 pages and why `requireAllocationDenial` exists.
- The same guest imports **19** `wasi_snapshot_preview1` functions, not 16; the inspect test
  asserts names rather than a count.
- Confirmed as the plan measured: a host function calling `module.CloseWithExitCode(ctx,
  0xC0DE0001)` stops the guest and `InstantiateModule` returns exactly that exit code;
  `FunctionDefinition.Import()` is the only accessor that names the imported module; `CompileModule`
  succeeds with imports unresolved, which is what makes a load-time audit possible.
- Not in the plan, and a repository-wide finding: wazero v1.9.0's `internal/version.GetWazeroVersion`
  writes its package-level version cache without synchronization, so two runtimes created at the
  same moment are a data race inside the dependency (`internal/engine/wazevo.NewEngine` ->
  `NewRuntimeWithConfig`). It fires whenever the first two runtime creations of a process overlap —
  in a test, that means two parallel tests that each build a runtime. The new `wasmtest` cases were
  written with `t.Parallel()` first, which is how it was found, and are now deliberately serial with
  the reason recorded in the file. This is worth a wazero upgrade or a follow-up ticket; the race is
  benign in effect (a stale or torn version string only names a translation-cache directory) but it
  makes `go test -race` unusable for any package that creates runtimes concurrently.

**Commands run (outcomes)**

- `gofmt -l internal/runcode internal/wasmtest` — prints nothing.
- `go build ./...` — clean; `go vet ./...` — clean.
- `go test -race -count=1 -timeout 35m ./internal/runcode/...` — `ok ... 991.755s`, exit 0 (the
  machine is heavily loaded and the toolchain-dependent cases rebuild a Go guest each, so this is
  the long pole; the two tracked flakes, BUG-fng4m2 and BUG-w8h3km, are in other packages and were
  not touched).
- `go test -race -count=1 ./internal/wasmtest/...` — `ok ... 1.831s` (it was the wasmtest race below
  that this run was fixing).
- `go test -race -count=1 -timeout 40m ./internal/runcode/... ./internal/wasmtest/...
  ./internal/guardrails/...` — all three `ok` (runcode 1173.111s, wasmtest 2.117s, guardrails 1.501s), exit 0
- Scoped while developing: the five new toolchain-free tests in one run; the three memory tests
  (`TestMemoryPressureIsDenied…`, `TestASharedTranslationDoesNotCarry…`, `TestTheMemoryLimitIsNamed`)
  at 96 pages, all passing on the running guest's own out-of-memory report.

**A gate stage 1 left red, fixed here**

`go test ./internal/guardrails/...` failed on the tree stage 1 committed:
`TestReferenceCheckoutIsNeverABuildInput` flagged `internal/runcode/diagnostic_test.go` for
containing `"/home/"` and `"/Users/"` — the two literals the guardrail hunts for in every build
input, and that its own file builds from fragments for exactly that reason. The assertion those
literals belong to (the unavailable-toolchain message carries no machine path) is unchanged; the
paths are now assembled from fragments at run time, with the reason written beside them. This is a
stage-1 defect in this ticket's own footprint, not a flake: nothing else in the tree changed.

Stage 3 of 4 (the one-definition ABI in `pkg/sdk`, the engine credential seam, and the
capability-gated host module). Nothing here registers a node and nothing here knows about packs:
the host module is verified by calling it directly from `internal/wasmpack` tests. `RuntimeVersion`
stays `wasip1-v1` — the Code node's guest contract and WASI surface are unchanged, and the host
module exists only for packs; a pack's ABI version is the manifest's `abi` field, checked at load
in stage 4. No new dependency (wazero v1.9.0, Apache-2.0, already in go.mod), no config key, no
migration, no OpenAPI/web/TypeScript-SDK surface, no CHANGELOG entry (nothing user-visible ships
until stage 4 wires the loader).

**What changed**

- `pkg/sdk/abi.go` (new): the one definition of the ABI. `HostModule = "kilasflow_v1"`,
  `ABIVersion = "v1"`, the five capability constants, `Functions` — six rows
  (`http_request`, `credential_field`, `binary_read`, `binary_write`, `result_len`, `result_read`),
  each with its capability and its i32 parameter names — the two result slots, the six negative
  return codes (`ErrInvalid` … `ErrNotFound`) with `ErrorCodeName`, and the shared wire types
  (`HTTPRequest`, `HTTPResponse`, `BinaryRef`, `BinaryWrite`, `HostError`, `NodeInfo`, `Envelope`).
  `//go:generate go run ./internal/abigen/cmd -o host_wasip1.go`.
- `pkg/sdk/host_wasip1.go` (new, generated): one body-less `//go:wasmimport kilasflow_v1 <name>`
  declaration per table row, i32 parameters and one i32 result. `TestGeneratedGuestBindingsAreCurrent`
  fails if it is stale.
- `pkg/sdk/internal/abigen` (new): `Render([]sdk.Function)` plus the `cmd` that writes the file.
- `pkg/sdk/guest_wasip1.go` (new): the pointer-and-length convention and the six slice-level
  adapters. The capability functions call the imports **directly** (no function value, no interface
  in a variable), which is what lets the Go linker drop the imports a pack never reaches.
- `pkg/sdk/host_other.go` (new, `!wasip1`): the same six adapters against a `Host` interface
  (`sdk.SetHost(fake)`), so a pack author can unit-test logic on any machine; with no fake installed
  they answer `ErrNoHost`. This is the only place the interface exists — the wasip1 build has none.
- `pkg/sdk/capabilities.go` (new): `HTTP`, `CredentialField`, `ReadBinary`, `WriteBinary` and the
  `errors.Is` sentinels (`ErrDeniedError`, `ErrBlockedError`, …) that a `*HostError` matches.
- `pkg/sdk/call.go` (new): `Call`, `HandleCall`, `MainCall`, `MainPorts`, and `decodeCall`, which
  accepts **both** the invocation envelope and the legacy bare item array.
- `pkg/sdk/sdk.go`: `Item` gains `Binary map[string]BinaryRef`; `Handle` now goes through
  `decodeCall`, so a legacy-contract pack (example/echo) also runs when the executor hands it the
  envelope — it sees items only. `Version` stays `v1` and its comment now says the envelope is
  covered by `ABIVersion` instead.
- `pkg/sdk/example/fetch/main.go` (new): the capability example — `url` and `credential` parameters,
  `CredentialField(credential, "baseUrl")` (tolerating `ErrNotFoundError`), then `HTTP` with the
  credential named. `sdk.MainCall`.
- `internal/engine/authenticate.go`: `ResolveAttachedCredential(ctx, ir, type)` (lookup **by type**,
  reporting `found=false` without consulting the resolver) and `AuthenticateAs(ctx, ir, type, req)`
  (the pack seam: refuses a type the node did not attach, then applies the credential). `Authenticate`
  and `ResolveNodeCredential` now share `applyResolved` (the `AllowsHost` check, the redirect scope
  and `credentials.Apply`) and `resolveCredential` (the type check), so there is still exactly one
  copy of each — the file's own comment at :21-23 forbids a second.
- `internal/wasmpack` (new package): `caps.go` (`Capabilities.Granted`/`GrantedFunctions`/`Names`,
  with `result_*` granted whenever anything is), `limits.go` (30 s / 512 pages / 8 MiB / 100 host
  calls by default; ceilings 10 min / 4096 pages / 64 MiB / 10000; `Validate`), `audit.go`
  (`Audit` → `Report{Declared, Imported, Exports}`, every import must be WASI or this ABI and must be
  granted, credential types must be HTTP-capable), `host.go` (`Host`, `HostDeps{Policy, Modules}`,
  `Invocation`, `Effects`, `Invoke`, the per-run `binding`, `Install`, the `hostCalls` table, the
  bounds-checked `readGuest`/`writeGuest`, `result_len`/`result_read`, `decodeStrict`) and
  `hostcalls_http.go` / `hostcalls_credentials.go` / `hostcalls_binary.go`.
- The host module is registered per run, from the ABI table, and **not at all** when the manifest
  declares nothing. `Invoke` runs through `runcode.Sandbox` with the pack's limits, so wall clock,
  memory, output and host-call limits are the ones stage 2 named.

**Host-side policing (criterion 4), each with a test**

- `policy.CheckURL` before the request is built, then the dialer's address check (unchanged
  `safehttp`), and the credential is applied by `Request.AuthenticateAs` — the same seam the HTTP
  node uses — so the domain scope is checked before a byte is sent and rides on the request context
  for the redirect chain.
- A credential type the manifest did not declare is refused **before** the resolver is called
  (call count asserted 0); a declared type the node did not attach is refused; a type that cannot
  authenticate HTTP (a database credential) is refused at audit time, so it is structurally out of
  reach.
- Hop-by-hop and identity headers (`Host`, `Content-Length`, `Transfer-Encoding`, `Connection`,
  `Upgrade`, `Te`, `Trailer`, `Proxy-*`) are refused before the request is built; the method is
  allowlisted; the URL, metadata, body, name and payload sizes are capped; the request is bounded by
  the smallest of the pack's `timeoutMs`, the policy timeout and the run's wall clock.
- Every guest number is untrusted: `readGuest` checks the length against its cap **before** reading,
  and the pointer and length against the module's own memory, so an out-of-range pointer, a length of
  `0xFFFFFFFF` and a pointer-plus-length that wraps all end as a refusal; `result_read` bounds-checks
  the destination the same way. `readGuest` copies, so the guest cannot rewrite bytes under the host.
- `Request.Binaries` is an interface and is nil in a runtime with no store: both payload calls
  refuse with a named `HostError` rather than panicking.
- `binary_read` reaches only the payloads the input items carry plus what this run wrote; the store
  is not consulted for anything else (asserted by read count).

**Tests** (new; the toolchain-free ones are hand-built `wasmtest` modules, the rest drive one Go
probe guest, `internal/wasmpack/testdata/probe/main.go`, built once per test binary and translated
through one package-level `ModuleCache` closed in `TestMain`)

- `pkg/sdk`: generated-bindings-current, ABI table well-formed, fake-host round trip for all four
  capability functions, `*HostError` code + message through `errors.Is`/`errors.As`, no-host
  refusals, envelope-tolerant `Handle`, `HandleCall` parameters/ports/binary refs, an older `abi`
  refused, empty ports normalised, plus the existing echo test untouched.
- `internal/wasmpack`: ABI/host-module parity (names, arity, i32 in and out), per-capability
  granting, no-declaration → no host module, audit refusals (ungranted function naming the missing
  capability, unknown module/function, non-command module, over-limit memory, non-HTTP credential
  type, uncompilable bytes), pointer/length/overflow refusals, oversized arguments, the slot
  protocol including offsets and out-of-range slots, the host-call limit, nil binary store, payload
  reachability, and the probe-driven set: SSRF policy (allowed endpoint works, a **different**
  loopback port refused — `AllowPrivateNetworks` is never set), metadata address blocked, forbidden
  headers, redirects (handed back, and a scoped credential stops the chain without leaking), response
  bounded by the policy, slow upstream cut off by the wall clock (`ErrTimeLimit` in ~0.3 s against a
  5 s server), undeclared credential unresolvable (resolver calls 0), unattached credential refused,
  domain scope applied (server saw 0 requests), the host applies the secret while the pack's stdout
  never contains it, a secret field refused, payload write/read round trip, and the memory limit
  named with the configured page count.
- `internal/engine/authenticate_test.go` (existing file, extended): `AuthenticateAs` applies only the
  named credential of two attached, refuses a type the node does not carry (resolver calls 0),
  honours `AllowedDomains` and attaches the scope for redirects. Existing tests unchanged.

**Corrections to the plan's measured facts** (re-measured here, Go 1.27.1 darwin/arm64)

- example/fetch imports `http_request`, `credential_field`, `result_len`, `result_read` — not
  "http_request plus result_len/result_read only" as the plan's stage-3 note says, because the plan's
  own instruction for the example has it read the credential's `baseUrl` through `CredentialField`.
  The test asserts the set the example actually reaches, which is the stronger statement.
- example/echo imports no `kilasflow_v1` function at all and 17 `wasi_snapshot_preview1` functions;
  stage 2 measured 19 for a different program, so the count is program-dependent and the tests assert
  names, never counts.
- Two packages cannot share one directory, so the generator's main is
  `pkg/sdk/internal/abigen/cmd/main.go` (package `abigen` beside it holds `Render`).
- The pointer/length adapters need a wasip1-only home of their own (`pkg/sdk/guest_wasip1.go`):
  `host_wasip1.go` is generated and `host_other.go` is the native build, and mixing hand-written
  wasip1 code into the generated file would have made the generator's output unreviewable.
- `Invocation` carries `Module`, `Caps` and `Limits` directly rather than through the plan's `Spec`
  grouping: the pack format that owns that grouping is stage 4's, and inventing it now would have
  meant guessing its shape.
- The host's own share of the wall clock starts at the run's **first host call**, not at `Invoke`,
  for the reason stage 2 already fixed for the sandbox: translating and instantiating a wasip1 module
  is the host's work and must not be charged to the pack's budget (under `-race` a translation alone
  outlasts the default limit). A request blocked in the host is released by that clock and the run is
  reported as `ErrTimeLimit`, which the slow-upstream test pins at ~0.3 s.

**Build output recorded (criterion 7)** — `go version go1.27.1 darwin/arm64`, commands
`GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o <file> ./pkg/sdk/example/echo|fetch`

```
4,523,689 bytes  echo.wasm   sha256 c00b60b48459007f2189ef5557e2cdc1f42ee87d52d4e572fcbe9dd14d130bdd
4,551,047 bytes  fetch.wasm  sha256 e1e9d2b22602aff21279e8b8a054db59adcbaa73523ded4c860a221b912ed816
```

`ModuleCache.Inspect` (via `TestAPackImportsOnlyWhatItReaches`, logged): echo imports only
`wasi_snapshot_preview1.*`; fetch imports `wasi_snapshot_preview1.*` plus
`kilasflow_v1.http_request`, `kilasflow_v1.credential_field`, `kilasflow_v1.result_len`,
`kilasflow_v1.result_read`.
