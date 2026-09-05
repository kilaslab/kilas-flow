---
id: FEAT-sfy1tq
title: Correct the documentation of record
status: done
priority: high
labels:
    - docs
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:56:11Z"
updated: "2026-09-05T15:01:28Z"
---

## Scope

The repository's own documentation of record makes claims that are no longer true, and it makes them in the first thing anybody reads.

`README.md:9-11` opens with a blockquote: "**Status: scaffolding.** The HTTP server, configuration, persistence, generated … workflow engine itself starts at Milestone 1 — see Roadmap." `.pine/roadmap.md` opens by saying "KilasFlow V1 is complete (`EPIC-c7gbdp`, 19/19 tickets, Milestones 0–7)". The engine, the editor, the credential store, the scheduler, the embed boundary, the n8n importer, the WAHA and Telegram packs and the routing interpreter have all shipped. The first sentence of the project's front page tells a reader none of that exists.

`README.md:45` states `POST /webhook/:id` is "Reserved for workflow triggers — currently 501". `internal/webhook.Handler` is mounted at `internal/api/routes.go` on both `/webhook` and `/webhook/*`, resolves bindings, enforces a 1 MiB body cap and a 30-second response timeout, and dedupes deliveries. The 501 is what a reader is told the product's inbound trigger surface returns.

The same endpoint table lists six routes. There are 33 operations under `/api/v1` alone.

The `Layout` section annotates each `internal/` package with the milestone it belonged to — "engine/ workflow execution (Milestone 1)" and so on. That was useful while milestones were being executed and now reads as a status claim about unfinished work.

Then there is the naming. `gflow-prd-v1.md` is 42 KB of design source written under the project's previous name: `gflow` throughout, `GFLOW_*` environment variables, `bin/gflow`. `.air.toml` and a stale untracked binary carry a second former name, `kflow`. `.pine/MEMORY.md` records the settled answer — "Product name is KilasFlow; Go module github.com/kilaslabs/kilas-flow; binary/cmd/env use kilasflow / KILASFLOW_" — so the repository holds three names for one product, and the largest document uses the wrong one on every page.

This ticket has no dependencies and should be done early. Every other p10 ticket points readers at material that currently contradicts itself, and V2-p10-9's site will link the README.

## Acceptance criteria

- [x] `README.md` contains no claim that is false at the time of writing, verified by checking each statement against the code rather than against the roadmap.
- [x] The status blockquote either states the project's real state or is removed; it does not describe shipped work as forthcoming.
- [x] The webhook row is corrected: the endpoint is described as it behaves, including the opaque per-tenant route segment and the uniform 404.
- [x] The endpoint table either covers the API or explicitly delegates to the generated reference and links it, rather than listing an unmarked subset.
- [x] The `Layout` section describes what each package does without milestone annotations that read as status.
- [x] `gflow-prd-v1.md` is resolved by an explicit decision, recorded in the ticket, and its status is stated in the file itself so no reader mistakes it for current documentation.
- [x] The naming inconsistency is either fixed or documented: a reader encountering `gflow` or `kflow` can find out in one step that they are former names.
- [x] The README's own sections that are good — the Telegram tunnel walkthrough, the `go:embed all:` explanation, the self-hosted docs rationale, the two-rules section — survive rather than being lost to a rewrite.

## Outcome

Two files changed: `README.md` and `gflow-prd-v1.md`. Nothing else needed changing.

### Before anything else: the worktree was 74 commits stale

This ticket was picked up in a worktree branched from `41aefe9`, which is 74 commits behind `main`. At that base the README was *correct* about the webhook returning 501 — `routes.go` really did mount `notImplemented` unconditionally there. The first pass of the audit was therefore worthless and was thrown away. Everything below was verified against `bf2d82e`. Anyone auditing documentation from a worktree should check `git log HEAD..main` before believing a single finding.

### What the ticket got wrong

**There are 35 operations under `/api/v1`, not 33.** 34 `huma.Register` calls across `internal/api/handlers/`, plus one `sse.Register` in `executions.go` for `stream-execution-events`, which is easy to miss because it is not a `huma.Register` call. Confirmed by building the binary, serving it, and counting the generated document: 27 paths, 35 operations, all of them under `/api/v1`.

**`.air.toml` does not contain `kflow`.** It says `kilasflow` everywhere, and `git log -p -- .air.toml` shows it never contained anything else. The file was left untouched. There is no `bin/` directory in a fresh worktree either, so there was no stale binary to find.

**`kflow` is not a former product name.** The only bare `kflow` in tracked files is `kflow_` in `.pine/roadmap.md`, where it is the planned prefix for tables KilasFlow creates inside a customer's own PostgreSQL database — a live design decision, not a leftover. `gflow` is the only former name. Whether that prefix should be `kilasflow_` is a real question, but it belongs to the p9 Datastore work and `.pine/roadmap.md` is outside this ticket's file scope. The PRD header now says what `kflow` actually is, so the next person does not repeat this.

**The webhook route is per trigger node, not per tenant.** `mintWebhookRoute` keys on `(tenant_id, workflow_id, node_id)`. Two triggers in one workflow get two routes.

**The README's 501 claim was false, but so is the implication that `routes.go` still mounts `notImplemented` on the prefix.** It mounts `deps.Webhook` and falls back to 501 only when that is nil. `cmd/kilasflow/main.go` always supplies a handler, so the shipped binary cannot return 501 there; the fallback exists for an embedder that composes `api.Deps` without one. Probed directly: `/webhook/anything` is a 404 problem document, and bare `/webhook` is a 404 with a different detail.

### Defects the ticket did not name, found by auditing the rest

- **`ai/maf/` was listed as the "Microsoft Agent Framework adapter".** It is a `doc.go` and nothing else, and `agent-framework-go` is not in `go.mod`. The README now says it is a reserved package and names `ai.LoopRuntime` as what actually runs the AI nodes.
- **"The engine deliberately does not import `internal/api`, GORM, or any agent framework" was one-third false.** `go list -deps ./internal/engine` has no `internal/api` and no `internal/ai`, so those two hold. GORM does not: `internal/repository` imports `gorm.io/gorm` directly and the engine imports `internal/repository`, so GORM is in the engine's build graph. The architectural intent survives — the engine only ever touches the interfaces — but the package boundary no longer enforces it, because interfaces and their GORM implementations share one package. The README now says exactly this, and notes that `internal/workflow` and `internal/execution` are still GORM-free.
- **Layout listed 14 `internal/` packages; there are 30.** It also omitted `packs/`, `sdk/`, `pkg/sdk/`, `third_party/`, `schemas/` and `cmd/nodepackgen/`. Synopses in the rewritten section are taken from each package's own doc comment via `go list`, so they are the packages' own account of themselves rather than a second description that can drift.
- **`KILASFLOW_<SECTION>_<KEY>` was true but misleading.** Only the first underscore after the prefix separates section from key; `envKeyToPath` cuts once. A reader guessing the rule would write `KILASFLOW_SERVER_READ_HEADER_TIMEOUT` expecting `server.read.header.timeout`, which matches no field and is discarded silently. Now stated.
- **Nothing said that credential storage is off without `KILASFLOW_ENCRYPTION_KEY`.** The server starts and logs a warning. That is a fine default but it is a trap if undocumented.
- **The "about 3.6 MB" docs-page figure is accurate.** It could not be checked at first because a fresh worktree has no `node_modules`; after `pnpm install`, `vendor-docs.mjs` printed `vendored scalar.js (3.6 MB)`. It was briefly softened to "a few megabytes" and restored once measured.

Everything else in the README held up: the Air/`GOBIN` explanation, the smoke-check descriptions including `KILASFLOW_SMOKE_SKIP_BUILD` and the Postgres AutoMigrate probe, `make lint` being vet + gofmt + svelte-check, the `3.0.3` variant, both guard test names, and both of the two rules (no absolute backend URLs exist under `web/src/`). The root page really does still report health and readiness, so that paragraph stayed — with a pointer to `/app/workflows`, which it was missing.

### Decisions

**The PRD is kept, marked, and *not* renamed.** The header is a blockquote at the very top saying it is historical, that `gflow` was the working name, what the current names are, that the code wins on disagreement, and where to read about the system as it is. It gives two measured examples of drift rather than asserting drift in the abstract: §48's Dockerfile specifies Node 22 / Go 1.25 / `distroless/static-debian12` against the shipped Node 24.16 / Go 1.27 / `:nonroot`, and §25 plans the AI loop on a framework that was never implemented.

I did rename it to `PRD-v1.md` and then reversed that. Twenty tracked files cite the path `gflow-prd-v1.md` — `.pine/roadmap.md` and nineteen ticket bodies — and this ticket's file scope excludes every one of them, so the rename would have created twenty dangling references in order to fix one cosmetic one. Manufacturing pointers to a file that no longer exists is precisely the defect this ticket exists to remove. Beyond the mechanics, the old name is a *correct* label for an archived artifact from the gflow era; a current-looking filename would invite the mistake the header is there to prevent.

The ticket suggested moving it under `docs/`. Rejected: `docs/` is the docs-site tickets' territory (V2-p10-9 and -12), and putting an archived design document where a static-site generator will find it turns it into a published page that reads as current documentation — worse than leaving it at the root with a header. The 87 `gflow` mentions were not rewritten, for the reason the ticket already gives.

**The endpoint table delegates to `/docs` and `/api/openapi.json`, not to V2-p10-10's reference.** That reference does not exist yet, and linking a document that has not been written would be the same species of false claim this ticket is fixing. The self-hosted `/docs` page and the generated OpenAPI document are what a reader has today, and both are generated from the handler types. They are named as paths rather than hyperlinks because they resolve on the reader's own instance, not on a public URL.

**The Roadmap milestone table was deleted rather than updated.** Milestones 0–7 are all shipped, so as a plan it is spent, and re-stating `.pine/roadmap.md` in the README is the exact duplication mechanism that produced every defect in this ticket. One sentence at the top of the README says what is built and points at the roadmap for what is next.

**`.air.toml` and `.gitignore` were not modified.** Neither has anything wrong with it.

### Verification

`go build ./...` and `go vet ./...` clean; `gofmt -l` clean. `make lint` initially failed on `pnpm check` because a fresh worktree has no `node_modules`; after `pnpm install` it passes — 1324 files, 0 errors, 0 warnings. The diff is two Markdown files, so none of this could have been affected either way, but it was run rather than assumed.

Claims about runtime behaviour were checked against a running binary rather than by reading: the operation count, the 404 on unresolved webhook routes, the four OpenAPI variants and the `3.0.3` version field, `/docs` serving HTML with a CSP and no external URLs in its markup, and the SPA fallback answering an unknown path.

### Not done, deliberately

The twenty `.pine/` references to `gflow-prd-v1.md` are correct and were left alone. The `kflow_` table prefix question is left for the p9 Datastore work. The Telegram walkthrough stays in the README until V2-p10-12 gives it a home on the docs site, as this ticket directs.

## Implementation Plan

Audit before editing. Go through `README.md` claim by claim and check each against the code; the three defects named above were found by reading, and reading further will find more. Record the audit on the ticket so the next person does not repeat it.

The endpoint table is a design decision, not a copy-editing one. Maintaining a table of 33 operations by hand guarantees it drifts again — this table is already the evidence for that. Recommend replacing it with the handful of routes that are genuinely stable and interesting outside the API — `/api/v1/health`, `/api/v1/ready`, `/docs`, `/api/openapi.json`, `/webhook/{route}`, the SPA fallback — and delegating everything under `/api/v1` to the generated reference from V2-p10-10, linked. That is fewer words and it stays true.

For the PRD, recommend keeping it and marking it rather than rewriting or deleting it. It is the design source the shipped system was built from and its reasoning is still valuable; rewriting 68 sections to change a product name is a large edit that would obscure what actually changed in the design over time, and deleting it discards history the tickets still reference. Move it under `docs/` alongside the site, and give it a header stating that it is the V1 PRD, that it uses the project's former name, that the current name is KilasFlow, and that where it disagrees with the code the code wins. One paragraph resolves the confusion without touching 2,739 lines.

Do not attempt to fix `.air.toml` or delete the stale `bin/kflow` binary in this ticket. `bin/` is gitignored and the binary is untracked, so it is a local artifact rather than a repository defect; mention it in the ticket so it is not rediscovered as a mystery.

Two things to protect while editing. The README's Telegram section — webhook versus polling delivery, `server.public_url`, `cloudflared` and the secret-token scheme — is the most operationally useful prose in the file and has no equivalent anywhere else; it should move to the docs site under V2-p10-12 rather than be dropped here. And the `//go:embed all:dist` explanation guards a real trap: without the `all:` prefix the build succeeds and produces a binary that serves `index.html` with no assets. That paragraph is load-bearing.

## References

- Roadmap plan, p10 section, entry V2-p10-14: `.pine/roadmap.md`.
- `README.md` — lines 9-11 (the status blockquote), line 45 (the 501 claim), the endpoint table, and the milestone-annotated Layout section.
- `.pine/roadmap.md` — the opening statement that V1 is complete.
- `internal/api/routes.go` — the webhook mount and the 33 registered operations.
- `internal/webhook/webhook.go` — the handler the README calls a 501.
- `gflow-prd-v1.md` — 2,739 lines under the former name, including §48's Dockerfile that names different base images than the shipped one.
- `.air.toml` — the second former name.
- `.pine/MEMORY.md` — the settled naming entry of 2026-08-29.
- `internal/web/embed.go` — the `all:` prefix trap the README documents and `TestEmbedIncludesUnderscoreAndDotPaths` guards.

## Work Evidence

Closed 2026-09-05.

`pine close --evidence` was run and its diffstat discarded as wrong. It picks a
base of "last commit at or before the ticket was created" — `c38dcdc` here — and
this worktree was fast-forwarded 74 commits during the work, so it attributed
222 files and 25,760 insertions of other people's commits to a documentation
ticket. The accurate diff, against the commit actually worked from — `bf2d82e`,
feat(nodes): bring the PostgreSQL node to n8n's operation set — plus this ticket
file itself:

```
 README.md       | 188 ++++++++++++++++++++++++++++++++++++++++----------------
 gflow-prd-v1.md |  32 ++++++++++
 2 files changed, 167 insertions(+), 53 deletions(-)
```

No Go, Svelte, or configuration file was touched. `go build ./...`,
`go vet ./...` and `make lint` all pass.
