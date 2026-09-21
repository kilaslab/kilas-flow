---
title: The epic acceptance capstone
description: The on-demand run of the four epic proofs against a real image and real third parties — what it proves, which credentials it needs, who can mint them, and how to read its verdict.
sidebar:
  order: 7
---

KilasFlow's definition of done for V2 is a scenario with four proofs: a Telegram
bot answering through an AI agent; the official WAHA chatting template imported,
activated, delivered to and replied from, for two tenants at once; a datastore
created, edited, workflow-written, n8n-imported and unreadable by a second
tenant; and an external application driving the product with no checkout of this
repository. All four must run with no Node.js process anywhere.

The capstone makes that scenario a command instead of a judgement call. It is
the second caller of the same proof bodies the per-PR suite runs — one
implementation, two arrangements, so the rarely-run one cannot rot.

- **The per-PR suite** (`e2e/tests/epic-acceptance.spec.ts`, `make test-e2e`)
  runs the proofs against a locally built binary and loopback stubs. It is
  hermetic, it runs on every pull request, and it is what catches a regression.
- **The capstone** (`e2e/capstone/`, `make test-e2e-capstone`) runs the same
  proofs against a docker image, real third-party services and the published
  npm package. It is on demand and on a schedule, never on a pull request.

A stub proves KilasFlow sends what it believes it should send. It cannot prove
that Telegram accepts `setWebhook` and delivers to the URL, that WAHA's HMAC is
computed over the bytes the trigger expects, that the published image's
distroless runtime has what it needs, or that `npm install @kilasflow/sdk`
resolves in a project that is not this one. That is the capstone's job, and it
is why it is allowed to be slow, credential-hungry and occasionally unavailable.

## What it proves today

The capstone is delivered in stages, and this page describes the stage that
exists rather than the one that is planned.

| proof | against an image today |
| --- | --- |
| artefacts — image identity | runs: the pulled image's version label, health version and entrypoint agree |
| proof 1 — Telegram through an AI agent | **skips**: the real Telegram/OpenRouter sides are not implemented yet |
| proof 2 — WAHA chatting template, two tenants | **skips**: the real WAHA side is not implemented yet |
| proof 3 — datastore on PostgreSQL | runs: full lifecycle and cross-tenant refusal |
| proof 4 — external consumer | runs against a packed tarball; the npm registry joins when the package is published |
| corpus fidelity | runs: the per-PR fidelity counts re-measured through the image's own API |

Proofs 1 and 2 skip with a reason and a recovery command rather than pretending,
and the report says so in as many words. Two further things have to exist before
the capstone can pass end to end: the real sides (the next stage of the work)
and the published artefacts — the image in `ghcr.io/kilaslab/kilasflow` and the
package on npm. Until they do, the report distinguishes what it could not run
from what it ran and disagreed with.

## Running it

Everything that needs no credential is provable against an image built on this
machine, and that is the rehearsal to run while developing the suite:

```bash
make docker                        # build kilasflow:<describe> and kilasflow:latest
KILASFLOW_CAPSTONE_IMAGE=kilasflow:latest \
KILASFLOW_CAPSTONE_PULL=0 \
KILASFLOW_CAPSTONE_SDK_SPEC=tarball \
  make test-e2e-capstone e2e-capstone-report
```

Docker, the `e2e/` dependencies (`pnpm install` in `e2e/`) and Node are all it
needs. The image host starts its own PostgreSQL container
(`pgvector/pgvector:pg17`), mints a fresh database per proof, and removes every
container it created on the way out — each one carries
`kilasflow.capstone.run=<run id>`, so cleanup is a label query and an unrelated
container on the same machine is never touched.

The real run pulls the published image and installs the published package:

```bash
KILASFLOW_CAPSTONE_IMAGE=ghcr.io/kilaslab/kilasflow:latest \
KILASFLOW_CAPSTONE_SDK_SPEC=@kilasflow/sdk@latest \
  make test-e2e-capstone e2e-capstone-report
```

`make test-e2e-capstone` deliberately ends with no verdict of its own — it
carries a leading `-` — because Playwright's "skipped" is how the capstone
records an explained non-run. `make e2e-capstone-report` is the verdict, and
`make e2e-capstone-scheduled` is the same verdict with an unavailable third
party not counted as a failure.

The image is named in one place. `KILASFLOW_CAPSTONE_IMAGE` is the whole
coordinate, so a run against a released version names it exactly —
`KILASFLOW_CAPSTONE_IMAGE=ghcr.io/kilaslab/kilasflow:v1.2.3` — and nothing
derives an image tag from the working tree, because
`scripts/docker-tags.sh` never publishes one.

## The credential set

Each row is one thing no agent and no repository can mint: somebody with the
right account has to. The **owner** column is who can produce it, and the
capstone's configuration is the only place these names are read.

| variable | what it is | owner | proves |
| --- | --- | --- | --- |
| `KILASFLOW_CAPSTONE_TELEGRAM_BOT_TOKEN` | a bot token from [@BotFather](https://t.me/BotFather), in `123456:AA…` form | project owner | a real `setWebhook`/`deleteWebhook` and a real message delivery |
| `KILASFLOW_CAPSTONE_TELEGRAM_CHAT_ID` | the chat that has started that bot; the reply has to arrive somewhere the run can see it | project owner | the bot answers the operator's own chat |
| `KILASFLOW_CAPSTONE_OPENROUTER_API_KEY` | the OpenRouter key from the roadmap's open items | project owner | the AI agent answers through a hosted model, not only a local one |
| `KILASFLOW_CAPSTONE_MODEL` | the model id that key may call, e.g. `anthropic/claude-sonnet-4` | project owner | the agent's answer is attributable to a named model |
| `KILASFLOW_CAPSTONE_WAHA_URL` | base URL of a **real WAHA server** the operator hosts | project owner | the official chatting template talks to a real WAHA, not a stub |
| `KILASFLOW_CAPSTONE_WAHA_API_KEY` | that server's API key | project owner | activation and the send call are accepted |
| `KILASFLOW_CAPSTONE_WAHA_SESSION_A` | a **paired** WhatsApp session name on that server | project owner | tenant A's trigger receives a real webhook |
| `KILASFLOW_CAPSTONE_WAHA_SESSION_B` | a second, independently paired session | project owner | two tenants are active at once without crossing deliveries |
| npm publish access for `@kilasflow/sdk` | an npm account with publish rights to the `@kilasflow` scope, wired into the release workflow as `NPM_TOKEN` | project owner | `npm install @kilasflow/sdk` resolves outside this repository |
| a published image in `ghcr.io/kilaslab/kilasflow` | the release workflow's `v*` path, no extra credential | release pipeline | proofs run against the shipped artefact, not a local build |

Two notes that shape the Telegram proof. `setWebhook` requires a publicly
reachable HTTPS URL, so the server under test has to be reachable from the
internet; the README's `cloudflared`/`ngrok` tunnel walkthrough is the supported
way to do that, and the suite uses it rather than inventing a second approach.
And a real WAHA server delivers `session.status` and `message.ack` events as
well as messages, which is why the suite waits for an execution whose trigger
output matches the message it sent rather than for any new execution.

Nothing in the report ever carries a secret: every record and both rendered
reports pass through a redaction step that strips the configured values, bearer
values a service echoes, and the webhook routes (a capability in their own
right) before a byte is written or printed.

## Configuration

| variable | default | meaning |
| --- | --- | --- |
| `KILASFLOW_CAPSTONE_IMAGE` | `ghcr.io/kilaslab/kilasflow:latest` | the image under test. It is the whole coordinate, so pinning a release is `KILASFLOW_CAPSTONE_IMAGE=ghcr.io/kilaslab/kilasflow:v1.2.3`; a rehearsal passes the local tag |
| `KILASFLOW_CAPSTONE_PULL` | `1` | `0` uses a local image without pulling — the rehearsal path |
| `KILASFLOW_CAPSTONE_SDK_SPEC` | `@kilasflow/sdk@latest` | the npm spec proof 4 installs; `tarball` packs `sdk/` instead |
| `KILASFLOW_CAPSTONE_NPM_REGISTRY` | npm's default | a private registry for the SDK install |
| `KILASFLOW_CAPSTONE_PG_IMAGE` | `pgvector/pgvector:pg17` | the PostgreSQL image proof 3 runs against |
| `KILASFLOW_CAPSTONE_KEEP` | unset | `1` leaves a failed run's containers in place for inspection; the teardown then says how to remove them |
| `KILASFLOW_CAPSTONE_RUN_ID` | minted per run | the label every container and network of this run carries |
| `KILASFLOW_CAPSTONE_CORPUS_STRICT` | unset | `1` fails the corpus proof on any drift from the scored baseline instead of reporting it |
| `KILASFLOW_CAPSTONE_*` credentials | unset | the credentials table above |

Two variables shared with the rest of the suite come from the repository rather
than from the capstone: `KILASFLOW_CORPUS_DIR` points the corpus measurement at
a materialised corpus outside `.corpus`, and `GITHUB_STEP_SUMMARY` makes the
scheduled run append its report to the run's summary page.

## Reading the verdict

Every proof writes its own record to `e2e/capstone-results/records/`, and
`make e2e-capstone-report` is the only thing that aggregates them into
`report.json` and `report.md`. There are four outcomes, and the difference
between the second and the third is the point of the whole design:

| outcome | means | exit code |
| --- | --- | --- |
| `passed` | the proof ran and agreed | — |
| `skipped` | it could not be configured; the report carries the reason and the recovery command | — |
| `unavailable` | a third party or the network was down — an outage, not a regression | **2** |
| `failed` | an assertion disagreed — this is the signal the suite exists for | **1** |

Exit `0` means every proof passed or explained itself. A record that is missing
entirely counts as a failure, because a test that crashed before writing one
must not read as silence.

`make e2e-capstone-scheduled` is the same verdict with one difference: exit `2`
does not fail the run. A schedule that goes red because somebody else's API was
down trains its readers to ignore red, which is worse than no schedule at all.
An assertion failure still fails in both modes.

## The schedule

`.github/workflows/capstone.yml` runs the capstone weekly and on
`workflow_dispatch`. It is not part of `ci.yml` and never gated on a pull
request. Until the published artefacts and the credentials exist, its runs
report `unavailable` rather than failing; when they do exist, the same job is
the epic's acceptance evidence.

## Where the numbers come from

The corpus half of the report re-measures the per-PR fidelity counts
(`internal/interop/n8n/corpus/baseline.json`) through the image's public API —
import, activate, run — where the Go scorer measures the same fixtures through
`n8n.Import`, `workflow.Compile` and the engine. Different paths over the same
corpus, so a disagreement is a finding rather than something to tune away. The
fixtures run with closed egress: a workflow that wants an outbound side is
recorded as *blocked*, which is the tier being measured, not as a failure.
