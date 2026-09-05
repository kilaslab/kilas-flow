---
id: FEAT-48hreg
title: Publish a native community module SDK on WebAssembly
status: todo
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
updated: "2026-09-05T05:14:52Z"
---

## Scope

KilasFlow already runs untrusted code in WebAssembly, and the mechanism is sound: `internal/runcode` compiles a user's Go to a `GOOS=wasip1 GOARCH=wasm` module and executes it under wazero with no filesystem, no environment, no arguments and no host functions. That is the right foundation for a third-party node ecosystem that keeps the single-binary promise — no cgo, no `buildmode=plugin`, no Node.js. This ticket turns it into a real module SDK: an author publishes a pack, an operator installs it, and its nodes appear in the catalogue and execute like built-ins.

Three things have to be fixed before that is possible, and each of them is a live defect in what ships today.

The Code node cannot compile in the shipped image. `cmd/kilasflow/main.go` wires `runcode.NewToolchainCompiler()`, whose `Available()` is `exec.LookPath("go")`, and the runtime stage of the `Dockerfile` is `gcr.io/distroless/static-debian12:nonroot` — no Go toolchain, no shell, nothing to look up. Every Code node in a container deployment fails with `ErrCompilerUnavailable`. `internal/runcode/doc.go` still records this as an open design question to "resolve before Milestone 5", which is now several milestones stale.

The compilation cache is unused. `Runner.Execute` calls `wazero.NewRuntimeWithConfig(runCtx, config)` inside the per-execution path and closes it on the way out; nothing in the tree ever constructs a `wazero.CompilationCache`. Every single run pays wazero's full compilation of the module again. `MemoryCache` caches the *artifact bytes*, which is a different and much cheaper thing, and it is process-local, so a restart throws away work that needed a Go toolchain to produce.

The guest has no capabilities at all. `Execute` instantiates `wasi_snapshot_preview1` and a `ModuleConfig` carrying stdin, stdout, stderr and the two system clocks, with no `WithFS`, no `WithEnv`, no `WithArgs` and no host module. For the Code node that is exactly right and should stay. For a node pack it is fatal: no HTTP call, no credential, no binary data, so a pack can only reshape the items it was handed. A community node that cannot talk to an API is not a community node.

The technology choices are settled. wazero directly, because it is already the dependency (`github.com/tetratelabs/wazero v1.9.0`) and adding Extism would put another abstraction between KilasFlow and the capability boundary it must audit. Not `buildmode=plugin`, which requires cgo and would end `CGO_ENABLED=0` and the distroless image with it.

## Acceptance criteria

- [ ] A shared `wazero.CompilationCache` spans executions, and a test shows the second run of an unchanged artifact does not recompile the module.
- [ ] Compiled artifacts survive a process restart, and the persistent cache keys on `RuntimeVersion` so an artifact built against a previous host ABI is rebuilt rather than loaded.
- [ ] The Code node behaves honestly in the shipped image: either the image gains a working compile path, or the node reports a user-facing diagnostic naming exactly what the operator must provide. `internal/runcode/doc.go` no longer describes this as open.
- [ ] A guest module receives capabilities only through an explicit host module — outbound HTTP, named credential access, binary read and write — and a pack that declares none is granted none.
- [ ] Every host call is policed on the host side: outbound HTTP goes through `internal/safehttp` with the same SSRF policy and per-credential domain scoping as the HTTP node, and a credential a pack did not declare is not resolvable.
- [ ] A pack distributed as `.wasm` modules plus a manifest registers node definitions tagged `pack`, carrying parameters, ports and validation, and its nodes run inside a workflow indistinguishably from built-ins.
- [ ] Per-call limits are enforced — wall clock, linear memory, output bytes, and the number of host calls — and exceeding any of them fails that node run with a named error, never the process.
- [ ] `pkg/sdk` publishes the guest-side Go module a pack author imports, with an example pack built in CI under `GOOS=wasip1 GOARCH=wasm`.

## Implementation Plan

Do the three prerequisites first, in the order they were listed, because each one is independently useful and the SDK is worthless without all three.

The compilation cache is a small change with a large effect: hoist a `wazero.CompilationCache` out of `Runner.Execute` into the `Runner` and pass it through `wazero.NewRuntimeConfig().WithCompilationCache(...)`. Keep the per-execution runtime — the isolation between two concurrent guests is worth more than the instantiation cost — but stop recompiling. Then make the artifact cache durable behind the existing `runcode.Cache` interface, which was written as an interface precisely for this. Then resolve the toolchain question and update `doc.go` with the answer rather than the question.

The host ABI is the real design work. wazero 1.9.0 does not implement the Component Model, so do not design against WIT; define a small hand-written ABI over a pointer-and-length convention in linear memory, with one Go definition in `pkg/sdk` generating both sides. `pkg/sdk` exists in the repository today as an empty directory holding only a `.gitkeep`, which is where this belongs.

The choice worth stating rather than assuming: whether a pack keeps the Code node's batch contract — everything it needs pre-resolved into one JSON document on stdin, one document back on stdout — or gets real host functions it can call mid-execution. Recommend host functions for packs and the batch contract for the Code node. A pack that must call an API, read a paginated response and decide what to fetch next cannot be expressed as a single batch, and host functions are also where the capability boundary becomes something an operator can enumerate and audit. The Code node stays capability-free, which is the whole reason it is safe to hand to a tenant.

Then the pack format: a manifest declaring node definitions, the credential types the pack needs, and the capabilities it requests, alongside its `.wasm` modules. Loading happens at composition in `cmd/kilasflow/main.go`, before the registry is shared. That is a hard constraint, not a preference: `internal/node.Registry.Register` refuses a duplicate `{type, version}` pair and the registry is documented as read-only once the server begins handling work. Hot-loading a pack into a running process is out of scope and should stay out.

Two traps. First, `SourceHash` folds `RuntimeVersion` into the artifact identity and `Execute` refuses an artifact whose `RuntimeVersion` does not match — keep both when the cache becomes persistent, or a stale artifact compiled against a removed host import will be loaded and trap at instantiation. Second, the security argument changes shape the moment host functions exist. Today the guarantee is structural: the guest has no capability, so there is nothing to audit. Afterwards every host function is an attack surface, and the SSRF policy, the credential scope and the internal-database guard must all be enforced on the host side of the call, never by anything the guest tells us.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p8, V2-p8-5.
- PRD: `gflow-prd-v1.md` §64 ("plugin SDK design", "arbitrary third-party Go nodes"), §§30–32 Go Code Node, Go Code Security, Go Code V1 Restrictions, §57 Security Requirements.
- Code: `internal/runcode/runcode.go` (`ToolchainCompiler.Available`, `ErrCompilerUnavailable`, `MemoryCache`, `Cache`, `SourceHash`, `RuntimeVersion`, `Runner.Execute` and its per-call `wazero.NewRuntimeWithConfig`, `DefaultLimits`), `internal/runcode/doc.go` (the stale open question), `cmd/kilasflow/main.go` (`runcode.NewToolchainCompiler()`), `Dockerfile` (`CGO_ENABLED=0`, `gcr.io/distroless/static-debian12:nonroot`), `internal/node/registry.go` (`Register`, immutability), `internal/safehttp/safehttp.go`, `pkg/sdk/` (empty placeholder), `go.mod` (`github.com/tetratelabs/wazero v1.9.0`).
