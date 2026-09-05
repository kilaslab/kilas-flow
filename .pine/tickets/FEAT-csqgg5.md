---
id: FEAT-csqgg5
title: Assemble the n8n importer regression corpus
status: done
priority: high
labels:
    - reference
    - guardrails
parent: EPIC-m42s3g
phase: p0
created: "2026-09-05T05:02:48Z"
updated: "2026-09-05T05:02:48Z"
---

## Scope

Every ticket in p1 through p4 claims to make imported n8n workflows work better. Today none of them can prove it, because the only n8n fixtures KilasFlow has are hand-written Go string constants inside `internal/interop/n8n/n8n_test.go` — fifteen tests, each built around a workflow the author invented to exercise the branch under test. There is no `testdata` directory anywhere in the repository (`find . -type d -name testdata` returns nothing). A hand-written fixture proves a code path; it cannot tell anyone how much of the real ecosystem imports, and V2's whole premise is a number: how many of a customer's actual workflows run here.

This ticket builds that measuring instrument. The corpus has three sources. First, the official WAHA templates, which live in `github.com/devlikeapro/waha-n8n-templates` (linked from `waha.devlike.pro/docs/integrations/n8n/`, rendered at `waha-n8n-templates.devlike.pro`): 10 template directories holding **13** n8n workflow documents — nine `template.json` files plus the four Chatwoot workflows — totalling 170 node instances, alongside two Typebot exports that are not n8n workflows and must not be counted. Second, the authentic fixtures already on disk in the reference checkout: **25** workflow documents under `packages/nodes-base/nodes/{HttpRequest,Set,If}/test/**` (11 HttpRequest, 9 If, 5 Set — the plan's "~40" is an overcount, and the `*.node.json` files beside them are codex metadata, not workflows). Third, the owner's own client workflows, which are the most valuable input and which **do not exist on disk anywhere yet** — nothing under `/Users/izzadev/projects/mitrachat` holds an exported workflow — so the corpus must work fully without them and absorb them later with no code change.

Neither third-party source may be committed. n8n's fixtures are `LicenseRef-n8n-sustainable-use` (declared in `packages/nodes-base/package.json`), whose distribution terms are incompatible with an Apache-2.0 commercial repository. The WAHA templates repository carries **no licence at all** — the GitHub API reports `license: null`, there is no `LICENSE` file in its tree at `1bd5536eef88f81692daaaba74d3bf6d621fb94f`, and its README grants nothing — which means all rights reserved, a stricter position than the SUL. The plan's instruction to "collect them into `internal/interop/n8n/testdata/`" would therefore vendor both n8n-licensed and unlicensed third-party JSON into this repository, which is exactly what the licence guardrail forbids. The corpus is fetched into a gitignored directory instead, pinned by upstream commit and per-file digest, and only KilasFlow-authored fixtures are committed.

The measured baseline matters as much as the mechanism. Across those 13 WAHA templates the node inventory is: `stickyNote` 34, `@devlikeapro/n8n-nodes-waha.WAHA` 23, `set` 17, `postgres` 16, `if` 14, `switch` 10, `wahaTrigger` 7, `noOp` 6, `httpRequest` 6, `splitOut` 5, `@devlikeapro/n8n-nodes-chatwoot.chatWoot` 5, `webhook` 4, `wait` 4, `manualTrigger` 4, `respondToWebhook` 3, `code` 3, `splitInBatches` 3, `emailSend` 3, and one each of `editImage`, `extractFromFile`, `scheduleTrigger`, `convertToFile`, `stopAndError`. Sticky Note is the most deployed node in the whole set and today becomes `kilasflow.unsupported`, whose `Validate` always fails — so the honest opening score is that essentially nothing is activatable, and that number is the point of writing it down.

## Acceptance criteria

- [x] A Go package exposes the whole corpus by name and source to any test in the repository, so engine and compiler tests can score fixtures without reaching into another package's `testdata` by relative path.
- [x] All 13 WAHA template workflows and all 25 `nodes-base` fixtures are reachable through one documented sync step that materialises them into a gitignored directory, pinned by upstream commit and verified per file by digest; no n8n-licensed or unlicensed third-party JSON is committed to this repository.
- [x] `go test ./...` passes on a clean clone with no corpus materialised — corpus-dependent tests skip with a message naming the exact command that fetches it.
- [x] A scoreboard test scores every fixture as imported / activatable / runnable and writes a committed report, so a later ticket's diff shows precisely which workflows it moved and which it did not.
- [x] The report records the baseline measured on the day this lands, including per-fixture failure reasons, and the corpus node-type inventory that p3 and p4 will pick their targets from.
- [x] The owner's own exported client workflows can be dropped into a private gitignored overlay directory and scored alongside the public corpus with no code change, and neither the report nor any test output ever prints fixture payload content.
- [x] Re-running the sync against the same pinned commit reproduces byte-identical fixtures, and a changed upstream file fails loudly instead of silently shifting the baseline.

## Implementation Plan

Start with the loader, because everything else is shaped by where the corpus can be read from. Go's `testdata` convention is package-local: a test in `internal/engine` cannot reach `internal/interop/n8n/testdata` except through a brittle `../../` path, and p1's tickets are mostly engine and compiler tickets. Write `internal/interop/n8n/corpus/corpus.go` as a real (internal) package that returns named documents — KilasFlow-authored fixtures via `go:embed` from its own directory, third-party fixtures read at runtime from a directory resolved from an environment variable with a repository-relative default. That gives one import path for every consumer and keeps the licensed material outside the module's committed bytes.

Then the sync: `scripts/corpus-sync.sh` plus a `make corpus` target beside the existing `smoke-*` targets. It fetches `devlikeapro/waha-n8n-templates` at the pinned commit `1bd5536eef88f81692daaaba74d3bf6d621fb94f` and copies the 25 fixtures out of the reference checkout at `/Users/izzadev/projects/mitrachat/n8n` (already materialised — `HttpRequest`, `Set` and `If` are in the existing sparse-checkout, so no widening is needed for this ticket). Commit a manifest listing every file with its source URL or path, upstream commit and SHA-256, and have the sync verify against it. Without the digests the scoreboard becomes unfalsifiable the first time upstream edits a template. Add the corpus directory and the private overlay directory to `.gitignore` in the same change, before any fixture is ever written to disk — a real client workflow contains live phone numbers, WAHA hostnames and credential names, and one accidental `git add -A` is all it takes.

Score with the real machinery, never a reimplementation: `n8n.Import([]byte)` for tier one; `workflow.Compile(document, catalogue)` against a catalogue built by `nodes.RegisterAll` for tier two, which is the same authority activation goes through, exactly as `TestImportedWorkflowCompiles` already does; and for tier three an `engine.Registry` fully populated by `nodes.RegisterExecutors` plus a run through `engine.NewRunner(...).Run`. Define the three tiers in the report itself so nobody re-argues them later: **imported** means `Import` returned no error, **activatable** means `Compile` returned no error, **runnable** means the workflow ran to completion with outbound HTTP, SQL and model calls stubbed. Tier three must never make a real network call, and a suite that needs a live WAHA server is a suite that gets disabled. Only the committed control fixtures are scored in CI: the third-party fixtures the baseline covers are licence-bound and never committed, so `BASELINE.md` is regenerated by hand with `make corpus-baseline` and its numbers recorded on the ticket that moves them.

Two traps. The first is that `Import` refuses a document with duplicate node names outright, and refusing is correct — but at corpus scale a single such template would read as an importer regression rather than a deliberate refusal, so the scoreboard must record the refusal *reason*, not just a boolean. The second is the temptation to fix what the baseline exposes. Sticky Note will sink almost every real template, `settings`/`pinData`/`meta` are dropped without a diagnostic, and the WAHA nodes all carry `typeVersion: 202409`, which the importer truncates through `int(node.TypeVersion)`. Those are p1-9, p1-12 and p1-11. This ticket records them; it does not repair them, and a green scoreboard on the day it lands would mean the corpus is wrong.

One decision is open: the report format. Options are a Markdown table committed as `BASELINE.md`, a JSON golden file regenerated with a `-update` flag, or both. Recommend both — JSON as the machine-checked golden that fails the test on unexplained drift, and a generated Markdown table beside it, because the number that matters is one a person reads in a diff and the JSON is what stops a ticket from quietly regressing a workflow it was not looking at.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p0 — Reference and guardrails", entry V2-p0-2; and "p1", which measures every ticket against this corpus.
- `github.com/devlikeapro/waha-n8n-templates` at `1bd5536eef88f81692daaaba74d3bf6d621fb94f` — 13 n8n workflow documents, no licence file, `license: null` via the GitHub API.
- Reference checkout fixtures: `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/{HttpRequest,Set,If}/test/**` (25 workflow documents), package licence `LicenseRef-n8n-sustainable-use`.
- Existing importer surface: `internal/interop/n8n/n8n.go` (`Import`, `Export`, `SupportedMappings`), `internal/interop/n8n/n8n_test.go` (`registry`, `TestImportedWorkflowCompiles`), `internal/workflow/compiler.go` (`Compile`), `nodes/core.go` (`RegisterAll`), `nodes/executors.go` (`RegisterExecutors`).
- V1 precedent for the compiler as the single validation authority: `FEAT-chxkvq`.

## Outcome

### The baseline

39 fixtures scored: **39 imported (100%), 10 activatable (26%), 1 runnable (3%)**.

| Source | Fixtures | Imported | Activatable | Runnable | Blocked |
| --- | ---: | ---: | ---: | ---: | ---: |
| `waha-templates` | 13 | 13 | **0** | 0 | 0 |
| `nodes-base` | 25 | 25 | 9 | 0 | 9 |
| `kilasflow` (control) | 1 | 1 | 1 | 1 | 0 |

Zero of the thirteen real WAHA templates can be activated, which is the honest
opening score the ticket predicted and the number the whole epic is measured
against. The only runnable fixture is KilasFlow's own control.

Two failure modes dominate the WAHA set and both are already-filed p1 work:
`connection must reference declared source and target ports` (9 of 13), which
is what an unsupported node with no ports does to every edge touching it, and
`configuration is invalid: this node was imported from …` (3 of 13), the
always-failing `kilasflow.unsupported` validator. Neither was repaired here — a
green scoreboard on the day this landed would have meant the corpus was wrong.

The counts the ticket predicted were confirmed exactly: 13 WAHA workflow
documents and 25 `nodes-base` fixtures. The two Typebot exports are excluded
structurally, by testing for `nodes` plus `connections`, rather than by name,
so a renamed file cannot quietly enter the count.

### What was built

`internal/interop/n8n/corpus` is a real package, not a `testdata` directory, so
engine and compiler tests import it by one path instead of reaching across
packages with `../../`. Authored fixtures come from `go:embed`; third-party
fixtures are read at run time from `KILASFLOW_CORPUS_DIR` (default `.corpus`)
and `KILASFLOW_CORPUS_PRIVATE_DIR` (default `.corpus-private`), both gitignored
before any fixture was ever written to disk.

`scripts/corpus-sync.sh` plus `make corpus` and `make corpus-baseline`. The
sync fetches `waha-n8n-templates` at the pinned `1bd5536` and copies the
`nodes-base` fixtures out of the reference checkout, then verifies every file
against the committed `MANIFEST.json` digests. Exit codes were checked: 0 clean,
1 on digest drift, 2 on a missing prerequisite.

The scoreboard scores with the real machinery — `n8n.Import`, then
`workflow.Compile` against a catalogue from `nodes.RegisterAll`, then
`engine.NewRunner` with executors from `nodes.RegisterExecutors` — never a
reimplementation. It writes both `baseline.json` (the machine-checked golden)
and `BASELINE.md` (the table a person reads in a diff), and fails on any
unexplained movement, naming the fixture and the exact transition.

### Three things the ticket did not anticipate

1. **The sync script may not hard-code the reference checkout.** Doing so would
   have put a machine path into a `.sh` file, which the licence guardrail from
   FEAT-yyjfjq correctly rejects as making foreign source a build input. The
   script requires `KILASFLOW_N8N_REFERENCE` instead, with no default — which
   also makes it portable to any machine, and it verifies the checkout is at
   the pinned commit before reading a byte.

2. **Tier three needed a fourth state.** Stubbing outbound calls by refusing
   them means a workflow whose graph executes perfectly still fails "runnable"
   if any node would have called out. All 9 activatable `nodes-base` fixtures
   are in exactly that position. Collapsing that into "not runnable" would have
   made the tier measure "has no HTTP node" rather than "executes", so a
   `blocked` state is recorded separately, classified on the wrapped
   `safehttp.ErrBlocked` and `sqlnode.ErrForbiddenTarget` sentinels rather than
   on error text. Only a fixture that is neither runnable nor blocked actually
   failed to execute.

3. **A scoreboard of all-failures cannot validate itself.** If everything fails,
   nothing in the numbers distinguishes "importing real n8n workflows is hard"
   from "the instrument is broken". One authored control fixture is committed
   and `TestControlFixturesPass` requires it to reach every tier, so an
   instrument failure shows up as a failing test rather than as a plausible
   zero.

### Privacy

The private overlay is scored alongside the public corpus with no code change
and contributes **nothing** to any committed file: no row, no name, no reason,
only aggregate booleans in the test log.
`TestPrivateOverlayIsScoredButNeverReported` proves both halves by dropping a
named workflow into the overlay and asserting the rendered report does not
contain its name.

### Clean clone

`go test ./...` passes with nothing materialised. `TestCorpusScoreboard` and
`TestManifestPinsEveryFetchedFixture` skip with the exact fetch command in the
message; the control and loader tests still run, because the authored fixtures
are embedded.
