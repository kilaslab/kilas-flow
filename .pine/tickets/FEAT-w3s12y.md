---
id: FEAT-w3s12y
title: Add isolated Go Code node with compiled WASM artifacts
status: done
priority: high
labels:
    - code
    - wasm
    - security
deps:
    - FEAT-6msqy3
parent: EPIC-c7gbdp
phase: p5
created: "2026-08-29T15:41:58Z"
updated: "2026-09-05T02:26:00Z"
---

## Scope

Implement the restricted Go Code node as a secure, cacheable WASM execution path. Compilation is an artifact lifecycle, not part of every workflow run; runtime isolation is mandatory.

## Acceptance criteria

- A registered Go Code node provides an editor/property form, source validation, stable source hash, compilation status/error, and artifact reference without storing plaintext secrets in artifacts or logs.
- Source compilation occurs when code changes, not on every workflow execution; artifacts are cached and invalidated by source/runtime version as documented.
- The WASM runtime enforces CPU/time, memory, and host capability limits; user code cannot access arbitrary filesystem, network, process, environment, or KilasFlow internal database resources.
- Execution accepts/returns the documented item data shape and persists structured/redacted node-run errors.
- Automated tests cover successful execution, compilation failure, cache reuse/invalidation, timeout, memory/capability denial, and cancellation.

## References

- PRD: §§30–32, 35, 50, 57; Milestone 5; Definition of Done item 16.
- Design reference: `03-ndv-ai-agent.png` and `04-ndv-node-settings-tab.png` for generic node-panel/settings layout only.

## Relevant documentation

- Use `find-docs` to retrieve the current wazero API/security documentation and any compiler toolchain documentation before selecting APIs. Record precise versions and sandbox assumptions in the ticket.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.

## Implementation Plan

- `internal/runcode` separates compilation from execution. Compiling Go needs the full toolchain, which cannot ship inside the distroless single binary; execution needs only the embedded wazero runtime, which can.
- Source is hashed with the runtime version, compiled once, and cached by that hash. A `Compiler` interface lets a deployment use the local toolchain, a compiler sidecar, or none at all.
- The data contract is stdin/stdout JSON rather than a host-function ABI, so the module needs nothing beyond WASI's standard streams.

## Resolved design question

The package doc flagged an open question: the Go toolchain is ~270MB and cannot ship in the distroless image. It is resolved by making the compiler a pluggable interface rather than a hard dependency. A deployment without one still executes previously cached artifacts and reports a clear "this deployment cannot compile Code nodes" status for source that has never been built, instead of failing every workflow containing a Code node. `ToolchainCompiler` covers dev, CI, and a compiler-sidecar image.

## Work Evidence

- Runtime: `github.com/tetratelabs/wazero` v1.9.0 with `wasi_snapshot_preview1`. Modules are built `GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0`.
- Sandbox assumptions, each proven rather than asserted. The module is instantiated with stdin, stdout, and stderr and **nothing else** — no `WithFS`, no `WithEnv`, no `WithArgs`, no host functions:
  - `TestUserCodeCannotReachTheFilesystem` — `os.ReadFile("/etc/passwd")` fails inside the sandbox.
  - `TestUserCodeCannotReachTheNetwork` — `net.Dial` fails inside the sandbox.
  - `TestUserCodeSeesNoEnvironment` — with `KILASFLOW_ENCRYPTION_KEY` set on the host, the module reads an empty string.
  - These matter because the wrapper deliberately *imports* `os` and `net`, so the denial is the sandbox's, not the compiler's. Relying on "it would not have compiled" would have been a much weaker guarantee — and the first version of these tests skipped for exactly that reason, which is why the wrapper now imports a useful standard library instead.
- Limits: `TestExecutionStopsAtItsTimeLimit` stops an endless loop (CPU is bounded by the same deadline), `TestMemoryPressureIsDeniedRatherThanExhaustingTheHost` denies a 4 GiB allocation inside a 2 MiB limit without touching the host, `TestOutputIsBounded` stops a module writing past its output cap, and `TestCodeNodeCannotRaiseTheDeploymentsLimits` proves a node may tighten the deployment ceiling but never raise it.
- Cancellation: `TestExecutionStopsWhenCancelled` cancels mid-run. wazero reports a deadline and a cancellation as reserved exit codes rather than context errors, so those are separated from a genuine non-zero exit before anything else is decided — without that, a timeout surfaced as "exited with status 4026531839".
- Artifact lifecycle: `TestCompilationHappensOncePerSourceAndIsInvalidatedByAChange` runs unchanged source three times and asserts exactly one build, then asserts a rebuild after the source changes. `TestArtifactCacheReportsHitsAndMisses` makes reuse observable. `TestAnArtifactFromAnOlderContractIsNotRun` proves a cached artifact from a previous `RuntimeVersion` is rebuilt rather than run against today's wrapper.
- No plaintext secrets in artifacts or logs: an artifact is a compiled module built from source only, compiler output has the build directory stripped so no server path reaches a user, and runtime errors are truncated and single-line.
- Errors are structured: a user's returned error surfaces its own message, a compilation failure is a `CompileError`, and a missing toolchain reads as a deployment problem rather than a syntax error.
- `go test ./...`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
