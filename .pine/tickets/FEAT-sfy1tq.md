---
id: FEAT-sfy1tq
title: Correct the documentation of record
status: todo
priority: high
labels:
    - docs
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:56:11Z"
updated: "2026-09-05T11:56:11Z"
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

- [ ] `README.md` contains no claim that is false at the time of writing, verified by checking each statement against the code rather than against the roadmap.
- [ ] The status blockquote either states the project's real state or is removed; it does not describe shipped work as forthcoming.
- [ ] The webhook row is corrected: the endpoint is described as it behaves, including the opaque per-tenant route segment and the uniform 404.
- [ ] The endpoint table either covers the API or explicitly delegates to the generated reference and links it, rather than listing an unmarked subset.
- [ ] The `Layout` section describes what each package does without milestone annotations that read as status.
- [ ] `gflow-prd-v1.md` is resolved by an explicit decision, recorded in the ticket, and its status is stated in the file itself so no reader mistakes it for current documentation.
- [ ] The naming inconsistency is either fixed or documented: a reader encountering `gflow` or `kflow` can find out in one step that they are former names.
- [ ] The README's own sections that are good — the Telegram tunnel walkthrough, the `go:embed all:` explanation, the self-hosted docs rationale, the two-rules section — survive rather than being lost to a rewrite.

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
