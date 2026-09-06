---
id: FEAT-5fhj6p
title: Run the epic acceptance scenario as an executable suite
status: doing
priority: high
labels:
    - e2e
    - testing
deps:
    - FEAT-5z37xh
    - FEAT-gg85se
    - FEAT-ykyfbd
    - FEAT-xr7ga9
    - FEAT-cpdp8y
    - FEAT-3taswf
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:06:43Z"
updated: "2026-09-06T06:58:47Z"
---

## Scope

`EPIC-m42s3g` states its acceptance scenario as four proofs, in order: a Telegram bot answering through an AI agent; the official WAHA chatting template imported, activated, receiving a real webhook and replying, with the same template imported twice for two different tenants; a datastore created, edited, written by a workflow, imported from an n8n Data Table export, and proven unreadable by a second tenant; and — added by p10 — an external application consuming KilasFlow with no checkout of this repository. All of them must run with no Node.js process anywhere.

That scenario is the definition of done for the entire V2 programme and it exists only as prose. Nothing runs it, so "is V2 finished" is a judgement call rather than a command.

V2-p11-3 through V2-p11-7 each prove one dimension against stubs and local substitutes, which is right for a suite that runs often. This ticket is the other kind of test: the one that uses the real Telegram Bot API, a real WAHA server, a real published image and a real published npm package, runs rarely and on demand, and answers the question the stubbed suites deliberately do not.

The gap between the two is not academic. A stub proves KilasFlow sends what it believes it should send. It cannot prove that Telegram accepts the `setWebhook` call and delivers to the URL, that WAHA's HMAC is computed over the bytes the trigger expects, that the published image's distroless runtime has what it needs, or that `npm install @kilasflow/sdk` resolves in a project that is not this one.

There is also a reporting job here that no individual suite can do. The corpus baseline reports `imported / activatable / runnable / blocked` counts, and V2-p10-19 publishes them as a fidelity statement. That statement is only trustworthy if something regularly re-measures it against the shipped artefact rather than the working tree.

## Acceptance criteria

- [ ] The four epic proofs run as one suite, in order, against a published image rather than a locally built binary.
- [ ] The Telegram proof uses a real bot token: the trigger registers its own webhook on activation, a real message is delivered, an agent answers, and a reply arrives — with the webhook removed on deactivation.
- [ ] The WAHA proof runs against a real WAHA server, including HMAC verification over the raw body, and covers the two-tenant case with both workflows active at once.
- [ ] The datastore proof runs against PostgreSQL, including the cross-tenant refusal and the n8n Data Table import.
- [ ] The external-consumer proof installs the published npm package into a scratch project outside this repository and drives a workflow and a datastore through it.
- [ ] The suite asserts that no Node.js process runs in or beside the server for any of the four proofs, since that is an explicit epic constraint and not an implementation detail.
- [ ] The run publishes a report: which proofs passed, the corpus fidelity counts measured against the shipped artefact, and the versions of the image and package under test.
- [ ] The suite runs on demand and on a schedule rather than on every pull request, and its credential requirements are documented so a second operator can run it.

## Implementation Plan

Sequence this last in the phase; it composes the other suites rather than duplicating them. Where a stubbed suite already drives a path through the UI, this one should reuse that code with different fixtures and endpoints, not restate it. The difference between the two is configuration and credentials, and it should stay that way — two implementations of the WAHA proof would drift and the rarely-run one would rot.

Credentials are the operational obstacle and should be named rather than discovered. The roadmap's own "Open items for the owner" already lists a Telegram bot token from BotFather and an OpenRouter key; this suite adds a WAHA instance and npm publish access. All of them belong in a documented secret set with a stated owner, because a suite that only one person can run is a suite that stops being run.

Telegram has a constraint that shapes the whole proof: `setWebhook` requires a publicly reachable HTTPS URL, so the server under test must be reachable from the internet. The README already documents the tunnel workflow with `cloudflared` and `ngrok` for exactly this, and the suite should use it rather than inventing a second approach. If p3-6 ships the optional `getUpdates` long-polling mode, note that it is *not* an acceptable substitute here — the epic's proof is the webhook path, and polling would prove a different thing.

Assert the no-Node.js constraint mechanically rather than by assertion in prose. Inspecting the running container's processes is enough, and it is the kind of claim that quietly becomes false when somebody adds a sidecar for a good reason.

Two things to keep out. Do not gate merges on this suite; it depends on third-party availability and a failure will more often mean somebody else's outage than a regression. And do not use it as the only coverage for anything — every path it exercises should already be covered against a stub by one of the other suites, so a failure here narrows to "the real service disagrees with our stub", which is a specific and useful signal.

One thing to decide: what happens when a proof fails because a third party is down. Recommend distinguishing an assertion failure from an availability failure in the report, so the schedule does not train the team to ignore red.

## References

- Roadmap plan, p11 section, entry V2-p11-8: `.pine/roadmap.md`.
- `.pine/tickets/EPIC-m42s3g.md` — the acceptance scenario, its ordering, and the no-Node.js constraint.
- `.pine/tickets/FEAT-5z37xh.md`, `FEAT-gg85se.md`, `FEAT-ykyfbd.md`, `FEAT-xr7ga9.md`, `FEAT-cpdp8y.md` — the suites this composes.
- `.pine/tickets/FEAT-53fht8.md` — V2-p10-2, the published image under test.
- `.pine/tickets/FEAT-3taswf.md` — V2-p10-8, the published package the external-consumer proof installs.
- `internal/interop/n8n/corpus/BASELINE.md` — the fidelity counts this run re-measures.
- `README.md` — the Telegram tunnel walkthrough, `server.public_url`, and the secret-token scheme.
- `.pine/roadmap.md` — "Open items for the owner", the credentials this suite formalises.
- `scripts/smoke-postgres.sh` — the PostgreSQL topology the datastore proof runs on.

## E2E progress (EpicSuite, 2026-09-06)

Delivered, verified, not closed (main verifies and closes). Three new files,
no harness edits, no edits outside new e2e files:

- `e2e/tests/epic-acceptance.spec.ts` — the four epic proofs in order plus a
  report test, `test.describe.serial`, observable waits only, single admitted
  private endpoint per server, `allow_private_networks` never set.
- `e2e/fixtures/epic-telegram.ts` — Telegram Bot API stub
  (getWebhookInfo/setWebhook/deleteWebhook/sendMessage/getUpdates) plus a
  byte-transparent /v1/* proxy to the local Ollama, so one endpoint covers
  the bot and the model together; secret derivation, signed delivery helper,
  Ollama probe/warm (xr7ga9 skip convention), full-channel SSE reader.
- `e2e/fixtures/epic-external.ts` — scratch external app (npm pack, tarball
  shape check, plain `npm install` outside the repo, packed-client import),
  its host page + static server, `withEnv` server tuning, and the mechanical
  no-Node check (lsof LISTEN pid resolves to the kilasflow binary; its
  subtree holds no `node` process).

Verification: `npx playwright test tests/epic-acceptance.spec.ts` (same path
as `make test-e2e`; binary+SPA via `make build-all`) → 6 passed, 1 skipped,
~20s warm. Per-proof evidence:

- Proof 1 (Telegram+AI): activation called getWebhookInfo then one setWebhook
  with `url=https://epic-external.invalid/webhook/<32hex>`,
  `allowed_updates=["message"]`, and `secret_token` equal to the test's own
  HMAC derivation; wrong/missing secret → 401 with no execution; real update
  → succeeded; agent output non-empty; `ai.model.delta` + `execution.completed`
  on the channel; stub got exactly one sendMessage with chat 774411 and text
  byte-equal to the agent output; deactivation called deleteWebhook and the
  route 404s afterwards. lsof+ps verdict clean while live.
- Proof 2 (WAHA x2 tenants): chatting template imported twice, fresh opaque
  routes, cross-delivery 404; forged HMAC → 401 with no execution, correct
  HMAC (sha512 hex over raw bytes) → succeeded on each tenant with the other
  silent; ≥2 `/api/sendText` stub hits containing "pong".
- Proof 3 (datastore sqlite): create → 2 columns → rename; workflow
  write+filtered read succeeded with the row listed; n8n CSV round-trips
  byte-identical; n8n Data Table import blocks naming dt_metrics_01, binds to
  the store and runs; second instance reads 404 (indistinguishable bodies)
  on get/rows/write/enumerate and its node probe fails `unknown datastore`
  with tenant A's row untouched.
- Proof 3 PG: SKIPPED — no DSN (`pgSkipReason()`); same gate as
  datastore-pg.spec.ts. Nothing asserted silently.
- Proof 4 (external, no checkout): scratch dir in os.tmpdir (asserted outside
  the repo), SDK 0.1.0 packed+installed via plain npm; tarball holds
  dist/server.js and no src/; directory-installed pack.e2ehello lists with
  source pack and runs to stub POST /sendMessage; SDK datastore
  create/edit/write/filtered-update/cursor-exhaustion/required-filter-refusal/delete;
  SDK importWorkflow reports /webhook/<32hex> and activates; SDK-minted embed
  session mounts the editor in the external host.html (embed-ready observed,
  waiting state gone, no alert). lsof+ps verdict clean while connected.
- Report: server f7deaa3-dirty, sdk 0.1.0, 56 node-types (builtin:51 pack:5),
  corpus present, postgres absent, ollama gemma4:12b-mlx.

Driver matrix: sqlite always; postgres joins on DSN; proof 1 joins on the
pinned Ollama model; proof 2 joins on the materialised corpus. All three
gates skip with their recovery command.

Honest deviations from the ticket (all stated in the spec header too): stub
third parties instead of real Telegram/WAHA/registry/image — the
rarely-run real-credential capstone is still open; setWebhook registers an
RFC-2606 `.invalid` URL (https shape without a tunnel) while deliveries go
to the real local route; the no-Node criterion is enforced literally as its
acceptance box words it (in or beside the SERVER — the SDK consumer and the
harness are Node by definition); corpus fidelity is reported as live
catalogue counts + versions, not a rescored BASELINE.md.

Left for main: close with evidence; decide whether this stubbed suite or a
separate scheduled file should own the real-credential run (recommended: keep
this file on the per-PR path — it is hermetic — and grow the real-credential
capstone separately per the ticket's "do not gate merges" rule).

## Blocked on real credentials (Main, 2026-09-06)
Hermetic suite green (6 passed + 1 PG skip, twice; full `make test-e2e` 57 passed + 4 honest PG skips). Remaining boxes need operator-owned secrets no agent can mint: a real Telegram bot token, a real WAHA session pair, npm registry publish, plus scheduling/credential-docs scope. Recommend: keep the hermetic file on the per-PR path; grow a real-credential capstone separately when the operator provides them. Status held at doing for that reason, not for lack of code.
