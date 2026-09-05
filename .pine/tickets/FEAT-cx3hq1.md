---
id: FEAT-cx3hq1
title: Stand up the browser end-to-end harness
status: todo
priority: high
labels:
    - e2e
    - testing
deps:
    - FEAT-7tgasa
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:01:57Z"
updated: "2026-09-05T12:01:57Z"
---

## Scope

There is no browser test infrastructure of any kind. `web/package.json` declares `vitest` and nothing else — no Playwright, no Cypress, no `@playwright/test` — and there is no `e2e` directory anywhere in the tree. The only artefacts resembling browser evidence are stray console logs and screenshots under `.playwright-mcp/`, produced by an interactive session rather than by a suite.

Everything the project verifies today stops at a boundary the user never sees. Go tests cover the engine, the compiler, the importer and the handlers. The four smoke scripts boot a real binary and assert `/api/v1/health`, `/api/openapi.json` and that the SPA fallback returns `<!doctype html>` — which proves the page is served, not that it works. `vitest` covers isolated frontend units. Between "the server answers" and "a person can build a workflow and watch it run" there is no coverage at all.

That gap is where this product's whole value sits. The editor is a Svelte Flow canvas with a node picker, a parameter panel driven by server-supplied property metadata, an expression editor, a credential picker, an execution event stream over SSE, and an embedded mode with a postMessage handshake. Every one of those is a place where a server change lands silently: `node.Definition` is serialized directly as the `/api/v1/node-types` payload, so any p2 metadata change reshapes the editor's forms without touching editor code.

This ticket builds the harness only. The suites that use it are V2-p11-3 through V2-p11-7, and each is separately scoped so this one does not become a bottleneck.

Three properties the harness must have, because they decide whether the suites are worth trusting.

**A real binary, a real database, a real SPA.** The value of an end-to-end test here is that it exercises the same composition path `cmd/kilasflow/main.go` builds — registry, executors, routing, webhook bindings, lifecycle verification. A harness that mocks the API tests the editor against a fiction of the server, which is precisely the drift the repository already spends effort preventing.

**Isolation per test.** `execution.max_concurrent` defaults to 10 and the scheduler runs; tests that share a database will interfere in ways that read as flakiness. Each test needs its own data directory, its own port and its own workflows.

**Determinism without mocking the product.** Real outbound calls to third-party APIs make a suite that fails on somebody else's outage. The seam already exists and is the right one: `internal/safehttp` governs every outbound request, so a local stub server plus an `allowed_hosts` entry gives determinism without weakening anything the product does.

## Acceptance criteria

- [ ] A Playwright project exists with a fixture that builds the binary and the SPA once, then boots a fresh instance per test file with its own port and data directory.
- [ ] A test can seed a tenant, a workflow, a credential and a schedule without driving the UI, so a test about the editor is not also a test about the create form.
- [ ] Outbound HTTP from a workflow under test reaches a local stub rather than the internet, configured through the existing `outbound.allowed_hosts` policy rather than by disabling the guard.
- [ ] The harness waits on observable application state — an execution status, a rendered node, an SSE event — and contains no fixed sleeps.
- [ ] A failing test emits a trace, a screenshot and the server's log for that instance, and the artefacts identify which instance produced them.
- [ ] Running the suite twice in a row from a dirty working tree produces the same result, and running it in parallel does not produce cross-test interference.
- [ ] `make test-e2e` runs the suite the same way locally and in the V2-p10-1 pipeline.
- [ ] The harness covers the embedded editor as well as the dashboard, since the embed frame is a separate route with its own postMessage handshake.

## Implementation Plan

Put the project in `e2e/` at the repository root rather than inside `web/`. It tests the assembled product — Go binary plus embedded SPA — not the frontend package, and `web/`'s `vitest` suite should stay a unit suite.

Build once, boot many. `scripts/openapi-spec.mjs` already demonstrates the exact pattern this needs: build a binary into a temp directory, reserve a free port by opening and closing a listener, spawn with `-config ''`, poll `/api/v1/ready`, tear down with SIGTERM then SIGKILL. Reuse that logic rather than reinventing it; the port-reservation detail in particular is what stops parallel workers colliding.

Note the constraint that a Go build is now a prerequisite of the frontend test suite, and that the SPA must be built into `internal/web/dist` before the binary is compiled or the embedded app will be the placeholder page. `make build-all` does both in the right order; a harness that runs `go build` alone will serve "SPA not built" and produce failures that look like editor bugs.

For determinism, prefer a stub over interception. Playwright's request interception operates in the browser, and the requests that matter here — a workflow's HTTP node calling an API — are made by the Go process, where the browser cannot see them. The stub must be a real HTTP server the workflow reaches, allowed through `outbound.allowed_hosts`. Do not set `allow_private_networks: true` globally to make this easier; that turns off the guard the product exists to have, and a suite running with a different security posture than production is a suite that cannot catch a security regression.

Seeding should go through the API rather than the database. It is the supported surface, it is what a host application would do, and a seeder writing GORM rows directly would need updating every time p6's migrations change the schema.

One decision to settle rather than default: whether the suite runs against a built container image or a locally built binary. Recommend the binary for the developer loop and the image in the pipeline once V2-p10-2 publishes one — the image is what ships, and `smoke-docker` already proves the pattern of testing a container.

## References

- Roadmap plan, p11 section, entry V2-p11-1: `.pine/roadmap.md`.
- `scripts/openapi-spec.mjs` — the build, free-port reservation, readiness poll and teardown pattern to reuse.
- `scripts/smoke-sqlite.sh`, `scripts/smoke-docker.sh` — the existing end-to-end assertions this suite supersedes and extends.
- `Makefile` — `build-all`, `dist-placeholder`, and where `test-e2e` belongs.
- `internal/web/embed.go` — why the SPA must be built before the binary.
- `internal/config/config.go` — `OutboundHTTP.AllowedHosts` and `AllowPrivateNetworks`, the determinism seam.
- `internal/safehttp/safehttp.go` — the dial-time address check the stub must be allowed through rather than around.
- `cmd/kilasflow/main.go` — the composition an end-to-end test is meant to exercise.
- `web/src/routes/embed/[id]/+page.svelte`, `sdk/src/browser.ts` — the embedded surface and its handshake.
- `.pine/tickets/FEAT-7tgasa.md` — V2-p10-1, the pipeline this suite runs in.
