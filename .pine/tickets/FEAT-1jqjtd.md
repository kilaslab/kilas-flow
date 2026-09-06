---
id: FEAT-1jqjtd
title: E2E browser test of every executable node with live n8n comparison
status: todo
priority: high
labels:
    - e2e
    - testing
deps:
    - FEAT-cx3hq1
    - FEAT-5z37xh
parent: EPIC-m42s3g
phase: p11
created: "2026-09-06T06:41:09Z"
updated: "2026-09-06T06:41:09Z"
---

## Scope

FEAT-5z37xh proved every registered node type renders and validates in the editor. What it does not prove is that every *executable* node actually runs correctly end to end through the browser, with real configuration and a real execution record — and whether its runtime behaviour matches n8n's for the same node.

This ticket closes that gap: drive every executable node through the KilasFlow editor in a real browser, execute it, and assert the outcome against the execution record. Where the node has an n8n counterpart, cross-check semantics against the live local n8n reference instance so behavioural drift is caught, not assumed.

Live n8n fixture (provided by requester, local only):
- URL: `http://localhost:5678/signin?redirect=%252F`
- Email: operator's n8n login / Password: **[REDACTED by Main 2026-09-06 — was committed here in plaintext; rotate it, export N8N_EMAIL/N8N_PASSWORD, never write values into tickets]**
- Treat the password as a secret: read it from env (`N8N_EMAIL` / `N8N_PASSWORD`) in code, never hardcode; document the secret set so a second operator can run the suite.

Tooling constraint: the implementer MUST read `skill://playwright-cli` first and use `playwright-cli` for all browser exploration/driving of both UIs (KilasFlow editor + n8n UI). Permanent assertions live in `e2e/` (Playwright project from FEAT-cx3hq1); `playwright-cli` is the driving tool, not a replacement for the committed suite.

## Acceptance criteria

- [ ] Every executable node type from live `GET /api/v1/node-types` is placed on the canvas, configured through the parameter panel, saved, executed, and its result asserted via the execution record (not just "no validation error").
- [ ] Coverage is driven from the live catalogue response; the suite fails on any executable node with neither a test nor an explicit justified exclusion (tiers: `editor-validated` / `stub-executed` / `live-service`, stated in the report).
- [ ] For each node with an n8n counterpart, the same logical operation is executed against the live local n8n (`localhost:5678`) and the outputs are compared (shape + values, modulo documented divergences listed in the report).
- [ ] Trigger nodes covered on their own terms: bind webhook, deliver, assert; self-registering triggers assert activate/deactivate lifecycle.
- [ ] Credential-requiring nodes go through the credential picker; missing credential yields the user-facing validation message, not a runtime crash.
- [ ] No fixed sleeps; wait on execution status / SSE event / rendered node. Failing test emits trace + screenshot + per-instance server log.
- [ ] `make test-e2e` runs the suite locally and in CI the same way.

## Implementation Plan

1. Build the node matrix first: fetch `/api/v1/node-types`, mark each `executable` vs `annotation/capsule`, generate the uncovered-list report and fail on difference (ratchet, same pattern as FEAT-5z37xh).
2. Table-driven core: one generic flow (place → fill each declared property by kind → save → run → assert execution status/output) parameterised by the server definition; hand-written cases only for triggers, agent cluster, DB family, Datastore, unsupported capsule (must refuse to activate with a diagnostic).
3. n8n cross-check: sign in to local n8n via `playwright-cli` (`open` → `goto` signin URL → `fill`/`click` via snapshot refs), build the counterpart workflow once per node family, execute, capture output; compare against KilasFlow run. Divergences become explicit entries (expected-diff list), never silent passes.
4. Determinism via existing seams: outbound HTTP through local stub + `outbound.allowed_hosts` (never disable the guard); per-test server instance (own port + data dir, `scripts/openapi-spec.mjs` pattern); `KILASFLOW_ENCRYPTION_KEY` set for credential seeding.
5. Sequence after FEAT-5z37xh (reuse its report mechanism) and FEAT-cx3hq1 (reuse `e2e/helpers/server.ts`); do not duplicate the harness.

Out of scope: performance numbers (belongs to the benchmark ticket); importing community templates (belongs to the workflow-library import ticket).

## References

- `skill://playwright-cli` — REQUIRED reading before implementation; all browser driving goes through it.
- `.pine/tickets/FEAT-cx3hq1.md` — `e2e/` harness, `e2e/helpers/server.ts`, stub via `scripts/e2e-stub.mjs`.
- `.pine/tickets/FEAT-5z37xh.md` — catalogue-driven coverage ratchet to extend from validate-only to execute-and-compare.
- `internal/node/registry.go`, `nodes/core.go` — catalogue source and `RegisterAll`.
- Live n8n: `http://localhost:5678/signin?redirect=%252F` (creds via env, see Scope).
