---
id: FEAT-v2nenc
title: Publish the docs site at a stable URL (sitemap, robots, edit links, README link)
status: todo
priority: high
labels:
    - docs
    - website
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

The site builds and validates links in CI, but it is not reachable. `docs-publish` (ci.yml) is gated on `vars.DOCS_PUBLISH`, GitHub Pages is off, `site`/`base` are unset (so no sitemap), and the README can only point into the tree.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-21). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n/a

# Steps to Reproduce

1. `docs/astro.config.mjs:28-29` reads `site` and `base` from `DOCS_SITE`/`DOCS_BASE`, which are unset locally. Starlight emits no sitemap (`ls docs/dist/sitemap*` → none), and the build warns.
2. `.github/workflows/ci.yml:473-494`: `docs-publish` runs only when `vars.DOCS_PUBLISH == 'true'`. `gh api repos/kilaslab/kilas-flow` → `"has_pages": false`, and `gh api …/pages` → 404.
3. There is no `llms.txt` in `docs/dist`, no versioning (`contributing.md:78-83` records this as an open decision), and no `editLink` in the Starlight config.
4. `README.md:125-126`: "It isn't hosted anywhere yet, so read it in the tree".

# Expected

A public URL (custom domain or Pages), a sitemap, `llms.txt` and `llms-full.txt`, edit links, a README badge or link, and a versioning policy tied to the first release tag.

# Actual

Integrators read raw Markdown on GitHub, where the `/start/...` root-relative links do not resolve.

# Acceptance Criteria
- [ ] `docs-publish` runs on main and the site answers publicly (GitHub Pages or Cloudflare Pages; custom domain optional)
- [ ] `site` and `base` are set; sitemap and robots.txt are served
- [ ] Starlight `editLink` points at the repo
- [ ] The README and the in-app Help menu link to the site
- [ ] A subpath or domain build passes link validation

# Implementation Plan

Enable Pages and set `DOCS_PUBLISH`, or choose Cloudflare Pages with a custom domain. Add `starlight-llms-txt` (exists: `/delucis/starlight-llms-txt`). Add `editLink.baseUrl`. Plan `starlight-versions` for the first tag.

# Notes

Related tickets: BUG-b4cb1c, FEAT-nxxbs5

Related (from the audit): FEAT-nxxbs5 (done), BUG-b4cb1c (todo)

# Related Files

the steps above.

# Attachments
