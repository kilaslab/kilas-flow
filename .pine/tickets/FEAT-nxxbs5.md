---
id: FEAT-nxxbs5
title: Stand up the documentation site on Astro Starlight
status: todo
priority: high
labels:
    - docs
deps:
    - FEAT-7tgasa
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:52:38Z"
updated: "2026-09-05T11:52:38Z"
---

## Scope

There is no documentation site and no `docs/` directory. A search for a static-site generator config — mkdocs, docusaurus, vitepress, astro, mdbook — finds nothing. The documentation that exists is scattered across five kinds of artifact with no route between them:

- `README.md`, a good 9 KB developer file whose opening blockquote still says "Status: scaffolding" and whose endpoint table lists six of 33 routes.
- `gflow-prd-v1.md`, 42 KB and 68 sections of design source, written under the project's previous name throughout.
- Per-directory READMEs of genuinely high quality — `packs/waha/README.md`, `third_party/waha/PROVENANCE.md`, `sdk/README.md`, `internal/interop/n8n/corpus/BASELINE.md`.
- Go package doc comments, which are the best prose in the repository and are reachable only by reading source.
- `.pine/`, a large tracked knowledge base of roadmap and tickets, which is planning material and not user documentation.

None of it is addressable by a URL, searchable, or organised for a reader who is not already inside the repository. For a product whose stated ICP is white-label, multi-tenant and embedded — a thing other people's engineers integrate — that is the gap that makes every other p10 ticket unreachable.

This ticket builds the vessel, not the contents. V2-p10-10 through V2-p10-14, V2-p10-17 and V2-p10-19 write pages into it; if this ticket also wrote pages, it would block all of them behind one large change.

The stack is decided: **Astro Starlight**. It has the strongest generated-API-reference story of the candidates through `starlight-openapi`, which V2-p10-10 needs; it ships search, MDX, code tabs and a light/dark theme without configuration; and it builds to static output that any host can serve.

Two constraints are specific to this repository and must be respected rather than discovered.

**The docs site must not enter the binary's build.** `web/` is a SvelteKit app on `adapter-static` whose output is copied into `internal/web/dist` and embedded by `//go:embed all:dist`. Adding documentation to that tree would put every documentation change into the Go binary and into the 3.7 MB the SPA already costs. The docs site is a sibling of `web/`, not a part of it.

**Dark is the default, not the alternative.** `web/src/app.css` puts the dark palette on bare `:root` and makes light opt-in through `[data-theme='light']`, with the Tailwind `dark` variant defined as the negation. Starlight's default is the opposite arrangement. A docs site that renders light by default beside a product that renders dark by default reads as two products.

## Acceptance criteria

- [ ] A Starlight site lives in `docs/` with its own `package.json`, builds to static output, and is not referenced by `web/`'s build, `internal/web/dist`, or any `go:embed` directive.
- [ ] The site's default theme is dark, matching the product, with light available as the explicit alternative rather than the fallback.
- [ ] The information architecture is committed with placeholder pages for every section p10 will fill, so each later documentation ticket adds pages rather than restructuring navigation.
- [ ] Full-text search works over the built site with no external service.
- [ ] The site builds in the V2-p10-1 pipeline and a broken build fails the run.
- [ ] The site is deployed on merge to the default branch, reachable at a stable public URL, and the URL is recorded in the README.
- [ ] Internal links are validated at build time, so a page renamed by a later ticket cannot silently orphan a link.
- [ ] The Node and pnpm versions used to build the site match the ones already pinned for `web/` and `sdk/`, so the repository has one toolchain and not two.

## Implementation Plan

Scaffold Starlight into `docs/` as a third pnpm project alongside `web/` and `sdk/`. Do not attempt a pnpm workspace in this ticket — the two existing projects are independent today and converting them is a separate concern that would put this ticket's success at the mercy of a lockfile migration.

Settle the information architecture before writing any theme code, since it is the thing the other seven documentation tickets bind to. A shape that matches the reader's questions rather than the repository's layout:

- **Start** — what KilasFlow is, install (V2-p10-3's quickstart), first workflow.
- **Concepts** — the architecture material from V2-p10-11.
- **Guides** — embedding and multi-tenancy (V2-p10-13), node authoring (V2-p10-17), n8n migration (V2-p10-19).
- **Operate** — deployment, configuration reference, security posture, upgrades (V2-p10-12).
- **Reference** — the API reference (V2-p10-10), the pack format, the API contract from V2-p10-4.

Use `autogenerate` for the directory-backed groups so a new page appears in the sidebar without a config edit; reserve explicit ordering for the Start section, where sequence is the point.

For theming, define the palette as tokens and set the dark values on the bare selector, mirroring `web/src/app.css` rather than reimplementing it. Do not import the SPA's stylesheet — it is Tailwind 4 with `@custom-variant` and shadcn-svelte tokens, and coupling the docs build to it would make a design change in the product break the documentation build. Copy the token values with a comment naming the source file.

Deployment: GitHub Pages from the same Actions workflow, since V2-p10-1 puts CI there and Pages needs no additional credential or account. Note that a project Pages site is served from a subpath, so Astro's `base` and `site` must both be set or every asset URL will be wrong in production and right in local preview — the single most common way this deployment fails.

Leave versioned documentation out of this ticket. Starlight has no built-in versioning, the product has not cut its first release tag until V2-p10-2, and a versioning scheme designed before there are two versions is a guess. Record it as a known future decision on the site's own contributing page.

## References

- Roadmap plan, p10 section, entry V2-p10-9: `.pine/roadmap.md`.
- `web/svelte.config.js`, `web/vite.config.ts`, `internal/web/embed.go` — the SPA build the docs site must stay out of.
- `web/src/app.css` — lines 84-93 and 140, the dark-first token arrangement to mirror.
- `Makefile` — `build-web`, `dist-placeholder`, and where docs targets belong.
- `README.md` — the current documentation of record, and where the site's URL is recorded.
- `devbox.json`, `web/package.json` — the pinned Node 24 and pnpm 10 versions.
- Astro Starlight documentation: sidebar `autogenerate`, `customCss`, `expressiveCode`, and the `site`/`base` pair required for subpath deployment.
