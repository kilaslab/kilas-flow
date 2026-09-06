---
id: FEAT-wdnc03
title: Import and run workflows from the n8n workflow library
status: todo
priority: high
labels:
    - e2e
    - testing
    - interop
    - import
deps:
    - FEAT-cx3hq1
    - FEAT-gg85se
    - FEAT-csqgg5
parent: EPIC-m42s3g
phase: p11
created: "2026-09-06T06:41:09Z"
updated: "2026-09-06T06:41:09Z"
---

## Scope

FEAT-gg85se proved one community template migrates end to end; the corpus tickets (FEAT-csqgg5, FEAT-nbqye0) measure import fidelity on pinned fixtures. None of them exercises the actual **n8n workflow library** as a user would: browse/select real workflows in n8n, export them, import into KilasFlow, and run them.

This ticket makes that path executable: sample workflows from the n8n workflow library via the live local n8n instance, export each, import into KilasFlow (API + editor), and assert import → activatable → runnable, with diagnostics where fidelity drops.

Live n8n fixture (provided by requester, local only):
- URL: `http://localhost:5678/signin?redirect=%252F`
- Email: operator's n8n login / Password: **[REDACTED by Main 2026-09-06 — was committed here in plaintext; rotate it, export N8N_EMAIL/N8N_PASSWORD, never write values into tickets]**
- Treat the password as a secret: read it from env (`N8N_EMAIL` / `N8N_PASSWORD`) in code, never hardcode; document the secret set so a second operator can run the suite.

Tooling constraint: the implementer MUST read `skill://playwright-cli` first and use `playwright-cli` for all browser work against the n8n UI (signin, browse library/templates, export) and the KilasFlow editor (import screen, activation). Permanent assertions live in `e2e/`; `playwright-cli` is the driving tool.

## Acceptance criteria

- [ ] A curated sample from the n8n workflow library (minimum: official WAHA "chatting" template + one workflow per major family: webhook-triggered, scheduled, data-shaping, flow-control, HTTP integration, AI agent) is exported from the live local n8n and imported into KilasFlow.
- [ ] Each sampled workflow reports `imported / activatable / runnable / blocked` with per-workflow import diagnostics surfaced (every dropped field from FEAT-nbqye0's diagnostic contract is visible, none silent).
- [ ] Each importable workflow opens in the editor with correct icons + parameter panels, rebinds credentials, re-points webhooks, activates, and runs to a green execution (stub-backed where third parties are involved).
- [ ] The WAHA chatting template case includes the two-tenant variant: same template imported twice for two tenants, both active at once, each receiving its own delivery and replying.
- [ ] The n8n Data Table variant (where the library workflow uses one) binds to a datastore and runs; a referenced table with no counterpart yields a diagnostic, never silent success.
- [ ] Import failures are classified: `unsupported-node` / `expression-gap` / `connection-gap` / `credential-gap`, counted in the report so the fidelity statement stays actionable.
- [ ] No fixed sleeps; artefacts (trace + screenshot + server log) on failure; runs via `make test-e2e`.

## Implementation Plan

1. Fixture strategy: export from live n8n via UI (`playwright-cli`: signin → open workflow → export JSON) for the browser-path proof; check the exported JSON into a gitignored, digest-pinned local dir (never commit library JSON — licence hygiene per FEAT-yyjfjq) and drive the repeatable suite from those pins.
2. Reuse, don't restate: import path asserts against `internal/interop/n8n` diagnostics; activation path reuses FEAT-gg85se's rebind/re-point/activate flow with different fixtures; datastore binding reuses FEAT-nch9dg mapping.
3. Two-tenant WAHA case reuses the per-tenant webhook path scoping (FEAT-hv4q8e) and HMAC-over-raw-body verification (FEAT-5kv1jq); assert both workflows active simultaneously with distinct URLs.
4. Report publishes the `imported / activatable / runnable / blocked` counts per sampled workflow plus the gap classification — feeds the fidelity statement FEAT-5fhj6p re-measures against the shipped artefact.
5. Deps: FEAT-cx3hq1 (harness), FEAT-gg85se (template-migration flow to reuse), FEAT-csqgg5 (corpus + baseline format).

Out of scope: runtime performance comparison (benchmark ticket); exhaustive all-node execution matrix (sibling ticket).

## References

- `skill://playwright-cli` — REQUIRED reading before implementation; n8n signin/export + KilasFlow import/activation all driven through it.
- `.pine/tickets/FEAT-gg85se.md` — template-migration flow this reuses with library fixtures.
- `.pine/tickets/FEAT-csqgg5.md`, `internal/interop/n8n/corpus/BASELINE.md` — corpus + fidelity-count format.
- `.pine/tickets/FEAT-nbqye0.md` — import-drop diagnostic contract (every drop reported).
- `.pine/tickets/FEAT-nch9dg.md` — Data Table → datastore mapping.
- `.pine/tickets/FEAT-yyjfjq.md` — licence boundary (no n8n bytes in repo/image; library JSON stays gitignored).
- Live n8n: `http://localhost:5678/signin?redirect=%252F` (creds via env, see Scope).
