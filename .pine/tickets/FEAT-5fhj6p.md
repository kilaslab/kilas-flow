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
updated: "2026-09-21T02:00:00Z"
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
- [x] The datastore proof runs against PostgreSQL, including the cross-tenant refusal and the n8n Data Table import.
- [ ] The external-consumer proof installs the published npm package into a scratch project outside this repository and drives a workflow and a datastore through it.
- [x] The suite asserts that no Node.js process runs in or beside the server for any of the four proofs, since that is an explicit epic constraint and not an implementation detail.
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
## Implementation notes — stage 1 of 3 (omp-FEAT-5fhj6p, 2026-09-20)

Stage 1 of the critic-corrected plan: reusable proofs behind host/side
interfaces, the no-Node predicate made whole, the hermetic suite a thin
caller. Stages 2-3 (the on-demand capstone under `e2e/capstone`, the real
sides, the schedule) are not started.

What changed:
- `e2e/scripts/capstone-lib.mjs` (new): the one pure implementation of the
  process-table logic — `NODE_LIKE`, `baseName`, `isNodeLike`, `parsePsTable`,
  `descendants` (full subtree, roots included) and `noNodeVerdict`. Exists
  because the previous check in `epic-external.ts` matched one exact `comm`
  string and looped over DIRECT children while its comment claimed a subtree
  check, and because proofs 2 and 3 never called it at all.
- `e2e/tests/epic-machinery.spec.ts` (new, per-PR path, no server/docker/skip):
  six tests over that logic, written failing-first (the module did not exist
  yet — `Cannot find module … capstone-lib.mjs`).
- `e2e/fixtures/epic-proofs.ts` (new): the four proof bodies moved verbatim
  behind `EpicHost` / `TelegramSide` / `WahaSide` / `ProofContext`, plus
  `assertNoNodeOk`, `deliverWahaSigned`, `workflowWebhookRoute` and
  `waitForMatchingExecution`. No `kind` branches: every environment-specific
  line is a host or side call. The route now comes from
  `GET /workflows/{id}/webhooks` rather than a stub-captured body, so the
  real side works too; `assertNoNodeOk` runs in all four proofs (hermetic
  coverage grows — it was proofs 1 and 4 only).
- `e2e/fixtures/epic-hermetic.ts` (new): `binaryHost()` and the stub
  `stubTelegramSide()` / `stubWahaSide()` — what keeps this file on the per-PR
  path.
- `e2e/fixtures/epic-external.ts`: `assertNoNodeBesideServer` rewritten onto
  the lib (`nodeChildren` -> `nodeProcesses`); the packed client surface typed
  instead of `any`; `ExternalSource`/`scaffoldExternalSource` seam for the
  registry mode.
- `e2e/tests/epic-acceptance.spec.ts`: thin wrappers, same titles, order,
  `describe.serial`, timeouts and gate strings. Everything else moved out.
- `.github/workflows/ci.yml`: 3-line "Install SDK dependencies" step in the
  e2e job. Proof 4 runs `npm pack sdk`, whose prepack is
  `tsc -p tsconfig.build.json`; verified in this worktree with
  `sdk/node_modules` moved aside — `sh: tsc: command not found`, npm error
  127 — so the e2e job was red for an SDK-install reason, not for the code.

Verification:
- BEFORE: `pnpm exec playwright test tests/epic-acceptance.spec.ts` with a
  scratch pgvector/pgvector:pg17 (kf-pg-feat-5fhj6p) -> 7 passed (2.0m).
  All four proofs ran: Ollama up, corpus materialised, PG DSN set.
- AFTER: same file plus `tests/epic-machinery.spec.ts` -> 13 passed (1.5m).
  Same 7 epic results, same pass/skip set, plus the 6 new machinery tests.
- Failing-first: the machinery spec failed with `Cannot find module …`;
  after implementing the lib, 6 passed.
- Mutations, each applied and reverted, each failing for the right reason:
  wrong-secret `401` -> `200` in the shared Telegram proof (`Expected: 200,
  Received: 401`), `404` -> `200` in the isolation proof (`Expected: 200,
  Received: 404`), `isNodeLike` forced false (3 machinery tests failed).
- PG coverage grew: the PostgreSQL test now runs the FULL lifecycle proof
  (create, columns, rename, workflow write + filtered read, CSV round trip,
  n8n Data Table import, bound run) and the full isolation proof on Postgres,
  where before it only wrote and read once.
- `go test ./internal/guardrails/...` -> ok (0.697s).
- Full `make test-e2e` (PG DSN set): every touched test passed — 7 epic + 6
  machinery. The suite as a whole was NOT green on this machine: 89 passed,
  12 skipped, 4 did not run, and failures in files this change does not touch
  (pack-editor, n8n-compare, node-coverage, library-import, waha-migration,
  datastore, ai-agent). `n8n-compare › the executable-node matrix …` fails in
  isolation too, so it is not load-related and not caused here.
- `make e2e-skip-budget`: 12 skipped (< 24) and 4 violations, all
  `skipped with no reason at all` in ai-agent-ollama — the documented
  local-Ollama cascade artefact (`ollama serve` is up; see .pine/memory/e2e.md).
  No skip and no violation comes from this change; the new spec adds six
  passing tests and zero skips.

Criterion 4 is ticked on the strength of the PG run above (lifecycle +
cross-tenant refusal + n8n Data Table import on a real pgvector PostgreSQL).
Criterion 6 is ticked because all four proofs now assert the no-Node verdict
per live server, with the subtree and whole-basename defects fixed and
covered by tests. The real-credential criteria (1, 2, 3, 5, 7, 8) stay open:
they need an operator-pushed tag, a publish, a real bot and WAHA session, and
the capstone stages that stage 2-3 add.

### Stage 2 (the capstone core) — Implementation notes

The stage was interrupted mid-flight (the agent harness hit a provider usage
limit) and finished by the orchestrator; the machinery spec is green as
committed.

**What changed**

- `e2e/capstone/epic-capstone.spec.ts` + `e2e/playwright.capstone.config.ts` +
  `e2e/capstone/global-teardown.ts` (new): the capstone runs the four proofs
  against a docker IMAGE host on its own Playwright config, with its own results
  directory (`e2e/capstone-results/`, gitignored) and its own teardown, so the
  per-PR suite and its skip budget never see it.
- `e2e/fixtures/epic-image.ts` (new): builds/loads the image, starts the stack
  (image + PostgreSQL container) on loopback, and asserts the container-side
  no-Node verdict through `docker top` — including Docker Desktop's table shape,
  where the node process reports `comm` as `MainThread` rather than `node`.
  That shape is a real defect the earlier predicate would have missed; the
  failing-first tests for it live in `epic-machinery.spec.ts`.
- `e2e/fixtures/epic-config.ts`, `e2e/fixtures/epic-corpus.ts` (new): the
  capstone's configuration (image reference, sides, credentials from the
  environment only) and the corpus fidelity check measured through the image
  API.
- `e2e/scripts/capstone-lib.mjs` (+371): the process-table parser for both
  `ps` shapes, the container verdict, the outcome classifier
  (passed / failed / unavailable / skipped) and the redaction of every secret
  shape the report could carry.
- `e2e/scripts/capstone-report.mjs` (new): the report CLI — exit 0 all passed,
  1 any failed or a missing record, 2 only unavailable — rendering the markdown
  a skipped proof carries with its reason and recovery command.
- `Makefile`: `test-e2e-capstone` and `e2e-capstone-report`, the first with a
  leading `-` on purpose so the verdict is the report step's; `CONTRIBUTING.md`
  documents both.

**Verification**

| Command | Outcome |
| --- | --- |
| `cd e2e && ./node_modules/.bin/playwright test tests/epic-machinery.spec.ts` | 17 passed (7.3s) — parser shapes, container verdict, classification (502 with/without a healthy re-probe, image pull, npm), redaction, report exit codes, registry mode, corpus join |
| `go build ./...`, `go vet ./...` | clean (no Go file changed) |
| the per-PR suite | untouched: the capstone lives on its own config and is not in the skip budget |

**Still unticked, with reasons**: AC1/AC2/AC3/AC5/AC7/AC8 need a published image,
a published npm package or real third-party credentials (Telegram bot token,
WAHA session) that no agent can mint. AC4 and AC6 are proven. Stage 3 wires the
real sides, the tunnel and the schedule workflow; what stage 2 proves is that
the machinery exists, classifies honestly and never leaks a secret.

## Verification pass — the eight criteria, measured on the current tree (2026-09-21)

Everything below was run on `main`@`2fb3786` in a scratch worktree
(`git worktree add --detach /tmp/wt-FEAT-5fhj6p main`), against a real
PostgreSQL 17 (`pgvector/pgvector:pg17`, container `kf-pg-feat-5fhj6p`,
127.0.0.1:32771), the materialised `.corpus`, and the pinned local model
(`gemma4:12b-mlx`). Every row is observed output, not a claim. Two defects were
found and fixed; the fixes are the commit this section belongs to.

### Criterion → status → evidence

1. **Four proofs as one suite, in order, against a published image** — *partial,
   blocked*. The suite, the order and the image host exist and run; the image
   is not published and proofs 1–2 have no real side yet.
   `KILASFLOW_CAPSTONE_IMAGE=kilasflow:latest KILASFLOW_CAPSTONE_PULL=0 KILASFLOW_CAPSTONE_SDK_SPEC=tarball make test-e2e-capstone e2e-capstone-report`
   → `5 passed, 2 skipped` (Playwright) / `capstone skipped: 7 proofs (5 passed, 2 skipped) — report at e2e/capstone-results/report.md`, exit 0.
   Proofs 1 and 2 skip with `the real Telegram/OpenRouter/WAHA sides are not
   implemented yet (stage 2 ships the capstone core)` — they skip even when the
   credentials are present, so this is stage-3 code, not only a credential gap.
   The published half needs `ghcr.io/kilaslab/kilasflow` to exist:
   `docker manifest inspect ghcr.io/kilaslab/kilasflow:latest` → `denied`.
2. **Telegram proof with a real bot token** — *blocked on credentials*.
   `readCapstoneConfig({}).missing('01-proof1-telegram')` names what is absent:
   `KILASFLOW_CAPSTONE_TELEGRAM_BOT_TOKEN`, `KILASFLOW_CAPSTONE_TELEGRAM_CHAT_ID`,
   `KILASFLOW_CAPSTONE_OPENROUTER_API_KEY`, `KILASFLOW_CAPSTONE_MODEL`. With all
   four set and stage 3 landed, `make test-e2e-capstone` is the proving command.
   A bot token can only come from @BotFather under the owner's Telegram account.
3. **WAHA proof against a real server, two tenants, HMAC over the raw body** —
   *blocked on credentials*. Same command, credentials
   `KILASFLOW_CAPSTONE_WAHA_URL`, `KILASFLOW_CAPSTONE_WAHA_API_KEY`,
   `KILASFLOW_CAPSTONE_WAHA_SESSION_A`, `KILASFLOW_CAPSTONE_WAHA_SESSION_B`
   (two paired sessions on an operator-hosted server). The stub arrangement
   proves the path today: hermetic proof 2 passed (`7 passed` below).
4. **Datastore proof on PostgreSQL, cross-tenant refusal, n8n Data Table
   import** — *ticked, re-proven here*.
   `KILASFLOW_TEST_POSTGRES_DSN=… playwright test tests/epic-acceptance.spec.ts`
   → `7 passed (31.9s)`, including `proof 3 — the datastore path holds on postgres`
   (full lifecycle + isolation, not a single write/read). Against the image:
   `capstone proof 3 - datastore on PostgreSQL` and `proof 3 - a second tenant
   reads nothing` both `passed` on a container-side database.
5. **External consumer installs the published npm package** — *blocked on
   publication*. `npm view @kilasflow/sdk version` → `E404 Not Found`. The
   registry code path is proven without publishing (machinery test
   `registry mode …` installs from a fake registry serving the real
   `npm pack ./sdk` bytes). Once published, the proving command is
   `KILASFLOW_CAPSTONE_SDK_SPEC=@kilasflow/sdk@latest make test-e2e-capstone`;
   today proof 4 runs against `tarball` and passes.
6. **No Node.js process in or beside the server, for all four proofs** —
   *ticked, re-proven here*. All four proof bodies call `assertNoNodeOk`
   (`epic-proofs.ts` lines 422, 518–519, 598, 663–664, 837). Container-side
   evidence from the report: `checked: no Node-like process across 5 process
   table(s) (docker:kilasflow.capstone.run=…)`.
7. **The run publishes a report: proofs, corpus fidelity against the shipped
   artefact, image and package versions** — *mechanism proven, box left unticked*.
   Observed report: verdict table per proof, `image kilasflow:latest
   (sha256:051bc11a…)`, `image version 2fb3786, revision 2fb37865a36d…`,
   `architecture arm64`, `entrypoint ["/app/kilasflow"]`, `health version
   2fb3786`, `sdk 0.1.0 integrity sha512-JZYjhqJd…`, `corpus coverage 40/40,
   tiers imported 40, activatable 15, runnable 5, blocked 10` with
   `drift []` against `internal/interop/n8n/corpus/baseline.json`. What is not
   proven is the word *shipped*: the artefact measured was built locally by the
   Dockerfile (`make docker`), because nothing is published. Not ticked for that
   conjunct alone.
8. **On demand and on a schedule, not on every pull request, with documented
   credentials** — *three of four observed, box left unticked*. On demand: the
   Makefile targets above, run. Not per-PR: `playwright.capstone.config.ts` has
   `testDir: './capstone'` and `playwright.config.ts` has `testDir: './tests'`,
   and the hermetic run did not pick the capstone up. Documented:
   `docs/src/content/docs/operate/acceptance-capstone.md` (credential table with
   an owner per row), enforced by a new test that fails when the suite reads a
   `KILASFLOW_CAPSTONE_*` variable the page does not name (mutation-checked:
   renaming `KILASFLOW_CAPSTONE_PG_IMAGE` on the page fails it, naming it wrong
   in both places passes it). Schedule: `.github/workflows/capstone.yml`, new in
   this commit, YAML-validated and structurally checked — but a `schedule`
   trigger only fires on the default branch, so it cannot have fired before this
   lands. That is the only unobserved part of the criterion.

### Defects found and fixed (this commit)

- **The default image under test was a coordinate that can never be pulled.**
  `IMAGE_TARGETS` gave `make test-e2e-capstone` the release exports, so with no
  environment the image resolved to `ghcr.io/kilaslab/kilasflow:2fb3786-dirty`
  (observed: `docker pull ghcr.io/kilaslab/kilasflow:2fb3786-dirty:` in the
  artefacts record) while `scripts/docker-tags.sh` publishes only `vX.Y.Z`,
  `vX.Y` and `latest`. Every unconfigured run — including the scheduled one —
  would have reported `artefact-missing` for a tag nobody could create.
  Fixed by dropping the two capstone targets from `IMAGE_TARGETS` and removing
  the `KILASFLOW_IMAGE`/`KILASFLOW_VERSION` fallback from `readCapstoneImage`.
  After the fix, `make test-e2e-capstone` with no environment names
  `ghcr.io/kilaslab/kilasflow:latest` (observed in the record), and a new
  machinery test asserts that a commit-derived tag cannot leak back in.
- **A promise the code made and nothing kept.** `epic-config.ts` states its
  variable list "is the documentation contract: the stage 3 docs-coverage guard
  test fails when the suite reads a `KILASFLOW_CAPSTONE_` variable the operator
  page does not name", and the module's purity comment promises the machinery
  tests exercise its defaulting and missing reasons. Neither test existed. Both
  exist now (`docs coverage …`, `capstone config …`), and both were
  mutation-checked in the failing direction.

### Availability versus assertion failure

The design already distinguished the two and it is now demonstrated, not just
asserted. Same suite, same command, three states:

| run | observed | verdict | exit |
| --- | --- | --- | --- |
| rehearsal, local image | `5 passed, 2 skipped` | `skipped` | 0 |
| `KILASFLOW_CAPSTONE_IMAGE=registry.invalid/kilaslab/kilasflow:latest` | `7 unavailable`, cause transport | `unavailable` | 2 (`make e2e-capstone-scheduled` → 0) |
| no environment (published image absent) | `7 failed`, cause `artefact-missing` | `failed` | 1 |

`--unavailable-ok` (the new `make e2e-capstone-scheduled`) changes only the
process status for the middle row, never the verdict in the report; an
assertion failure still fails. `.github/workflows/capstone.yml` reads its verdict
that way, so an outage does not go red and a missing artefact does.

### Credential set and owner

Named in full, with the proof each one enables, in
`docs/src/content/docs/operate/acceptance-capstone.md`. The short version: every
row is **project owner** territory — a Telegram bot token from @BotFather, the
chat that started it, the OpenRouter key and model id, an operator-hosted WAHA
server with its API key and two paired sessions; plus two that are pipeline
rather than secret (`NPM_TOKEN` for the `@kilasflow` scope in the release
workflow, and a `v*` tag to publish the image). No agent can mint any of them,
and none of them is faked: each proof reports `skipped` with the missing
variable named and a recovery command.

### Left for main

- Tick 7 and 8 after the first scheduled fire and the first published
  `make test-e2e-capstone`, if the orchestrator wants them ticked at all.
- Stage 3 (the real Telegram/WAHA sides, the tunnel) is what AC1/AC2/AC3 need
  beyond credentials; it is not started and is not a verification finding.
- Pre-existing and unrelated: `make e2e-skip-budget` reports 4 violations in
  `ai-agent-ollama` (`skipped with no reason at all`) on a machine where the
  local Ollama cascade fires. Unchanged by this pass and not caused by it.
