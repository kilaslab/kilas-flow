---
id: FEAT-7cg0cd
title: Run programmatic community nodes in a JavaScript sidecar
status: todo
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
updated: "2026-09-05T05:11:19Z"
---

## Scope

This is the deferred decision from the V2 plan, and it stays deferred until p1 through p4 have landed. The research that shaped this roadmap found that across the 100 most-viewed n8n.io templates — 2,377 node instances — only 11 were third-party `n8n-nodes-*` packages: 0.46%. A JavaScript sidecar is the most expensive thing on the roadmap and buys the least coverage, which is why WAHA went native through the routing interpreter and the OpenAPI generator instead. It earns its place only for *programmatic* community nodes, the ones with a real `execute()` that a declarative pack cannot replicate.

The prerequisites are not technical, they are evidential. Until the p0 corpus can report `imported / activatable / executable` counts and the declarative pack path has been proven against WAHA and Telegram, nobody can say which programmatic nodes are actually needed. Picking this up before then means building a Node.js runtime for a demand that has not been measured.

The licence question must be re-opened before a line of code, not after. `packages/workflow/package.json` in the 2.34.0 reference checkout carries `"license": "LicenseRef-n8n-sustainable-use"`, and so does `packages/nodes-base`. KilasFlow's own LICENSE is Apache-2.0 and the product is white-label, multi-tenant and embedded — the configuration n8n's licensing FAQ names as not allowed. A community node package does not escape this: the owner's own `n8n-nodes-mitrachat` declares `n8n-workflow` as a `peerDependency` of `>=1.0.0` and a devDependency of `^2.16.0`, so loading it means loading SUL code into a process KilasFlow ships. Whether a clean-room reimplementation of the `n8n-workflow` runtime surface is viable, and whether the sidecar is distributed at all or only installed by an operator who accepts n8n's terms themselves, is the gate on this ticket.

The runtime is Node 24 LTS, not Bun, and that is settled. `isolated-vm@6.1.2` appears in n8n 2.34.0's `pnpm-lock.yaml` as a dependency of `packages/@n8n/expression-runtime`, `packages/cli` and `packages/nodes-base`; `n8n-workflow` depends on `@n8n/expression-runtime` as a workspace package, so anything importing `n8n-workflow` pulls in a native V8 C++ addon that cannot load on Bun's JavaScriptCore. Separately, `bun build --compile` statically links LGPL-2 JavaScriptCore into the produced binary, which is not a licence posture this project wants next to its Apache-2.0 distribution.

Shipping a sidecar also ends the single-binary promise. The runtime image today is `gcr.io/distroless/static-debian12:nonroot` with a single `CGO_ENABLED=0` Go binary and no shell; a sidecar means a second image or a second stage carrying a Node runtime and an npm dependency tree. That trade is part of what this ticket decides, not a detail to discover during implementation.

## Acceptance criteria

- [ ] The licence position is recorded in `.pine/memory/licensing.md` before implementation starts, and states explicitly whether the sidecar is distributed by KilasFlow, installed by the operator, or built clean-room.
- [ ] A programmatic community node package installed by an operator loads through its `package.json` `n8n` manifest (`n8nNodesApiVersion`, `nodes`, `credentials`) and appears in the node catalogue tagged `sidecar`, distinct from `builtin` and `pack`.
- [ ] Host and sidecar speak NDJSON over a Unix domain socket. Nothing the protocol depends on is ever read from the child's stdout or stderr; a package that prints a startup banner does not corrupt a single message.
- [ ] One sidecar process serves exactly one tenant, and a test proves a second tenant's execution never reaches a process holding the first tenant's decrypted credentials.
- [ ] A sidecar that crashes, hangs, or exceeds its memory or wall-clock limit fails that node run with a diagnostic and leaves the rest of the execution and the host process intact.
- [ ] Outbound HTTP from a community node is subject to the same SSRF policy and per-credential domain scoping as a native node, or the node is refused — the boundary is never silently wider than `internal/safehttp`.
- [ ] The image and deployment consequences are documented: what the runtime image becomes, what an operator installs, and what a deployment that declines the sidecar loses.

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
