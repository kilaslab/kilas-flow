---
id: BUG-vzzkg3
title: Board and docs claim three capabilities the code does not have
status: todo
priority: high
labels:
    - docs
    - platform
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T05:26:31Z"
---

## Problem

Three tickets are `status: done` with **every acceptance criterion still unticked**, and the
documentation was written against them, so both the board and the docs currently tell the next
reader (human or agent) that these paths work.

## Evidence

- `FEAT-3taswf` (Publish `@kilasflow/sdk` to npm) — body criteria all `- [ ]`. Live check
  2026-09-20: `npm view @kilasflow/sdk version` → `E404 Not Found`. `git tag` count is 0.
- `FEAT-48hreg` (native community module SDK on WebAssembly) — body criteria all `- [ ]`.
  The guest side exists (`pkg/sdk`), the host side does not: `internal/nodepack/nodepack.go:38`
  has no module field on `Pack`, `internal/nodepack/loaddir.go:6-8` still describes WASM packs
  as future work, and `internal/runcode` grants the guest no host functions.
- `FEAT-7cg0cd` (JavaScript sidecar) — the protocol and per-tenant pool exist in `sidecar/`,
  but the package is imported by no production code (grep `kilaslab/kilas-flow/sidecar`
  outside `./sidecar/` returns nothing), there is no config key for it, and
  `node.SourceSidecar` has no `RegisterFrom` call site.
- `docs/src/content/docs/guides/community-nodes.md:28,46` presents the WASM pack build as a
  working path; `:77-83` presents the sidecar as something an operator configures.
- `docs/src/content/docs/concepts/tenancy-and-embedding.md:270` states "Nothing creates a
  tenant through the API. No operation exists for it." — contradicted by
  `POST /api/v1/tenants` (`internal/api/handlers/admin.go:208`) and by the generated
  reference at `docs/src/content/docs/reference/api/tenants.md`.
- `FEAT-77rveq` and `FEAT-cwmw90` are duplicate open tickets for the same deliverable
  (`GET /workflows/{id}/webhooks`).

## Acceptance criteria

- [ ] `FEAT-3taswf`, `FEAT-48hreg` and `FEAT-7cg0cd` are either reopened with criteria that
      match what actually shipped, or re-scoped in place with a comment naming what remains.
- [ ] `community-nodes.md` states plainly which parts of the WASM and sidecar stories are not
      shipped, or the pages are removed until they are.
- [ ] `concepts/tenancy-and-embedding.md` matches the code on tenant provisioning.
- [ ] The duplicate webhook-URL tickets are merged into one.
- [ ] `pine doctor` is clean afterwards.

## Out of scope

Building the WASM host ABI or wiring the sidecar. This ticket only makes the board and the
docs tell the truth about them.
